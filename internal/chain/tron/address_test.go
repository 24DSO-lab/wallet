package tron

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd/hdtest"
)

func TestKeccak256Vectors(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"},
		{"abc", "4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45"},
	}
	for _, c := range cases {
		got := keccak256([]byte(c.in))
		if hex.EncodeToString(got[:]) != c.want {
			t.Errorf("keccak256(%q) = %x, want %s", c.in, got, c.want)
		}
	}
	// Сверка с независимой реализацией на длинных входах (несколько блоков).
	for _, n := range []int{1, 135, 136, 137, 272, 1000} {
		in := bytes.Repeat([]byte{0xA5}, n)
		if keccak256(in) != hdtest.Keccak256(in) {
			t.Errorf("keccak256 differs from hdtest at len %d", n)
		}
	}
}

// Вектор Dev 2 (docs: hd_test): мнемоника abandon…about, путь m/44'/195'/0'/0/0.
const (
	vecPub  = "04ff21f8e64d3a3c0198edfbb7afdc79be959432e92e2f8a1984bb436a414b8edcec0345aad0c1bf7da04fd036dd7f9f617e30669224283d950fab9dd84831dc83"
	vecAddr = "TUEZSdKsoDHQMeZwihtdoBiN46zxhGWYdH"
)

func TestAddressFromPublicKey(t *testing.T) {
	a := &Adapter{}
	pub, _ := hex.DecodeString(vecPub)
	got, err := a.AddressFromPublicKey(pub)
	if err != nil || got != vecAddr {
		t.Fatalf("got %q, %v; want %q", got, err, vecAddr)
	}
	if ref, _ := hdtest.TronAddress(pub); ref != got {
		t.Errorf("differs from hdtest: %q", ref)
	}
	for name, bad := range map[string][]byte{
		"nil": nil, "compressed": append([]byte{0x02}, pub[1:33]...), "short": pub[:64],
		"bad prefix": append([]byte{0x05}, pub[1:]...),
	} {
		if _, err := a.AddressFromPublicKey(bad); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseAddress(t *testing.T) {
	a := &Adapter{}
	if err := a.ValidateAddress(USDTContractMain); err != nil {
		t.Fatalf("known contract address rejected (checksum typo in constant?): %v", err)
	}
	if err := a.ValidateAddress(USDTContractNile); err != nil {
		t.Fatalf("nile contract address rejected: %v", err)
	}
	flip := []byte(vecAddr)
	if flip[10] == 'A' { // подмена на другой допустимый символ base58
		flip[10] = 'B'
	} else {
		flip[10] = 'A'
	}
	cases := []struct {
		name, in string
		want     error
	}{
		{"empty", "", ErrAddressEmpty},
		{"eth", "0x9858EfFD232B4033E47d90003D41EC34EcaEda94", ErrAddressEthereum},
		{"btc", "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq", ErrAddressBitcoin},
		{"not T", "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", ErrAddressNotTron},
		{"checksum", string(flip), ErrAddressChecksum},
		{"bad char", "T0EZSdKsoDHQMeZwihtdoBiN46zxhGWYdH", ErrAddressFormat},
		{"too long", vecAddr + vecAddr, ErrAddressLength},
	}
	for _, c := range cases {
		err := a.ValidateAddress(c.in)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
		if err != nil && domain.CodeOf(err) != domain.CodeInvalidAddress {
			t.Errorf("%s: code %q", c.name, domain.CodeOf(err))
		}
	}
	// Верная сумма, но чужой префикс (0x42): отклоняется по префиксу.
	p := make([]byte, 21)
	p[0] = 0x42
	if err := a.ValidateAddress(base58CheckEncode(p)); err == nil {
		t.Error("foreign prefix accepted")
	}
}

func FuzzParseAddress(f *testing.F) {
	for _, s := range []string{vecAddr, USDTContractMain, "", "0x00", "T", "TTTTTTTTTTTTTTTTTTTTTTTTTTTTTTTTTT"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		addr, err := ParseAddress(s)
		if err != nil {
			return
		}
		if addr.String() != s {
			t.Fatalf("accepted non-canonical address %q -> %q", s, addr.String())
		}
	})
}

func FuzzBase58Check(f *testing.F) {
	f.Add([]byte{0x41, 1, 2, 3})
	f.Add([]byte{0, 0, 0})
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) == 0 {
			// Пустой payload декодер отклоняет намеренно.
			if _, err := base58CheckDecode(base58CheckEncode(payload)); err == nil {
				t.Fatal("empty payload accepted")
			}
			return
		}
		got, err := base58CheckDecode(base58CheckEncode(payload))
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("roundtrip failed: %x %v", payload, err)
		}
	})
}
