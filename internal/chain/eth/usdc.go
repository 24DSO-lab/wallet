package eth

import (
	"errors"
	"fmt"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// Адреса контракта USDC (ERC-20 в Ethereum).
//
// Источник: официальная документация Circle,
// https://developers.circle.com/stablecoins/usdc-contract-addresses
// Проверено 2026-11-16. Страница даты публикации не содержит.
//
// Менять эти значения можно только отдельным PR со ссылкой на источник:
// подмена адреса подменяет токен, который получает пользователь. Защита это
// TestUSDCContractsArePinned, где адреса записаны второй раз буквально.
const (
	USDCMainnetContract = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	USDCSepoliaContract = "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"
)

// Идентификаторы сетей и знаки после запятой (ADR, справочник активов).
const (
	ChainIDMainnet uint64 = 1
	ChainIDSepolia uint64 = 11155111

	ETHDecimals  uint8 = 18
	USDCDecimals uint8 = 6
)

// Путь деривации общего адреса ETH и USDC (ADR). Coin type ETH одинаков в
// профилях main и test, в отличие от BTC.
const derivationPath = "m/44'/60'/0'/0/0"

// ErrUSDCContract означает, что константа с адресом контракта испорчена.
var ErrUSDCContract = errors.New("eth: USDC contract constant is not a valid checksummed address")

// USDCContractAddress returns the profile-specific verified USDC contract.
func USDCContractAddress(p domain.Profile) (string, error) {
	params, err := paramsFor(p)
	if err != nil {
		return "", err
	}
	return params.usdc.String(), nil
}

// networkParams это параметры сети профиля.
type networkParams struct {
	profile domain.Profile
	chainID uint64
	usdc    Address
}

// paramsFor возвращает chainId и контракт USDC профиля. Профиль один на
// приложение, поэтому смешать main и test в одном адаптере нельзя.
func paramsFor(p domain.Profile) (networkParams, error) {
	var (
		chainID  uint64
		contract string
	)
	switch p {
	case domain.ProfileMain:
		chainID, contract = ChainIDMainnet, USDCMainnetContract
	case domain.ProfileTest:
		chainID, contract = ChainIDSepolia, USDCSepoliaContract
	default:
		return networkParams{}, fmt.Errorf("eth: unknown profile %q", string(p))
	}
	// Константа обязана быть записана с контрольной суммой: опечатка в ней
	// иначе осталась бы незамеченной.
	a, checksummed, err := parseAddress(contract)
	if err != nil || !checksummed {
		return networkParams{}, ErrUSDCContract
	}
	return networkParams{profile: p, chainID: chainID, usdc: a}, nil
}
