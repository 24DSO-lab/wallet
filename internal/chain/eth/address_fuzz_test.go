package eth

import (
	"strings"
	"testing"
)

// FuzzParseAddress проверяет, что разбор не паникует и что всё принятое
// возвращается в каноническую форму, которую разбор принимает с контрольной суммой.
func FuzzParseAddress(f *testing.F) {
	for _, v := range eip55Vectors {
		f.Add(v)
	}
	f.Add("")
	f.Add("0x")
	f.Add("0x0000000000000000000000000000000000000000")
	f.Add("0X5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed")
	f.Add("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD")

	f.Fuzz(func(t *testing.T, s string) {
		a, checksummed, err := parseAddress(s)
		verr := ValidateAddress(s)
		if err != nil {
			if verr == nil {
				t.Fatalf("ValidateAddress accepted %q that parseAddress rejects", s)
			}
			return
		}
		canonical := a.String()
		b, ok, err := parseAddress(canonical)
		if err != nil || b != a {
			t.Fatalf("canonical form %q does not round-trip: %v", canonical, err)
		}
		// Каноническая форма несёт контрольную сумму, если в ней есть буквы
		// обоих регистров; иначе проверять нечем и признак ложен.
		body := canonical[2:]
		mixed := body != strings.ToLower(body) && body != strings.ToUpper(body)
		if ok != mixed {
			t.Fatalf("canonical %q: checksummed=%v, mixed case=%v", canonical, ok, mixed)
		}
		if checksummed && s != canonical {
			t.Fatalf("%q claims a valid checksum but differs from canonical %q", s, canonical)
		}
		if _, _, err := parseAddress(strings.ToLower(canonical)); err != nil {
			t.Fatalf("lowercase form of %q rejected: %v", canonical, err)
		}
	})
}

// FuzzAddressFromPublicKey: любой вход либо отклоняется, либо даёт адрес,
// который проходит собственную проверку.
func FuzzAddressFromPublicKey(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, 65))
	f.Add(append([]byte{0x04}, make([]byte, 64)...))
	f.Fuzz(func(t *testing.T, pub []byte) {
		a, err := addressFromPublicKey(pub)
		if err != nil {
			return
		}
		if err := ValidateAddress(a.String()); err != nil {
			t.Fatalf("derived address %s fails validation: %v", a, err)
		}
	})
}
