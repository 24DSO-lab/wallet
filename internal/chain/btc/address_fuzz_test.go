package btc

import (
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
)

func FuzzValidateAddress(f *testing.F) {
	for _, address := range []string{
		"", "bc1", "tb1", "bc1qqqqqq", "BC1QW508D6QEJXTDG4Y5R3ZARVARY0C5XW7KG3G4TY",
		"bc1\x00qqqqqq", "bc1éqqqqqq",
	} {
		f.Add(address)
	}
	f.Fuzz(func(t *testing.T, address string) {
		_ = ValidateAddress(domain.ProfileTest, address)
	})
}
