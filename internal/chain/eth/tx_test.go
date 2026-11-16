package eth

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
)

func decHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func bigDec(t testing.TB, s string) *big.Int {
	t.Helper()
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad decimal %q", s)
	}
	return n
}

func (v txVector) tx(t testing.TB) dynTx {
	return dynTx{
		ChainID: v.chainID, Nonce: v.nonce, Gas: v.gas,
		MaxPriorityFee: bigDec(t, v.prio), MaxFee: bigDec(t, v.maxFee), Value: bigDec(t, v.value),
		To: mustAddr(t, v.to), Data: decHex(t, v.data),
	}
}

func TestTxEncodingMatchesIndependentVectors(t *testing.T) {
	for _, v := range txVectors {
		t.Run(v.name, func(t *testing.T) {
			tx := v.tx(t)
			unsigned := tx.unsigned()
			if hex.EncodeToString(unsigned) != v.unsigned {
				t.Fatalf("unsigned\n got  %x\n want %s", unsigned, v.unsigned)
			}
			if d := keccak256(unsigned); hex.EncodeToString(d[:]) != v.digest {
				t.Errorf("digest = %x, want %s", d, v.digest)
			}
			r, s := new(big.Int).SetBytes(decHex(t, v.r)), new(big.Int).SetBytes(decHex(t, v.s))
			signed := tx.signed(v.v, r, s)
			if hex.EncodeToString(signed) != v.signed {
				t.Fatalf("signed\n got  %x\n want %s", signed, v.signed)
			}
			if id := keccak256(signed); "0x"+hex.EncodeToString(id[:]) != v.txid {
				t.Errorf("txid = %x, want %s", id, v.txid)
			}
		})
	}
}

func TestParseUnsignedRoundTrip(t *testing.T) {
	for _, v := range txVectors {
		got, err := parseUnsigned(decHex(t, v.unsigned))
		if err != nil {
			t.Fatalf("%s: %v", v.name, err)
		}
		want := v.tx(t)
		if got.ChainID != want.ChainID || got.Nonce != want.Nonce || got.Gas != want.Gas || got.To != want.To ||
			got.Value.Cmp(want.Value) != 0 || got.MaxFee.Cmp(want.MaxFee) != 0 ||
			got.MaxPriorityFee.Cmp(want.MaxPriorityFee) != 0 || !bytes.Equal(got.Data, want.Data) {
			t.Errorf("%s: parsed %+v, want %+v", v.name, got, want)
		}
	}
}

func TestParseUnsignedRejects(t *testing.T) {
	base := txVectors[0].tx(t)
	enc := func(f func(fields [][]byte) [][]byte) []byte {
		return append([]byte{txTypeDynamicFee}, rlpList(f(base.fields())...)...)
	}
	tests := map[string][]byte{
		"empty":            nil,
		"type only":        {0x02},
		"legacy type":      append([]byte{0x01}, rlpList(base.fields()...)...),
		"no type byte":     rlpList(base.fields()...),
		"signed form":      decHex(t, txVectors[0].signed),
		"trailing byte":    append(base.unsigned(), 0),
		"eight fields":     enc(func(f [][]byte) [][]byte { return f[:8] }),
		"ten fields":       enc(func(f [][]byte) [][]byte { return append(f, rlpBytes(nil)) }),
		"empty to":         enc(func(f [][]byte) [][]byte { f[5] = rlpBytes(nil); return f }),
		"short to":         enc(func(f [][]byte) [][]byte { f[5] = rlpBytes(make([]byte, 19)); return f }),
		"to is list":       enc(func(f [][]byte) [][]byte { f[5] = rlpList(); return f }),
		"access list":      enc(func(f [][]byte) [][]byte { f[8] = rlpList(rlpList(rlpBytes(make([]byte, 20)), rlpList())); return f }),
		"access not list":  enc(func(f [][]byte) [][]byte { f[8] = rlpBytes(nil); return f }),
		"data is list":     enc(func(f [][]byte) [][]byte { f[7] = rlpList(); return f }),
		"nonce leading 0":  enc(func(f [][]byte) [][]byte { f[1] = rlpBytes([]byte{0, 1}); return f }),
		"value 33 bytes":   enc(func(f [][]byte) [][]byte { f[6] = rlpBytes(bytes.Repeat([]byte{1}, 33)); return f }),
		"chain id is list": enc(func(f [][]byte) [][]byte { f[0] = rlpList(); return f }),
	}
	for name, b := range tests {
		if _, err := parseUnsigned(b); !errors.Is(err, ErrTxFormat) {
			t.Errorf("%s: err = %v, want ErrTxFormat", name, err)
		}
	}
}

func FuzzParseUnsigned(f *testing.F) {
	for _, v := range txVectors {
		b, _ := hex.DecodeString(v.unsigned)
		f.Add(b)
	}
	f.Add([]byte{0x02, 0xc0})
	f.Fuzz(func(t *testing.T, b []byte) {
		tx, err := parseUnsigned(b)
		if err != nil {
			return
		}
		if !bytes.Equal(tx.unsigned(), b) {
			t.Fatalf("accepted %x does not re-encode to itself", b)
		}
	})
}

// Подпись из независимой реализации восстанавливается в адрес ключа; любое
// искажение ломает проверку.
func TestCheckSignatureAgainstIndependentVectors(t *testing.T) {
	for _, v := range txVectors {
		t.Run(v.name, func(t *testing.T) {
			var digest [32]byte
			copy(digest[:], decHex(t, v.digest))
			var sig [65]byte
			copy(sig[:32], decHex(t, v.r))
			copy(sig[32:64], decHex(t, v.s))
			sig[64] = v.v

			q, err := recoverPublicKey(digest, new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:64]), sig[64])
			if err != nil {
				t.Fatal(err)
			}
			comp := q.Compressed()
			got, err := checkSignature(digest, sig, comp[:])
			if err != nil || strings.ToLower(got.String()) != v.sender {
				t.Fatalf("sender = %s, %v; want %s", got, err, v.sender)
			}

			flipV := sig
			flipV[64] ^= 1
			if _, err := checkSignature(digest, flipV, comp[:]); !errors.Is(err, ErrSignature) {
				t.Errorf("wrong v accepted: %v", err)
			}
			badR := sig
			badR[5] ^= 1
			if _, err := checkSignature(digest, badR, comp[:]); !errors.Is(err, ErrSignature) {
				t.Errorf("wrong r accepted: %v", err)
			}
			otherDigest := digest
			otherDigest[0] ^= 1
			if _, err := checkSignature(otherDigest, sig, comp[:]); !errors.Is(err, ErrSignature) {
				t.Errorf("wrong digest accepted: %v", err)
			}
			wrongKey, _ := secp256k1.ScalarBaseMult(scalarOf(9))
			wk := wrongKey.Compressed()
			if _, err := checkSignature(digest, sig, wk[:]); !errors.Is(err, ErrSignature) {
				t.Errorf("foreign claimed key accepted: %v", err)
			}
		})
	}
}

func TestCheckSignatureRejectsHighSAndRanges(t *testing.T) {
	v := txVectors[0]
	var digest [32]byte
	copy(digest[:], decHex(t, v.digest))
	pt, _ := secp256k1.ScalarBaseMult(scalarOf(1))
	comp := pt.Compressed()

	s := new(big.Int).SetBytes(decHex(t, v.s))
	high := new(big.Int).Sub(curveOrder, s) // та же подпись с s' = n - s и другой v
	var sig [65]byte
	copy(sig[:32], decHex(t, v.r))
	high.FillBytes(sig[32:64])
	sig[64] = v.v ^ 1
	if _, err := checkSignature(digest, sig, comp[:]); !errors.Is(err, ErrSignature) {
		t.Errorf("high s accepted: %v", err)
	}

	var zero [65]byte
	if _, err := checkSignature(digest, zero, comp[:]); !errors.Is(err, ErrSignature) {
		t.Errorf("zero signature accepted: %v", err)
	}
	n := [65]byte{}
	curveOrder.FillBytes(n[:32])
	copy(n[32:64], decHex(t, v.s))
	if _, err := checkSignature(digest, n, comp[:]); !errors.Is(err, ErrSignature) {
		t.Errorf("r = n accepted: %v", err)
	}
	bad := [65]byte{}
	copy(bad[:], decHex(t, v.r+v.s))
	bad[64] = 2
	if _, err := checkSignature(digest, bad, comp[:]); !errors.Is(err, ErrSignature) {
		t.Errorf("v = 2 accepted: %v", err)
	}
}

// Круговая проверка со своей подписью secp256k1.Sign (что делает Keystore).
func TestRecoverMatchesSecp256k1Sign(t *testing.T) {
	for k := byte(1); k <= 6; k++ {
		priv := scalarOf(k)
		pt, _ := secp256k1.ScalarBaseMult(priv)
		comp := pt.Compressed()
		for i := range 4 {
			digest := keccak256([]byte{k, byte(i)})
			sg, err := secp256k1.Sign(priv, digest[:])
			if err != nil {
				t.Fatal(err)
			}
			var sig [65]byte
			copy(sig[:32], sg.R[:])
			copy(sig[32:64], sg.S[:])
			sig[64] = sg.RecID
			got, err := checkSignature(digest, sig, comp[:])
			unc := pt.Uncompressed()
			want, _ := addressFromPublicKey(unc[:])
			if err != nil || got != want {
				t.Fatalf("key %d msg %d: %s, %v; want %s", k, i, got, err, want)
			}
		}
	}
}
