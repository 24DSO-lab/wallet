package btc

import (
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
)

func testWitnessAddress(hrp string, program []byte) string {
	data, _ := convertBits(program, 8, 5, true)
	payload := append([]byte{0}, data...)
	values := append(hrpExpand(hrp), payload...)
	values = append(values, 0, 0, 0, 0, 0, 0)
	mod := bech32Polymod(values) ^ 1
	checksum := make([]byte, 6)
	for i := range checksum {
		checksum[i] = byte((mod >> (5 * (5 - i))) & 31)
	}
	var b strings.Builder
	b.WriteString(hrp)
	b.WriteByte('1')
	for _, v := range payload {
		b.WriteByte(charset[v])
	}
	for _, v := range checksum {
		b.WriteByte(charset[v])
	}
	return b.String()
}

func TestValidateAddress(t *testing.T) {
	program := make([]byte, 20)
	for i := range program {
		program[i] = byte(i + 1)
	}
	main := testWitnessAddress("bc", program)
	testnet := testWitnessAddress("tb", program)
	tests := []struct {
		name string
		profile domain.Profile
		address string
		wantErr bool
		code domain.ErrorCode
	}{
		{"mainnet", domain.ProfileMain, main, false, ""},
		{"testnet", domain.ProfileTest, testnet, false, ""},
		{"wrong hrp", domain.ProfileTest, main, true, domain.CodeWrongNetwork},
		{"mixed case", domain.ProfileMain, strings.ToUpper(main[:5]) + main[5:], true, domain.CodeInvalidAddress},
		{"bad checksum", domain.ProfileMain, main[:len(main)-1] + "q", true, domain.CodeInvalidAddress},
		{"empty", domain.ProfileMain, "", true, domain.CodeInvalidAddress},
		{"invalid profile", "unknown", main, true, domain.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAddress(tt.profile, tt.address)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAddress() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.code != "" && domain.CodeOf(err) != tt.code {
				t.Fatalf("error code = %q, want %q", domain.CodeOf(err), tt.code)
			}
		})
	}
}

func TestValidateAddressRejectsNonP2WPKH(t *testing.T) {
	program := make([]byte, 32)
	addr := testWitnessAddress("tb", program)
	if err := ValidateAddress(domain.ProfileTest, addr); domain.CodeOf(err) != domain.CodeInvalidAddress {
		t.Fatalf("32-byte witness program must be rejected, got %v", err)
	}
}
