// Package ripemd160 считает RIPEMD-160. Нужен для hash160 (отпечаток родителя
// в xpub и адреса BTC); в стандартной библиотеке его нет, а
// golang.org/x/crypto/ripemd160 не vendored. Проверяется на официальных
// векторах и на hashlib (см. тесты).
package ripemd160

import (
	"encoding/binary"
	"math/bits"
)

// Size это длина результата в байтах.
const Size = 20

var (
	// Порядок слов сообщения: пять раундов по 16 для левой и правой линий.
	selLeft = [80]uint8{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		7, 4, 13, 1, 10, 6, 15, 3, 12, 0, 9, 5, 2, 14, 11, 8,
		3, 10, 14, 4, 9, 15, 8, 1, 2, 7, 0, 6, 13, 11, 5, 12,
		1, 9, 11, 10, 0, 8, 12, 4, 13, 3, 7, 15, 14, 5, 6, 2,
		4, 0, 5, 9, 7, 12, 2, 10, 14, 1, 3, 8, 11, 6, 15, 13,
	}
	selRight = [80]uint8{
		5, 14, 7, 0, 9, 2, 11, 4, 13, 6, 15, 8, 1, 10, 3, 12,
		6, 11, 3, 7, 0, 13, 5, 10, 14, 15, 8, 12, 4, 9, 1, 2,
		15, 5, 1, 3, 7, 14, 6, 9, 11, 8, 12, 2, 10, 0, 4, 13,
		8, 6, 4, 1, 3, 11, 15, 0, 5, 12, 2, 13, 9, 7, 10, 14,
		12, 15, 10, 4, 1, 5, 8, 7, 6, 2, 13, 14, 0, 3, 9, 11,
	}
	rotLeft = [80]uint8{
		11, 14, 15, 12, 5, 8, 7, 9, 11, 13, 14, 15, 6, 7, 9, 8,
		7, 6, 8, 13, 11, 9, 7, 15, 7, 12, 15, 9, 11, 7, 13, 12,
		11, 13, 6, 7, 14, 9, 13, 15, 14, 8, 13, 6, 5, 12, 7, 5,
		11, 12, 14, 15, 14, 15, 9, 8, 9, 14, 5, 6, 8, 6, 5, 12,
		9, 15, 5, 11, 6, 8, 13, 12, 5, 12, 13, 14, 11, 8, 5, 6,
	}
	rotRight = [80]uint8{
		8, 9, 9, 11, 13, 15, 15, 5, 7, 7, 8, 11, 14, 14, 12, 6,
		9, 13, 15, 7, 12, 8, 9, 11, 7, 7, 12, 7, 6, 15, 13, 11,
		9, 7, 15, 11, 8, 6, 6, 14, 12, 13, 5, 14, 13, 13, 7, 5,
		15, 5, 8, 11, 14, 14, 6, 14, 6, 9, 12, 9, 12, 5, 15, 8,
		8, 5, 12, 9, 12, 5, 14, 6, 8, 13, 6, 5, 15, 13, 11, 11,
	}
	constLeft  = [5]uint32{0x00000000, 0x5A827999, 0x6ED9EBA1, 0x8F1BBCDC, 0xA953FD4E}
	constRight = [5]uint32{0x50A28BE6, 0x5C4DD124, 0x6D703EF3, 0x7A6D76E9, 0x00000000}
)

func f(round int, x, y, z uint32) uint32 {
	switch round {
	case 0:
		return x ^ y ^ z
	case 1:
		return (x & y) | (^x & z)
	case 2:
		return (x | ^y) ^ z
	case 3:
		return (x & z) | (y &^ z)
	default:
		return x ^ (y | ^z)
	}
}

// Sum возвращает RIPEMD-160 от data.
func Sum(data []byte) [Size]byte {
	h := [5]uint32{0x67452301, 0xEFCDAB89, 0x98BADCFE, 0x10325476, 0xC3D2E1F0}

	// Дополнение: 0x80, нули, длина в битах (64 бита, little-endian).
	padLen := 64 - (len(data)+9)%64
	if padLen == 64 {
		padLen = 0
	}
	msg := make([]byte, 0, len(data)+9+padLen)
	msg = append(msg, data...)
	msg = append(msg, 0x80)
	msg = append(msg, make([]byte, padLen)...)
	msg = binary.LittleEndian.AppendUint64(msg, uint64(len(data))*8)

	for off := 0; off < len(msg); off += 64 {
		var x [16]uint32
		for i := range x {
			x[i] = binary.LittleEndian.Uint32(msg[off+4*i:])
		}
		compress(&h, &x)
	}

	var out [Size]byte
	for i, v := range h {
		binary.LittleEndian.PutUint32(out[4*i:], v)
	}
	return out
}

func compress(h *[5]uint32, x *[16]uint32) {
	al, bl, cl, dl, el := h[0], h[1], h[2], h[3], h[4]
	ar, br, cr, dr, er := h[0], h[1], h[2], h[3], h[4]

	for j := 0; j < 80; j++ {
		round := j / 16
		t := bits.RotateLeft32(al+f(round, bl, cl, dl)+x[selLeft[j]]+constLeft[round], int(rotLeft[j])) + el
		al, el, dl, cl, bl = el, dl, bits.RotateLeft32(cl, 10), bl, t

		t = bits.RotateLeft32(ar+f(4-round, br, cr, dr)+x[selRight[j]]+constRight[round], int(rotRight[j])) + er
		ar, er, dr, cr, br = er, dr, bits.RotateLeft32(cr, 10), br, t
	}

	t := h[1] + cl + dr
	h[1] = h[2] + dl + er
	h[2] = h[3] + el + ar
	h[3] = h[4] + al + br
	h[4] = h[0] + bl + cr
	h[0] = t
}
