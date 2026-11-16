package eth

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

// Refresh загружает баланс и историю ETH или USDC. Каждый актив обновляется
// независимо: USDC не обращается к истории ETH как к отдельному активу, а
// берёт из обычных транзакций только комиссию и статус своих переводов.
//
// Любой сбой источника это ошибка всего вызова: Snapshot не умеет сообщать
// «история неполная», а выдавать неполную историю или нулевую комиссию за
// настоящие данные нельзя. Сбой одного актива не мешает обновлению другого,
// потому что вызовы независимы.
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
	wei, err := a.rpc.Balance(ctx, own)
	if err != nil {
		return ports.Snapshot{}, err
	}
	bal, err := amountOf(wei)
	if err != nil {
		return ports.Snapshot{}, err
	}
	txs, err := a.idx.NormalTxs(ctx, own)
	if err != nil {
		return ports.Snapshot{}, err
	}
	return ports.Snapshot{
		Asset:    domain.ETH,
		Balance:  domain.Balance{Confirmed: bal},
		History:  ethHistory(walletID, own, txs),
		FeeAsset: domain.ETH,
		// FeeBalance для ETH нулевой: комиссия платится из самого баланса.
		FetchedAt: a.now(),
	}, nil
}

func (a *Adapter) refreshUSDC(ctx context.Context, walletID string, own Address) (ports.Snapshot, error) {
	raw, err := a.rpc.Call(ctx, a.params.usdc, encodeBalanceOf(own))
	if err != nil {
		return ports.Snapshot{}, err
	}
	word, err := decodeWord(raw)
	if err != nil {
		return ports.Snapshot{}, badResponse(err)
	}
	bal, err := amountOf(word)
	if err != nil {
		return ports.Snapshot{}, err
	}
	// Комиссия USDC платится в ETH: его баланс показывается рядом (ADR).
	wei, err := a.rpc.Balance(ctx, own)
	if err != nil {
		return ports.Snapshot{}, err
	}
	feeBal, err := amountOf(wei)
	if err != nil {
		return ports.Snapshot{}, err
	}
	transfers, err := a.idx.TokenTransfers(ctx, own, a.params.usdc)
	if err != nil {
		return ports.Snapshot{}, err
	}
	normal, err := a.idx.NormalTxs(ctx, own)
	if err != nil {
		return ports.Snapshot{}, err
	}
	return ports.Snapshot{
		Asset:      domain.USDC,
		Balance:    domain.Balance{Confirmed: bal},
		History:    usdcHistory(walletID, own, a.params.usdc, transfers, normal),
		FeeBalance: feeBal,
		FeeAsset:   domain.ETH,
		FetchedAt:  a.now(),
	}, nil
}

func amountOf(n *big.Int) (domain.Amount, error) {
	amt, err := domain.NewAmount(n)
	if err != nil {
		return domain.Amount{}, badResponse(fmt.Errorf("eth: amount out of range: %w", err))
	}
	return amt, nil
}

// feeOf считает комиссию, которую заплатил отправитель: газ на цену газа.
func feeOf(tx IndexedTx) domain.Amount {
	fee := new(big.Int).Mul(new(big.Int).SetUint64(tx.GasUsed), tx.GasPrice)
	amt, err := domain.NewAmount(fee)
	if err != nil {
		return domain.Amount{}
	}
	return amt
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
func ethHistory(walletID string, own Address, txs []IndexedTx) []domain.Tx {
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
			rec.Fee = feeOf(tx)
			if tx.HasTo {
				rec.Counterparty = tx.To.String()
			}
		case domain.DirectionIn:
			rec.Counterparty = tx.From.String()
		case domain.DirectionSelf:
			rec.Fee = feeOf(tx)
		}
		out = append(out, rec)
	}
	sortHistory(out)
	return out
}

// usdcHistory склеивает переводы токена с обычными транзакциями. Переводы
// даёт индексатор токенов, а комиссия и статус берутся из обычной транзакции с
// тем же хэшем, и только если её отправитель это мы: чужую комиссию мы не
// платили. В одной транзакции переводов может быть несколько: запись одна на
// хэш (ключ записи это кошелёк, актив и хэш), сумма это чистое изменение
// баланса.
func usdcHistory(walletID string, own, contract Address, transfers []IndexedTransfer, normal []IndexedTx) []domain.Tx {
	byHash := make(map[string]IndexedTx, len(normal))
	for _, tx := range normal {
		byHash[tx.Hash] = tx
	}

	type agg struct {
		in, out     big.Int
		counterIn   Address
		counterOut  Address
		onlySelf    bool
		block       uint64
		time        IndexedTransfer
		haveCounter bool
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
			if g.in.Sign() == 0 && g.out.Sign() == 0 {
				g.in.Set(tr.Value)
				g.out.Set(tr.Value)
			}
		case tr.To == own:
			g.onlySelf = false
			g.in.Add(&g.in, tr.Value)
			g.counterIn = tr.From
			g.haveCounter = true
		default:
			g.onlySelf = false
			g.out.Add(&g.out, tr.Value)
			g.counterOut = tr.To
			g.haveCounter = true
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
			net = new(big.Int).Set(&g.in)
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
			continue
		}
		rec.Amount = amt
		if tx, ok := byHash[hash]; ok && tx.From == own {
			rec.Fee = feeOf(tx)
		}
		out = append(out, rec)
	}
	sortHistory(out)
	return out
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
