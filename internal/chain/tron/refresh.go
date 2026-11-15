package tron

import (
	"context"
	"fmt"

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
	trx, _, err := a.api.Account(ctx, owner)
	if err != nil {
		return ports.Snapshot{}, err
	}
	snap := ports.Snapshot{Asset: asset, FetchedAt: a.now()}
	if asset == domain.TRX {
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
	transfers, err := a.api.TRC20Transfers(ctx, contract, owner, historyLimit)
	if err != nil {
		return ports.Snapshot{}, err
	}
	hist, err := a.history(ctx, w.ID, owner, transfers)
	if err != nil {
		return ports.Snapshot{}, err
	}
	snap.Balance = domain.Balance{Confirmed: amt}
	snap.History = hist
	snap.FeeBalance = domain.AmountFromUint64(trx)
	snap.FeeAsset = domain.TRX
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
	self := owner.String()
	out := make([]domain.Tx, 0, len(ts))
	lookups := 0
	for _, t := range ts {
		tx := domain.Tx{
			WalletID: walletID, Asset: domain.USDT, TxID: domain.TxID(t.TxID), Amount: t.Value,
			Time: t.Time, FeeAsset: domain.TRX, Status: domain.TxPending,
		}
		switch {
		case t.From == self && t.To == self:
			tx.Direction = domain.DirectionSelf
		case t.From == self:
			tx.Direction, tx.Counterparty = domain.DirectionOut, t.To
		case t.To == self:
			tx.Direction, tx.Counterparty = domain.DirectionIn, t.From
		default:
			continue // перевод не касается кошелька: ответ провайдера не доверяем
		}
		if t.Confirmed && lookups < maxInfoLookups {
			lookups++
			info, err := a.api.TxInfo(ctx, t.TxID)
			if err != nil {
				return nil, err
			}
			if info.Found {
				st := statusOf(info)
				tx.Status, tx.BlockHeight = st.State, st.BlockHeight
				// Комиссию платит отправитель.
				if tx.Direction != domain.DirectionIn {
					tx.Fee = domain.AmountFromUint64(info.FeeSun)
				}
			}
		} else if t.Confirmed {
			// Вне лимита запросов: перевод есть в списке подтверждённых.
			tx.Status = domain.TxConfirmed
		}
		out = append(out, tx)
	}
	return out, nil
}
