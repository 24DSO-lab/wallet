package eth

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"
)

// Эталоны из описания RLP (ethereum.org, "RLP").
func TestRLPEncodeKnownValues(t *testing.T) {
	long := strings.Repeat("a", 56)
	tests := []struct {
		name string
		got  []byte
		want string
	}{
		{"dog", rlpBytes([]byte("dog")), "83646f67"},
		{"empty string", rlpBytes(nil), "80"},
		{"byte 0x00", rlpBytes([]byte{0}), "00"},
		{"byte 0x0f", rlpBytes([]byte{0x0f}), "0f"},
		{"byte 0x80", rlpBytes([]byte{0x80}), "8180"},
		{"integer 0", rlpUint(big.NewInt(0)), "80"},
		{"integer 15", rlpUint(big.NewInt(15)), "0f"},
		{"integer 1024", rlpUint(big.NewInt(1024)), "820400"},
		{"empty list", rlpList(), "c0"},
		{"cat dog", rlpList(rlpBytes([]byte("cat")), rlpBytes([]byte("dog"))), "c88363617483646f67"},
		{"set of three", rlpList(rlpList(), rlpList(rlpList()), rlpList(rlpList(), rlpList(rlpList()))), "c7c0c1c0c3c0c1c0"},
		{"55 bytes", rlpBytes(bytes.Repeat([]byte("a"), 55)), "b7" + strings.Repeat("61", 55)},
		{"56 bytes", rlpBytes([]byte(long)), "b838" + strings.Repeat("61", 56)},
		{"Lorem", rlpBytes([]byte("Lorem ipsum dolor sit amet, consectetur adipisicing elit")), "b8384c6f72656d20697073756d20646f6c6f722073697420616d65742c20636f6e7365637465747572206164697069736963696e6720656c6974"},
	}
	for _, tt := range tests {
		if hex.EncodeToString(tt.got) != tt.want {
			t.Errorf("%s: got %x, want %s", tt.name, tt.got, tt.want)
		}
	}
}

func TestRLPListLongForm(t *testing.T) {
	items := make([][]byte, 0, 30)
	for range 30 {
		items = append(items, rlpBytes([]byte("ab"))) // 3 байта каждый, всего 90
	}
	enc := rlpList(items...)
	if enc[0] != 0xf8 || enc[1] != 90 {
		t.Fatalf("header = %x", enc[:2])
	}
	it, err := rlpDecode(enc)
	if err != nil || !it.List || len(it.Items) != 30 {
		t.Fatalf("decode: %v %+v", err, it)
	}
}

func TestRLPDecodeRoundTrip(t *testing.T) {
	enc := rlpList(rlpUint64(1024), rlpBytes([]byte("dog")), rlpList(rlpBytes(nil), rlpBytes([]byte{0x7f})))
	it, err := rlpDecode(enc)
	if err != nil {
		t.Fatal(err)
	}
	n, err := it.Items[0].uint64()
	if err != nil || n != 1024 || string(it.Items[1].Data) != "dog" || !it.Items[2].List || len(it.Items[2].Items) != 2 {
		t.Errorf("decoded = %+v (%d, %v)", it, n, err)
	}
}

func TestRLPDecodeRejects(t *testing.T) {
	deep := rlpList()
	for range rlpMaxDepth + 2 {
		deep = rlpList(deep)
	}
	tests := []struct {
		name string
		in   string
		is   error
	}{
		{"empty", "", ErrRLP},
		{"truncated string", "83646f", ErrRLP},
		{"truncated list", "c883636174", ErrRLP},
		{"trailing byte", "83646f6700", ErrRLP},
		{"single byte with prefix", "8100", errRLPNonCanon},
		{"single byte 7f with prefix", "817f", errRLPNonCanon},
		{"long form for short string", "b80100", errRLPNonCanon}, // длина 1 в длинной форме
		{"long form 55", "b837" + strings.Repeat("61", 55), errRLPNonCanon},
		{"length leading zero", "b900" + "3800" + strings.Repeat("61", 56), errRLPNonCanon},
		{"long list form for short", "f800", errRLPNonCanon},
		{"length overflows input", "bbffffffff", ErrRLP},
		{"huge length of length", "bf0100000000000000", ErrRLP},
		{"length longer than input", "b8ff61", ErrRLP},
	}
	for _, tt := range tests {
		raw, _ := hex.DecodeString(tt.in)
		if _, err := rlpDecode(raw); !errors.Is(err, tt.is) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.is)
		}
	}
	if _, err := rlpDecode(deep); !errors.Is(err, errRLPDepth) {
		t.Errorf("deep nesting: %v", err)
	}
}

func TestRLPIntegerCanonical(t *testing.T) {
	good := rlpItem{Data: []byte{0x04, 0x00}}
	if n, err := good.uint64(); err != nil || n != 1024 {
		t.Errorf("1024: %d %v", n, err)
	}
	bad := map[string]rlpItem{
		"leading zero": {Data: []byte{0x00, 0x01}},
		"zero byte":    {Data: []byte{0x00}}, // ноль обязан быть пустой строкой
		"nine bytes":   {Data: bytes.Repeat([]byte{1}, 9)},
		"list":         {List: true},
	}
	for name, it := range bad {
		if _, err := it.uint64(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := (rlpItem{Data: bytes.Repeat([]byte{1}, 33)}).bigInt(32); err == nil {
		t.Error("33-byte number accepted as uint256")
	}
}

// FuzzRLPDecode: разбор не паникует, а всё принятое кодируется обратно в те же
// байты (каноничность: один вход, одно прочтение).
func FuzzRLPDecode(f *testing.F) {
	for _, s := range []string{"c0", "83646f67", "c88363617483646f67", "c7c0c1c0c3c0c1c0", "b838" + strings.Repeat("61", 56)} {
		b, _ := hex.DecodeString(s)
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		it, err := rlpDecode(b)
		if err != nil {
			return
		}
		if back := reencode(it); !bytes.Equal(back, b) {
			t.Fatalf("accepted %x re-encodes to %x", b, back)
		}
	})
}

func reencode(it rlpItem) []byte {
	if !it.List {
		return rlpBytes(it.Data)
	}
	parts := make([][]byte, 0, len(it.Items))
	for _, c := range it.Items {
		parts = append(parts, reencode(c))
	}
	return rlpList(parts...)
}
