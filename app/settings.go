package app

import (
	"errors"
	"strings"
	"time"

	"github.com/24DSO-lab/wallet/internal/chain/eth"
	"github.com/24DSO-lab/wallet/internal/chain/tron"
	"github.com/24DSO-lab/wallet/internal/config"
	"github.com/24DSO-lab/wallet/internal/domain"
)

func (a *App) SetProfile(profile string) error {
	a.profileMu.Lock()
	defer a.profileMu.Unlock()
	p := domain.Profile(profile)
	if !p.Valid() {
		return errors.New("invalid_input")
	}
	if p == domain.ProfileMain && !config.MainnetEnabled {
		return errors.New(string(domain.CodeMainnetDisabled))
	}
	if err := a.ready(); err != nil {
		return err
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
		return a.fail(err)
	}
	if newService == nil {
		return errors.New("profile_switch_unavailable")
	}
	a.mu.Lock()
	a.svc = newService
	a.profile = p
	a.mu.Unlock()
	a.lockSessionState()
	return nil
}

// GetAssetCatalog returns public metadata for every supported asset, even while locked.
func (a *App) GetAssetCatalog() ([]AssetCatalogDTO, error) {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()

	profile := a.activeProfile()
	if !profile.Valid() {
		return nil, errors.New("invalid_profile")
	}

	btcName, ethName, tronName := "Bitcoin", "Ethereum", "TRON"
	btcURL, ethURL, tronURL := "https://mempool.space", "https://etherscan.io", "https://tronscan.org"
	if profile == domain.ProfileTest {
		btcName, ethName, tronName = "Bitcoin testnet4", "Ethereum Sepolia", "TRON Nile"
		btcURL, ethURL, tronURL = "https://mempool.space/testnet4", "https://sepolia.etherscan.io", "https://nile.tronscan.org"
	}

	usdc, err := eth.USDCContractAddress(profile)
	if err != nil {
		return nil, errors.New("asset_catalog_unavailable")
	}
	usdt, err := tron.USDTContractAddress(profile)
	if err != nil {
		return nil, errors.New("asset_catalog_unavailable")
	}

	return []AssetCatalogDTO{
		{Asset: "BTC", Network: "bitcoin", NetworkName: btcName, Kind: "native", ExplorerURL: btcURL},
		{Asset: "ETH", Network: "ethereum", NetworkName: ethName, Kind: "native", ExplorerURL: ethURL},
		{Asset: "USDC", Network: "ethereum", NetworkName: ethName, Kind: "token", Standard: "ERC-20", ContractAddress: usdc, ExplorerURL: ethURL, ContractExplorerURL: ethURL + "/address/" + usdc},
		{Asset: "USDT", Network: "tron", NetworkName: tronName, Kind: "token", Standard: "TRC-20", ContractAddress: usdt, ExplorerURL: tronURL, ContractExplorerURL: tronURL + "/#/contract/" + usdt},
		{Asset: "TRX", Network: "tron", NetworkName: tronName, Kind: "native", ExplorerURL: tronURL},
	}, nil
}

// GetProviderSettings exposes configuration state only; the credential never crosses the Wails boundary.
func (a *App) GetProviderSettings() (ProviderSettingsDTO, error) {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return ProviderSettingsDTO{}, err
	}
	if err := a.requireUnlocked(); err != nil {
		return ProviderSettingsDTO{}, err
	}
	a.mu.Lock()
	get := a.getProviderSettings
	a.mu.Unlock()
	if get == nil {
		return ProviderSettingsDTO{}, errors.New("provider_settings_unavailable")
	}
	configured, err := get()
	if err != nil {
		return ProviderSettingsDTO{}, errors.New("provider_settings_unavailable")
	}
	return ProviderSettingsDTO{EtherscanAPIKeyConfigured: configured}, nil
}

// SaveEtherscanAPIKey stores the key in the OS-protected secret store. It is never returned to the frontend.
func (a *App) SaveEtherscanAPIKey(key string) error {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 256 || strings.ContainsAny(key, "\r\n\t") {
		return errors.New("invalid_input")
	}
	a.mu.Lock()
	save := a.saveEtherscanAPIKey
	a.mu.Unlock()
	if save == nil {
		return errors.New("provider_settings_unavailable")
	}
	if err := save(key); err != nil {
		return errors.New("provider_settings_unavailable")
	}
	a.touch()
	return nil
}

// DeleteEtherscanAPIKey removes the saved credential from the OS-protected secret store.
func (a *App) DeleteEtherscanAPIKey() error {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	a.mu.Lock()
	remove := a.deleteEtherscanAPIKey
	a.mu.Unlock()
	if remove == nil {
		return errors.New("provider_settings_unavailable")
	}
	if err := remove(); err != nil {
		return errors.New("provider_settings_unavailable")
	}
	a.touch()
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

// GetLogPath returns the path of the technical log file (empty when disabled).
func (a *App) GetLogPath() string {
	return a.logPath
}
