// Package app exposes the stable, secret-safe Wails boundary for the Vue UI.
// Domain and port types deliberately never cross this boundary.
package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/24DSO-lab/wallet/internal/config"
	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
	"github.com/24DSO-lab/wallet/internal/service"
)

const defaultLockTimeout = 5 * time.Minute

// Dependencies are supplied by the composition root. Optional callbacks are
// deliberately explicit: changing the network profile or creating a backup
// requires application-level infrastructure, not a setting-only mutation.
type Dependencies struct {
	Service       *service.Service
	Profile       domain.Profile
	SwitchProfile func(context.Context, domain.Profile) (*service.Service, error)
	CreateBackup  func(context.Context, string) error
	RestoreBackup func(context.Context, string) (*service.Service, error)
}

type pendingDraft struct {
	walletID string
	intent   domain.SendIntent
	prepared ports.Prepared
	replacement *ports.Replacement
	expires  time.Time
	inFlight bool
}

// App is the Wails facade. It owns only UI session state and prepared drafts;
// persistent wallet state remains in service/store.
type App struct {
	mu            sync.Mutex
	ctx           context.Context
	svc           *service.Service
	profile       domain.Profile
	switchProfile func(context.Context, domain.Profile) (*service.Service, error)
	createBackup  func(context.Context, string) error
	restoreBackup func(context.Context, string) (*service.Service, error)
	locked        bool
	lockTimeout   time.Duration
	lockTimer     *time.Timer
	drafts        map[string]pendingDraft

	pendingMnemonic          secret.Bytes
	pendingWalletName        string
	pendingChallengePositions []int
	pendingWalletExpires     time.Time
}

// New preserves the Wails startup contract. Production startup should use
// NewWithDependencies after constructing the service and adapters.
func New() *App { return NewWithDependencies(Dependencies{}) }

// NewWithDependencies constructs the facade with explicit runtime dependencies.
func NewWithDependencies(d Dependencies) *App {
	p := d.Profile
	if !p.Valid() {
		p = domain.ProfileTest
	}
	return &App{
		ctx: context.Background(), svc: d.Service, profile: p,
		switchProfile: d.SwitchProfile, createBackup: d.CreateBackup, restoreBackup: d.RestoreBackup,
		locked: true, lockTimeout: defaultLockTimeout,
		drafts: make(map[string]pendingDraft),
	}
}

// Startup is intended to be assigned to options.App.OnStartup.
func (a *App) Startup(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ctx != nil {
		a.ctx = ctx
	}
}

func (a *App) context() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx == nil {
		return context.Background()
	}
	return a.ctx
}

func (a *App) ready() error {
	if a.svc == nil {
		return errors.New("app_not_configured")
	}
	return nil
}

func (a *App) touch() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.locked {
		return
	}
	if a.lockTimer != nil {
		a.lockTimer.Stop()
	}
	a.lockTimer = time.AfterFunc(a.lockTimeout, a.Lock)
}

func (a *App) requireUnlocked() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.locked {
		return errors.New("session_locked")
	}
	if !a.pendingMnemonic.IsEmpty() && !time.Now().Before(a.pendingWalletExpires) {
		a.pendingMnemonic.Wipe()
		a.pendingWalletName = ""
		a.pendingChallengePositions = nil
		a.pendingWalletExpires = time.Time{}
	}
	return nil
}

// KeepAlive records explicit foreground user activity. Background refreshes
// and polling only call requireUnlocked and therefore cannot postpone locking.
func (a *App) KeepAlive() error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	a.touch()
	return nil
}

// GetSession returns non-secret UI session state.
func (a *App) GetSession() (SessionDTO, error) {
	if err := a.ready(); err != nil {
		return SessionDTO{}, err
	}
	wallets, err := a.svc.ListWallets(a.context())
	if err != nil {
		return SessionDTO{}, safeError(err)
	}
	a.mu.Lock()
	if len(wallets) == 0 {
		a.locked = false // first-run setup has no password-protected keystore yet
	}
	dto := SessionDTO{Locked: a.locked, Profile: string(a.profile), Timeout: int(a.lockTimeout / time.Minute)}
	a.mu.Unlock()
	return dto, nil
}

// Unlock verifies the supplied password; it is never retained by App.
func (a *App) Unlock(password string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if password == "" {
		return errors.New("invalid_input")
	}
	if err := a.svc.VerifyPassword(a.context(), secret.FromString(password)); err != nil {
		return safeError(err)
	}
	a.mu.Lock()
	a.locked = false
	a.mu.Unlock()
	a.touch()
	return nil
}

// Lock immediately blocks operations requiring an unlocked session and drops
// all cached unsigned drafts. It does not alter encrypted keystore files.
func (a *App) Lock() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.locked = true
	if a.lockTimer != nil {
		a.lockTimer.Stop()
		a.lockTimer = nil
	}
	clear(a.drafts)
	a.pendingMnemonic.Wipe()
	a.pendingWalletName = ""
	a.pendingChallengePositions = nil
	a.pendingWalletExpires = time.Time{}
}

// ListWallets returns public wallet metadata and cached balances.
func (a *App) ListWallets() ([]WalletDTO, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	if err := a.requireUnlocked(); err != nil {
		return nil, err
	}
	wallets, err := a.svc.ListWallets(a.context())
	if err != nil {
		return nil, safeError(err)
	}
	out := make([]WalletDTO, 0, len(wallets))
	for _, w := range wallets {
		balances, err := a.svc.GetBalances(a.context(), w.ID)
		if err != nil {
			return nil, safeError(err)
		}
		out = append(out, walletDTO(w, balances))
	}
	return out, nil
}

// CreateWallet generates a new mnemonic. The mnemonic is returned once and is
// never stored in App or logged; the caller must display it for backup.
// PrepareWallet generates a mnemonic but does not persist a wallet yet.
// The phrase is held in Go memory until the user confirms the challenge.
func (a *App) PrepareWallet(name string, words int) (PrepareWalletDTO, error) {
	if err := a.ready(); err != nil {
		return PrepareWalletDTO{}, err
	}
	if err := a.requireUnlocked(); err != nil {
		return PrepareWalletDTO{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || (words != 12 && words != 24) {
		return PrepareWalletDTO{}, domain.NewError(domain.CodeInvalidInput, errors.New("invalid wallet name or mnemonic length"))
	}
	mnemonic, err := a.svc.NewMnemonic(words)
	if err != nil {
		return PrepareWalletDTO{}, safeError(err)
	}
	positions, err := randomChallengePositions(words)
	if err != nil {
		mnemonic.Wipe()
		return PrepareWalletDTO{}, safeError(err)
	}

	a.mu.Lock()
	a.pendingMnemonic.Wipe()
	a.pendingMnemonic = mnemonic
	a.pendingWalletName = name
	a.pendingChallengePositions = positions
	a.pendingWalletExpires = time.Now().Add(5 * time.Minute)
	a.mu.Unlock()
	a.touch()

	return PrepareWalletDTO{
		Mnemonic: string(mnemonic.Expose()),
		ChallengePositions: append([]int(nil), positions...),
	}, nil
}

// ConfirmCreateWallet persists the pending mnemonic only after the Go facade
// verifies the exact challenge generated by PrepareWallet.
func (a *App) ConfirmCreateWallet(name, password, passphrase string, positions []int, answers []string) (WalletDTO, error) {
	if err := a.ready(); err != nil {
		return WalletDTO{}, err
	}
	if err := a.requireUnlocked(); err != nil {
		return WalletDTO{}, err
	}
	name = strings.TrimSpace(name)
	if len(password) < 10 {
		return WalletDTO{}, domain.NewError(domain.CodeInvalidInput, errors.New("password must contain at least 10 characters"))
	}

	a.mu.Lock()
	if a.pendingMnemonic.IsEmpty() || time.Now().After(a.pendingWalletExpires) {
		a.pendingMnemonic.Wipe()
		a.pendingWalletName = ""
		a.pendingChallengePositions = nil
		a.pendingWalletExpires = time.Time{}
		a.mu.Unlock()
		return WalletDTO{}, domain.NewError(domain.CodeDraftExpired, errors.New("pending wallet creation expired"))
	}
	if name != a.pendingWalletName {
		a.mu.Unlock()
		return WalletDTO{}, domain.NewError(domain.CodeInvalidInput, errors.New("wallet name changed during creation"))
	}
	pending := a.pendingMnemonic.Clone()
	expected := append([]int(nil), a.pendingChallengePositions...)
	a.mu.Unlock()
	defer pending.Wipe()

	words := bytes.Fields(pending.Expose())
	if len(positions) != len(expected) || len(answers) != len(expected) {
		return WalletDTO{}, domain.NewError(domain.CodeVerificationFailed, errors.New("mnemonic challenge is incomplete"))
	}
	for i, position := range expected {
		if positions[i] != position || position < 0 || position >= len(words) {
			return WalletDTO{}, domain.NewError(domain.CodeVerificationFailed, errors.New("mnemonic challenge positions do not match"))
		}
		answer := []byte(answers[i])
		normalized := bytes.ToLower(bytes.TrimSpace(answer))
		valid := bytes.Equal(words[position], normalized)
		clear(answer)
		if !valid {
			return WalletDTO{}, domain.NewError(domain.CodeVerificationFailed, errors.New("mnemonic challenge answer does not match"))
		}
	}

	wallet, err := a.svc.ImportWallet(a.context(), name, pending.Clone(), ports.Secrets{
		Password: secret.FromString(password), Passphrase: secret.FromString(passphrase),
	})
	if err != nil {
		return WalletDTO{}, safeError(err)
	}
	a.mu.Lock()
	a.pendingMnemonic.Wipe()
	a.pendingWalletName = ""
	a.pendingChallengePositions = nil
	a.pendingWalletExpires = time.Time{}
	a.mu.Unlock()
	a.touch()

	balances, err := a.svc.GetBalances(a.context(), wallet.ID)
	if err != nil {
		return WalletDTO{}, safeError(err)
	}
	return walletDTO(wallet, balances), nil
}

// CancelCreateWallet wipes an uncommitted mnemonic when the user leaves the wizard.
func (a *App) CancelCreateWallet() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pendingMnemonic.Wipe()
	a.pendingWalletName = ""
	a.pendingChallengePositions = nil
	a.pendingWalletExpires = time.Time{}
}

func randomChallengePositions(words int) ([]int, error) {
	if words < 3 {
		return nil, domain.NewError(domain.CodeInvalidInput, errors.New("challenge requires at least three mnemonic words"))
	}
	seen := make(map[int]struct{}, 3)
	positions := make([]int, 0, 3)
	for len(positions) < 3 {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(words)))
		if err != nil {
			return nil, err
		}
		position := int(n.Int64())
		if _, exists := seen[position]; exists {
			continue
		}
		seen[position] = struct{}{}
		positions = append(positions, position)
	}
	sort.Ints(positions)
	return positions, nil
}

func (a *App) ImportWallet(name, mnemonic, password, passphrase string) (WalletDTO, error) {
	if err := a.ready(); err != nil {
		return WalletDTO{}, err
	}
	if err := a.requireUnlocked(); err != nil {
		return WalletDTO{}, err
	}
	if password == "" || mnemonic == "" {
		return WalletDTO{}, errors.New("invalid_input")
	}
	w, err := a.svc.ImportWallet(a.context(), name, secret.FromString(mnemonic), ports.Secrets{Password: secret.FromString(password), Passphrase: secret.FromString(passphrase)})
	if err != nil {
		return WalletDTO{}, safeError(err)
	}
	balances, err := a.svc.GetBalances(a.context(), w.ID)
	if err != nil {
		return WalletDTO{}, safeError(err)
	}
	a.mu.Lock()
	a.locked = false
	a.mu.Unlock()
	a.touch()
	return walletDTO(w, balances), nil
}

// ChangePassword re-encrypts all wallet keystore entries with a new password.
func (a *App) ChangePassword(oldPassword, newPassword string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if len(newPassword) < 10 {
		return domain.NewError(domain.CodeInvalidInput, errors.New("password must contain at least 10 characters"))
	}
	if err := a.svc.ChangePassword(a.context(), secret.FromString(oldPassword), secret.FromString(newPassword)); err != nil {
		return safeError(err)
	}
	a.touch()
	return nil
}

// RevealSeed exposes a wallet mnemonic only for this explicit, password-verified
// request. The returned secret buffer is wiped before crossing the Wails boundary.
func (a *App) RevealSeed(id, password, passphrase string) (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	if err := a.requireUnlocked(); err != nil {
		return "", err
	}
	mnemonic, err := a.svc.RevealSeed(a.context(), id, ports.Secrets{
		Password: secret.FromString(password), Passphrase: secret.FromString(passphrase),
	})
	if err != nil {
		return "", safeError(err)
	}
	defer mnemonic.Wipe()
	a.touch()
	return string(mnemonic.Expose()), nil
}

func (a *App) RenameWallet(id, name string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if err := a.svc.RenameWallet(a.context(), id, name); err != nil {
		return safeError(err)
	}
	return nil
}

func (a *App) DeleteWallet(id, confirmedName, password, passphrase string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if err := a.svc.DeleteWallet(a.context(), id, confirmedName, ports.Secrets{Password: secret.FromString(password), Passphrase: secret.FromString(passphrase)}); err != nil {
		return safeError(err)
	}
	return nil
}

func (a *App) GetReceiveAddress(id, asset string) (string, error) {
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
	// Bitcoin receive addresses are discovered by the BIP84 gap-limit scan;
	// refresh before asking the address repository for the next unused one.
	if domainAsset == domain.BTC {
		if _, err := a.svc.RefreshWallet(a.context(), id, domain.BTC); err != nil {
			return "", safeError(err)
		}
	}
	address, err := a.svc.GetReceiveAddress(a.context(), id, domainAsset)
	if err != nil {
		return "", safeError(err)
	}
	return address.Address, nil
}

func (a *App) GetHistory(id, asset string) ([]TxDTO, error) {
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
	_, _ = a.svc.ReconcilePending(a.context())
	txs, err := a.svc.GetHistory(a.context(), id, domainAsset)
	if err != nil {
		return nil, safeError(err)
	}
	out := make([]TxDTO, 0, len(txs))
	for _, tx := range txs {
		dto := txDTO(tx)
		dto.CanReplace = a.svc.HasRBF(a.context(), id, tx.TxID)
		dto.ExplorerURL = transactionExplorerURL(a.profile, tx)
		out = append(out, dto)
	}
	return out, nil
}

func (a *App) RefreshWallet(id, asset string) (BalanceDTO, error) {
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
	snapshot, err := a.svc.RefreshWallet(a.context(), id, da)
	if err != nil {
		return BalanceDTO{}, safeError(err)
	}
	return snapshotDTO(snapshot), nil
}

func (a *App) PrepareSend(walletID, asset, recipient, amount string) (QuoteDTO, error) {
	if err := a.ready(); err != nil {
		return QuoteDTO{}, err
	}
	if err := a.requireUnlocked(); err != nil {
		return QuoteDTO{}, err
	}
	da, err := parseAsset(asset)
	if err != nil {
		return QuoteDTO{}, err
	}
	info, _ := da.Info()
	units, err := domain.ParseDecimal(amount, info.Decimals)
	if err != nil {
		return QuoteDTO{}, safeError(err)
	}
	intent := domain.SendIntent{Wallet: domain.PublicWallet{ID: walletID}, Asset: da, To: recipient, Amount: units}
	prepared, err := a.svc.PrepareSend(a.context(), intent)
	if err != nil {
		return QuoteDTO{}, safeError(err)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return QuoteDTO{}, errors.New("internal")
	}
	id := hex.EncodeToString(raw[:])
	a.mu.Lock()
	a.drafts[id] = pendingDraft{walletID: walletID, intent: intent, prepared: prepared, expires: time.Now().Add(10 * time.Minute)}
	a.mu.Unlock()
	return QuoteDTO{DraftID: id, Asset: asset, Network: string(prepared.Network), Recipient: prepared.Summary.To, Amount: prepared.Summary.Amount.FormatDecimal(info.Decimals), Fee: prepared.Fee.FormatDecimal(feeDecimals(prepared.FeeAsset, info.Decimals)), FeeAsset: string(prepared.FeeAsset), Total: prepared.Summary.Total.FormatDecimal(info.Decimals), Warnings: warningCodes(prepared.Warnings)}, nil
}


// PrepareRBF creates a fee-bump quote for an accepted, unconfirmed BTC send.
func (a *App) PrepareRBF(walletID, txid string) (QuoteDTO, error) {
	if err := a.ready(); err != nil { return QuoteDTO{}, err }
	if err := a.requireUnlocked(); err != nil { return QuoteDTO{}, err }
	replacement, err := a.svc.PrepareRBF(a.context(), walletID, domain.TxID(txid))
	if err != nil { return QuoteDTO{}, safeError(err) }
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil { return QuoteDTO{}, errors.New("internal") }
	id := hex.EncodeToString(raw[:])
	a.mu.Lock()
	a.drafts[id] = pendingDraft{walletID:walletID, intent:replacement.Intent, prepared:replacement.Prepared, replacement:&replacement, expires:time.Now().Add(10*time.Minute)}
	a.mu.Unlock()
	return QuoteDTO{DraftID:id, Asset:string(domain.BTC), Network:string(domain.NetworkBitcoin), Recipient:replacement.Prepared.Summary.To, Amount:replacement.Prepared.Summary.Amount.FormatDecimal(8), Fee:replacement.Prepared.Fee.FormatDecimal(8), FeeAsset:string(domain.BTC), Total:replacement.Prepared.Summary.Total.FormatDecimal(8), Warnings:warningCodes(replacement.Prepared.Warnings), ReplacesTxID:txid}, nil
}

func (a *App) ConfirmSend(draftID, password, passphrase string) (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	if err := a.requireUnlocked(); err != nil {
		return "", err
	}
	a.mu.Lock()
	draft, ok := a.drafts[draftID]
	if ok && (time.Now().After(draft.expires) || draft.inFlight) {
		if time.Now().After(draft.expires) {
			delete(a.drafts, draftID)
		}
		ok = false
	} else if ok {
		// Single-flight: a second concurrent confirmation must not obtain the
		// same draft while signing is in progress.
		draft.inFlight = true
		a.drafts[draftID] = draft
	}
	a.mu.Unlock()
	if !ok {
		return "", errors.New("draft_expired_or_in_use")
	}
	var txid domain.TxID
	var err error
	secrets := ports.Secrets{Password: secret.FromString(password), Passphrase: secret.FromString(passphrase)}
	if draft.replacement != nil { txid, err = a.svc.ConfirmRBF(a.context(), draft.walletID, *draft.replacement, secrets) } else { txid, err = a.svc.ConfirmSend(a.context(), draft.walletID, draft.intent, draft.prepared, secrets) }
	a.mu.Lock()
	if err != nil && txid == "" && domain.CodeOf(err) != domain.CodeDuplicateSend {
		// Failures before persistence are safe to retry, except when another
		// unresolved transaction makes this draft stale.
		if current, exists := a.drafts[draftID]; exists {
			current.inFlight = false
			a.drafts[draftID] = current
		}
	} else {
		// A non-empty txid means the signed payload is durable in pending_tx.
		// Never reuse this draft: reconciliation, not another confirmation,
		// determines whether the network accepted the transaction.
		delete(a.drafts, draftID)
	}
	a.mu.Unlock()
	if err != nil {
		return "", safeError(err)
	}
	return string(txid), nil
}

func (a *App) SetProfile(profile string) error {
	p := domain.Profile(profile)
	if !p.Valid() {
		return errors.New("invalid_input")
	}
	if p == domain.ProfileMain && !config.MainnetEnabled {
		return errors.New(string(domain.CodeMainnetDisabled))
	}
	a.mu.Lock()
	current, change := a.profile, a.switchProfile
	a.mu.Unlock()
	if current == p {
		return nil
	}
	if change == nil {
		return errors.New("profile_switch_unavailable")
	}
	newService, err := change(a.context(), p)
	if err != nil {
		return safeError(err)
	}
	if newService == nil {
		return errors.New("profile_switch_unavailable")
	}
	a.mu.Lock()
	a.svc = newService
	a.profile = p
	a.mu.Unlock()
	a.Lock()
	return nil
}

func (a *App) SetLockTimeout(minutes int) error {
	switch minutes {
	case 1, 5, 10, 15, 30:
	default:
		return errors.New("invalid_input")
	}
	a.mu.Lock()
	a.lockTimeout = time.Duration(minutes) * time.Minute
	if !a.locked {
		if a.lockTimer != nil {
			a.lockTimer.Stop()
		}
		a.lockTimer = time.AfterFunc(a.lockTimeout, a.Lock)
	}
	a.mu.Unlock()
	return nil
}

// RestoreBackup opens a native file picker, restores the selected archive,
// then verifies its keystore password before unlocking the restored profile.
func (a *App) RestoreBackup(password string) (bool, error) {
	if err := a.ready(); err != nil {
		return false, err
	}
	if err := a.requireUnlocked(); err != nil {
		return false, err
	}
	if a.restoreBackup == nil {
		return false, errors.New("restore_not_configured")
	}
	path, err := wailsruntime.OpenFileDialog(a.context(), wailsruntime.OpenDialogOptions{
		Title: "Restore wallet backup",
		Filters: []wailsruntime.FileFilter{{DisplayName: "Wallet backup (*.zip)", Pattern: "*.zip"}},
	})
	if err != nil {
		return false, safeError(err)
	}
	if path == "" {
		return false, nil
	}
	if err := a.requireUnlocked(); err != nil {
		return false, err
	}

	a.Lock()
	restored, restoreErr := a.restoreBackup(a.context(), path)
	if restored != nil {
		a.mu.Lock()
		a.svc = restored
		a.mu.Unlock()
	}
	if restoreErr != nil {
		return false, safeError(restoreErr)
	}
	if restored == nil {
		return false, errors.New("restore_failed")
	}
	if err := restored.VerifyPassword(a.context(), secret.FromString(password)); err != nil {
		return false, safeError(err)
	}
	a.mu.Lock()
	a.locked = false
	a.mu.Unlock()
	a.touch()
	return true, nil
}

func (a *App) CreateBackup(password string) (bool, error) {
	if err := a.ready(); err != nil {
		return false, err
	}
	if err := a.requireUnlocked(); err != nil {
		return false, err
	}
	if err := a.svc.VerifyPassword(a.context(), secret.FromString(password)); err != nil {
		return false, safeError(err)
	}
	a.mu.Lock()
	create := a.createBackup
	a.mu.Unlock()
	if create == nil {
		return false, errors.New("backup_unavailable")
	}
	destination, err := wailsruntime.OpenDirectoryDialog(a.context(), wailsruntime.OpenDialogOptions{
		Title: "Choose wallet backup folder",
	})
	if err != nil {
		return false, safeError(err)
	}
	if destination == "" {
		return false, nil
	}
	if err := a.requireUnlocked(); err != nil {
		return false, err
	}
	if err := create(a.context(), destination); err != nil {
		return false, safeError(err)
	}
	a.touch()
	return true, nil
}

func transactionExplorerURL(profile domain.Profile, tx domain.Tx) string {
	info, ok := tx.Asset.Info()
	if !ok || tx.TxID == "" {
		return ""
	}
	id := url.PathEscape(string(tx.TxID))
	switch info.Network {
	case domain.NetworkBitcoin:
		if profile == domain.ProfileTest {
			return "https://mempool.space/testnet/tx/" + id
		}
		return "https://mempool.space/tx/" + id
	case domain.NetworkEthereum:
		if profile == domain.ProfileTest {
			return "https://sepolia.etherscan.io/tx/" + id
		}
		return "https://etherscan.io/tx/" + id
	case domain.NetworkTron:
		if profile == domain.ProfileTest {
			return "https://nile.tronscan.org/#/transaction/" + id
		}
		return "https://tronscan.org/#/transaction/" + id
	default:
		return ""
	}
}

func parseReceiveAsset(s string) (domain.Asset, error) {
	if s == string(domain.TRX) {
		return domain.TRX, nil
	}
	return parseAsset(s)
}

func parseAsset(s string) (domain.Asset, error) {
	a, err := domain.ParseAsset(s)
	if err != nil {
		return "", errors.New("invalid_input")
	}
	return a, nil
}

// safeError deliberately exposes only stable error codes, never provider text,
// file paths, SQL details, or secret-bearing causes.
func safeError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(string(domain.CodeOf(err)))
}

func (a *App) Shutdown(context.Context) { a.Lock() }
