// Package base58 кодирует и разбирает base58check: сериализацию xpub и tpub
// (BIP32) и адреса вида base58check. Данные, которые кодирует пакет,
// публичные; секреты сюда не передаются.
package base58

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"math/big"
)

const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

const checksumLen = 4

// Ошибки разбора.
var (
	ErrInvalidChar = errors.New("base58: character outside the alphabet")
	ErrChecksum    = errors.New("base58: checksum mismatch")
	ErrTooShort    = errors.New("base58: data shorter than the checksum")
)

var bigBase = big.NewInt(58)

func checksum(payload []byte) []byte {
	first := sha256.Sum256(payload)
	second := sha256.Sum256(first[:])
	return second[:checksumLen]
}

// CheckEncode кодирует payload с четырьмя байтами контрольной суммы
// (двойной SHA-256).
func CheckEncode(payload []byte) string {
	data := append(append([]byte(nil), payload...), checksum(payload)...)
	return encode(data)
}

// CheckDecode разбирает строку, проверяет контрольную сумму и возвращает
// payload без неё.
func CheckDecode(s string) ([]byte, error) {
	data, err := decode(s)
	if err != nil {
		return nil, err
	}
	if len(data) < checksumLen {
		return nil, ErrTooShort
	}
	payload, sum := data[:len(data)-checksumLen], data[len(data)-checksumLen:]
	if subtle.ConstantTimeCompare(sum, checksum(payload)) != 1 {
		return nil, ErrChecksum
	}
	return payload, nil
}

func encode(data []byte) string {
	zeros := 0
	for zeros < len(data) && data[zeros] == 0 {
		zeros++
	}
	n := new(big.Int).SetBytes(data)
	mod := new(big.Int)
	var rev []byte
	for n.Sign() > 0 {
		n.DivMod(n, bigBase, mod)
		rev = append(rev, alphabet[mod.Int64()])
	}
	out := make([]byte, 0, zeros+len(rev))
	for i := 0; i < zeros; i++ {
		out = append(out, alphabet[0])
	}
	for i := len(rev) - 1; i >= 0; i-- {
		out = append(out, rev[i])
	}
	return string(out)
}

func decode(s string) ([]byte, error) {
	n := new(big.Int)
	zeros := 0
	leading := true
	for i := 0; i < len(s); i++ {
		idx := indexOf(s[i])
		if idx < 0 {
			return nil, ErrInvalidChar
		}
		if leading && idx == 0 {
			zeros++
		} else {
			leading = false
		}
		n.Mul(n, bigBase)
		n.Add(n, big.NewInt(int64(idx)))
	}
	body := n.Bytes()
	out := make([]byte, zeros+len(body))
	copy(out[zeros:], body)
	return out, nil
}

func indexOf(c byte) int {
	for i := 0; i < len(alphabet); i++ {
		if alphabet[i] == c {
			return i
		}
	}
	return -1
}
