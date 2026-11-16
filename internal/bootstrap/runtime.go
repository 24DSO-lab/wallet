// Package bootstrap owns the desktop application's composition root.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/24DSO-lab/wallet/app"
	"github.com/24DSO-lab/wallet/internal/backup"
	"github.com/24DSO-lab/wallet/internal/chain/btc"
	"github.com/24DSO-lab/wallet/internal/chain/eth"
	"github.com/24DSO-lab/wallet/internal/chain/tron"
	"github.com/24DSO-lab/wallet/internal/config"
	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/keystore"
	"github.com/24DSO-lab/wallet/internal/logging"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/provider"
	"github.com/24DSO-lab/wallet/internal/service"
	"github.com/24DSO-lab/wallet/internal/store"
)

type profileRuntime struct {
	service *service.Service
	db      *store.DB
	backup  *backup.Service
}

// Runtime keeps profile-specific resources alive for the lifetime of Wails.
type Runtime struct {
	App *app.App

	mu       sync.Mutex
	baseDir  string
	keys     config.APIKeys
	logger   *slog.Logger
	profiles map[domain.Profile]profileRuntime
	active   domain.Profile
}

// New builds the default testnet runtime. Provider credentials are read only
// from environment variables and never exposed through the Wails DTO layer.
func New() (*Runtime, error) {
	baseDir, err := config.DefaultBaseDir()
	if err != nil {
		return nil, err
	}
	rawKeys := map[config.Provider]string{
		config.ProviderETHRPC:   os.Getenv("ALCHEMY_API_KEY"),
		config.ProviderETHIndex: os.Getenv("ETHERSCAN_API_KEY"),
		config.ProviderTron:     os.Getenv("TRONGRID_API_KEY"),
	}
	keys := config.NewAPIKeys(rawKeys)
	redactor := logging.NewRedactor(keys.Values()...)
	logger := logging.New(os.Stderr, slog.LevelInfo, redactor)
	rt := &Runtime{
		baseDir: baseDir, keys: keys, logger: logger,
		profiles: make(map[domain.Profile]profileRuntime),
		active: domain.ProfileTest,
	}
	initial, err := rt.build(context.Background(), domain.ProfileTest)
	if err != nil {
		return nil, err
	}
	rt.profiles[domain.ProfileTest] = initial
	rt.App = app.NewWithDependencies(app.Dependencies{
		Service: initial.service, Profile: domain.ProfileTest,
		SwitchProfile: rt.switchProfile, CreateBackup: rt.createBackup, RestoreBackup: rt.restoreBackup,
	})
	return rt, nil
}

func (r *Runtime) build(ctx context.Context, profile domain.Profile) (profileRuntime, error) {
	cfg, err := config.New(profile, r.baseDir, r.keys)
	if err != nil {
		return profileRuntime{}, err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return profileRuntime{}, fmt.Errorf("create profile data directory: %w", err)
	}

	btcClient, err := provider.New(provider.Options{
		Endpoints: cfg.Providers[config.ProviderBTC].All(),
		Redactor:  logging.NewRedactor(r.keys.Values()...),
	})
	if err != nil {
		return profileRuntime{}, fmt.Errorf("configure Bitcoin provider: %w", err)
	}
	btcAPI, err := btc.NewEsplora(btcClient, profile)
	if err != nil {
		return profileRuntime{}, err
	}
	btcAdapter, err := btc.NewAdapter(btcAPI)
	if err != nil {
		return profileRuntime{}, err
	}

	rpcEndpoints := []string{publicEthereumRPC(profile)}
	if key, ok := r.keys.Get(config.ProviderETHRPC); ok {
		rpcEndpoints = append([]string{cfg.Providers[config.ProviderETHRPC].Primary + "/" + key}, rpcEndpoints...)
	}
	rpcClient, err := provider.New(provider.Options{
		Endpoints: rpcEndpoints, Redactor: logging.NewRedactor(r.keys.Values()...),
	})
	if err != nil {
		return profileRuntime{}, fmt.Errorf("configure Ethereum RPC provider: %w", err)
	}
	jsonRPC, err := eth.NewJSONRPC(rpcClient)
	if err != nil {
		return profileRuntime{}, err
	}
	indexClient, err := provider.New(provider.Options{
		Endpoints: []string{"https://api.etherscan.io/v2/api"},
		Redactor:  logging.NewRedactor(r.keys.Values()...),
	})
	if err != nil {
		return profileRuntime{}, fmt.Errorf("configure Ethereum indexer: %w", err)
	}
	indexKey, _ := r.keys.Get(config.ProviderETHIndex)
	chainID := eth.ChainIDSepolia
	if profile == domain.ProfileMain {
		chainID = eth.ChainIDMainnet
	}
	indexer, err := eth.NewEtherscan(indexClient, chainID, indexKey)
	if err != nil {
		return profileRuntime{}, err
	}
	ethAdapter, err := eth.New(profile, jsonRPC, indexer, eth.Options{})
	if err != nil {
		return profileRuntime{}, err
	}

	tronHeader := make(http.Header)
	if key, ok := r.keys.Get(config.ProviderTron); ok {
		tronHeader.Set("TRON-PRO-API-KEY", key)
	}
	tronClient, err := provider.New(provider.Options{
		Endpoints: cfg.Providers[config.ProviderTron].All(),
		Header:    tronHeader,
		Redactor:  logging.NewRedactor(r.keys.Values()...),
	})
	if err != nil {
		return profileRuntime{}, fmt.Errorf("configure TRON provider: %w", err)
	}
	tronAPI, err := tron.NewTronGrid(tronClient)
	if err != nil {
		return profileRuntime{}, err
	}
	tronAdapter, err := tron.New(tronAPI, profile)
	if err != nil {
		return profileRuntime{}, err
	}

	db, err := store.Open(ctx, filepath.Join(cfg.DataDir, "wallet.db"), store.WithLogger(r.logger))
	if err != nil {
		return profileRuntime{}, fmt.Errorf("open profile database: %w", err)
	}
	ks, err := keystore.New(keystore.Config{
		Dir: filepath.Join(cfg.DataDir, "keystore"), Profile: profile, Logger: r.logger,
		Addresses: map[domain.Network]keystore.AddressFunc{
			domain.NetworkBitcoin: func(pub []byte) (string, error) {
				return btc.AddressFromPublicKey(profile, pub)
			},
			domain.NetworkEthereum: ethAdapter.AddressFromPublicKey,
			domain.NetworkTron:     tronAdapter.AddressFromPublicKey,
		},
	})
	if err != nil {
		_ = db.Close()
		return profileRuntime{}, fmt.Errorf("open encrypted keystore: %w", err)
	}
	svc, err := service.New(db, ks, []ports.ChainAdapter{btcAdapter, ethAdapter, tronAdapter}, service.WithLogger(r.logger))
	if err != nil {
		_ = db.Close()
		return profileRuntime{}, fmt.Errorf("construct wallet service: %w", err)
	}
	backupService, err := backup.New(backup.Config{
		DataDir: cfg.DataDir, DB: db, CheckKeystoreFile: keystore.CheckFile,
		MaxSchemaVersion: store.SchemaVersion(),
	})
	if err != nil {
		_ = db.Close()
		return profileRuntime{}, fmt.Errorf("configure backup service: %w", err)
	}
	return profileRuntime{service: svc, db: db, backup: backupService}, nil
}

func publicEthereumRPC(profile domain.Profile) string {
	if profile == domain.ProfileTest {
		return "https://ethereum-sepolia-rpc.publicnode.com"
	}
	return "https://ethereum-rpc.publicnode.com"
}

func (r *Runtime) switchProfile(ctx context.Context, profile domain.Profile) (*service.Service, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.profiles[profile]
	if !ok {
		built, err := r.build(ctx, profile)
		if err != nil {
			return nil, err
		}
		current = built
		r.profiles[profile] = current
	}
	r.active = profile
	return current.service, nil
}

func (r *Runtime) createBackup(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.profiles[r.active]
	if !ok || current.backup == nil {
		return errors.New("backup service is unavailable")
	}
	destination := filepath.Join(r.baseDir, string(r.active), "backups")
	_, err := current.backup.Create(ctx, destination)
	return err
}

// restoreBackup closes the active profile database before the atomic backup
// swap, then rebuilds its service and adapters against the restored files.
func (r *Runtime) restoreBackup(ctx context.Context, archivePath string) (*service.Service, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	profile := r.active
	current, ok := r.profiles[profile]
	if !ok || current.backup == nil {
		return nil, errors.New("backup service is unavailable")
	}
	if current.db != nil {
		if err := current.db.Close(); err != nil {
			return current.service, fmt.Errorf("close database before restore: %w", err)
		}
	}
	delete(r.profiles, profile)

	result, err := current.backup.Restore(ctx, archivePath)
	if err != nil {
		rebuilt, buildErr := r.build(ctx, profile)
		if buildErr == nil {
			r.profiles[profile] = rebuilt
			return rebuilt.service, err
		}
		return nil, errors.Join(err, fmt.Errorf("reopen existing wallet after failed restore: %w", buildErr))
	}

	rebuilt, err := r.build(ctx, profile)
	if err != nil {
		return nil, fmt.Errorf("open restored wallet (previous data retained at %s): %w", result.PreviousDir, err)
	}
	r.profiles[profile] = rebuilt
	return rebuilt.service, nil
}

// Shutdown locks the wallet session and closes every profile database.
func (r *Runtime) Shutdown(ctx context.Context) {
	if r.App != nil {
		r.App.Shutdown(ctx)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for profile, resources := range r.profiles {
		if resources.db != nil {
			if err := resources.db.Close(); err != nil {
				r.logger.Error("close profile database", "profile", profile, "error", err)
			}
		}
		delete(r.profiles, profile)
	}
}
