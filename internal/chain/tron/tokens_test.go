package tron

import (
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// TestUSDTContractPinned падает при любой правке адресов контрактов:
// подмена константы подменяет токен. Менять значения только вместе с
// ContractSource и отдельным PR (ADR).
func TestUSDTContractPinned(t *testing.T) {
	if USDTContractMain != "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t" {
		t.Fatal("USDT main contract address changed")
	}
	if USDTContractNile != "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf" {
		t.Fatal("USDT Nile contract address changed")
	}
	a, err := ParseAddress(USDTContractMain)
	if err != nil || a.Hex() != "41a614f803b6fd780986a42c78ec9c7f77e6ded13c" {
		t.Fatalf("main contract hex form: %v %s", err, a.Hex())
	}
	for p, want := range map[domain.Profile]string{domain.ProfileMain: USDTContractMain, domain.ProfileTest: USDTContractNile} {
		got, err := usdtContract(p)
		if err != nil || got.String() != want {
			t.Errorf("profile %s: %v %v", p, got, err)
		}
		if USDTSources[p].Address != want {
			t.Errorf("profile %s: source record points to another address", p)
		}
	}
	if info, _ := domain.USDT.Info(); info.Decimals != USDTDecimals {
		t.Errorf("decimals: domain says %d, tron says %d", info.Decimals, USDTDecimals)
	}
}

// TestUSDTSourcesRecorded требует, чтобы человек сверил адреса с первоисточником
// и записал дату. Красный тест здесь значит: адреса ещё не сверены.
func TestUSDTSourcesRecorded(t *testing.T) {
	for p, s := range USDTSources {
		if s.Source == "" || s.CheckedOn == "" {
			t.Errorf("profile %s: contract %s is not verified against its source (set CheckedOn after checking %q)", p, s.Address, s.Source)
		}
	}
}
