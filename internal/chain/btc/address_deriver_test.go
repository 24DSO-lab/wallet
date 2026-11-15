package btc

import (
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
)

func TestEncodeP2WPKHAddressRoundTrip(t *testing.T) {
	program := make([]byte, 20)
	for i := range program {
		program[i] = byte(i + 1)
	}
	for _, profile := range []domain.Profile{domain.ProfileMain, domain.ProfileTest} {
		t.Run(string(profile), func(t *testing.T) {
			address, err := encodeP2WPKHAddress(profile, program)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if err := ValidateAddress(profile, address); err != nil {
				t.Fatalf("ValidateAddress(%q): %v", address, err)
			}
			hrp, version, got, err := decodeWitnessAddress(address)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			wantHRP := "bc"
			if profile == domain.ProfileTest {
				wantHRP = "tb"
			}
			if hrp != wantHRP || version != 0 {
				t.Fatalf("decoded hrp/version = %q/%d, want %q/0", hrp, version, wantHRP)
			}
			if string(got) != string(program) {
				t.Fatalf("decoded program = %x, want %x", got, program)
			}
		})
	}
}

func TestEncodeP2WPKHAddressRejectsInvalidInput(t *testing.T) {
	if _, err := encodeP2WPKHAddress(domain.ProfileTest, make([]byte, 19)); err == nil {
		t.Fatal("expected invalid program length error")
	}
	if _, err := encodeP2WPKHAddress(domain.Profile("unknown"), make([]byte, 20)); err == nil {
		t.Fatal("expected invalid profile error")
	}
}

func TestHDAddressDeriverRejectsInvalidXpubAndProfile(t *testing.T) {
	deriver := HDAddressDeriver{}
	if _, _, err := deriver.DeriveAddress("not-an-xpub", domain.ProfileTest, domain.ChainReceive, 0); err == nil {
		t.Fatal("expected invalid xpub error")
	}
	if _, _, err := deriver.DeriveAddress("not-an-xpub", domain.ProfileTest, domain.AddressChain("other"), 0); err == nil {
		t.Fatal("expected invalid chain error")
	}
	if _, _, err := deriver.DeriveAddress("not-an-xpub", domain.ProfileTest, domain.ChainReceive, hdHardenedIndex); err == nil {
		t.Fatal("expected hardened address index error")
	}
}
