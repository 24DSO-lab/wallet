package hdtest

import (
	"crypto/sha3"
	"encoding/hex"
	"testing"
)

func TestKeccakAgainstKnownAndStdlib(t *testing.T) {
	for in, want := range map[string]string{
		"":    "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470",
		"abc": "4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45",
	} {
		got := Keccak256([]byte(in))
		if hex.EncodeToString(got[:]) != want {
			t.Errorf("Keccak256(%q) = %x, want %s", in, got, want)
		}
	}
	// Та же перестановка с другим байтом дополнения обязана совпасть с SHA3-256
	// стандартной библиотеки, на длинах до и после границы блока в 136 байт.
	for _, n := range []int{0, 1, 135, 136, 137, 272, 1000} {
		msg := make([]byte, n)
		for i := range msg {
			msg[i] = byte(i*31 + 7)
		}
		want := sha3.Sum256(msg)
		if got := SHA3256(msg); got != want {
			t.Errorf("len %d: sponge %x != stdlib %x", n, got, want)
		}
	}
}
