// Package secp256k1 реализует кривую secp256k1 и подпись ECDSA на math/big.
//
// Зачем свой пакет. На момент написания в vendor нет ни dcrd/secp256k1, ни
// btcec, а загрузка модулей закрыта (см. описание PR). Пакет закрывает ровно
// то, что нужно keystore и деривации BIP32: умножение на скаляр, сложение
// точек, разбор и сериализацию ключей, подпись RFC 6979 с канонической s и
// проверку подписи. Весь код проверяется на известных векторах и на
// независимой реализации (см. тесты). Замена на проверенную библиотеку
// должна быть правкой этого пакета: наружу он отдаёт только то, что ниже.
//
// Ограничение: math/big не работает за постоянное время. Для кошелька, где
// подпись выполняется локально по запросу пользователя, а локальный
// злоумышленник вне модели угроз (ADR), это принято, но это основание
// заменить пакет библиотекой с постоянным временем, как только её можно
// будет подключить. Промежуточные значения math/big затереть нельзя; явно
// затираются только скаляры, которые пакет получает и создаёт сам.
package secp256k1

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"math/big"
)

// Длины сериализованных значений.
const (
	ScalarLen             = 32
	PubKeyCompressedLen   = 33
	PubKeyUncompressedLen = 65
)

// Ошибки пакета.
var (
	ErrInvalidScalar = errors.New("secp256k1: scalar is zero or not below the group order")
	ErrInvalidPoint  = errors.New("secp256k1: invalid public key")
	ErrInfinity      = errors.New("secp256k1: result is the point at infinity")
	ErrInvalidDigest = errors.New("secp256k1: digest must be 32 bytes")
	ErrRecoveryID    = errors.New("secp256k1: signature needs a recovery id above 1")
)

var (
	curveP   = mustHex("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F")
	curveN   = mustHex("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141")
	curveGx  = mustHex("79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798")
	curveGy  = mustHex("483ADA7726A3C4655DA4FBFC0E1108A8FD17B448A68554199C47D08FFB10D4B8")
	halfN    = new(big.Int).Rsh(curveN, 1)
	sqrtExp  = new(big.Int).Rsh(new(big.Int).Add(curveP, big.NewInt(1)), 2) // (p+1)/4, p = 3 mod 4
	bigSeven = big.NewInt(7)
)

func mustHex(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 16)
	if !ok {
		panic("secp256k1: bad constant")
	}
	return v
}

// Point это точка кривой в аффинных координатах. Нулевое значение не точка.
// Значения Point не изменяются после создания.
type Point struct {
	x, y *big.Int
}

// X возвращает копию координаты X.
func (p Point) X() *big.Int { return new(big.Int).Set(p.x) }

// Y возвращает копию координаты Y.
func (p Point) Y() *big.Int { return new(big.Int).Set(p.y) }

// IsZero сообщает, что это нулевое значение, а не точка.
func (p Point) IsZero() bool { return p.x == nil }

// Equal сравнивает точки.
func (p Point) Equal(q Point) bool {
	if p.IsZero() || q.IsZero() {
		return p.IsZero() && q.IsZero()
	}
	return p.x.Cmp(q.x) == 0 && p.y.Cmp(q.y) == 0
}

// Compressed возвращает сжатый ключ в 33 байта (02 или 03, затем X).
func (p Point) Compressed() [PubKeyCompressedLen]byte {
	var out [PubKeyCompressedLen]byte
	out[0] = 0x02 | byte(p.y.Bit(0))
	p.x.FillBytes(out[1:])
	return out
}

// Uncompressed возвращает несжатый ключ в 65 байт (04, X, Y).
func (p Point) Uncompressed() [PubKeyUncompressedLen]byte {
	var out [PubKeyUncompressedLen]byte
	out[0] = 0x04
	p.x.FillBytes(out[1:33])
	p.y.FillBytes(out[33:])
	return out
}

// ParsePoint разбирает сжатый (33 байта) или несжатый (65 байт) ключ и
// проверяет, что точка лежит на кривой. Гибридный формат (06, 07) отвергается.
func ParsePoint(b []byte) (Point, error) {
	switch {
	case len(b) == PubKeyCompressedLen && (b[0] == 0x02 || b[0] == 0x03):
		x := new(big.Int).SetBytes(b[1:])
		if x.Cmp(curveP) >= 0 {
			return Point{}, ErrInvalidPoint
		}
		y := ySquared(x)
		y.Exp(y, sqrtExp, curveP)
		if modMul(y, y).Cmp(ySquared(x)) != 0 {
			return Point{}, ErrInvalidPoint
		}
		if byte(y.Bit(0)) != b[0]&1 {
			y.Sub(curveP, y)
		}
		return Point{x: x, y: y}, nil
	case len(b) == PubKeyUncompressedLen && b[0] == 0x04:
		x := new(big.Int).SetBytes(b[1:33])
		y := new(big.Int).SetBytes(b[33:])
		if x.Cmp(curveP) >= 0 || y.Cmp(curveP) >= 0 || modMul(y, y).Cmp(ySquared(x)) != 0 {
			return Point{}, ErrInvalidPoint
		}
		return Point{x: x, y: y}, nil
	}
	return Point{}, ErrInvalidPoint
}

// ParseScalar разбирает 32 байта как ненулевой скаляр меньше порядка группы.
// Вызывающий затирает результат функцией WipeInt, когда скаляр секретный.
func ParseScalar(b []byte) (*big.Int, error) {
	if len(b) != ScalarLen {
		return nil, ErrInvalidScalar
	}
	k := new(big.Int).SetBytes(b)
	if k.Sign() == 0 || k.Cmp(curveN) >= 0 {
		WipeInt(k)
		return nil, ErrInvalidScalar
	}
	return k, nil
}

// WipeInt затирает слова числа и обнуляет его. Копии, которые math/big мог
// сделать при вычислениях, затереть нельзя.
func WipeInt(x *big.Int) {
	if x == nil {
		return
	}
	clear(x.Bits())
	x.SetInt64(0)
}

// ScalarBaseMult возвращает k*G для скаляра k из 32 байт.
func ScalarBaseMult(k []byte) (Point, error) {
	s, err := ParseScalar(k)
	if err != nil {
		return Point{}, err
	}
	defer WipeInt(s)
	return affine(mulJac(jacobian{x: curveGx, y: curveGy, z: big.NewInt(1)}, s))
}

// ScalarMult возвращает k*p для скаляра k из 32 байт.
func ScalarMult(p Point, k []byte) (Point, error) {
	s, err := ParseScalar(k)
	if err != nil {
		return Point{}, err
	}
	defer WipeInt(s)
	return affine(mulJac(toJac(p), s))
}

// Add возвращает p+q. Если сумма это бесконечно удалённая точка, возвращает
// ErrInfinity.
func Add(p, q Point) (Point, error) {
	return affine(addJac(toJac(p), toJac(q)))
}

// AddScalarBase возвращает p + k*G, где k это 32 байта, меньшие порядка
// группы (ноль допустим). Это шаг публичной деривации BIP32.
func AddScalarBase(p Point, k []byte) (Point, error) {
	if len(k) != ScalarLen {
		return Point{}, ErrInvalidScalar
	}
	s := new(big.Int).SetBytes(k)
	defer WipeInt(s)
	if s.Cmp(curveN) >= 0 {
		return Point{}, ErrInvalidScalar
	}
	if s.Sign() == 0 {
		return p, nil
	}
	g := jacobian{x: curveGx, y: curveGy, z: big.NewInt(1)}
	return affine(addJac(toJac(p), mulJac(g, s)))
}

// AddScalarsModOrder возвращает (a+b) mod n в 32 байтах. Ноль в результате
// это ошибка: для BIP32 такой ключ недопустим.
func AddScalarsModOrder(a, b []byte) ([ScalarLen]byte, error) {
	var out [ScalarLen]byte
	if len(a) != ScalarLen || len(b) != ScalarLen {
		return out, ErrInvalidScalar
	}
	x := new(big.Int).SetBytes(a)
	y := new(big.Int).SetBytes(b)
	defer WipeInt(x)
	defer WipeInt(y)
	if x.Cmp(curveN) >= 0 || y.Cmp(curveN) >= 0 {
		return out, ErrInvalidScalar
	}
	x.Add(x, y)
	x.Mod(x, curveN)
	if x.Sign() == 0 {
		return out, ErrInvalidScalar
	}
	x.FillBytes(out[:])
	return out, nil
}

// ScalarBelowOrder сообщает, что 32 байта задают число меньше порядка группы.
func ScalarBelowOrder(b []byte) bool {
	if len(b) != ScalarLen {
		return false
	}
	x := new(big.Int).SetBytes(b)
	defer WipeInt(x)
	return x.Cmp(curveN) < 0
}

// Signature это подпись ECDSA с каноническим s (s не больше n/2).
type Signature struct {
	R, S [ScalarLen]byte
	// RecID это чётность Y точки R после приведения s к канонической форме
	// (0 или 1). Случай X(R) >= n (вероятность около 2^-128) возвращается
	// ошибкой, поэтому значений 2 и 3 нет.
	RecID byte
}

// Sign подписывает 32-байтный дайджест детерминированно (RFC 6979, SHA-256)
// и возвращает подпись с канонической s. priv это 32 байта секретного ключа.
func Sign(priv, digest []byte) (Signature, error) {
	var sig Signature
	if len(digest) != 32 {
		return sig, ErrInvalidDigest
	}
	d, err := ParseScalar(priv)
	if err != nil {
		return sig, err
	}
	defer WipeInt(d)
	z := new(big.Int).SetBytes(digest)
	g := jacobian{x: curveGx, y: curveGy, z: big.NewInt(1)}

	gen := newNonceGen(priv, digest)
	defer gen.wipe()
	for {
		k := gen.next()
		kk := new(big.Int).SetBytes(k[:])
		clear(k[:])
		if kk.Sign() == 0 || kk.Cmp(curveN) >= 0 {
			WipeInt(kk)
			continue
		}
		rp, err := affine(mulJac(g, kk))
		if err != nil {
			WipeInt(kk)
			continue
		}
		r := new(big.Int).Mod(rp.x, curveN)
		if r.Sign() == 0 {
			WipeInt(kk)
			continue
		}
		// s = k^-1 (z + r*d) mod n
		s := new(big.Int).Mul(r, d)
		s.Add(s, z)
		kInv := new(big.Int).ModInverse(kk, curveN)
		s.Mul(s, kInv)
		s.Mod(s, curveN)
		WipeInt(kk)
		WipeInt(kInv)
		if s.Sign() == 0 {
			continue
		}
		if rp.x.Cmp(curveN) >= 0 {
			// X(R) >= n: вероятность 2^-128, recid был бы 2 или 3.
			return Signature{}, ErrRecoveryID
		}
		recID := byte(rp.y.Bit(0))
		if s.Cmp(halfN) > 0 {
			s.Sub(curveN, s)
			recID ^= 1
		}
		r.FillBytes(sig.R[:])
		s.FillBytes(sig.S[:])
		sig.RecID = recID
		return sig, nil
	}
}

// Verify проверяет подпись ECDSA. Принимает любое s, и каноническое, и нет:
// требование канонического s проверяет вызывающий через IsLowS.
func Verify(pub Point, digest, r, s []byte) bool {
	if pub.IsZero() || len(digest) != 32 || len(r) != ScalarLen || len(s) != ScalarLen {
		return false
	}
	rr := new(big.Int).SetBytes(r)
	ss := new(big.Int).SetBytes(s)
	if rr.Sign() == 0 || ss.Sign() == 0 || rr.Cmp(curveN) >= 0 || ss.Cmp(curveN) >= 0 {
		return false
	}
	z := new(big.Int).SetBytes(digest)
	w := new(big.Int).ModInverse(ss, curveN)
	u1 := new(big.Int).Mul(z, w)
	u1.Mod(u1, curveN)
	u2 := new(big.Int).Mul(rr, w)
	u2.Mod(u2, curveN)
	g := jacobian{x: curveGx, y: curveGy, z: big.NewInt(1)}
	sum := addJac(mulJac(g, u1), mulJac(toJac(pub), u2))
	pt, err := affine(sum)
	if err != nil {
		return false
	}
	return new(big.Int).Mod(pt.x, curveN).Cmp(rr) == 0
}

// IsLowS сообщает, что s не больше n/2.
func IsLowS(s []byte) bool {
	if len(s) != ScalarLen {
		return false
	}
	return new(big.Int).SetBytes(s).Cmp(halfN) <= 0
}

// nonceGen это HMAC-DRBG из RFC 6979, раздел 3.2, для q = n и SHA-256.
type nonceGen struct {
	k, v    [32]byte
	started bool
}

func newNonceGen(priv, digest []byte) *nonceGen {
	// bits2octets(h1): z mod n в 32 байтах.
	h := new(big.Int).SetBytes(digest)
	h.Mod(h, curveN)
	var h1 [32]byte
	h.FillBytes(h1[:])

	g := &nonceGen{}
	for i := range g.v {
		g.v[i] = 0x01
	}
	g.k = hmacSHA256(g.k[:], g.v[:], []byte{0x00}, priv, h1[:])
	g.v = hmacSHA256(g.k[:], g.v[:])
	g.k = hmacSHA256(g.k[:], g.v[:], []byte{0x01}, priv, h1[:])
	g.v = hmacSHA256(g.k[:], g.v[:])
	return g
}

// next возвращает следующего кандидата в k. Повторный вызов продолжает
// поиск по правилу RFC 6979 (шаг h.3).
func (g *nonceGen) next() [32]byte {
	if g.started {
		g.k = hmacSHA256(g.k[:], g.v[:], []byte{0x00})
		g.v = hmacSHA256(g.k[:], g.v[:])
	}
	g.started = true
	g.v = hmacSHA256(g.k[:], g.v[:])
	return g.v
}

func (g *nonceGen) wipe() {
	clear(g.k[:])
	clear(g.v[:])
}

func hmacSHA256(key []byte, parts ...[]byte) [32]byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write(p)
	}
	var out [32]byte
	m.Sum(out[:0])
	return out
}

// --- арифметика в якобиановых координатах (a = 0) ---

type jacobian struct {
	x, y, z *big.Int
}

func infinity() jacobian {
	return jacobian{x: big.NewInt(1), y: big.NewInt(1), z: big.NewInt(0)}
}

func (a jacobian) isInfinity() bool { return a.z.Sign() == 0 }

func toJac(p Point) jacobian {
	return jacobian{x: p.x, y: p.y, z: big.NewInt(1)}
}

func affine(a jacobian) (Point, error) {
	if a.isInfinity() {
		return Point{}, ErrInfinity
	}
	zi := new(big.Int).ModInverse(a.z, curveP)
	zi2 := modMul(zi, zi)
	return Point{x: modMul(a.x, zi2), y: modMul(a.y, modMul(zi2, zi))}, nil
}

func modMul(a, b *big.Int) *big.Int {
	r := new(big.Int).Mul(a, b)
	return r.Mod(r, curveP)
}

func modSub(a, b *big.Int) *big.Int {
	r := new(big.Int).Sub(a, b)
	return r.Mod(r, curveP)
}

func modMulSmall(a *big.Int, c int64) *big.Int {
	r := new(big.Int).Mul(a, big.NewInt(c))
	return r.Mod(r, curveP)
}

func ySquared(x *big.Int) *big.Int {
	r := modMul(modMul(x, x), x)
	r.Add(r, bigSeven)
	return r.Mod(r, curveP)
}

func doubleJac(a jacobian) jacobian {
	if a.isInfinity() || a.y.Sign() == 0 {
		return infinity()
	}
	ysq := modMul(a.y, a.y)
	s := modMulSmall(modMul(a.x, ysq), 4)
	m := modMulSmall(modMul(a.x, a.x), 3)
	x3 := modSub(modMul(m, m), modMulSmall(s, 2))
	y3 := modSub(modMul(m, modSub(s, x3)), modMulSmall(modMul(ysq, ysq), 8))
	z3 := modMul(modMulSmall(a.y, 2), a.z)
	return jacobian{x: x3, y: y3, z: z3}
}

func addJac(a, b jacobian) jacobian {
	if a.isInfinity() {
		return b
	}
	if b.isInfinity() {
		return a
	}
	z1z1 := modMul(a.z, a.z)
	z2z2 := modMul(b.z, b.z)
	u1 := modMul(a.x, z2z2)
	u2 := modMul(b.x, z1z1)
	s1 := modMul(a.y, modMul(b.z, z2z2))
	s2 := modMul(b.y, modMul(a.z, z1z1))
	if u1.Cmp(u2) == 0 {
		if s1.Cmp(s2) == 0 {
			return doubleJac(a)
		}
		return infinity()
	}
	h := modSub(u2, u1)
	r := modSub(s2, s1)
	h2 := modMul(h, h)
	h3 := modMul(h, h2)
	v := modMul(u1, h2)
	x3 := modSub(modSub(modMul(r, r), h3), modMulSmall(v, 2))
	y3 := modSub(modMul(r, modSub(v, x3)), modMul(s1, h3))
	z3 := modMul(h, modMul(a.z, b.z))
	return jacobian{x: x3, y: y3, z: z3}
}

// mulJac умножает точку на скаляр методом двоичного разложения слева направо.
func mulJac(p jacobian, k *big.Int) jacobian {
	r := infinity()
	for i := k.BitLen() - 1; i >= 0; i-- {
		r = doubleJac(r)
		if k.Bit(i) == 1 {
			r = addJac(r, p)
		}
	}
	return r
}
