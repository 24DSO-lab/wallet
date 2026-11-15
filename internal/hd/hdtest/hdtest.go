// Package hdtest содержит независимые от адаптеров функции адресов для
// тестов: Keccak-256, адреса ETH (EIP-55) и TRON, адрес P2WPKH (bech32).
//
// Это тестовая подпорка. Production-код её не импортирует: настоящие
// функции AddressFromPublicKey пишут Dev 5 (ETH) и Dev 6 (TRON) в своих
// адаптерах, а адреса BTC строит Dev 4. Подпорка нужна, чтобы гейт этапа 1
// и тесты keystore не зависели от чужих пакетов; когда адаптеры появятся,
// их стоит сверить с ней.
package hdtest

import (
	"encoding/binary"
	"errors"
	"math/bits"
	"strings"

	"crypto/sha256"

	"github.com/24DSO-lab/wallet/internal/hd/internal/base58"
	"github.com/24DSO-lab/wallet/internal/hd/internal/ripemd160"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
)

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
	for round := 0; round < 24; round++ {
		for i := 0; i < 5; i++ {
			bc[i] = st[i] ^ st[i+5] ^ st[i+10] ^ st[i+15] ^ st[i+20]
		}
		for i := 0; i < 5; i++ {
			t := bc[(i+4)%5] ^ bits.RotateLeft64(bc[(i+1)%5], 1)
			for j := 0; j < 25; j += 5 {
				st[j+i] ^= t
			}
		}
		t := st[1]
		for i := 0; i < 24; i++ {
			j := keccakPi[i]
			b := st[j]
			st[j] = bits.RotateLeft64(t, keccakRot[i])
			t = b
		}
		for j := 0; j < 25; j += 5 {
			for i := 0; i < 5; i++ {
				bc[i] = st[j+i]
			}
			for i := 0; i < 5; i++ {
				st[j+i] ^= ^bc[(i+1)%5] & bc[(i+2)%5]
			}
		}
		st[0] ^= keccakRC[round]
	}
}

// sponge это общий каркас; domain 0x01 даёт Keccak-256 (Ethereum), 0x06 SHA3-256.
func sponge(data []byte, domain byte) [32]byte {
	const rate = 136
	msg := append([]byte(nil), data...)
	msg = append(msg, domain)
	for len(msg)%rate != 0 {
		msg = append(msg, 0)
	}
	msg[len(msg)-1] |= 0x80
	var st [25]uint64
	for off := 0; off < len(msg); off += rate {
		for i := 0; i < rate/8; i++ {
			st[i] ^= binary.LittleEndian.Uint64(msg[off+8*i:])
		}
		keccakF(&st)
	}
	var out [32]byte
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint64(out[8*i:], st[i])
	}
	return out
}

// Keccak256 это Keccak-256 в варианте Ethereum (не SHA3-256).
func Keccak256(data []byte) [32]byte { return sponge(data, 0x01) }

// SHA3256 нужен только чтобы сверить перестановку со стандартной библиотекой.
func SHA3256(data []byte) [32]byte { return sponge(data, 0x06) }

// ETHAddress возвращает адрес EIP-55 для несжатого ключа в 65 байт.
func ETHAddress(pub []byte) (string, error) {
	if len(pub) != 65 || pub[0] != 0x04 {
		return "", errors.New("hdtest: need a 65-byte uncompressed key")
	}
	h := Keccak256(pub[1:])
	const hexd = "0123456789abcdef"
	lower := make([]byte, 40)
	for i, b := range h[12:] {
		lower[2*i], lower[2*i+1] = hexd[b>>4], hexd[b&15]
	}
	sum := Keccak256(lower)
	out := []byte("0x")
	for i, c := range lower {
		nib := sum[i/2] >> 4
		if i%2 == 1 {
			nib = sum[i/2] & 15
		}
		if c >= 'a' && nib >= 8 {
			c -= 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out), nil
}

// TronAddress возвращает адрес TRON (base58check, префикс 0x41) для несжатого ключа.
func TronAddress(pub []byte) (string, error) {
	if len(pub) != 65 || pub[0] != 0x04 {
		return "", errors.New("hdtest: need a 65-byte uncompressed key")
	}
	h := Keccak256(pub[1:])
	return base58.CheckEncode(append([]byte{0x41}, h[12:]...)), nil
}

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func bech32Polymod(v []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, x := range v {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(x)
		for i := 0; i < 5; i++ {
			if top>>i&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

// P2WPKHAddress строит адрес witness v0 (bc1q или tb1q) из hash160 ключа.
func P2WPKHAddress(hrp string, hash160 [20]byte) string {
	data := []byte{0}
	acc, nbits := 0, 0
	for _, b := range hash160 {
		acc = acc<<8 | int(b)
		nbits += 8
		for nbits >= 5 {
			nbits -= 5
			data = append(data, byte(acc>>nbits&31))
		}
	}
	if nbits > 0 {
		data = append(data, byte(acc<<(5-nbits)&31))
	}
	exp := make([]byte, 0, len(hrp)*2+1+len(data)+6)
	for i := 0; i < len(hrp); i++ {
		exp = append(exp, hrp[i]>>5)
	}
	exp = append(exp, 0)
	for i := 0; i < len(hrp); i++ {
		exp = append(exp, hrp[i]&31)
	}
	exp = append(append(exp, data...), 0, 0, 0, 0, 0, 0)
	pm := bech32Polymod(exp) ^ 1
	var sb strings.Builder
	sb.WriteString(hrp + "1")
	for _, d := range data {
		sb.WriteByte(bech32Charset[d])
	}
	for i := 0; i < 6; i++ {
		sb.WriteByte(bech32Charset[pm>>(5*(5-i))&31])
	}
	return sb.String()
}

// P2WPKHFromPublicKey строит адрес P2WPKH (hrp bc или tb) из несжатого ключа
// в 65 байт: так будет выглядеть функция адреса BTC для keystore.
func P2WPKHFromPublicKey(hrp string, pub []byte) (string, error) {
	p, err := secp256k1.ParsePoint(pub)
	if err != nil || len(pub) != 65 {
		return "", errors.New("hdtest: need a valid 65-byte uncompressed key")
	}
	c := p.Compressed()
	s := sha256.Sum256(c[:])
	return P2WPKHAddress(hrp, ripemd160.Sum(s[:])), nil
}
