package eth

import (
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// Адреса записаны здесь второй раз буквально, с официальной страницы Circle
// (https://developers.circle.com/stablecoins/usdc-contract-addresses, проверено
// 2026-11-16). Если правка константы в usdc.go подменит токен, этот тест падает.
// Править его можно только вместе с константой и ссылкой на источник.
const (
	pinnedUSDCMainnet = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	pinnedUSDCSepolia = "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"
)

func TestUSDCContractsArePinned(t *testing.T) {
	if USDCMainnetContract != pinnedUSDCMainnet {
		t.Errorf("mainnet USDC contract changed: %s", USDCMainnetContract)
	}
	if USDCSepoliaContract != pinnedUSDCSepolia {
		t.Errorf("Sepolia USDC contract changed: %s", USDCSepoliaContract)
	}
}

func TestUSDCContractsAreChecksummedAndDistinct(t *testing.T) {
	for name, s := range map[string]string{"main": USDCMainnetContract, "sepolia": USDCSepoliaContract} {
		_, checksummed, err := parseAddress(s)
		if err != nil || !checksummed {
			t.Errorf("%s contract %s: checksummed=%v err=%v", name, s, checksummed, err)
		}
	}
	if USDCMainnetContract == USDCSepoliaContract {
		t.Error("main and Sepolia must use different contracts")
	}
}

func TestParamsFor(t *testing.T) {
	main, err := paramsFor(domain.ProfileMain)
	if err != nil || main.chainID != 1 || main.usdc.String() != pinnedUSDCMainnet {
		t.Errorf("main = %+v, %v", main, err)
	}
	test, err := paramsFor(domain.ProfileTest)
	if err != nil || test.chainID != 11155111 || test.usdc.String() != pinnedUSDCSepolia {
		t.Errorf("test = %+v, %v", test, err)
	}
	if _, err := paramsFor(domain.Profile("dev")); err == nil {
		t.Error("unknown profile accepted")
	}
}

func TestDecimals(t *testing.T) {
	for asset, want := range map[domain.Asset]uint8{domain.ETH: ETHDecimals, domain.USDC: USDCDecimals} {
		info, ok := asset.Info()
		if !ok || info.Decimals != want || info.Network != domain.NetworkEthereum {
			t.Errorf("%s: domain reference book = %+v, adapter expects %d decimals", asset, info, want)
		}
	}
}
