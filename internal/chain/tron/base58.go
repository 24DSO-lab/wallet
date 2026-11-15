package tron

import (
	"crypto/sha256"
	"errors"
	"math/big"
)

const b58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var (
	errBase58Char     = errors.New("tron: invalid base58 character")
	errBase58Checksum = errors.New("tron: invalid base58check checksum")
	errBase58Short    = errors.New("tron: base58check payload too short")
)

// b58Index это таблица значений символов; 0xFF означает недопустимый символ.
var b58Index = func() [256]byte {
	var t [256]byte
	for i := range t {
		t[i] = 0xFF
	}
	for i := 0; i < len(b58Alphabet); i++ {
		t[b58Alphabet[i]] = byte(i)
	}
	return t
}()

func sha256d(b []byte) [32]byte {
	h := sha256.Sum256(b)
	return sha256.Sum256(h[:])
}

// base58Encode кодирует байты в base58 (алфавит Bitcoin).
func base58Encode(b []byte) string {
	zeros := 0
	for zeros < len(b) && b[zeros] == 0 {
		zeros++
	}
	n := new(big.Int).SetBytes(b)
	base := big.NewInt(58)
	mod := new(big.Int)
	var rev []byte
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		rev = append(rev, b58Alphabet[mod.Int64()])
	}
	out := make([]byte, 0, zeros+len(rev))
	for range zeros {
		out = append(out, b58Alphabet[0])
	}
	for i := len(rev) - 1; i >= 0; i-- {
		out = append(out, rev[i])
	}
	return string(out)
}

// base58Decode декодирует base58. Длина входа ограничена вызывающим.
func base58Decode(s string) ([]byte, error) {
	zeros := 0
	for zeros < len(s) && s[zeros] == b58Alphabet[0] {
		zeros++
	}
	n := new(big.Int)
	base := big.NewInt(58)
	for i := 0; i < len(s); i++ {
		v := b58Index[s[i]]
		if v == 0xFF {
			return nil, errBase58Char
		}
		n.Mul(n, base)
		n.Add(n, big.NewInt(int64(v)))
	}
	body := n.Bytes()
	out := make([]byte, zeros+len(body))
	copy(out[zeros:], body)
	return out, nil
}

// base58CheckEncode дописывает 4 байта двойного SHA-256 и кодирует в base58.
func base58CheckEncode(payload []byte) string {
	sum := sha256d(payload)
	buf := make([]byte, 0, len(payload)+4)
	buf = append(buf, payload...)
	buf = append(buf, sum[:4]...)
	return base58Encode(buf)
}

// base58CheckDecode декодирует base58 и проверяет контрольную сумму.
func base58CheckDecode(s string) ([]byte, error) {
	raw, err := base58Decode(s)
	if err != nil {
		return nil, err
	}
	if len(raw) < 5 {
		return nil, errBase58Short
	}
	payload, sum := raw[:len(raw)-4], raw[len(raw)-4:]
	want := sha256d(payload)
	if [4]byte(sum) != [4]byte(want[:4]) {
		return nil, errBase58Checksum
	}
	return payload, nil
}
