package domain

import (
	"errors"
	"fmt"
)

// Asset это тикер актива. В кошельке четыре актива (WalletAssets), справочник
// зашит в код и не зависит от БД.
type Asset string

// Активы кошелька и комиссионная монета TRON.
const (
	BTC  Asset = "BTC"
	ETH  Asset = "ETH"
	USDC Asset = "USDC"
	USDT Asset = "USDT"

	// TRX это комиссионная монета сети TRON, нужная для отправки USDT.
	// Она не входит в четыре актива кошелька (WalletAssets её не содержит):
	// её баланс показывается и принимается, но отправка TRX в версии 1
	// не поддерживается. ADR, раздел «Модель данных».
	TRX Asset = "TRX"
)

// Network это сеть блокчейна. Актив жёстко определяет сеть.
type Network string

// Сети, которые поддерживает кошелёк.
const (
	NetworkBitcoin  Network = "bitcoin"
	NetworkEthereum Network = "ethereum"
	NetworkTron     Network = "tron"
)

// Valid сообщает, что сеть входит в поддерживаемые.
func (n Network) Valid() bool {
	switch n {
	case NetworkBitcoin, NetworkEthereum, NetworkTron:
		return true
	}
	return false
}

// Profile это профиль сети: основная или тестовая. Он один на всё приложение,
// а не на кошелёк, и определяет каталог данных (см. internal/config).
type Profile string

// Профили сети.
const (
	ProfileMain Profile = "main"
	ProfileTest Profile = "test"
)

// Valid сообщает, что профиль известен.
func (p Profile) Valid() bool {
	return p == ProfileMain || p == ProfileTest
}

// AssetKind отличает основную монету сети от токена.
type AssetKind string

// Виды активов.
const (
	KindNative AssetKind = "native"
	KindToken  AssetKind = "token"
)

// AssetInfo это запись справочника активов (таблица ADR).
type AssetInfo struct {
	Asset    Asset
	Network  Network
	Kind     AssetKind
	Decimals uint8 // знаков после запятой: степень десяти минимальной единицы
	FeeAsset Asset // монета, в которой платится комиссия
}

// ErrUnknownAsset возвращается для актива вне справочника.
var ErrUnknownAsset = errors.New("domain: unknown asset")

// Info возвращает запись справочника. Для неизвестного актива второе значение false.
func (a Asset) Info() (AssetInfo, bool) {
	switch a {
	case BTC:
		return AssetInfo{BTC, NetworkBitcoin, KindNative, 8, BTC}, true
	case ETH:
		return AssetInfo{ETH, NetworkEthereum, KindNative, 18, ETH}, true
	case USDC:
		return AssetInfo{USDC, NetworkEthereum, KindToken, 6, ETH}, true
	case USDT:
		return AssetInfo{USDT, NetworkTron, KindToken, 6, TRX}, true
	case TRX:
		return AssetInfo{TRX, NetworkTron, KindNative, 6, TRX}, true
	}
	return AssetInfo{}, false
}

// IsWalletAsset сообщает, что актив один из четырёх активов кошелька.
// Для TRX возвращает false.
func (a Asset) IsWalletAsset() bool {
	switch a {
	case BTC, ETH, USDC, USDT:
		return true
	}
	return false
}

// WalletAssets возвращает четыре актива кошелька в порядке показа.
func WalletAssets() []Asset {
	return []Asset{BTC, ETH, USDC, USDT}
}

// ParseAsset разбирает тикер актива кошелька. Регистр важен, TRX отклоняется:
// его нельзя выбрать как актив операции.
func ParseAsset(s string) (Asset, error) {
	a := Asset(s)
	if !a.IsWalletAsset() {
		return "", fmt.Errorf("%w: %q", ErrUnknownAsset, s)
	}
	return a, nil
}
