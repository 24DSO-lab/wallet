package eth

import (
	"errors"
	"math/big"

	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
)

// Восстановление открытого ключа по подписи ECDSA (как ecrecover в Ethereum).
// Пакет secp256k1 даёт арифметику точек, но не восстановление, а править
// чужой пакет (зона Dev 2) не нужно: шаг укладывается в три вызова.

var (
	// ErrSignature означает, что подпись не годится: параметры вне диапазона,
	// высокая s, ключ не восстанавливается или не совпадает с заявленным.
	ErrSignature = errors.New("eth: invalid signature")

	curveOrder, _ = new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)
	curveHalfOrd  = new(big.Int).Rsh(curveOrder, 1)
)

// recoverPublicKey возвращает ключ, подписавший digest подписью (r, s) с
// чётностью y точки R равной v (0 или 1). Q = r^-1 * (s*R - z*G).
func recoverPublicKey(digest [32]byte, r, s *big.Int, v byte) (secp256k1.Point, error) {
	if v > 1 || r.Sign() <= 0 || r.Cmp(curveOrder) >= 0 || s.Sign() <= 0 || s.Cmp(curveOrder) >= 0 {
		return secp256k1.Point{}, ErrSignature
	}
	// Точка R: x = r (случай x = r + n не возникает, Keystore его отвергает),
	// чётность y задаёт v. Сжатый префикс 0x02 это чётная y, 0x03 нечётная.
	var comp [secp256k1.PubKeyCompressedLen]byte
	comp[0] = 0x02 + v
	r.FillBytes(comp[1:])
	rp, err := secp256k1.ParsePoint(comp[:])
	if err != nil {
		return secp256k1.Point{}, ErrSignature
	}
	rInv := new(big.Int).ModInverse(r, curveOrder)
	u1 := new(big.Int).Mul(s, rInv)
	u1.Mod(u1, curveOrder)
	z := new(big.Int).SetBytes(digest[:])
	u2 := new(big.Int).Mul(z, rInv)
	u2.Neg(u2).Mod(u2, curveOrder)

	var k1, k2 [secp256k1.ScalarLen]byte
	u1.FillBytes(k1[:])
	u2.FillBytes(k2[:])
	a, err := secp256k1.ScalarMult(rp, k1[:])
	if err != nil {
		return secp256k1.Point{}, ErrSignature
	}
	if u2.Sign() == 0 { // z кратно n: практически недостижимо, но корректно
		return a, nil
	}
	b, err := secp256k1.ScalarBaseMult(k2[:])
	if err != nil {
		return secp256k1.Point{}, ErrSignature
	}
	q, err := secp256k1.Add(a, b)
	if err != nil {
		return secp256k1.Point{}, ErrSignature
	}
	return q, nil
}

// checkSignature проверяет подпись digest и возвращает адрес подписавшего. Он
// восстанавливается из самой подписи, а заявленный ключ (сжатый, 33 байта)
// обязан с ним совпасть. Это защита и от неверного v: транзакция с неверной
// чётностью восстановилась бы в чужой адрес.
func checkSignature(digest [32]byte, sig [65]byte, claimedPub []byte) (Address, error) {
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:64])
	if s.Cmp(curveHalfOrd) > 0 { // EIP-2: узлы отвергают высокую s
		return Address{}, ErrSignature
	}
	q, err := recoverPublicKey(digest, r, s, sig[64])
	if err != nil {
		return Address{}, err
	}
	if claimed, err := secp256k1.ParsePoint(claimedPub); err != nil || !claimed.Equal(q) {
		return Address{}, ErrSignature
	}
	unc := q.Uncompressed()
	return addressFromPublicKey(unc[:])
}
