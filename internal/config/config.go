// Package config описывает настройки запуска: профиль сети, каталог данных,
// адреса провайдеров с резервными и ключи API.
//
// Профиль сети один на приложение (ADR). Каждый профиль живёт в своём
// каталоге данных (A4): keystore, база и копии main и test не смешиваются.
// Адреса контрактов USDC и USDT здесь не хранятся, они живут в пакетах
// адаптеров (Dev 5 и Dev 6).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// MaxWallets это число кошельков, которое создаёт интерфейс (ADR): четвёртый
// кошелёк это правка этой константы, схема данных на неё не опирается.
const MaxWallets = 3

// MainnetEnabled remains false until ADR-001 acceptance gates 1, 2, 3 and 5 pass.
// Enabling this is an explicit release decision, never a user preference.
const MainnetEnabled = false

// appDirName это имя каталога приложения внутри пользовательского каталога.
const appDirName = "Wallet"

// Provider это имя внешнего источника данных, ключ для адресов и ключей API.
type Provider string

// Провайдеры (решение D5). Адреса и лимиты каждый разработчик адаптера
// проверяет на старте и записывает в README своего пакета.
const (
	ProviderBTC      Provider = "btc"       // Esplora: mempool.space, резерв Blockstream
	ProviderETHRPC   Provider = "eth_rpc"   // JSON-RPC Ethereum (Alchemy или аналог)
	ProviderETHIndex Provider = "eth_index" // история: Etherscan
	ProviderTron     Provider = "tron"      // TronGrid
)

// Endpoints это основной адрес провайдера и резервные по порядку.
type Endpoints struct {
	Primary  string
	Fallback []string
}

// All возвращает основной адрес и резервные в порядке попыток.
func (e Endpoints) All() []string {
	return append([]string{e.Primary}, e.Fallback...)
}

// Config это неизменяемая конфигурация запуска.
type Config struct {
	Profile   domain.Profile
	DataDir   string // каталог профиля: <база>/<profile>
	Providers map[Provider]Endpoints
	APIKeys   APIKeys
}

// Ошибки конфигурации.
var (
	ErrInvalidProfile  = errors.New("config: invalid network profile")
	ErrEmptyBaseDir    = errors.New("config: base data directory is empty")
	ErrInvalidEndpoint = errors.New("config: invalid provider endpoint")
)

// New собирает конфигурацию профиля. baseDir это корневой каталог данных
// приложения (см. DefaultBaseDir), keys приходят из настроек бэкенда и
// никогда не уходят во фронтенд. Каталоги не создаются: это делают
// владельцы данных (Dev 2 и Dev 3).
func New(profile domain.Profile, baseDir string, keys APIKeys) (Config, error) {
	if !profile.Valid() {
		return Config{}, fmt.Errorf("%w: %q", ErrInvalidProfile, string(profile))
	}
	if baseDir == "" {
		return Config{}, ErrEmptyBaseDir
	}
	cfg := Config{
		Profile:   profile,
		DataDir:   DataDir(baseDir, profile),
		Providers: defaultProviders(profile),
		APIKeys:   keys,
	}
	for name, ep := range cfg.Providers {
		for _, raw := range ep.All() {
			if err := checkEndpoint(raw); err != nil {
				return Config{}, fmt.Errorf("provider %s: %w", name, err)
			}
		}
	}
	return cfg, nil
}

// DataDir возвращает каталог данных профиля: keystore, база и копии каждого
// профиля лежат отдельно (A4).
func DataDir(baseDir string, profile domain.Profile) string {
	return filepath.Join(baseDir, string(profile))
}

// DefaultBaseDir возвращает корневой каталог данных в пользовательском
// каталоге ОС (на Windows это %AppData%\Wallet).
func DefaultBaseDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locate user config dir: %w", err)
	}
	return filepath.Join(dir, appDirName), nil
}

// BTCCoinType возвращает coin type BIP44 для BTC: 0 в main, 1 в test (A4).
func BTCCoinType(p domain.Profile) uint32 {
	if p == domain.ProfileTest {
		return 1
	}
	return 0
}

// checkEndpoint требует абсолютный https-адрес. Исключение для проверок
// на httptest делается в тестах через подмену адреса, а не здесь.
func checkEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%w: %q must be an absolute https URL", ErrInvalidEndpoint, raw)
	}
	return nil
}

// defaultProviders возвращает адреса провайдеров профиля. Это отправная точка
// из решения D5: Dev 4-6 проверяют их и условия тарифов на старте и правят
// здесь отдельным PR. Для test BTC выбран testnet; если Dev 4 остановится на
// signet, меняется только эта запись.
func defaultProviders(p domain.Profile) map[Provider]Endpoints {
	if p == domain.ProfileTest {
		return map[Provider]Endpoints{
			ProviderBTC:      {Primary: "https://mempool.space/testnet4/api", Fallback: []string{"https://blockstream.info/testnet4/api"}},
			ProviderETHRPC:   {Primary: "https://eth-sepolia.g.alchemy.com/v2"},
			ProviderETHIndex: {Primary: "https://api-sepolia.etherscan.io/api"},
			ProviderTron:     {Primary: "https://nile.trongrid.io"},
		}
	}
	return map[Provider]Endpoints{
		ProviderBTC:      {Primary: "https://mempool.space/api", Fallback: []string{"https://blockstream.info/api"}},
		ProviderETHRPC:   {Primary: "https://eth-mainnet.g.alchemy.com/v2"},
		ProviderETHIndex: {Primary: "https://api.etherscan.io/api"},
		ProviderTron:     {Primary: "https://api.trongrid.io"},
	}
}
