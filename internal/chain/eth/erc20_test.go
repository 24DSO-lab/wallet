package eth

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
)

// Эталон: transfer(0x5aAe…BeAed, 1 USDC = 1000000) в ABI-кодировке.
const transferVector = "a9059cbb" +
	"0000000000000000000000005aaeb6053f3e94c9b9a09f33669435e7ef1beaed" +
	"00000000000000000000000000000000000000000000000000000000000f4240"

func TestEncodeTransferKnownVector(t *testing.T) {
	got, err := encodeTransfer(mustAddr(t, addrA), big.NewInt(1_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != transferVector {
		t.Fatalf("calldata = %x\nwant      %s", got, transferVector)
	}
}

func TestEncodeBalanceOfKnownVector(t *testing.T) {
	want := "70a08231" + "0000000000000000000000005aaeb6053f3e94c9b9a09f33669435e7ef1beaed"
	if got := hex.EncodeToString(encodeBalanceOf(mustAddr(t, addrA))); got != want {
		t.Fatalf("calldata = %s, want %s", got, want)
	}
}

func TestEncodeTransferAmountBounds(t *testing.T) {
	to := mustAddr(t, addrA)
	if _, err := encodeTransfer(to, maxUint256); err != nil {
		t.Errorf("2^256-1 rejected: %v", err)
	}
	over := new(big.Int).Add(maxUint256, big.NewInt(1))
	for name, v := range map[string]*big.Int{"nil": nil, "negative": big.NewInt(-1), "2^256": over} {
		if _, err := encodeTransfer(to, v); !errors.Is(err, ErrCalldata) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestDecodeTransferRoundTrip(t *testing.T) {
	for _, amt := range []*big.Int{big.NewInt(0), big.NewInt(1), big.NewInt(1_000_000), maxUint256} {
		data, err := encodeTransfer(mustAddr(t, addrB), amt)
		if err != nil {
			t.Fatal(err)
		}
		to, got, err := decodeTransfer(data)
		if err != nil || to != mustAddr(t, addrB) || got.Cmp(amt) != 0 {
			t.Errorf("amount %s: to %s, got %s, err %v", amt, to, got, err)
		}
	}
}

func TestDecodeTransferRejectsNonCanonical(t *testing.T) {
	good, _ := hex.DecodeString(transferVector)
	mutate := func(f func(b []byte) []byte) []byte { return f(bytes.Clone(good)) }
	tests := map[string][]byte{
		"empty":           nil,
		"selector only":   good[:4],
		"truncated":       good[:len(good)-1],
		"extra byte":      append(bytes.Clone(good), 0),
		"other selector":  mutate(func(b []byte) []byte { b[0] ^= 1; return b }),
		"transferFrom":    mutate(func(b []byte) []byte { copy(b, []byte{0x23, 0xb8, 0x72, 0xdd}); return b }),
		"dirty high addr": mutate(func(b []byte) []byte { b[4] = 1; return b }),
		"dirty addr last": mutate(func(b []byte) []byte { b[4+11] = 0xff; return b }),
	}
	for name, data := range tests {
		if _, _, err := decodeTransfer(data); !errors.Is(err, ErrCalldata) {
			t.Errorf("%s: err = %v, want ErrCalldata", name, err)
		}
	}
}

func TestDecodeWord(t *testing.T) {
	if n, err := decodeWord(append(make([]byte, 31), 7)); err != nil || n.Int64() != 7 {
		t.Errorf("word = %v, %v", n, err)
	}
	for _, b := range [][]byte{nil, make([]byte, 31), make([]byte, 33)} {
		if _, err := decodeWord(b); err == nil {
			t.Errorf("length %d accepted", len(b))
		}
	}
}

func FuzzDecodeTransfer(f *testing.F) {
	good, _ := hex.DecodeString(transferVector)
	f.Add(good)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		to, amt, err := decodeTransfer(data)
		if err != nil {
			return
		}
		// Всё принятое кодируется обратно в те же байты.
		back, err := encodeTransfer(to, amt)
		if err != nil || !bytes.Equal(back, data) {
			t.Fatalf("accepted calldata %x does not round-trip (%x, %v)", data, back, err)
		}
	})
}
