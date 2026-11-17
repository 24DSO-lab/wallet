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


// TestProductionAdaptersMatchIndependentPassphraseVectors checks the second
// published BIP39 vector (same public mnemonic, passphrase TREZOR) through the
// production address adapters. This catches regressions in passphrase handling
// as well as chain-specific public-key-to-address conversion.
func TestProductionAdaptersMatchIndependentPassphraseVectors(t *testing.T) {
	const mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	seed, err := hd.SeedFromMnemonic(secret.FromString(mnemonic), secret.FromString("TREZOR"))
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

	ethAdapter := &eth.Adapter{}
	gotETH, err := ethAdapter.AddressFromPublicKey(main.ETHPublicKey[:])
	if err != nil {
		t.Fatalf("production ETH adapter: %v", err)
	}
	if want := "0x9c32F71D4DB8Fb9e1A58B0a80dF79935e7256FA6"; gotETH != want {
		t.Errorf("ETH address = %s, want %s", gotETH, want)
	}

	tronAdapter := &tron.Adapter{}
	gotTRON, err := tronAdapter.AddressFromPublicKey(main.TronPublicKey[:])
	if err != nil {
		t.Fatalf("production TRON adapter: %v", err)
	}
	if want := "TAyDUYP5rcf56xFwrg8cU1qQwvnWpkeapM"; gotTRON != want {
		t.Errorf("TRON address = %s, want %s", gotTRON, want)
	}

	deriver := btc.HDAddressDeriver{}
	for _, tc := range []struct {
		name, xpub string
		profile    domain.Profile
		want       string
	}{
		{"main receive 0", main.BTCXpub, domain.ProfileMain, "bc1qv5rmq0kt9yz3pm36wvzct7p3x6mtgehjul0feu"},
		{"test receive 0", test.BTCXpub, domain.ProfileTest, "tb1qfwxzlmn42rh6wu6gt4f0yrfpe4gf0jtwgxagrr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, path, err := deriver.DeriveAddress(tc.xpub, tc.profile, domain.ChainReceive, 0)
			if err != nil {
				t.Fatalf("production BTC adapter: %v", err)
			}
			if got != tc.want {
				t.Errorf("BTC address = %s, want %s", got, tc.want)
			}
			if want := "m/84'/0'/0'/0/0"; tc.profile == domain.ProfileMain && path != want {
				t.Errorf("BTC path = %s, want %s", path, want)
			}
			if want := "m/84'/1'/0'/0/0"; tc.profile == domain.ProfileTest && path != want {
				t.Errorf("BTC path = %s, want %s", path, want)
			}
			if err := btc.ValidateAddress(tc.profile, got); err != nil {
				t.Errorf("production BTC address failed validation: %v", err)
			}
		})
	}
}
