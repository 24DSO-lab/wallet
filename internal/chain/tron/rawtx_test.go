package tron

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
)

// Эталон собран независимым кодировщиком на Python по Tron.proto (не этим
// кодом): owner 41·11…, contract USDT main, to 41·22…, сумма 1234567.
const (
	goldenRaw  = "0a02ab122208010203040506070840dbf8a7ffbc315aae01081f12a9010a31747970652e676f6f676c65617069732e636f6d2f70726f746f636f6c2e54726967676572536d617274436f6e747261637412740a15411111111111111111111111111111111111111111121541a614f803b6fd780986a42c78ec9c7f77e6ded13c2244a9059cbb0000000000000000000000002222222222222222222222222222222222222222000000000000000000000000000000000000000000000000000000000012d68770fbd095ffbc319001c0c39307"
	goldenTxID = "8d49e477dcf55582dc014f9ef765122953cdbb623bda7a094b19764fec393d31"
)

func goldenTx(t *testing.T) RawTx {
	t.Helper()
	contract, _ := usdtContract("main")
	var owner, to Addr
	owner[0], to[0] = 0x41, 0x41
	copy(owner[1:], bytes.Repeat([]byte{0x11}, 20))
	copy(to[1:], bytes.Repeat([]byte{0x22}, 20))
	data, err := transferCalldata(to, big.NewInt(1234567))
	if err != nil {
		t.Fatal(err)
	}
	ts := int64(1_700_000_000_123)
	return RawTx{RefBlockBytes: [2]byte{0xab, 0x12}, RefBlockHash: [8]byte{1, 2, 3, 4, 5, 6, 7, 8},
		Expiration: ts + 300000, Timestamp: ts, FeeLimit: 15_000_000, Owner: owner, Contract: contract, Data: data}
}

func TestRawTxGolden(t *testing.T) {
	r := goldenTx(t)
	got := r.Marshal()
	if hex.EncodeToString(got) != goldenRaw {
		t.Fatalf("marshal differs from the independent encoder:\n got %x\nwant %s", got, goldenRaw)
	}
	if id := TxIDOf(got); hex.EncodeToString(id[:]) != goldenTxID {
		t.Fatalf("txid %x", id)
	}
	back, err := ParseRawTx(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back.Marshal(), got) || back.Owner != r.Owner || back.FeeLimit != r.FeeLimit {
		t.Fatal("parse/marshal roundtrip differs")
	}
	to, amt, err := parseTransferCalldata(back.Data)
	if err != nil || amt.Int64() != 1234567 || to[1] != 0x22 {
		t.Fatalf("calldata: %v %v %v", to, amt, err)
	}
}

// Любое вмешательство в байты должно отклоняться разбором, а не пропускаться.
func TestParseRawTxRejects(t *testing.T) {
	good, _ := hex.DecodeString(goldenRaw)
	ct := func(b []byte) []byte { return append(append([]byte{}, good...), b...) }
	cases := map[string][]byte{
		"empty":                  {},
		"truncated":              good[:len(good)-1],
		"trailing garbage":       ct([]byte{0xFF}),
		"memo field 10":          ct(pbAppendBytes(nil, 10, []byte("hi"))),
		"scripts field 12":       ct(pbAppendBytes(nil, 12, []byte{1})),
		"auths field 9":          ct(pbAppendBytes(nil, 9, []byte{1})),
		"second fee_limit":       ct(pbAppendUint(nil, 18, 1)),
		"wire type 5":            ct([]byte{0x0d, 1, 2, 3, 4}),
		"overlong varint":        append(append([]byte{}, good[:len(good)-4]...), 0xc0, 0xc3, 0x93, 0x87, 0x00),
		"fee_limit out of order": append(pbAppendUint(nil, 18, 5), good...),
	}
	for name, in := range cases {
		if _, err := ParseRawTx(in); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !errors.Is(err, ErrDraftMalformed) {
			t.Errorf("%s: wrong error %v", name, err)
		}
	}
}

// Вставка в глубину: чужие поля внутри Contract, Any и TriggerSmartContract.
func TestParseRawTxRejectsNestedFields(t *testing.T) {
	r := goldenTx(t)
	build := func(trigExtra, anyExtra, ctExtra []byte, ctype uint64, url string) []byte {
		trig := pbAppendBytes(nil, 1, r.Owner[:])
		trig = pbAppendBytes(trig, 2, r.Contract[:])
		trig = append(trig, trigExtra...)
		trig = pbAppendBytes(trig, 4, r.Data)
		anyMsg := pbAppendBytes(nil, 1, []byte(url))
		anyMsg = pbAppendBytes(anyMsg, 2, trig)
		anyMsg = append(anyMsg, anyExtra...)
		c := pbAppendUint(nil, 1, ctype)
		c = pbAppendBytes(c, 2, anyMsg)
		c = append(c, ctExtra...)
		raw := pbAppendBytes(nil, 1, r.RefBlockBytes[:])
		raw = pbAppendBytes(raw, 4, r.RefBlockHash[:])
		raw = pbAppendUint(raw, 8, uint64(r.Expiration))
		raw = pbAppendBytes(raw, 11, c)
		raw = pbAppendUint(raw, 14, uint64(r.Timestamp))
		return pbAppendUint(raw, 18, uint64(r.FeeLimit))
	}
	if _, err := ParseRawTx(build(nil, nil, nil, 31, anyTypeURLTrigger)); err == nil {
		// call_value отсутствует, поле 3 внутри Trigger идёт после 2: порядок канонический.
	} else {
		t.Fatalf("baseline must parse: %v", err)
	}
	cases := map[string][]byte{
		"call_value":       build(pbAppendUint(nil, 3, 1), nil, nil, 31, anyTypeURLTrigger),
		"call_token_value": build(pbAppendUint(nil, 5, 1), nil, nil, 31, anyTypeURLTrigger),
		"token_id":         build(pbAppendUint(nil, 6, 1), nil, nil, 31, anyTypeURLTrigger),
		"any extra":        build(nil, pbAppendBytes(nil, 3, []byte{1}), nil, 31, anyTypeURLTrigger),
		"permission_id":    build(nil, nil, pbAppendUint(nil, 5, 2), 31, anyTypeURLTrigger),
		"contract name":    build(nil, nil, pbAppendBytes(nil, 4, []byte("x")), 31, anyTypeURLTrigger),
		"transfer type 1":  build(nil, nil, nil, 1, anyTypeURLTrigger),
		"wrong type url":   build(nil, nil, nil, 31, "type.googleapis.com/protocol.TransferContract"),
	}
	for name, in := range cases {
		if _, err := ParseRawTx(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseTransferCalldataRejects(t *testing.T) {
	r := goldenTx(t)
	mut := func(f func(d []byte) []byte) []byte { return f(append([]byte{}, r.Data...)) }
	for name, d := range map[string][]byte{
		"short":         r.Data[:67],
		"long":          append(append([]byte{}, r.Data...), 0),
		"other method":  mut(func(d []byte) []byte { d[0] = 0x09; return d }), // approve(address,uint256) = 095ea7b3 и т.д.
		"dirty padding": mut(func(d []byte) []byte { d[5] = 1; return d }),
	} {
		if _, _, err := parseTransferCalldata(d); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := transferCalldata(Addr{0x41}, new(big.Int).Lsh(big.NewInt(1), 256)); err == nil {
		t.Error("amount above uint256 accepted")
	}
}

func FuzzParseRawTx(f *testing.F) {
	good, _ := hex.DecodeString(goldenRaw)
	f.Add(good)
	f.Add([]byte{})
	f.Add(good[:40])
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := ParseRawTx(b)
		if err != nil {
			return
		}
		// Всё, что разобрано, обязано сериализоваться обратно в те же байты.
		if !bytes.Equal(r.Marshal(), b) {
			t.Fatalf("accepted non-canonical input %x", b)
		}
	})
}

func FuzzPBParse(f *testing.F) {
	f.Add([]byte{0x0a, 0x02, 1, 2})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01})
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = pbParse(b) })
}
