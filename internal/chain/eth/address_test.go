package eth

import (
	"errors"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
)

// Векторы из текста EIP-55.
var eip55Vectors = []string{
	// Только заглавные: контрольной суммы нет, форма совпадает с суммой по случайности.
	"0x52908400098527886E0F7030069857D2E4169EE7",
	"0x8617E340B3D01FA5F11F306F4090FD50E238070D",
	// Только строчные.
	"0xde709f2102306220921060314715629080e2fb77",
	"0x27b1fdb04752bbc536007a920d24acb045561c26",
	// Смешанный регистр.
	"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
	"0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
	"0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
	"0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb",
}

func TestEIP55Vectors(t *testing.T) {
	for _, v := range eip55Vectors {
		a, _, err := parseAddress(v)
		if err != nil {
			t.Fatalf("parseAddress(%s): %v", v, err)
		}
		if got := a.String(); got != v {
			t.Errorf("checksum of %s = %s", v, got)
		}
	}
}

func TestParseAddressChecksumFlag(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", true},
		{"0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", false}, // строчные
		{"0x5AAEB6053F3E94C9B9A09F33669435E7EF1BEAED", false}, // заглавные
		{"0x1111111111111111111111111111111111111111", false}, // букв нет
	}
	for _, tt := range tests {
		_, got, err := parseAddress(tt.addr)
		if err != nil || got != tt.want {
			t.Errorf("parseAddress(%s) = checksummed %v, err %v; want %v", tt.addr, got, err, tt.want)
		}
	}
}

func TestValidateAddressRejects(t *testing.T) {
	const good = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	// Одна буква в другом регистре ломает контрольную сумму.
	flipped := strings.Replace(good, "aAeb", "AAeb", 1)

	tests := []struct {
		name, addr string
		is         error
	}{
		{"empty", "", ErrAddressFormat},
		{"short", "0x5aAeb6", ErrAddressFormat},
		{"only prefix", "0x", ErrAddressFormat},
		{"no prefix", good[2:], ErrAddressFormat},
		{"upper prefix", "0X" + good[2:], ErrAddressFormat},
		{"41 digits", good + "0", ErrAddressFormat},
		{"39 digits", good[:len(good)-1], ErrAddressFormat},
		{"non-hex", "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeg", ErrAddressFormat},
		{"spaces", " " + good, ErrAddressFormat},
		{"trailing newline", good + "\n", ErrAddressFormat},
		{"unicode digit", "0x" + strings.Repeat("٣", 40), ErrAddressFormat},
		{"flipped case", flipped, ErrAddressChecksum},
		{"zero address", "0x0000000000000000000000000000000000000000", ErrZeroAddress},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAddress(tt.addr)
			if err == nil {
				t.Fatalf("ValidateAddress(%q) accepted", tt.addr)
			}
			if !errors.Is(err, tt.is) {
				t.Errorf("error = %v, want %v", err, tt.is)
			}
			if code := domain.CodeOf(err); code != domain.CodeInvalidAddress {
				t.Errorf("code = %q, want %q", code, domain.CodeInvalidAddress)
			}
		})
	}
}

func TestValidateAddressAccepts(t *testing.T) {
	for _, v := range eip55Vectors {
		if err := ValidateAddress(v); err != nil {
			t.Errorf("ValidateAddress(%s): %v", v, err)
		}
	}
}

func TestAddressWarnings(t *testing.T) {
	if w := addressWarnings("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"); len(w) != 0 {
		t.Errorf("checksummed address warned: %v", w)
	}
	for _, v := range []string{
		"0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed",
		"0x5AAEB6053F3E94C9B9A09F33669435E7EF1BEAED",
	} {
		w := addressWarnings(v)
		if len(w) != 1 || w[0].Code != domain.WarnNoChecksumAddress {
			t.Errorf("addressWarnings(%s) = %v, want one no_checksum_address", v, w)
		}
	}
	if w := addressWarnings("garbage"); len(w) != 0 {
		t.Errorf("invalid address produced warnings %v (it must fail validation instead)", w)
	}
}

func scalarOf(k byte) []byte {
	b := make([]byte, secp256k1.ScalarLen)
	b[len(b)-1] = k
	return b
}

// Адреса приватных ключей 1, 2 и 3 общеизвестны.
func TestAddressFromPublicKeyKnownKeys(t *testing.T) {
	tests := []struct {
		k    byte
		want string
	}{
		{1, "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf"},
		{2, "0x2B5AD5c4795c026514f8317c7a215E218DcCD6cF"},
		{3, "0x6813Eb9362372EEF6200f3b1dbC3f819671cBA69"},
	}
	for _, tt := range tests {
		pt, err := secp256k1.ScalarBaseMult(scalarOf(tt.k))
		if err != nil {
			t.Fatal(err)
		}
		pub := pt.Uncompressed()
		got, err := addressFromPublicKey(pub[:])
		if err != nil {
			t.Fatalf("key %d: %v", tt.k, err)
		}
		if got.String() != tt.want {
			t.Errorf("key %d: address %s, want %s", tt.k, got, tt.want)
		}
	}
}

func TestAddressFromPublicKeyRejects(t *testing.T) {
	pt, _ := secp256k1.ScalarBaseMult(scalarOf(1))
	good := pt.Uncompressed()
	comp := pt.Compressed()

	offCurve := good
	offCurve[64] ^= 1

	wrongPrefix := good
	wrongPrefix[0] = 0x02

	tests := []struct {
		name string
		pub  []byte
	}{
		{"nil", nil},
		{"compressed", comp[:]},
		{"truncated", good[:64]},
		{"extra byte", append(good[:], 0)},
		{"off curve", offCurve[:]},
		{"wrong prefix", wrongPrefix[:]},
		{"all zero", make([]byte, 65)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := addressFromPublicKey(tt.pub); !errors.Is(err, ErrPublicKey) {
				t.Errorf("err = %v, want ErrPublicKey", err)
			}
		})
	}
}
