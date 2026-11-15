package eth

import (
	"bytes"
	"crypto/sha3"
	"encoding/hex"
	"testing"
)

func TestKeccak256KnownValues(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"empty", "", "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"},
		{"abc", "abc", "4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keccak256([]byte(tt.in))
			if hex.EncodeToString(got[:]) != tt.want {
				t.Fatalf("keccak256(%q) = %x, want %s", tt.in, got, tt.want)
			}
		})
	}
}

// Селекторы ERC-20 это первые 4 байта Keccak-256 от сигнатуры функции.
func TestKeccak256ERC20Selectors(t *testing.T) {
	tests := []struct{ sig, want string }{
		{"transfer(address,uint256)", "a9059cbb"},
		{"balanceOf(address)", "70a08231"},
	}
	for _, tt := range tests {
		got := keccak256([]byte(tt.sig))
		if hex.EncodeToString(got[:4]) != tt.want {
			t.Errorf("selector of %s = %x, want %s", tt.sig, got[:4], tt.want)
		}
	}
}

// Перестановка и разбиение на блоки сверяются со стандартной библиотекой:
// SHA3-256 отличается от Keccak-256 только байтом паддинга.
func TestKeccakSumMatchesStdlibSHA3(t *testing.T) {
	for n := 0; n <= 3*keccakRate+7; n++ {
		data := bytes.Repeat([]byte{byte(n), 0xa5, 0x5a}, n)[:n]
		want := sha3.Sum256(data)
		if got := keccakSum(data, 0x06); got != want {
			t.Fatalf("len %d: sponge with SHA3 padding = %x, stdlib = %x", n, got, want)
		}
	}
}

func TestKeccak256PartsEqualConcatenation(t *testing.T) {
	a, b, c := []byte("transfer"), []byte("(address,"), []byte("uint256)")
	if keccak256(a, b, c) != keccak256([]byte("transfer(address,uint256)")) {
		t.Fatal("hashing parts differs from hashing the concatenation")
	}
}
