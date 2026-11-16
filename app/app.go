// Package app exposes the stable, secret-safe Wails boundary for the Vue UI.
// Domain and port types deliberately never cross this boundary.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

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
	CreateBackup  func(context.Context) error
}

type pendingDraft struct {
	walletID string
	intent   domain.SendIntent
	prepared ports.Prepared
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
	createBackup  func(context.Context) error
	locked        bool
	lockTimeout   time.Duration
	lockTimer     *time.Timer
	drafts        map[string]pendingDraft
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
		switchProfile: d.SwitchProfile, createBackup: d.CreateBackup,
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
	if a.lockTimer == nil {
		a.lockTimer = time.AfterFunc(a.lockTimeout, a.Lock)
	} else {
		a.lockTimer.Reset(a.lockTimeout)
	}
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
func (a *App) CreateWallet(name string, words int, password, passphrase string) (CreateWalletDTO, error) {
	if err := a.ready(); err != nil {
		return CreateWalletDTO{}, err
	}
	if err := a.requireUnlocked(); err != nil {
		return CreateWalletDTO{}, err
	}
	if password == "" {
		return CreateWalletDTO{}, errors.New("invalid_input")
	}
	w, mnemonic, err := a.svc.CreateWallet(a.context(), name, words, ports.Secrets{Password: secret.FromString(password), Passphrase: secret.FromString(passphrase)})
	if err != nil {
		return CreateWalletDTO{}, safeError(err)
	}
	defer mnemonic.Wipe()
	a.mu.Lock()
	a.locked = false
	a.mu.Unlock()
	a.touch()
	balances, err := a.svc.GetBalances(a.context(), w.ID)
	if err != nil {
		return CreateWalletDTO{}, safeError(err)
	}
	return CreateWalletDTO{Wallet: walletDTO(w, balances), Mnemonic: string(mnemonic.Expose())}, nil
}

// ImportWallet imports an existing BIP-39 mnemonic. All secret buffers are
// consumed and wiped by the service before this method returns.
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
	domainAsset, err := parseAsset(asset)
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
	txs, err := a.svc.GetHistory(a.context(), id, domainAsset)
	if err != nil {
		return nil, safeError(err)
	}
	out := make([]TxDTO, 0, len(txs))
	for _, tx := range txs {
		out = append(out, txDTO(tx))
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
	da, err := parseAsset(asset)
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
	txid, err := a.svc.ConfirmSend(a.context(), draft.walletID, draft.intent, draft.prepared, ports.Secrets{Password: secret.FromString(password), Passphrase: secret.FromString(passphrase)})
	a.mu.Lock()
	if err != nil {
		// Release the draft on failure so the user may retry confirmation.
		if current, exists := a.drafts[draftID]; exists {
			current.inFlight = false
			a.drafts[draftID] = current
		}
	} else {
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

func (a *App) CreateBackup(password string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if err := a.svc.VerifyPassword(a.context(), secret.FromString(password)); err != nil {
		return safeError(err)
	}
	a.mu.Lock()
	create := a.createBackup
	a.mu.Unlock()
	if create == nil {
		return errors.New("backup_unavailable")
	}
	if err := create(a.context()); err != nil {
		return safeError(err)
	}
	return nil
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
