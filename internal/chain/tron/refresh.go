package tron

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

// Refresh загружает баланс актива. Для USDT это ещё баланс TRX (комиссионная
// монета) и история переводов TRC20; для TRX только баланс. Актив другой сети
// отклоняется.
func (a *Adapter) Refresh(ctx context.Context, w domain.PublicWallet, asset domain.Asset) (ports.Snapshot, error) {
	if asset != domain.USDT && asset != domain.TRX {
		return ports.Snapshot{}, domain.NewError(domain.CodeWrongNetwork, fmt.Errorf("tron: asset %q is not on TRON", string(asset)))
	}
	owner, err := ownTron(w)
	if err != nil {
		return ports.Snapshot{}, err
	}
	trx, _, accountErr := a.api.Account(ctx, owner)
	snap := ports.Snapshot{Asset: asset, FetchedAt: a.now()}
	if asset == domain.TRX {
		if accountErr != nil {
			return ports.Snapshot{}, accountErr
		}
		snap.Balance = domain.Balance{Confirmed: domain.AmountFromUint64(trx)}
		return snap, nil
	}

	contract, err := usdtContract(a.profile)
	if err != nil {
		return ports.Snapshot{}, domain.NewError(domain.CodeInternal, err)
	}
	bal, err := a.api.TRC20Balance(ctx, contract, owner)
	if err != nil {
		return ports.Snapshot{}, err
	}
	amt, err := domain.NewAmount(bal)
	if err != nil {
		return ports.Snapshot{}, bad("balance out of range")
	}
	snap.Balance = domain.Balance{Confirmed: amt}
	snap.FeeAsset = domain.TRX
	if accountErr != nil {
		snap.FeeBalanceError = string(domain.CodeOf(accountErr))
	} else {
		snap.FeeBalance = domain.AmountFromUint64(trx)
	}
	transfers, transfersErr := a.api.TRC20Transfers(ctx, contract, owner, historyLimit)
	if transfersErr != nil {
		snap.HistoryError = string(domain.CodeOf(transfersErr))
		return snap, nil
	}
	hist, historyErr := a.history(ctx, w.ID, owner, transfers)
	if historyErr != nil {
		snap.HistoryError = string(domain.CodeOf(historyErr))
		return snap, nil
	}
	snap.History = hist
	return snap, nil
}

// Resources возвращает свободные bandwidth и energy кошелька. Контракт
// ports.Snapshot полей для ресурсов не имеет, поэтому они отдаются отдельным
// методом; добавление поля в Snapshot это отдельный PR с меткой contract.
func (a *Adapter) Resources(ctx context.Context, w domain.PublicWallet) (Resources, error) {
	owner, err := ownTron(w)
	if err != nil {
		return Resources{}, err
	}
	return a.api.Resources(ctx, owner)
}

// history приводит переводы к domain.Tx. Блок и комиссия запрашиваются у
// узла только для maxInfoLookups самых новых подтверждённых переводов.
func (a *Adapter) history(ctx context.Context, walletID string, owner Addr, ts []Transfer) ([]domain.Tx, error) {
	type aggregate struct {
		in, out, self big.Int
		time time.Time
		confirmed bool
		sent bool
		onlySelf bool
		counterIn, counterOut string
		hasCounterIn, hasCounterOut bool
		mixedCounterIn, mixedCounterOut bool
	}
	selfAddress := owner.String()
	groups := make(map[string]*aggregate, len(ts))
	order := make([]string, 0, len(ts))
	for _, transfer := range ts {
		if transfer.From != selfAddress && transfer.To != selfAddress {
			continue
		}
		txid := strings.ToLower(transfer.TxID)
		group := groups[txid]
		if group == nil {
			group = &aggregate{time: transfer.Time, confirmed: transfer.Confirmed, onlySelf: true}
			groups[txid] = group
			order = append(order, txid)
		} else {
			group.confirmed = group.confirmed && transfer.Confirmed
			if transfer.Time.Before(group.time) {
				group.time = transfer.Time
			}
		}
		switch {
		case transfer.From == selfAddress && transfer.To == selfAddress:
			group.self.Add(&group.self, transfer.Value.BigInt())
			group.sent = true
		case transfer.To == selfAddress:
			group.onlySelf = false
			group.in.Add(&group.in, transfer.Value.BigInt())
			if !group.hasCounterIn {
				group.counterIn, group.hasCounterIn = transfer.From, true
			} else if group.counterIn != transfer.From {
				group.mixedCounterIn = true
			}
		case transfer.From == selfAddress:
			group.onlySelf = false
			group.sent = true
			group.out.Add(&group.out, transfer.Value.BigInt())
			if !group.hasCounterOut {
				group.counterOut, group.hasCounterOut = transfer.To, true
			} else if group.counterOut != transfer.To {
				group.mixedCounterOut = true
			}
		}
	}

	out := make([]domain.Tx, 0, len(order))
	lookups := 0
	for _, txid := range order {
		group := groups[txid]
		tx := domain.Tx{
			WalletID: walletID, Asset: domain.USDT, TxID: domain.TxID(txid),
			FeeAsset: domain.TRX, Status: domain.TxPending, Time: group.time,
		}
		amount := new(big.Int)
		switch {
		case group.onlySelf:
			tx.Direction = domain.DirectionSelf
			amount.Set(&group.self)
		default:
			net := new(big.Int).Sub(&group.in, &group.out)
			switch net.Sign() {
			case 1:
				tx.Direction = domain.DirectionIn
				amount.Set(net)
				if !group.mixedCounterIn {
					tx.Counterparty = group.counterIn
				}
			case -1:
				tx.Direction = domain.DirectionOut
				amount.Neg(net)
				if !group.mixedCounterOut {
					tx.Counterparty = group.counterOut
				}
			default:
				tx.Direction = domain.DirectionSelf
				amount.Set(&group.in)
			}
		}
		value, err := domain.NewAmount(amount)
		if err != nil {
			return nil, bad("aggregated transfer amount out of range")
		}
		tx.Amount = value

		if group.confirmed && lookups < maxInfoLookups {
			lookups++
			info, err := a.api.TxInfo(ctx, txid)
			if err != nil {
				return nil, err
			}
			if info.Found {
				status := statusOf(info)
				tx.Status, tx.BlockHeight = status.State, status.BlockHeight
				if group.sent {
					tx.Fee = domain.AmountFromUint64(info.FeeSun)
				}
			}
		} else if group.confirmed {
			// Вне лимита запросов: все события транзакции уже подтверждены.
			tx.Status = domain.TxConfirmed
		}
		out = append(out, tx)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Time.Equal(out[j].Time) {
			return out[i].Time.After(out[j].Time)
		}
		return out[i].TxID < out[j].TxID
	})
	return out, nil
}
