package tron

import "math/bits"

// Keccak-256 в варианте Ethereum (дополнение 0x01, не SHA3-256). Стандартная
// библиотека даёт только SHA3, а golang.org/x/crypto/sha3 не входит в vendor
// проекта, поэтому перестановка записана здесь (около 60 строк). Проверяется
// известными векторами и сверкой с независимой реализацией в hdtest.

var keccakRC = [24]uint64{
	0x0000000000000001, 0x0000000000008082, 0x800000000000808a, 0x8000000080008000,
	0x000000000000808b, 0x0000000080000001, 0x8000000080008081, 0x8000000000008009,
	0x000000000000008a, 0x0000000000000088, 0x0000000080008009, 0x000000008000000a,
	0x000000008000808b, 0x800000000000008b, 0x8000000000008089, 0x8000000000008003,
	0x8000000000008002, 0x8000000000000080, 0x000000000000800a, 0x800000008000000a,
	0x8000000080008081, 0x8000000000008080, 0x0000000080000001, 0x8000000080008008,
}

var (
	keccakRot = [24]int{1, 3, 6, 10, 15, 21, 28, 36, 45, 55, 2, 14, 27, 41, 56, 8, 25, 43, 62, 18, 39, 61, 20, 44}
	keccakPi  = [24]int{10, 7, 11, 17, 18, 3, 5, 16, 8, 21, 24, 4, 15, 23, 19, 13, 12, 2, 20, 14, 22, 9, 6, 1}
)

func keccakF(st *[25]uint64) {
	var bc [5]uint64
	for round := range 24 {
		for i := range 5 {
			bc[i] = st[i] ^ st[i+5] ^ st[i+10] ^ st[i+15] ^ st[i+20]
		}
		for i := range 5 {
			t := bc[(i+4)%5] ^ bits.RotateLeft64(bc[(i+1)%5], 1)
			for j := 0; j < 25; j += 5 {
				st[j+i] ^= t
			}
		}
		t := st[1]
		for i := range 24 {
			j := keccakPi[i]
			b := st[j]
			st[j] = bits.RotateLeft64(t, keccakRot[i])
			t = b
		}
		for j := 0; j < 25; j += 5 {
			for i := range 5 {
				bc[i] = st[j+i]
			}
			for i := range 5 {
				st[j+i] ^= ^bc[(i+1)%5] & bc[(i+2)%5]
			}
		}
		st[0] ^= keccakRC[round]
	}
}

// keccak256 возвращает Keccak-256 (Ethereum) от data.
func keccak256(data []byte) [32]byte {
	const rate = 136
	var st [25]uint64
	absorb := func(block []byte) {
		for i := 0; i < rate/8; i++ {
			var w uint64
			for k := 7; k >= 0; k-- {
				w = w<<8 | uint64(block[i*8+k])
			}
			st[i] ^= w
		}
		keccakF(&st)
	}
	for len(data) >= rate {
		absorb(data[:rate])
		data = data[rate:]
	}
	last := make([]byte, rate)
	copy(last, data)
	last[len(data)] ^= 0x01
	last[rate-1] ^= 0x80
	absorb(last)
	var out [32]byte
	for i := range 4 {
		for k := range 8 {
			out[i*8+k] = byte(st[i] >> (8 * k))
		}
	}
	return out
}
