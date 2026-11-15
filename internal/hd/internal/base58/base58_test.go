package base58

import (
	"encoding/hex"
	"errors"
	"testing"
)

// Эталоны получены независимой реализацией на Python.
var base58Vectors = []struct{ payload, want string }{
	{"", "3QJmnh"},
	{"00", "1Wh4bh"},
	{"000001", "11BwW2qR"},
	{"41000102030405060708090a0b0c0d0e0f10111213", "T9yED5xMV5ARV98BexN97aLZ1UUq7eKSxm"},
	{"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f404142434445464748494a4b4c4d", "13p53kFicsFQWKrmyXHm5KJdnpR1JtA12efDMRUz1VycEJUPzYEakyiCz8m2iX9zJjNcrMfbSo3vMyjyJ9qmLdg8eK6wgAcYXrXd9xLCCReKp43"},
	{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "2wkBET2rRgE8pahuaczxKbmv7ciehqsne57F9gtzf1PVZS9BEY"},
	{"00000000000102", "11111WBbv7Sp"},
}

func TestCheckEncodeDecode(t *testing.T) {
	for _, v := range base58Vectors {
		payload, _ := hex.DecodeString(v.payload)
		if got := CheckEncode(payload); got != v.want {
			t.Errorf("CheckEncode(%s) = %s, want %s", v.payload, got, v.want)
		}
		back, err := CheckDecode(v.want)
		if err != nil || hex.EncodeToString(back) != v.payload {
			t.Errorf("CheckDecode(%s) = %x, %v", v.want, back, err)
		}
	}
}

func TestCheckDecodeRejects(t *testing.T) {
	good := CheckEncode([]byte{0x41, 1, 2, 3})
	tampered := []byte(good)
	if tampered[3] == 'z' {
		tampered[3] = 'y'
	} else {
		tampered[3]++
	}
	for name, tc := range map[string]struct {
		in   string
		want error
	}{
		"bad checksum": {string(tampered), ErrChecksum},
		"bad char 0":   {"0OIl", ErrInvalidChar},
		"too short":    {"1", ErrTooShort},
		"empty":        {"", ErrTooShort},
	} {
		if _, err := CheckDecode(tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}

func FuzzCheckDecode(f *testing.F) {
	f.Add("3QJmnh")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		payload, err := CheckDecode(s)
		if err != nil {
			return
		}
		// Всё, что разобралось, обязано кодироваться обратно без потерь
		// (кроме ведущих единиц, которые кодируются нулевыми байтами).
		if got := CheckEncode(payload); got != s {
			t.Fatalf("%q decoded to %x but re-encodes as %q", s, payload, got)
		}
	})
}

func TestCheckDecodeRejectsLongInput(t *testing.T) {
	if _, err := CheckDecode(string(make([]byte, maxEncodedLen+1))); !errors.Is(err, ErrTooLong) {
		t.Errorf("got %v", err)
	}
	long := make([]byte, 1<<20)
	for i := range long {
		long[i] = '2'
	}
	if _, err := CheckDecode(string(long)); !errors.Is(err, ErrTooLong) {
		t.Errorf("1 MiB input: %v", err)
	}
}
