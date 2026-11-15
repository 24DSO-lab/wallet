package eth

import (
	"encoding/binary"
	"math/bits"
)

// Keccak-256 в варианте Ethereum (паддинг 0x01, не NIST SHA3-256 с 0x06).
//
// Зачем свой код. golang.org/x/crypto/sha3 не входит в vendor, а загрузка
// модулей закрыта; стандартный crypto/sha3 даёт только NIST-вариант. Перестановка
// Keccak-f[1600] у обоих одна, поэтому тест сверяет её со стандартной
// библиотекой (разница только в байте паддинга), а паддинг Ethereum проверяется
// на известных значениях. Когда x/crypto/sha3 можно будет подключить, этот
// файл заменяется вызовом sha3.NewLegacyKeccak256 (запрос в PR с обоснованием).
const keccakRate = 136 // байт в блоке для 256-битного вывода

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

// keccakF1600 это перестановка Keccak-f[1600] над состоянием из 25 слов.
func keccakF1600(a *[25]uint64) {
	var bc [5]uint64
	for round := range 24 {
		for i := range 5 { // theta
			bc[i] = a[i] ^ a[i+5] ^ a[i+10] ^ a[i+15] ^ a[i+20]
		}
		for i := range 5 {
			t := bc[(i+4)%5] ^ bits.RotateLeft64(bc[(i+1)%5], 1)
			for j := 0; j < 25; j += 5 {
				a[j+i] ^= t
			}
		}
		t := a[1] // rho и pi
		for i := range 24 {
			j := keccakPi[i]
			bc[0] = a[j]
			a[j] = bits.RotateLeft64(t, keccakRot[i])
			t = bc[0]
		}
		for j := 0; j < 25; j += 5 { // chi
			for i := range 5 {
				bc[i] = a[j+i]
			}
			for i := range 5 {
				a[j+i] ^= ^bc[(i+1)%5] & bc[(i+2)%5]
			}
		}
		a[0] ^= keccakRC[round] // iota
	}
}

func keccakAbsorb(st *[25]uint64, block []byte) {
	for i := range keccakRate / 8 {
		st[i] ^= binary.LittleEndian.Uint64(block[i*8:])
	}
}

// keccakSum это губка с ёмкостью 512 бит и байтом паддинга pad.
func keccakSum(data []byte, pad byte) [32]byte {
	var st [25]uint64
	for len(data) >= keccakRate {
		keccakAbsorb(&st, data[:keccakRate])
		keccakF1600(&st)
		data = data[keccakRate:]
	}
	var last [keccakRate]byte
	copy(last[:], data)
	last[len(data)] ^= pad
	last[keccakRate-1] ^= 0x80
	keccakAbsorb(&st, last[:])
	keccakF1600(&st)

	var out [32]byte
	for i := range 4 {
		binary.LittleEndian.PutUint64(out[i*8:], st[i])
	}
	return out
}

// keccak256 возвращает Keccak-256 (вариант Ethereum) от конкатенации частей.
func keccak256(parts ...[]byte) [32]byte {
	if len(parts) == 1 {
		return keccakSum(parts[0], 0x01)
	}
	var n int
	for _, p := range parts {
		n += len(p)
	}
	buf := make([]byte, 0, n)
	for _, p := range parts {
		buf = append(buf, p...)
	}
	return keccakSum(buf, 0x01)
}
