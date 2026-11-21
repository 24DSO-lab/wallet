// Package app exposes the stable, secret-safe Wails boundary for the Vue UI.
// Domain and port types deliberately never cross this boundary.
package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
	"github.com/24DSO-lab/wallet/internal/service"
)

const defaultLockTimeout = 5 * time.Minute
const btcReceiveAddressTimeout = 45 * time.Second
const btcReceiveLookupTimeout = 30 * time.Second

// Dependencies are supplied by the composition root. Optional callbacks are
// deliberately explicit: changing the network profile or creating a backup
// requires application-level infrastructure, not a setting-only mutation.
type Dependencies struct {
	Service               *service.Service
	Profile               domain.Profile
	SwitchProfile         func(context.Context, domain.Profile) (*service.Service, error)
	CreateBackup          func(context.Context, string) error
	RestoreBackup         func(context.Context, string, secret.Bytes) (*service.Service, error)
	GetProviderSettings   func() (bool, error)
	SaveEtherscanAPIKey   func(string) error
	DeleteEtherscanAPIKey func() error
	Logger                *slog.Logger // technical log; nil disables it
	LogPath               string       // shown in Settings so the file is easy to find
}

type pendingDraft struct {
	walletID    string
	intent      domain.SendIntent
	prepared    ports.Prepared
	replacement *ports.Replacement
	expires     time.Time
	inFlight    bool
}

// App is the Wails facade. It owns only UI session state and prepared drafts;
// persistent wallet state remains in service/store.
type App struct {
	profileMu             sync.RWMutex // serializes profile changes/restores with profile-bound operations
	transactionMu         sync.RWMutex // lets Lock wait for sensitive operations without waiting for UI dialogs
	mu                    sync.Mutex
	closed                bool
	ctx                   context.Context
	svc                   *service.Service
	profile               domain.Profile
	switchProfile         func(context.Context, domain.Profile) (*service.Service, error)
	createBackup          func(context.Context, string) error
	restoreBackup         func(context.Context, string, secret.Bytes) (*service.Service, error)
	getProviderSettings   func() (bool, error)
	saveEtherscanAPIKey   func(string) error
	deleteEtherscanAPIKey func() error
	logger                *slog.Logger
	logPath               string
	locked                bool
	lockTimeout           time.Duration
	lockTimer             *time.Timer
	drafts                map[string]pendingDraft

	pendingMnemonic              secret.Bytes
	pendingWalletName            string
	pendingChallengePositions    []int
	pendingWalletExpires         time.Time
	pendingWalletInFlight        bool
	pendingWalletCancelRequested bool
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
		getProviderSettings: d.GetProviderSettings, saveEtherscanAPIKey: d.SaveEtherscanAPIKey,
		deleteEtherscanAPIKey: d.DeleteEtherscanAPIKey, logger: d.Logger, logPath: d.LogPath,
		locked:                true, lockTimeout: defaultLockTimeout,
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

func (a *App) currentService() *service.Service {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.svc
}

func (a *App) activeProfile() domain.Profile {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.profile
}

func (a *App) ready() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("app_shutdown")
	}
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

func (a *App) Shutdown(context.Context) {
	// Stop new profile-bound work before Runtime closes its databases. Keep the
	// same lock order as normal operations and backup restoration.
	a.profileMu.Lock()
	defer a.profileMu.Unlock()
	a.transactionMu.Lock()
	defer a.transactionMu.Unlock()
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	a.lockSessionState()
}
