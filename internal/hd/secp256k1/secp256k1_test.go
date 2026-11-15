package secp256k1

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"
)

// Эталоны сгенерированы независимой реализацией (Python, библиотека
// cryptography поверх OpenSSL, детерминированная подпись RFC 6979 с приведением
// s к канонической форме). Последняя запись signVectors это известный вектор
// "Satoshi Nakamoto" для секретного ключа 1.

var signVectors = []struct{ priv, pub, digest, r, s string }{
	{"0000000000000000000000000000000000000000000000000000000000000001", "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798", "568b468388b8c8b8c763407119d4da6d99718c3d8c24f4f50f6e723c122b2b75", "20cf53dfb9a9a047f8a851cb70f735e0b3c9f0150f7ee00c5affb839cd6e9cba", "359c032e36443c04b252eeba7b4fda437a04bfc470914b6307c678884da89585"},
	{"0000000000000000000000000000000000000000000000000000000000000002", "02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5", "6aa30a0ecc984742c8cb2ddc3207ec0bcab95a1e6ffb0d07a0eeb1637e160731", "8f1d3b940538c9cd356c5d96e673700df2183ead152fd13fddf8c22b65db061d", "449d8b58252aab9b4dcfb3a205c03d8c7769dcc653df95e8461690e646d369fa"},
	{"0000000000000000000000000000000000000000000000000000000000000003", "02f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9", "0cef9a7402ec2c02b520ca79fa087432590420c3cae84354e9d2a3eb7351fed9", "a89d0d39b6c195bbf40eac7b53bdabb0f86decdfe418aaf1218087f35525b764", "3a23f5d48d681539bc859765772c257e0873434331f9e895d2273f330075a4f8"},
	{"fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364140", "0379be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798", "4f8ab749543b27b66e41e723bf0ee4bad10d32f7b88ba4cb49eafd599ce22357", "ba2e5375b12121db15692d816797b3f944aa06e06ee5191b7c93a2f5bed47ac4", "3e80d8ec98f5584dcd9cc0a2442c862e679e12fa2102b24ecc87cab917f8301d"},
	{"fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd036413f", "03c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5", "55dd23fea3133817ddfba6e3f836e3ea67ba6561e051d5628a9e1f808b73ccc1", "33967126be977aaaddcbb68c388b647cc6ba55b3557a501ef98c7a2f9cc482d7", "7f8fcd01f8a9e5fa43b91b2b85be2f46ffede1ed7981d28452520871560e0d20"},
	{"1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", "02bb50e2d89a4ed70663d080659fe0ad4b9bc3e06c17a227433966cb59ceee020d", "b44bda795933304f554babda099bca88d41cf00f3400f7994c8d1d2dc1abd348", "1d2e7a99cd81ceb60fadf6cf11b4217af411a1be332059ce0e40f968ad1f63f0", "6c1227441ce297e1a5a75937b689555213d05d388fa00aae412dd9b11379ae62"},
	{"6ab9f1eb8f7d3388f4f9d586f66e99fd54080df2c446f0e58668b09c08a16dd0", "03669b8afcec803a0d323e9a17f3ea8e68e8abe5a278020a929adbec52421adbd0", "e420ccb70d15732301ab8bba67fde379926587be7c7c32b01934c12051df4a40", "88001f6e3c41d08babdb6569faa109563917ac9668eab6ff725efcb73dacddac", "367686b834829c9fa6a653254c0729d674ee9565aa399f905b907f586a2ac738"},
	{"015f7e6bc5aeaf483724089e9252cc13b50951a6b69412522765cff4d780306e", "0260b2003c386519fc9eadf2b5cf124dd8eea4c4e68d5e154050a9346ea98ce600", "5cfb3084e671f2ac2a99bc3ddc0c7a462b28607da78af5c34c8a5099616f6b66", "2d207a52b59bf69d29268955fed1198a06083212f7fe7992d94a4bb8ecea6d4a", "3478434caf2b4452bf3400f262d6efaef07c04bcf39d37c856bba80e29b4cdf4"},
	{"2f5052c9fd15b19a18c584d01363568198613f0c34e84409ef7938709a159ec2", "0362a74e399c39ed5593852a30147f2959b56bb827dfa3e60e464b02ccf87dc5e8", "6fe63dcd7d886c3eadb5ec2a0eef291236adf7c573b83089cac0da4a0ec8e704", "7a1ab35a1b22eeeee1721fe6718c365673a62b2990aa64cbf40300bdc6f1e9f9", "7252f97e10a90d0cdecfb85fe484b7af6d1a4986efdb08cee6267c94c19b25b0"},
	{"0000000000000000000000000000000000000000000000000000000000000001", "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798", "a0dc65ffca799873cbea0ac274015b9526505daaaed385155425f7337704883e", "934b1ea10a4b3c1757e2b0c017d0b6143ce3c9a7e6a4a49860d7a6ab210ee3d8", "2442ce9d2b916064108014783e923ec36b49743e2ffa1c4496f01a512aafd9e5"}, // Satoshi Nakamoto
}

var mulVectors = []struct{ k1, k2, want string }{
	{"1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", "6ab9f1eb8f7d3388f4f9d586f66e99fd54080df2c446f0e58668b09c08a16dd0", "02b16a7a89bebcedb5b9dfbbe292f95d2869795d9e5f23b5fa2e80682f0fcbbbb1"},
	{"015f7e6bc5aeaf483724089e9252cc13b50951a6b69412522765cff4d780306e", "2f5052c9fd15b19a18c584d01363568198613f0c34e84409ef7938709a159ec2", "02abbb57fd2e272fa1fb6b7def082ec7433acc61a3a80e2a71b56016088432c367"},
	{"fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364140", "6ab9f1eb8f7d3388f4f9d586f66e99fd54080df2c446f0e58668b09c08a16dd0", "02669b8afcec803a0d323e9a17f3ea8e68e8abe5a278020a929adbec52421adbd0"},
	{"0000000000000000000000000000000000000000000000000000000000000002", "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", "03ec5dab39b69e57cfb9541b8e5075e8b217a9b38e0bb0c180ca071825a35abb4a"},
}

var addVectors = []struct{ a, b, want string }{
	{"02bb50e2d89a4ed70663d080659fe0ad4b9bc3e06c17a227433966cb59ceee020d", "03669b8afcec803a0d323e9a17f3ea8e68e8abe5a278020a929adbec52421adbd0", "0207bea33e2159c8de5d98f70af50cb7891dbd24ffca10ab6973fcd65aea33461e"},
	{"0260b2003c386519fc9eadf2b5cf124dd8eea4c4e68d5e154050a9346ea98ce600", "0362a74e399c39ed5593852a30147f2959b56bb827dfa3e60e464b02ccf87dc5e8", "03dfc5119b35c2a08943f028e0b05831304dd376377afca93266436866c77d8278"},
	{"0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798", "02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5", "02f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9"},
	{"02f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9", "02f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9", "03fff97bd5755eeea420453a14355235d382f6472f8568a18b2f057a1460297556"},
}

func mustHexBytes(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func TestScalarBaseMultAndSerialization(t *testing.T) {
	for _, v := range signVectors {
		p, err := ScalarBaseMult(mustHexBytes(t, v.priv))
		if err != nil {
			t.Fatalf("ScalarBaseMult(%s): %v", v.priv, err)
		}
		c := p.Compressed()
		if got := hex.EncodeToString(c[:]); got != v.pub {
			t.Errorf("pub(%s) = %s, want %s", v.priv, got, v.pub)
		}
		u := p.Uncompressed()
		q, err := ParsePoint(u[:])
		if err != nil || !q.Equal(p) {
			t.Errorf("uncompressed round trip failed for %s: %v", v.priv, err)
		}
		q, err = ParsePoint(c[:])
		if err != nil || !q.Equal(p) {
			t.Errorf("compressed round trip failed for %s: %v", v.priv, err)
		}
	}
}

func TestScalarMultAndAdd(t *testing.T) {
	for _, v := range mulVectors {
		var base Point
		for _, sv := range signVectors {
			// База это публичный ключ второго множителя.
			if sv.priv == v.k2 {
				base, _ = ParsePoint(mustHexBytes(t, sv.pub))
			}
		}
		if base.IsZero() {
			p, err := ScalarBaseMult(mustHexBytes(t, v.k2))
			if err != nil {
				t.Fatal(err)
			}
			base = p
		}
		got, err := ScalarMult(base, mustHexBytes(t, v.k1))
		if err != nil {
			t.Fatal(err)
		}
		c := got.Compressed()
		if hex.EncodeToString(c[:]) != v.want {
			t.Errorf("%s * pub(%s) = %x, want %s", v.k1, v.k2, c, v.want)
		}
	}
	for _, v := range addVectors {
		a, err := ParsePoint(mustHexBytes(t, v.a))
		if err != nil {
			t.Fatal(err)
		}
		b, err := ParsePoint(mustHexBytes(t, v.b))
		if err != nil {
			t.Fatal(err)
		}
		sum, err := Add(a, b)
		if err != nil {
			t.Fatal(err)
		}
		c := sum.Compressed()
		if hex.EncodeToString(c[:]) != v.want {
			t.Errorf("add(%s, %s) = %x, want %s", v.a, v.b, c, v.want)
		}
	}
}

func TestAddInverseIsInfinity(t *testing.T) {
	g, _ := ScalarBaseMult(mustHexBytes(t, "0000000000000000000000000000000000000000000000000000000000000001"))
	neg, _ := ScalarBaseMult(mustHexBytes(t, "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364140"))
	if _, err := Add(g, neg); err != ErrInfinity {
		t.Fatalf("G + (n-1)G: got %v, want ErrInfinity", err)
	}
}

func TestAddScalarBaseMatchesAdd(t *testing.T) {
	a, _ := ScalarBaseMult(mustHexBytes(t, signVectors[5].priv))
	k := mustHexBytes(t, signVectors[6].priv)
	kg, _ := ScalarBaseMult(k)
	want, _ := Add(a, kg)
	got, err := AddScalarBase(a, k)
	if err != nil || !got.Equal(want) {
		t.Fatalf("AddScalarBase mismatch: %v", err)
	}
	zero := make([]byte, 32)
	got, err = AddScalarBase(a, zero)
	if err != nil || !got.Equal(a) {
		t.Fatalf("AddScalarBase(p, 0) must return p: %v", err)
	}
	over := mustHexBytes(t, "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141")
	if _, err := AddScalarBase(a, over); err != ErrInvalidScalar {
		t.Fatalf("scalar equal to n: got %v", err)
	}
}

func TestAddScalarsModOrder(t *testing.T) {
	nMinus1 := mustHexBytes(t, "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364140")
	one := mustHexBytes(t, "0000000000000000000000000000000000000000000000000000000000000001")
	if _, err := AddScalarsModOrder(nMinus1, one); err != ErrInvalidScalar {
		t.Fatalf("(n-1)+1 = 0 mod n must be rejected, got %v", err)
	}
	two := mustHexBytes(t, "0000000000000000000000000000000000000000000000000000000000000002")
	got, err := AddScalarsModOrder(nMinus1, two)
	if err != nil || !bytes.Equal(got[:], one) {
		t.Fatalf("(n-1)+2 = %x, %v; want 1", got, err)
	}
}

func TestParseScalarRejects(t *testing.T) {
	for name, b := range map[string][]byte{
		"empty":    nil,
		"short":    make([]byte, 31),
		"long":     make([]byte, 33),
		"zero":     make([]byte, 32),
		"order":    mustHexBytes(t, "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141"),
		"all ones": bytes.Repeat([]byte{0xff}, 32),
	} {
		if _, err := ParseScalar(b); err != ErrInvalidScalar {
			t.Errorf("%s: got %v, want ErrInvalidScalar", name, err)
		}
	}
}

func TestParsePointRejects(t *testing.T) {
	g := mustHexBytes(t, "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
	badPrefix := append([]byte{0x04}, g[1:]...) // 33 байта с префиксом 04
	hybrid := mustHexBytes(t, "0679be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8")
	offCurve := mustHexBytes(t, "0479be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b9")
	xOverP := append([]byte{0x02}, bytes.Repeat([]byte{0xff}, 32)...)
	noRoot := append([]byte{0x02}, make([]byte, 32)...)
	noRoot[32] = 5 // x^3+7 не квадратичный вычет по модулю p: на кривой точки с таким X нет
	for name, b := range map[string][]byte{
		"empty":     nil,
		"prefix":    badPrefix,
		"hybrid":    hybrid,
		"off curve": offCurve,
		"x >= p":    xOverP,
		"truncated": g[:32],
		"no root":   noRoot,
	} {
		if _, err := ParsePoint(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// recoverPub восстанавливает публичный ключ из подписи: Q = r^-1 (s*R - z*G).
func recoverPub(t *testing.T, digest []byte, sig Signature) Point {
	t.Helper()
	n := new(big.Int).SetBytes(mustHexBytes(t, "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141"))
	r := new(big.Int).SetBytes(sig.R[:])
	s := new(big.Int).SetBytes(sig.S[:])
	z := new(big.Int).SetBytes(digest)
	rInv := new(big.Int).ModInverse(r, n)
	u1 := new(big.Int).Mul(z, rInv)
	u1.Neg(u1).Mod(u1, n)
	u2 := new(big.Int).Mul(s, rInv)
	u2.Mod(u2, n)
	rp, err := ParsePoint(append([]byte{0x02 | sig.RecID}, sig.R[:]...))
	if err != nil {
		t.Fatalf("R from recid: %v", err)
	}
	a, err := ScalarBaseMult(u1.FillBytes(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ScalarMult(rp, u2.FillBytes(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	q, err := Add(a, b)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestSignAgainstVectors(t *testing.T) {
	for _, v := range signVectors {
		priv, digest := mustHexBytes(t, v.priv), mustHexBytes(t, v.digest)
		sig, err := Sign(priv, digest)
		if err != nil {
			t.Fatalf("Sign(%s): %v", v.priv, err)
		}
		if got := hex.EncodeToString(sig.R[:]); got != v.r {
			t.Errorf("r(%s) = %s, want %s", v.priv, got, v.r)
		}
		if got := hex.EncodeToString(sig.S[:]); got != v.s {
			t.Errorf("s(%s) = %s, want %s", v.priv, got, v.s)
		}
		if !IsLowS(sig.S[:]) {
			t.Errorf("s is not canonical for %s", v.priv)
		}
		if sig.RecID > 1 {
			t.Errorf("RecID = %d, must be 0 or 1", sig.RecID)
		}
		pub, _ := ParsePoint(mustHexBytes(t, v.pub))
		if !Verify(pub, digest, sig.R[:], sig.S[:]) {
			t.Errorf("own signature does not verify for %s", v.priv)
		}
		if !recoverPub(t, digest, sig).Equal(pub) {
			t.Errorf("RecID %d does not recover the signer for %s", sig.RecID, v.priv)
		}
		bad := append([]byte(nil), digest...)
		bad[0] ^= 1
		if Verify(pub, bad, sig.R[:], sig.S[:]) {
			t.Errorf("signature verifies for a different digest (%s)", v.priv)
		}
	}
}

func TestSatoshiNakamotoVector(t *testing.T) {
	// Известный вектор для ключа 1, сообщение "Satoshi Nakamoto", SHA-256.
	last := signVectors[len(signVectors)-1]
	if last.r != "934b1ea10a4b3c1757e2b0c017d0b6143ce3c9a7e6a4a49860d7a6ab210ee3d8" ||
		last.s != "2442ce9d2b916064108014783e923ec36b49743e2ffa1c4496f01a512aafd9e5" {
		t.Fatalf("independent oracle disagrees with the published vector: r=%s s=%s", last.r, last.s)
	}
}

func TestSignRejectsBadInput(t *testing.T) {
	good := mustHexBytes(t, signVectors[0].priv)
	digest := make([]byte, 32)
	if _, err := Sign(good, digest[:31]); err != ErrInvalidDigest {
		t.Errorf("short digest: %v", err)
	}
	if _, err := Sign(make([]byte, 32), digest); err != ErrInvalidScalar {
		t.Errorf("zero key: %v", err)
	}
	if _, err := Sign(good[:31], digest); err != ErrInvalidScalar {
		t.Errorf("short key: %v", err)
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	v := signVectors[0]
	pub, _ := ParsePoint(mustHexBytes(t, v.pub))
	digest := mustHexBytes(t, v.digest)
	r, s := mustHexBytes(t, v.r), mustHexBytes(t, v.s)
	zero := make([]byte, 32)
	if Verify(Point{}, digest, r, s) || Verify(pub, digest[:31], r, s) ||
		Verify(pub, digest, zero, s) || Verify(pub, digest, r, zero) ||
		Verify(pub, digest, r[:31], s) {
		t.Error("Verify accepted malformed input")
	}
	// Некатоническое s (n - s) подпись формально валидна, но IsLowS отвергает.
	n := new(big.Int).SetBytes(mustHexBytes(t, "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141"))
	hi := new(big.Int).Sub(n, new(big.Int).SetBytes(s)).FillBytes(make([]byte, 32))
	if !Verify(pub, digest, r, hi) {
		t.Error("high-s variant must verify mathematically")
	}
	if IsLowS(hi) {
		t.Error("IsLowS accepted a high s")
	}
}

func FuzzParsePoint(f *testing.F) {
	f.Add(mustHexBytes(f, signVectors[0].pub))
	f.Add([]byte{0x04})
	f.Add(bytes.Repeat([]byte{0xff}, 65))
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := ParsePoint(b)
		if err != nil {
			return
		}
		c := p.Compressed()
		q, err := ParsePoint(c[:])
		if err != nil || !q.Equal(p) {
			t.Fatalf("accepted key %x does not survive compression: %v", b, err)
		}
	})
}
