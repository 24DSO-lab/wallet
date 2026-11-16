package hd_test

import (
	"testing"

	"github.com/24DSO-lab/wallet/internal/chain/btc"
	"github.com/24DSO-lab/wallet/internal/chain/eth"
	"github.com/24DSO-lab/wallet/internal/chain/tron"
	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd"
	"github.com/24DSO-lab/wallet/internal/secret"
)

// This acceptance test connects the BIP39/BIP32 derivation to the production
// chain adapters. The expected values are independently generated golden
// vectors for the public BIP39 "abandon ... about" test mnemonic. Never use a
// real wallet mnemonic or secret in this test.
func TestProductionAdaptersMatchIndependentAddressVectors(t *testing.T) {
	const mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	seed, err := hd.SeedFromMnemonic(secret.FromString(mnemonic), secret.Bytes{})
	if err != nil {
		t.Fatalf("derive BIP39 seed: %v", err)
	}
	defer seed.Wipe()

	main, err := hd.DerivePublicData(seed, domain.ProfileMain)
	if err != nil {
		t.Fatalf("derive mainnet public data: %v", err)
	}
	test, err := hd.DerivePublicData(seed, domain.ProfileTest)
	if err != nil {
		t.Fatalf("derive testnet public data: %v", err)
	}

	// ETH and TRON use the same account paths and therefore the same public
	// keys in both profiles. Exercise the actual production adapters rather
	// than only the independent hdtest reference implementation.
	ethAdapter := &eth.Adapter{}
	tronAdapter := &tron.Adapter{}
	gotETH, err := ethAdapter.AddressFromPublicKey(main.ETHPublicKey[:])
	if err != nil {
		t.Fatalf("production ETH adapter: %v", err)
	}
	if want := "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"; gotETH != want {
		t.Errorf("ETH address = %s, want %s", gotETH, want)
	}
	gotTRON, err := tronAdapter.AddressFromPublicKey(main.TronPublicKey[:])
	if err != nil {
		t.Fatalf("production TRON adapter: %v", err)
	}
	if want := "TUEZSdKsoDHQMeZwihtdoBiN46zxhGWYdH"; gotTRON != want {
		t.Errorf("TRON address = %s, want %s", gotTRON, want)
	}
	if main.ETHPublicKey != test.ETHPublicKey || main.TronPublicKey != test.TronPublicKey {
		t.Fatal("ETH/TRON public keys unexpectedly depend on Bitcoin network profile")
	}

	// BTC uses the account xpub and non-hardened public derivation in the
	// production scanner adapter. Check receive and change branches on both
	// profiles, including the returned BIP84 path.
	deriver := btc.HDAddressDeriver{}
	cases := []struct {
		name, xpub string
		profile domain.Profile
		chain domain.AddressChain
		index uint32
		wantAddress, wantPath string
	}{
		{"main receive 0", main.BTCXpub, domain.ProfileMain, domain.ChainReceive, 0, "bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu", "m/84'/0'/0'/0/0"},
		{"main receive 1", main.BTCXpub, domain.ProfileMain, domain.ChainReceive, 1, "bc1qnjg0jd8228aq7egyzacy8cys3knf9xvrerkf9g", "m/84'/0'/0'/0/1"},
		{"main change 0", main.BTCXpub, domain.ProfileMain, domain.ChainChange, 0, "bc1q8c6fshw2dlwun7ekn9qwf37cu2rn755upcp6el", "m/84'/0'/0'/1/0"},
		{"test receive 0", test.BTCXpub, domain.ProfileTest, domain.ChainReceive, 0, "tb1q6rz28mcfaxtmd6v789l9rrlrusdprr9pqcpvkl", "m/84'/1'/0'/0/0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			address, path, err := deriver.DeriveAddress(tc.xpub, tc.profile, tc.chain, tc.index)
			if err != nil {
				t.Fatalf("production BTC adapter: %v", err)
			}
			if address != tc.wantAddress {
				t.Errorf("BTC address = %s, want %s", address, tc.wantAddress)
			}
			if path != tc.wantPath {
				t.Errorf("BTC path = %s, want %s", path, tc.wantPath)
			}
			if err := btc.ValidateAddress(tc.profile, address); err != nil {
				t.Errorf("production BTC address failed validation: %v", err)
			}
		})
	}
}
