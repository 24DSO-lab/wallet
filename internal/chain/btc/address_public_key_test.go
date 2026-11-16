package btc

import (
	"encoding/hex"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
)

func TestAddressFromPublicKey(t *testing.T) {
	// Private scalar 1 has the standard secp256k1 generator point as public key.
	var privateKey [secp256k1.ScalarLen]byte
	privateKey[len(privateKey)-1] = 1
	point, err := secp256k1.ScalarBaseMult(privateKey[:])
	if err != nil {
		t.Fatal(err)
	}
	uncompressed := point.Uncompressed()

	const wantHash160 = "751e76e8199196d454941c45d1b3a323f1433bd6"
	tests := []struct {
		name    string
		profile domain.Profile
		hrp     string
	}{
		{name: "mainnet", profile: domain.ProfileMain, hrp: "bc"},
		{name: "testnet", profile: domain.ProfileTest, hrp: "tb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			address, err := AddressFromPublicKey(tt.profile, uncompressed[:])
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateAddress(tt.profile, address); err != nil {
				t.Fatalf("derived address is invalid: %v", err)
			}
			hrp, version, program, err := decodeWitnessAddress(address)
			if err != nil {
				t.Fatal(err)
			}
			if hrp != tt.hrp || version != 0 {
				t.Fatalf("decoded address = (%q, %d), want (%q, 0)", hrp, version, tt.hrp)
			}
			if got := hex.EncodeToString(program); got != wantHash160 {
				t.Fatalf("witness program = %s, want %s", got, wantHash160)
			}
		})
	}
}

func TestAddressFromPublicKeyRejectsInvalidInput(t *testing.T) {
	valid, err := secp256k1.ScalarBaseMult(append(make([]byte, secp256k1.ScalarLen-1), 1))
	if err != nil {
		t.Fatal(err)
	}
	uncompressed := valid.Uncompressed()

	tests := []struct {
		name    string
		profile domain.Profile
		pub     []byte
	}{
		{name: "invalid profile", profile: "unknown", pub: uncompressed[:]},
		{name: "compressed key", profile: domain.ProfileTest, pub: valid.Compressed()[:]},
		{name: "invalid point", profile: domain.ProfileTest, pub: make([]byte, secp256k1.PubKeyUncompressedLen)},
		{name: "empty key", profile: domain.ProfileTest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if address, err := AddressFromPublicKey(tt.profile, tt.pub); err == nil || address != "" {
				t.Fatalf("AddressFromPublicKey() = (%q, %v), want error", address, err)
			}
		})
	}
}
