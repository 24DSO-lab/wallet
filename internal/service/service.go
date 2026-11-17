// Package service orchestrates wallet use cases over the domain ports.
// It deliberately contains no Wails DTOs: those belong to the separately
// reviewed application contract.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-lab/wallet/internal/config"
	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
)

var (
	ErrNilStore     = errors.New("service: store is required")
	ErrNilKeystore  = errors.New("service: keystore is required")
	ErrChainMissing = errors.New("service: chain adapter is not configured")
)

// Option configures a Service. Options are applied only during construction.
type Option func(*options)

type options struct {
	logger *slog.Logger
	now    func() time.Time
	newID  func() (string, error)
}

// WithLogger sets the structured logger. A discard logger is used by default.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// WithClock replaces the clock, primarily for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(o *options) {
		if now != nil {
			o.now = now
		}
	}
}

// WithIDGenerator replaces the cryptographically-random wallet ID generator.
// Production callers should normally use the default generator.
func WithIDGenerator(generate func() (string, error)) Option {
	return func(o *options) {
		if generate != nil {
			o.newID = generate
		}
	}
}

// Service is safe for concurrent use. Wallet creation/import is serialized so
// concurrent calls cannot exceed the configured wallet limit.
type Service struct {
	store       ports.Store
	keystore    ports.Keystore
	chains      map[domain.Network]ports.ChainAdapter
	log         *slog.Logger
	now         func() time.Time
	newID       func() (string, error)
	lifecycle   sync.Mutex
	sendMu      sync.Mutex // serializes preparation/confirmation around unresolved broadcasts
	reconcileMu sync.Mutex // prevents duplicate status reconciliation of the same pending records
}

// New validates and copies all dependencies. A chain map entry must be keyed
// by the same network reported by the adapter.
func New(st ports.Store, ks ports.Keystore, chains []ports.ChainAdapter, opts ...Option) (*Service, error) {
	if st == nil {
		return nil, ErrNilStore
	}
	if ks == nil {
		return nil, ErrNilKeystore
	}
	o := options{
		logger: slog.New(slog.NewTextHandler(discardWriter{}, nil)),
		now:    time.Now,
		newID:  randomID,
	}
	for _, apply := range opts {
		if apply != nil {
			apply(&o)
		}
	}
	adapters := make(map[domain.Network]ports.ChainAdapter, len(chains))
	for _, chain := range chains {
		if chain == nil || !chain.Network().Valid() {
			return nil, fmt.Errorf("service: invalid chain adapter")
		}
		n := chain.Network()
		if _, exists := adapters[n]; exists {
			return nil, fmt.Errorf("service: duplicate chain adapter for %s", n)
		}
		adapters[n] = chain
	}
	return &Service{store: st, keystore: ks, chains: adapters, log: o.logger, now: o.now, newID: o.newID}, nil
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("service: generate wallet id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// ListWallets returns public wallet records in the repository's display order.
func (s *Service) ListWallets(ctx context.Context) ([]domain.Wallet, error) {
	if err := ctx.Err(); err != nil {
		return nil, contextError(err)
	}
	return s.store.Wallets().List(ctx)
}

// NewMnemonic generates an unpersisted BIP-39 phrase for the two-phase
// desktop creation flow. The caller owns the returned secret and must wipe it.
func (s *Service) NewMnemonic(words int) (secret.Bytes, error) {
	if words != 12 && words != 24 {
		return secret.Bytes{}, domain.NewError(domain.CodeInvalidInput, errors.New("mnemonic length must be 12 or 24 words"))
	}
	return s.keystore.NewMnemonic(words)
}

// CreateWallet creates a wallet and returns its mnemonic exactly once as a
// secret.Bytes value. The caller owns the returned secret and MUST call Wipe.
// Password and passphrase are consumed and wiped by this method on every path.
func (s *Service) CreateWallet(ctx context.Context, name string, words int, sec ports.Secrets) (domain.Wallet, secret.Bytes, error) {
	defer wipeSecrets(sec)
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.Wallet{}, secret.Bytes{}, domain.NewError(domain.CodeInvalidInput, errors.New("wallet name is empty"))
	}
	if words != 12 && words != 24 {
		return domain.Wallet{}, secret.Bytes{}, domain.NewError(domain.CodeInvalidInput, errors.New("mnemonic length must be 12 or 24 words"))
	}
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Wallet{}, secret.Bytes{}, contextError(err)
	}
	wallets, err := s.store.Wallets().List(ctx)
	if err != nil {
		return domain.Wallet{}, secret.Bytes{}, err
	}
	if len(wallets) >= config.MaxWallets {
		return domain.Wallet{}, secret.Bytes{}, domain.NewError(domain.CodeWalletLimit, errors.New("maximum wallet count reached"))
	}
	mnemonic, err := s.keystore.NewMnemonic(words)
	if err != nil {
		return domain.Wallet{}, secret.Bytes{}, err
	}
	defer mnemonic.Wipe()
	wallet, err := s.persistWallet(ctx, name, mnemonic, sec)
	if err != nil {
		return domain.Wallet{}, secret.Bytes{}, err
	}
	return wallet, mnemonic.Clone(), nil
}

// ImportWallet imports a BIP-39 mnemonic. Ownership of mnemonic and sec is
// transferred to this call; all secret buffers are wiped before it returns.
func (s *Service) ImportWallet(ctx context.Context, name string, mnemonic secret.Bytes, sec ports.Secrets) (domain.Wallet, error) {
	defer mnemonic.Wipe()
	defer wipeSecrets(sec)
	name = strings.TrimSpace(name)
	if name == "" || mnemonic.IsEmpty() {
		return domain.Wallet{}, domain.NewError(domain.CodeInvalidInput, errors.New("wallet name and mnemonic are required"))
	}
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Wallet{}, contextError(err)
	}
	wallets, err := s.store.Wallets().List(ctx)
	if err != nil {
		return domain.Wallet{}, err
	}
	if len(wallets) >= config.MaxWallets {
		return domain.Wallet{}, domain.NewError(domain.CodeWalletLimit, errors.New("maximum wallet count reached"))
	}
	return s.persistWallet(ctx, name, mnemonic, sec)
}

func (s *Service) persistWallet(ctx context.Context, name string, mnemonic secret.Bytes, sec ports.Secrets) (domain.Wallet, error) {
	id, err := s.newID()
	if err != nil {
		return domain.Wallet{}, err
	}
	if id == "" {
		return domain.Wallet{}, domain.NewError(domain.CodeInternal, errors.New("wallet id generator returned an empty id"))
	}
	keys, err := s.keystore.Save(ctx, id, mnemonic, sec)
	if err != nil {
		return domain.Wallet{}, err
	}
	if keys.BTCXpub == "" || keys.ETHAddress == "" || keys.TronAddress == "" {
		rollbackErr := s.removeKeystoreFile(ctx, id, sec.Password)
		return domain.Wallet{}, errors.Join(domain.NewError(domain.CodeInternal, errors.New("keystore returned incomplete public wallet data")), rollbackErr)
	}
	wallets, err := s.store.Wallets().List(ctx)
	if err != nil {
		rollbackErr := s.removeKeystoreFile(ctx, id, sec.Password)
		return domain.Wallet{}, errors.Join(err, rollbackErr)
	}
	wallet := domain.Wallet{
		PublicWallet: domain.PublicWallet{ID: id, Name: name, BTCXpub: keys.BTCXpub, ETHAddress: keys.ETHAddress, TronAddress: keys.TronAddress},
		KeystoreID:   id, CreatedAt: s.now().UTC(), SortOrder: len(wallets),
	}
	// Keep this transaction explicit even though the SQLite WalletRepo also
	// materializes the two account addresses: ports.Store implementations must
	// all expose the same postcondition (wallet plus ETH/TRON receive addresses).
	err = s.store.WithTx(ctx, func(txctx context.Context, tx ports.Store) error {
		if err := tx.Wallets().Create(txctx, wallet); err != nil {
			return err
		}
		return tx.Addresses().Upsert(txctx, []domain.Address{
			{WalletID: id, Network: domain.NetworkEthereum, Address: keys.ETHAddress, Path: "m/44'/60'/0'/0/0", Chain: domain.ChainReceive, Index: 0},
			{WalletID: id, Network: domain.NetworkTron, Address: keys.TronAddress, Path: "m/44'/195'/0'/0/0", Chain: domain.ChainReceive, Index: 0},
		})
	})
	if err != nil {
		rollbackErr := s.removeKeystoreFile(ctx, id, sec.Password)
		return domain.Wallet{}, errors.Join(err, rollbackErr)
	}
	s.log.Info("wallet created", "wallet_id", id)
	return wallet, nil
}

// removeKeystoreFile compensates a failed wallet creation. Cleanup is allowed
// to finish even when the caller's context was canceled after Save succeeded.
func (s *Service) removeKeystoreFile(ctx context.Context, id string, password secret.Bytes) error {
	cleanupPassword := password.Clone()
	defer cleanupPassword.Wipe()
	return s.keystore.Delete(context.WithoutCancel(ctx), id, ports.Secrets{Password: cleanupPassword})
}

// DeleteWallet removes a wallet only after the caller confirms its exact name
// and proves knowledge of the keystore password. The encrypted keystore is
// deleted first; if the database deletion fails, the mnemonic is re-encrypted
// under the same wallet ID before the error is returned. The mnemonic and all
// temporary secret copies are wiped on every path.
func (s *Service) DeleteWallet(ctx context.Context, id, confirmedName string, sec ports.Secrets) error {
	defer wipeSecrets(sec)
	if strings.TrimSpace(id) == "" || strings.TrimSpace(confirmedName) == "" {
		return domain.NewError(domain.CodeInvalidInput, errors.New("wallet id and confirmation name are required"))
	}
	wallet, err := s.store.Wallets().Get(ctx, id)
	if err != nil {
		return err
	}
	if confirmedName != wallet.Name {
		return domain.NewError(domain.CodeInvalidInput, errors.New("wallet name confirmation does not match"))
	}

	revealSecrets := cloneSecrets(sec)
	defer wipeSecrets(revealSecrets)
	mnemonic, err := s.keystore.RevealSeed(ctx, wallet.KeystoreID, revealSecrets)
	if err != nil {
		return err
	}
	defer mnemonic.Wipe()

	deleteSecrets := cloneSecrets(sec)
	defer wipeSecrets(deleteSecrets)
	if deleteErr := s.keystore.Delete(ctx, wallet.KeystoreID, deleteSecrets); deleteErr != nil {
		// Delete may report a directory-sync error after unlinking the file.
		// Check with a non-cancelled context before deciding whether to restore.
		checkSecrets := cloneSecrets(sec)
		checkMnemonic, checkErr := s.keystore.RevealSeed(context.WithoutCancel(ctx), wallet.KeystoreID, checkSecrets)
		wipeSecrets(checkSecrets)
		checkMnemonic.Wipe()
		if checkErr == nil {
			return deleteErr
		}
		if restoreErr := s.restoreKeystore(ctx, wallet, mnemonic, sec); restoreErr != nil {
			s.log.Error("wallet deletion rollback failed", "wallet_id", id, "error", restoreErr)
			return errors.Join(deleteErr, fmt.Errorf("restore keystore after failed delete: %w", restoreErr))
		}
		return deleteErr
	}

	if err := s.store.Wallets().Delete(ctx, id); err != nil {
		// The database is the public source of truth. Restore the encrypted
		// keystore if its corresponding wallet row could not be removed.
		if restoreErr := s.restoreKeystore(ctx, wallet, mnemonic, sec); restoreErr != nil {
			s.log.Error("wallet deletion rollback failed", "wallet_id", id, "error", restoreErr)
			return errors.Join(err, fmt.Errorf("restore keystore after failed wallet deletion: %w", restoreErr))
		}
		return err
	}
	s.log.Info("wallet deleted", "wallet_id", id)
	return nil
}

// restoreKeystore re-encrypts a previously revealed mnemonic after a partial
// wallet deletion. It uses an uncancelled context because leaving a wallet row
// without its keystore is worse than completing this local rollback.
func (s *Service) restoreKeystore(ctx context.Context, wallet domain.Wallet, mnemonic secret.Bytes, sec ports.Secrets) error {
	restoreSecrets := cloneSecrets(sec)
	defer wipeSecrets(restoreSecrets)
	keys, err := s.keystore.Save(context.WithoutCancel(ctx), wallet.KeystoreID, mnemonic, restoreSecrets)
	if err != nil {
		return err
	}
	if keys.BTCXpub != wallet.BTCXpub || keys.ETHAddress != wallet.ETHAddress || keys.TronAddress != wallet.TronAddress {
		return errors.New("restored keystore public keys do not match the wallet record")
	}
	return nil
}

// cloneSecrets creates short-lived independent buffers for a keystore call.
func cloneSecrets(sec ports.Secrets) ports.Secrets {
	return ports.Secrets{Password: sec.Password.Clone(), Passphrase: sec.Passphrase.Clone()}
}

// RenameWallet changes only the public display name.
func (s *Service) RenameWallet(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if id == "" || name == "" {
		return domain.NewError(domain.CodeInvalidInput, errors.New("wallet id and name are required"))
	}
	return s.store.Wallets().Rename(ctx, id, name)
}

// VerifyPassword verifies the application password without retaining it.
func (s *Service) VerifyPassword(ctx context.Context, password secret.Bytes) error {
	defer password.Wipe()
	return s.keystore.Verify(ctx, ports.Secrets{Password: password})
}

// ChangePassword atomically re-encrypts all keystore files. Both inputs are
// consumed and wiped by this method.
func (s *Service) ChangePassword(ctx context.Context, oldPassword, newPassword secret.Bytes) error {
	defer oldPassword.Wipe()
	defer newPassword.Wipe()
	return s.keystore.ChangePassword(ctx, oldPassword, newPassword)
}

// GetReceiveAddress returns the next unused address for the selected asset.
func (s *Service) GetReceiveAddress(ctx context.Context, walletID string, asset domain.Asset) (domain.Address, error) {
	info, ok := asset.Info()
	if !ok || (!asset.IsWalletAsset() && asset != domain.TRX) {
		return domain.Address{}, domain.NewError(domain.CodeInvalidInput, domain.ErrUnknownAsset)
	}
	wallet, err := s.store.Wallets().Get(ctx, walletID)
	if err != nil {
		return domain.Address{}, err
	}

	// BTC must be checked against the provider on every request. A locally
	// cached address marked unused may already have received an on-chain
	// transaction since the last balance refresh.
	if asset == domain.BTC {
		chain := s.chains[info.Network]
		receiver, supported := chain.(ports.ReceiveAddressAdapter)
		if !supported {
			return domain.Address{}, domain.NewError(domain.CodeProviderUnavailable, ErrChainMissing)
		}
		address, err := receiver.ReceiveAddress(ctx, wallet.PublicWallet, asset)
		if err != nil {
			return domain.Address{}, err
		}
		if address.WalletID != walletID || address.Network != info.Network ||
			address.Chain != domain.ChainReceive || address.Address == "" || address.Path == "" ||
			chain.ValidateAddress(address.Address) != nil {
			return domain.Address{}, domain.NewError(domain.CodeProviderBadResponse, errors.New("adapter returned an invalid receive address"))
		}
		if err := s.store.Addresses().Upsert(ctx, []domain.Address{address}); err != nil {
			return domain.Address{}, err
		}
		return address, nil
	}

	addresses, err := s.store.Addresses().List(ctx, walletID, info.Network)
	if err != nil {
		return domain.Address{}, err
	}
	for _, address := range addresses {
		if !address.Used {
			return address, nil
		}
	}
	return domain.Address{}, domain.NewError(domain.CodeNotFound, domain.ErrNotFound)
}

// GetBalances returns all cached wallet-asset balances plus the TRX fee
// balance. Missing cache entries are omitted; callers can distinguish an
// unrefreshed asset from a zero balance.
func (s *Service) GetBalances(ctx context.Context, walletID string) (map[domain.Asset]ports.BalanceRecord, error) {
	if _, err := s.store.Wallets().Get(ctx, walletID); err != nil {
		return nil, err
	}
	assets := []domain.Asset{domain.BTC, domain.ETH, domain.USDC, domain.USDT, domain.TRX}
	out := make(map[domain.Asset]ports.BalanceRecord, len(assets))
	for _, asset := range assets {
		record, err := s.store.Balances().Get(ctx, walletID, asset)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[asset] = record
	}
	return out, nil
}

// RevealSeed decrypts a wallet mnemonic for an explicit backup/recovery flow.
// The returned secret belongs to the caller and MUST be wiped. Password and
// passphrase inputs are consumed and wiped by this method.
func (s *Service) RevealSeed(ctx context.Context, walletID string, sec ports.Secrets) (secret.Bytes, error) {
	defer wipeSecrets(sec)
	wallet, err := s.store.Wallets().Get(ctx, walletID)
	if err != nil {
		return secret.Bytes{}, err
	}
	return s.keystore.RevealSeed(ctx, wallet.KeystoreID, sec)
}

// GetHistory returns cached history for one wallet and asset. An empty asset
// returns all assets, as defined by ports.TransactionRepo.
func (s *Service) GetHistory(ctx context.Context, walletID string, asset domain.Asset) ([]domain.Tx, error) {
	if _, err := s.store.Wallets().Get(ctx, walletID); err != nil {
		return nil, err
	}
	if asset != "" && !asset.IsWalletAsset() {
		return nil, domain.NewError(domain.CodeInvalidInput, domain.ErrUnknownAsset)
	}
	return s.store.Transactions().List(ctx, walletID, asset)
}

// RefreshWallet refreshes one asset and persists the complete provider snapshot
// atomically. Provider data is treated as untrusted and must already have been
// validated by the network adapter.
func (s *Service) RefreshWallet(ctx context.Context, walletID string, asset domain.Asset) (ports.Snapshot, error) {
	info, ok := asset.Info()
	if !ok || (!asset.IsWalletAsset() && asset != domain.TRX) {
		return ports.Snapshot{}, domain.NewError(domain.CodeInvalidInput, domain.ErrUnknownAsset)
	}
	wallet, err := s.store.Wallets().Get(ctx, walletID)
	if err != nil {
		return ports.Snapshot{}, err
	}
	chain := s.chains[info.Network]
	if chain == nil {
		return ports.Snapshot{}, domain.NewError(domain.CodeProviderUnavailable, ErrChainMissing)
	}
	snapshot, err := chain.Refresh(ctx, wallet.PublicWallet, asset)
	if err != nil {
		return ports.Snapshot{}, err
	}
	if snapshot.Asset != asset {
		return ports.Snapshot{}, domain.NewError(domain.CodeProviderBadResponse, errors.New("adapter returned a snapshot for a different asset"))
	}
	if snapshot.FeeAsset != "" && snapshot.FeeAsset != info.FeeAsset {
		return ports.Snapshot{}, domain.NewError(domain.CodeProviderBadResponse, errors.New("adapter returned an unexpected fee asset"))
	}
	for i := range snapshot.Addresses {
		if snapshot.Addresses[i].WalletID != walletID || snapshot.Addresses[i].Network != info.Network || chain.ValidateAddress(snapshot.Addresses[i].Address) != nil {
			return ports.Snapshot{}, domain.NewError(domain.CodeProviderBadResponse, errors.New("adapter returned an invalid wallet address"))
		}
	}
	for i := range snapshot.History {
		tx := snapshot.History[i]
		if tx.WalletID != walletID || tx.Asset != asset || tx.TxID == "" || !tx.Status.Valid() {
			return ports.Snapshot{}, domain.NewError(domain.CodeProviderBadResponse, errors.New("adapter returned invalid transaction history"))
		}
		switch tx.Direction {
		case domain.DirectionIn, domain.DirectionOut, domain.DirectionSelf:
		default:
			return ports.Snapshot{}, domain.NewError(domain.CodeProviderBadResponse, errors.New("adapter returned an invalid transaction direction"))
		}
	}
	if asset != domain.BTC && len(snapshot.UTXOs) != 0 {
		return ports.Snapshot{}, domain.NewError(domain.CodeProviderBadResponse, errors.New("adapter returned UTXOs for a non-Bitcoin asset"))
	}
	for i := range snapshot.UTXOs {
		utxo := snapshot.UTXOs[i]
		if utxo.WalletID != walletID || utxo.TxID == "" || utxo.Amount.IsZero() || chain.ValidateAddress(utxo.Address) != nil {
			return ports.Snapshot{}, domain.NewError(domain.CodeProviderBadResponse, errors.New("adapter returned an invalid Bitcoin UTXO"))
		}
	}
	if snapshot.FetchedAt.IsZero() {
		snapshot.FetchedAt = s.now().UTC()
	}
	err = s.store.WithTx(ctx, func(txctx context.Context, tx ports.Store) error {
		if err := tx.Addresses().Upsert(txctx, snapshot.Addresses); err != nil {
			return err
		}
		if err := tx.Balances().Put(txctx, walletID, asset, snapshot.Balance, snapshot.FetchedAt); err != nil {
			return err
		}
		if asset == domain.BTC {
			if err := tx.UTXOs().Replace(txctx, walletID, snapshot.UTXOs); err != nil {
				return err
			}
		}
		if err := tx.Transactions().Upsert(txctx, snapshot.History); err != nil {
			return err
		}
		if snapshot.FeeAsset != "" && snapshot.FeeAsset != asset {
			feeBalance := domain.Balance{Confirmed: snapshot.FeeBalance}
			if err := tx.Balances().Put(txctx, walletID, snapshot.FeeAsset, feeBalance, snapshot.FetchedAt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ports.Snapshot{}, err
	}
	return snapshot, nil
}

// PrepareSend creates and verifies an unsigned draft. It never asks for a
// password and never has access to private keys.
func (s *Service) PrepareSend(ctx context.Context, intent domain.SendIntent) (ports.Prepared, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if err := intent.Validate(); err != nil {
		return ports.Prepared{}, err
	}
	wallet, err := s.store.Wallets().Get(ctx, intent.Wallet.ID)
	if err != nil {
		return ports.Prepared{}, err
	}
	intent.Wallet = wallet.PublicWallet
	info, _ := intent.Asset.Info()
	// Resolve previously ambiguous sends before deciding whether a new send is safe.
	_, _ = s.ReconcilePending(ctx)
	if err := s.rejectPendingBroadcast(ctx, wallet.ID, info.Network); err != nil {
		return ports.Prepared{}, err
	}
	chain := s.chains[info.Network]
	if chain == nil {
		return ports.Prepared{}, domain.NewError(domain.CodeProviderUnavailable, ErrChainMissing)
	}
	if err := chain.ValidateAddress(intent.To); err != nil {
		return ports.Prepared{}, domain.NewError(domain.CodeInvalidAddress, err)
	}
	prepared, err := chain.Prepare(ctx, intent)
	if err != nil {
		return ports.Prepared{}, err
	}
	if prepared.Network != info.Network || prepared.Asset != intent.Asset {
		return ports.Prepared{}, domain.NewError(domain.CodeVerificationFailed, errors.New("adapter prepared a different network or asset"))
	}
	if err := chain.Verify(prepared, intent); err != nil {
		return ports.Prepared{}, err
	}
	return prepared, nil
}

// rejectPendingBroadcast prevents a new send from reusing a nonce or UTXO
// while a previous signed transaction has an unresolved broadcast outcome.
func (s *Service) rejectPendingBroadcast(ctx context.Context, walletID string, network domain.Network) error {
	pending, err := s.store.PendingTxs().List(ctx)
	if err != nil {
		return err
	}
	for _, tx := range pending {
		if tx.WalletID == walletID && tx.Network == network && !tx.BroadcastAccepted {
			return domain.NewError(domain.CodeDuplicateSend, errors.New("wallet has an unresolved broadcast on this network"))
		}
	}
	return nil
}

// ConfirmSend re-verifies the exact draft immediately before signing, then
// stores the signed transaction and unsigned draft before broadcasting. The
// recovery record remains until confirmation so an accepted BTC transaction
// can be replaced by fee; ambiguous broadcasts are never blindly retried.
// Password and passphrase are consumed and wiped by this method.
func (s *Service) ConfirmSend(ctx context.Context, walletID string, intent domain.SendIntent, prepared ports.Prepared, sec ports.Secrets) (domain.TxID, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	defer wipeSecrets(sec)
	if walletID == "" || intent.Wallet.ID != walletID {
		return "", domain.NewError(domain.CodeInvalidInput, errors.New("wallet id does not match send intent"))
	}
	wallet, err := s.store.Wallets().Get(ctx, walletID)
	if err != nil {
		return "", err
	}
	intent.Wallet = wallet.PublicWallet
	info, ok := intent.Asset.Info()
	if !ok || !intent.Asset.IsWalletAsset() {
		return "", domain.NewError(domain.CodeInvalidInput, domain.ErrUnknownAsset)
	}
	if err := s.rejectPendingBroadcast(ctx, walletID, info.Network); err != nil {
		return "", err
	}
	chain := s.chains[info.Network]
	if chain == nil {
		return "", domain.NewError(domain.CodeProviderUnavailable, ErrChainMissing)
	}
	if err := intent.Validate(); err != nil {
		return "", err
	}
	if err := chain.ValidateAddress(intent.To); err != nil {
		return "", domain.NewError(domain.CodeInvalidAddress, err)
	}
	if err := chain.Verify(prepared, intent); err != nil {
		return "", err
	}
	signatures, err := s.keystore.Sign(ctx, wallet.KeystoreID, sec, prepared.Digests)
	if err != nil {
		return "", err
	}
	signed, err := chain.Finalize(prepared, signatures)
	if err != nil {
		return "", err
	}
	if signed.Network != info.Network || signed.TxID == "" || len(signed.Raw) == 0 {
		return "", domain.NewError(domain.CodeVerificationFailed, errors.New("adapter returned invalid signed transaction"))
	}
	now := s.now().UTC()
	pending := ports.PendingTx{TxID: signed.TxID, WalletID: walletID, Asset: intent.Asset, Network: info.Network, Raw: append([]byte(nil), signed.Raw...), Draft: append([]byte(nil), prepared.Draft...), CreatedAt: now}
	history := domain.Tx{
		WalletID: walletID, Asset: intent.Asset, TxID: signed.TxID, Direction: domain.DirectionOut,
		Amount: intent.Amount, Fee: prepared.Fee, FeeAsset: prepared.FeeAsset, Status: domain.TxPending,
		Time: now, Counterparty: intent.To,
	}
	// Persist the recoverable signed payload and the visible pending history in
	// one database transaction before the non-idempotent network request.
	if err := s.store.WithTx(ctx, func(txctx context.Context, tx ports.Store) error {
		if err := tx.PendingTxs().Put(txctx, pending); err != nil {
			return err
		}
		return tx.Transactions().Upsert(txctx, []domain.Tx{history})
	}); err != nil {
		return "", err
	}
	txid, err := chain.Broadcast(ctx, signed)
	if err != nil {
		// The signed transaction is already durable in pending_tx. Return its
		// locally computed ID so the Wails facade can consume the draft and the
		// UI can reconcile status instead of signing/broadcasting it again.
		return signed.TxID, domain.NewError(domain.CodeBroadcastUnknown, contextError(err))
	}
	if txid != signed.TxID {
		return signed.TxID, domain.NewError(domain.CodeBroadcastUnknown, errors.New("broadcast transaction id differs from locally finalized id"))
	}
	pending.BroadcastAccepted = true
	if err := s.store.PendingTxs().Put(ctx, pending); err != nil {
		s.log.Error("broadcast succeeded but acceptance state could not be persisted", "wallet_id", walletID, "error", err)
		// The transaction is already in the network. Keep the conservative
		// unresolved state; a later status lookup will mark it accepted.
	}
	return txid, nil
}

// ReconcilePending checks every locally retained signed transaction against
// its network adapter. Confirmed and failed transactions are written to the
// history and their recovery payloads are deleted in the same database
// transaction. A still-pending transaction is marked accepted and retained
// for a possible BTC fee replacement.
//
// The returned count is the number of transactions moved out of the pending
// state. Failures for individual records are joined and do not prevent the
// remaining records from being checked. The signed Raw payload is never
// logged or returned.
func (s *Service) ReconcilePending(ctx context.Context) (int, error) {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	pending, err := s.store.PendingTxs().List(ctx)
	if err != nil {
		return 0, fmt.Errorf("list pending transactions: %w", err)
	}

	var (
		reconciled int
		failures   []error
	)
	for _, item := range pending {
		if err := ctx.Err(); err != nil {
			failures = append(failures, contextError(err))
			break
		}

		info, ok := item.Asset.Info()
		if !ok || !item.Asset.IsWalletAsset() || info.Network != item.Network {
			failures = append(failures, fmt.Errorf("pending transaction %q has inconsistent asset/network", item.TxID))
			continue
		}

		// If a replacement is already known to have reached the mempool, the
		// predecessor is no longer an independently pending transaction.
		replaced := false
		for _, child := range pending {
			if child.ReplacesTxID == item.TxID && child.BroadcastAccepted {
				replaced = true
				break
			}
		}
		if replaced {
			err := s.store.WithTx(ctx, func(txctx context.Context, tx ports.Store) error {
				history, err := tx.Transactions().Get(txctx, item.WalletID, item.Asset, item.TxID)
				if err != nil {
					return err
				}
				history.Status = domain.TxReplaced
				if err := tx.Transactions().Upsert(txctx, []domain.Tx{history}); err != nil {
					return err
				}
				return tx.PendingTxs().Delete(txctx, item.TxID)
			})
			if err != nil {
				failures = append(failures, fmt.Errorf("retire replaced transaction %q: %w", item.TxID, err))
			} else {
				reconciled++
			}
			continue
		}
		chain := s.chains[item.Network]
		if chain == nil {
			// A profile may intentionally omit an adapter (for example while
			// offline or during a partial network rollout). Keep the durable
			// pending record and retry when that adapter is configured.
			s.log.Warn("pending transaction reconciliation deferred: chain adapter unavailable",
				"wallet_id", item.WalletID, "asset", item.Asset, "network", item.Network)
			continue
		}

		status, err := chain.Status(ctx, item.TxID)
		if err != nil {
			failures = append(failures, fmt.Errorf("query status for transaction %q: %w", item.TxID, err))
			continue
		}
		if !status.State.Valid() {
			failures = append(failures, fmt.Errorf("provider returned invalid status %q for transaction %q", status.State, item.TxID))
			continue
		}
		if status.State == domain.TxPending {
			// A pending status alone is insufficient: some providers (notably
			// TronGrid's transaction-info endpoint) report "not found" as
			// pending. Keep the signed recovery record unresolved until a node
			// explicitly recognizes the transaction.
			if !status.Known {
				continue
			}
			if !item.BroadcastAccepted {
				item.BroadcastAccepted = true
				if err := s.store.PendingTxs().Put(ctx, item); err != nil {
					failures = append(failures, fmt.Errorf("persist accepted broadcast %q: %w", item.TxID, err))
				}
			}
			continue
		}

		err = s.store.WithTx(ctx, func(txctx context.Context, tx ports.Store) error {
			history, err := tx.Transactions().Get(txctx, item.WalletID, item.Asset, item.TxID)
			if err != nil {
				return fmt.Errorf("load transaction history: %w", err)
			}
			history.Status = status.State
			history.BlockHeight = status.BlockHeight
			if err := tx.Transactions().Upsert(txctx, []domain.Tx{history}); err != nil {
				return fmt.Errorf("update transaction history: %w", err)
			}
			if err := tx.PendingTxs().Delete(txctx, item.TxID); err != nil {
				return fmt.Errorf("delete pending transaction: %w", err)
			}
			return nil
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("reconcile transaction %q: %w", item.TxID, err))
			continue
		}
		reconciled++
	}

	return reconciled, errors.Join(failures...)
}

// Settings returns only non-sensitive settings; the repository enforces the
// sensitive-key filter.
func (s *Service) Settings(ctx context.Context) (map[string]string, error) {
	return s.store.Settings().List(ctx)
}

// GetSetting reads a setting by its explicit key. Sensitive values should be
// fetched only by a backend component that needs them, never copied to a DTO.
func (s *Service) GetSetting(ctx context.Context, key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", domain.NewError(domain.CodeInvalidInput, errors.New("setting key is empty"))
	}
	return s.store.Settings().Get(ctx, key)
}

// SetSetting stores a setting. Sensitive keys are still excluded from Settings.
func (s *Service) SetSetting(ctx context.Context, key, value string) error {
	if strings.TrimSpace(key) == "" {
		return domain.NewError(domain.CodeInvalidInput, errors.New("setting key is empty"))
	}
	return s.store.Settings().Set(ctx, key, value)
}

func wipeSecrets(sec ports.Secrets) { sec.Password.Wipe(); sec.Passphrase.Wipe() }

func contextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.NewError(domain.CodeTimeout, err)
	}
	if errors.Is(err, context.Canceled) {
		return domain.NewError(domain.CodeCanceled, err)
	}
	return err
}
