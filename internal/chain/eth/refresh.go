package eth

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

// Refresh загружает баланс и историю ETH или USDC. Каждый актив обновляется
// независимо: USDC не обращается к истории ETH как к отдельному активу, а
// берёт из обычных транзакций только комиссию и статус своих переводов.
//
// Ошибка критического источника баланса завершает Refresh. Ошибка индексатора
// истории не скрывает достоверный on-chain баланс: она помечается в Snapshot,
// а сервис сохраняет предыдущую историю и неизвестный fee balance.
func (a *Adapter) Refresh(ctx context.Context, w domain.PublicWallet, asset domain.Asset) (ports.Snapshot, error) {
	if asset != domain.ETH && asset != domain.USDC {
		return ports.Snapshot{}, domain.NewError(domain.CodeInvalidInput, fmt.Errorf("eth: unsupported asset %q", string(asset)))
	}
	if w.ID == "" {
		return ports.Snapshot{}, domain.NewError(domain.CodeInvalidInput, errors.New("eth: wallet is not set"))
	}
	own, _, err := parseAddress(w.ETHAddress)
	if err != nil || own == (Address{}) {
		return ports.Snapshot{}, domain.NewError(domain.CodeInvalidInput, errors.New("eth: wallet has no valid Ethereum address"))
	}
	if err := a.ensureChain(ctx); err != nil {
		return ports.Snapshot{}, err
	}

	if asset == domain.ETH {
		return a.refreshETH(ctx, w.ID, own)
	}
	return a.refreshUSDC(ctx, w.ID, own)
}

func (a *Adapter) refreshETH(ctx context.Context, walletID string, own Address) (ports.Snapshot, error) {
	var (
		wei        *big.Int
		txs        []IndexedTx
		balanceErr error
		historyErr error
		wg         sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		wei, balanceErr = a.rpc.Balance(ctx, own)
	}()
	go func() {
		defer wg.Done()
		txs, historyErr = a.idx.NormalTxs(ctx, own)
	}()
	wg.Wait()
	if balanceErr != nil {
		return ports.Snapshot{}, balanceErr
	}
	bal, err := amountOf(wei)
	if err != nil {
		return ports.Snapshot{}, err
	}
	snap := ports.Snapshot{
		Asset: domain.ETH, Balance: domain.Balance{Confirmed: bal},
		FeeAsset: domain.ETH, FetchedAt: a.now(),
	}
	if historyErr != nil {
		snap.HistoryError = string(domain.CodeOf(historyErr))
	} else {
		snap.History, err = ethHistory(walletID, own, txs)
		if err != nil {
			snap.HistoryError = string(domain.CodeOf(err))
		}
	}
	return snap, nil
}

func (a *Adapter) refreshUSDC(ctx context.Context, walletID string, own Address) (ports.Snapshot, error) {
	var (
		raw          []byte
		wei          *big.Int
		transfers    []IndexedTransfer
		normal       []IndexedTx
		tokenErr     error
		feeErr       error
		transfersErr error
		normalErr    error
		wg           sync.WaitGroup
	)
	wg.Add(4)
	go func() {
		defer wg.Done()
		raw, tokenErr = a.rpc.Call(ctx, a.params.usdc, encodeBalanceOf(own))
	}()
	go func() {
		defer wg.Done()
		wei, feeErr = a.rpc.Balance(ctx, own)
	}()
	go func() {
		defer wg.Done()
		transfers, transfersErr = a.idx.TokenTransfers(ctx, own, a.params.usdc)
	}()
	go func() {
		defer wg.Done()
		normal, normalErr = a.idx.NormalTxs(ctx, own)
	}()
	wg.Wait()
	if tokenErr != nil {
		return ports.Snapshot{}, tokenErr
	}
	word, err := decodeWord(raw)
	if err != nil {
		return ports.Snapshot{}, badResponse(err)
	}
	bal, err := amountOf(word)
	if err != nil {
		return ports.Snapshot{}, err
	}
	snap := ports.Snapshot{
		Asset: domain.USDC, Balance: domain.Balance{Confirmed: bal},
		FeeAsset: domain.ETH, FetchedAt: a.now(),
	}
	if feeErr != nil {
		snap.FeeBalanceError = string(domain.CodeOf(feeErr))
	} else if feeBal, amountErr := amountOf(wei); amountErr != nil {
		snap.FeeBalanceError = string(domain.CodeOf(amountErr))
	} else {
		snap.FeeBalance = feeBal
	}
	switch {
	case transfersErr != nil:
		snap.HistoryError = string(domain.CodeOf(transfersErr))
	case normalErr != nil:
		snap.HistoryError = string(domain.CodeOf(normalErr))
	default:
		snap.History, err = usdcHistory(walletID, own, a.params.usdc, transfers, normal)
		if err != nil {
			snap.HistoryError = string(domain.CodeOf(err))
		}
	}
	return snap, nil
}

func amountOf(n *big.Int) (domain.Amount, error) {
	amt, err := domain.NewAmount(n)
	if err != nil {
		return domain.Amount{}, badResponse(fmt.Errorf("eth: amount out of range: %w", err))
	}
	return amt, nil
}

// feeOf считает комиссию, которую заплатил отправитель: газ на цену газа.
func feeOf(tx IndexedTx) (domain.Amount, error) {
	if tx.GasUsed == 0 || tx.GasPrice == nil || tx.GasPrice.Sign() <= 0 {
		return domain.Amount{}, badResponse(errors.New("eth: transaction has invalid gas fee fields"))
	}
	fee := new(big.Int).Mul(new(big.Int).SetUint64(tx.GasUsed), tx.GasPrice)
	amt, err := domain.NewAmount(fee)
	if err != nil {
		return domain.Amount{}, badResponse(fmt.Errorf("eth: transaction fee out of range: %w", err))
	}
	return amt, nil
}

func direction(from, to, own Address) domain.Direction {
	switch {
	case from == own && to == own:
		return domain.DirectionSelf
	case from == own:
		return domain.DirectionOut
	default:
		return domain.DirectionIn
	}
}

func status(failed bool) domain.TxStatus {
	if failed {
		return domain.TxFailed
	}
	return domain.TxConfirmed
}

// ethHistory собирает историю ETH из обычных транзакций. Попадают переводы
// с ненулевой суммой и исходящие переводы без calldata (в том числе на ноль).
// Вызовы контрактов с нулевой суммой (перевод USDC, approve) в историю ETH не
// входят: их комиссию показывает история токена. Дубликаты по хэшу
// отбрасываются.
func ethHistory(walletID string, own Address, txs []IndexedTx) ([]domain.Tx, error) {
	seen := make(map[string]struct{}, len(txs))
	var out []domain.Tx
	for _, tx := range txs {
		if _, dup := seen[tx.Hash]; dup {
			continue
		}
		if tx.From != own && (!tx.HasTo || tx.To != own) {
			continue // чужая запись: индексатор не доверенный
		}
		if tx.Value.Sign() == 0 && (tx.HasInput || tx.From != own) {
			continue
		}
		seen[tx.Hash] = struct{}{}

		value, err := domain.NewAmount(tx.Value)
		if err != nil {
			continue
		}
		rec := domain.Tx{
			WalletID:    walletID,
			Asset:       domain.ETH,
			TxID:        domain.TxID(tx.Hash),
			Amount:      value,
			FeeAsset:    domain.ETH,
			Status:      status(tx.Failed),
			BlockHeight: tx.Block,
			Time:        tx.Time,
		}
		if tx.HasTo {
			rec.Direction = direction(tx.From, tx.To, own)
		} else {
			rec.Direction = domain.DirectionOut // создание контракта
		}
		switch rec.Direction {
		case domain.DirectionOut:
			rec.Fee, err = feeOf(tx)
			if err != nil {
				return nil, err
			}
			if tx.HasTo {
				rec.Counterparty = tx.To.String()
			}
		case domain.DirectionIn:
			rec.Counterparty = tx.From.String()
		case domain.DirectionSelf:
			rec.Fee, err = feeOf(tx)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, rec)
	}
	sortHistory(out)
	return out, nil
}

// usdcHistory склеивает переводы токена с обычными транзакциями. Переводы
// даёт индексатор токенов, а комиссия и статус берутся из обычной транзакции с
// тем же хэшем, и только если её отправитель это мы: чужую комиссию мы не
// платили. В одной транзакции переводов может быть несколько: запись одна на
// хэш (ключ записи это кошелёк, актив и хэш), сумма это чистое изменение
// баланса.
func usdcHistory(walletID string, own, contract Address, transfers []IndexedTransfer, normal []IndexedTx) ([]domain.Tx, error) {
	byHash := make(map[string]IndexedTx, len(normal))
	for _, tx := range normal {
		byHash[tx.Hash] = tx
	}

	type agg struct {
		in, out, self big.Int
		counterIn   Address
		counterOut  Address
		onlySelf    bool
		sent        bool
		block       uint64
		time        IndexedTransfer
	}
	order := make([]string, 0, len(transfers))
	groups := make(map[string]*agg, len(transfers))
	for _, tr := range transfers {
		if tr.Contract != contract {
			continue // другой токен: подмена контракта в ответе не принимается
		}
		if tr.From != own && tr.To != own {
			continue
		}
		g := groups[tr.Hash]
		if g == nil {
			g = &agg{onlySelf: true, block: tr.Block, time: tr}
			groups[tr.Hash] = g
			order = append(order, tr.Hash)
		}
		switch {
		case tr.From == own && tr.To == own:
			// перевод себе не меняет баланс, но запись о нём нужна
			g.sent = true
			g.self.Add(&g.self, tr.Value)
		case tr.To == own:
			g.onlySelf = false
			g.in.Add(&g.in, tr.Value)
			g.counterIn = tr.From
		default:
			g.onlySelf = false
			g.sent = true
			g.out.Add(&g.out, tr.Value)
			g.counterOut = tr.To
		}
	}

	out := make([]domain.Tx, 0, len(order))
	for _, hash := range order {
		g := groups[hash]
		rec := domain.Tx{
			WalletID:    walletID,
			Asset:       domain.USDC,
			TxID:        domain.TxID(hash),
			FeeAsset:    domain.ETH,
			Status:      domain.TxConfirmed, // событие Transfer есть только у успешной транзакции
			BlockHeight: g.block,
			Time:        g.time.Time,
		}
		net := new(big.Int).Sub(&g.in, &g.out)
		switch {
		case g.onlySelf:
			rec.Direction = domain.DirectionSelf
			net = new(big.Int).Set(&g.self)
		case net.Sign() > 0:
			rec.Direction = domain.DirectionIn
			rec.Counterparty = g.counterIn.String()
		case net.Sign() < 0:
			rec.Direction = domain.DirectionOut
			rec.Counterparty = g.counterOut.String()
			net.Neg(net)
		default:
			rec.Direction = domain.DirectionSelf // вошло и вышло поровну
			net = new(big.Int).Set(&g.in)
		}
		amt, err := domain.NewAmount(net)
		if err != nil {
			return nil, badResponse(fmt.Errorf("eth: USDC transfer amount out of range: %w", err))
		}
		rec.Amount = amt
		if g.sent {
			tx, ok := byHash[hash]
			if !ok || tx.From != own {
				return nil, badResponse(errors.New("eth: outgoing USDC transfer has no matching sender transaction"))
			}
			rec.Fee, err = feeOf(tx)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, rec)
	}
	sortHistory(out)
	return out, nil
}

// sortHistory упорядочивает записи от новых к старым, при равенстве по хэшу.
func sortHistory(h []domain.Tx) {
	sort.SliceStable(h, func(i, j int) bool {
		if !h[i].Time.Equal(h[j].Time) {
			return h[i].Time.After(h[j].Time)
		}
		return h[i].TxID < h[j].TxID
	})
}
