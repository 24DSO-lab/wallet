package service

import (
	"context"
	"errors"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

// PrepareRBF creates a fresh, independently verified replacement draft for an
// accepted, unconfirmed Bitcoin transaction. It never accesses private keys.
func (s *Service) PrepareRBF(ctx context.Context, walletID string, txid domain.TxID) (ports.Replacement, error) {
	unlockData, lockErr := s.lockDataRead()
	if lockErr != nil {
		return ports.Replacement{}, lockErr
	}
	defer unlockData()

	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	wallet, err := s.store.Wallets().Get(ctx, walletID)
	if err != nil { return ports.Replacement{}, err }
	original, err := s.store.PendingTxs().Get(ctx, txid)
	if err != nil { return ports.Replacement{}, err }
	if original.WalletID != walletID || original.Network != domain.NetworkBitcoin || original.Asset != domain.BTC || !original.BroadcastAccepted {
		return ports.Replacement{}, domain.NewError(domain.CodeInvalidInput, errors.New("transaction is not an accepted Bitcoin transaction for this wallet"))
	}
	if err := s.rejectExistingReplacement(ctx, original.TxID); err != nil { return ports.Replacement{}, err }
	chain := s.chains[domain.NetworkBitcoin]
	adapter, ok := chain.(ports.RBFAdapter)
	if !ok { return ports.Replacement{}, domain.NewError(domain.CodeProviderUnavailable, errors.New("bitcoin adapter does not support RBF")) }
	replacement, err := adapter.PrepareReplacement(ctx, wallet.PublicWallet, original)
	if err != nil { return ports.Replacement{}, err }
	if replacement.ReplacesTxID != original.TxID || replacement.Intent.Wallet.ID != walletID ||
		replacement.Prepared.Network != domain.NetworkBitcoin || replacement.Prepared.Asset != domain.BTC {
		return ports.Replacement{}, domain.NewError(domain.CodeVerificationFailed, errors.New("RBF adapter returned an inconsistent replacement"))
	}
	if err := adapter.VerifyReplacement(ctx, wallet.PublicWallet, original, replacement); err != nil { return ports.Replacement{}, err }
	return replacement, nil
}

// ConfirmRBF re-verifies the replacement immediately before signing, persists
// the signed recovery payload before broadcast, and only retires the old
// pending record after the provider accepts the replacement.
func (s *Service) ConfirmRBF(ctx context.Context, walletID string, replacement ports.Replacement, sec ports.Secrets) (domain.TxID, error) {
	unlockData, lockErr := s.lockDataWrite()
	if lockErr != nil {
		wipeSecrets(sec)
		return "", lockErr
	}
	defer unlockData()

	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	defer wipeSecrets(sec)
	if walletID == "" || replacement.Intent.Wallet.ID != walletID || replacement.ReplacesTxID == "" {
		return "", domain.NewError(domain.CodeInvalidInput, errors.New("invalid RBF confirmation"))
	}
	wallet, err := s.store.Wallets().Get(ctx, walletID)
	if err != nil { return "", err }
	original, err := s.store.PendingTxs().Get(ctx, replacement.ReplacesTxID)
	if err != nil { return "", err }
	if original.WalletID != walletID || original.Network != domain.NetworkBitcoin || original.Asset != domain.BTC || !original.BroadcastAccepted {
		return "", domain.NewError(domain.CodeInvalidInput, errors.New("original transaction is not eligible for replacement"))
	}
	if err := s.rejectExistingReplacement(ctx, original.TxID); err != nil { return "", err }
	chain := s.chains[domain.NetworkBitcoin]
	adapter, ok := chain.(ports.RBFAdapter)
	if !ok { return "", domain.NewError(domain.CodeProviderUnavailable, errors.New("bitcoin adapter does not support RBF")) }
	if err := adapter.VerifyReplacement(ctx, wallet.PublicWallet, original, replacement); err != nil { return "", err }
	sigs, err := s.keystore.Sign(ctx, wallet.KeystoreID, sec, replacement.Prepared.Digests)
	if err != nil { return "", err }
	signed, err := chain.Finalize(replacement.Prepared, sigs)
	if err != nil { return "", err }
	if signed.Network != domain.NetworkBitcoin || signed.TxID == "" || len(signed.Raw) == 0 || signed.TxID == original.TxID {
		return "", domain.NewError(domain.CodeVerificationFailed, errors.New("adapter returned invalid RBF transaction"))
	}
	now := s.now().UTC()
	next := ports.PendingTx{TxID:signed.TxID, WalletID:walletID, Asset:domain.BTC, Network:domain.NetworkBitcoin,
		Raw:append([]byte(nil),signed.Raw...), Draft:append([]byte(nil),replacement.Prepared.Draft...),
		ReplacesTxID:original.TxID, CreatedAt:now}
	history := domain.Tx{WalletID:walletID, Asset:domain.BTC, TxID:signed.TxID, Direction:domain.DirectionOut,
		Amount:replacement.Intent.Amount, Fee:replacement.Prepared.Fee, FeeAsset:domain.BTC, Status:domain.TxPending,
		Time:now, Counterparty:replacement.Intent.To}
	if err := s.store.WithTx(ctx, func(txctx context.Context, tx ports.Store) error {
		if err := tx.PendingTxs().Put(txctx,next); err != nil { return err }
		return tx.Transactions().Upsert(txctx,[]domain.Tx{history})
	}); err != nil { return "", err }
	got, err := chain.Broadcast(ctx,signed)
	if err != nil { return signed.TxID, domain.NewError(domain.CodeBroadcastUnknown,contextError(err)) }
	if got != signed.TxID { return signed.TxID, domain.NewError(domain.CodeBroadcastUnknown,errors.New("broadcast transaction id differs from locally finalized id")) }
	next.BroadcastAccepted = true
	if err := s.store.WithTx(ctx,func(txctx context.Context,tx ports.Store) error {
		oldHistory, err := tx.Transactions().Get(txctx,walletID,domain.BTC,original.TxID)
		if err != nil { return err }
		oldHistory.Status = domain.TxReplaced
		if err := tx.Transactions().Upsert(txctx,[]domain.Tx{oldHistory}); err != nil { return err }
		if err := tx.PendingTxs().Put(txctx,next); err != nil { return err }
		return tx.PendingTxs().Delete(txctx,original.TxID)
	}); err != nil {
		s.log.Error("RBF broadcast succeeded but old transaction retirement could not be persisted","wallet_id",walletID,"old_txid",original.TxID,"new_txid",signed.TxID,"error",err)
	}
	return signed.TxID,nil
}

// HasRBF reports whether a pending record can be offered for replacement.
func (s *Service) HasRBF(ctx context.Context, walletID string, txid domain.TxID) bool {
	unlockData, lockErr := s.lockDataRead()
	if lockErr != nil {
		return false
	}
	defer unlockData()

	p, err := s.store.PendingTxs().Get(ctx,txid)
	if err != nil || p.WalletID != walletID || p.Asset != domain.BTC || p.Network != domain.NetworkBitcoin || !p.BroadcastAccepted { return false }
	if _, ok := s.chains[domain.NetworkBitcoin].(ports.RBFAdapter); !ok {
		return false
	}
	return s.rejectExistingReplacement(ctx, p.TxID) == nil
}

func (s *Service) rejectExistingReplacement(ctx context.Context, original domain.TxID) error {
	pending, err := s.store.PendingTxs().List(ctx)
	if err != nil { return err }
	for _, item := range pending {
		if item.ReplacesTxID == original {
			return domain.NewError(domain.CodeDuplicateSend, errors.New("a replacement attempt for this Bitcoin transaction is already pending reconciliation"))
		}
	}
	return nil
}
