package app

import (
	"context"

	"github.com/24DSO-lab/wallet/internal/domain"
)

func (a *App) GetReceiveAddress(id, asset string) (string, error) {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return "", err
	}
	if err := a.requireUnlocked(); err != nil {
		return "", err
	}
	domainAsset, err := parseReceiveAsset(asset)
	if err != nil {
		return "", err
	}
	// BTC uses a receive-only BIP84 gap-limit lookup. It must not trigger a
	// full balance/history/UTXO refresh from this interactive screen.
	ctx := a.context()
	if domainAsset == domain.BTC {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, btcReceiveLookupTimeout)
		defer cancel()
	}
	address, err := a.currentService().GetReceiveAddress(ctx, id, domainAsset)
	if err != nil {
		return "", safeError(err)
	}
	return address.Address, nil
}

func (a *App) GetHistory(id, asset string) ([]TxDTO, error) {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return nil, err
	}
	if err := a.requireUnlocked(); err != nil {
		return nil, err
	}
	var domainAsset domain.Asset
	if asset != "" {
		var err error
		domainAsset, err = parseAsset(asset)
		if err != nil {
			return nil, err
		}
	}
	// Refresh the status of locally retained signed transactions before
	// returning cached history. Provider failures do not hide existing history.
	_, _ = a.currentService().ReconcilePending(a.context())
	txs, err := a.currentService().GetHistory(a.context(), id, domainAsset)
	if err != nil {
		return nil, safeError(err)
	}
	out := make([]TxDTO, 0, len(txs))
	for _, tx := range txs {
		dto := txDTO(tx)
		dto.CanReplace = a.currentService().HasRBF(a.context(), id, tx.TxID)
		dto.ExplorerURL = transactionExplorerURL(a.activeProfile(), tx)
		out = append(out, dto)
	}
	return out, nil
}

func (a *App) RefreshWallet(id, asset string) (BalanceDTO, error) {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return BalanceDTO{}, err
	}
	if err := a.requireUnlocked(); err != nil {
		return BalanceDTO{}, err
	}
	da, err := parseReceiveAsset(asset)
	if err != nil {
		return BalanceDTO{}, err
	}
	ctx := a.context()
	if da == domain.BTC {
		// Bitcoin refresh performs sequential explorer requests for the BIP84
		// gap-limit scan. Bound the UI operation just like receive-address lookup.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, btcReceiveAddressTimeout)
		defer cancel()
	}
	snapshot, err := a.currentService().RefreshWallet(ctx, id, da)
	if err != nil {
		return BalanceDTO{}, safeError(err)
	}
	return snapshotDTO(snapshot), nil
}
