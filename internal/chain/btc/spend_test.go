package btc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
)

func testSpendKeys(t *testing.T) (secret.Bytes, string, string, string) {
	t.Helper()
	seed := secret.Copy([]byte("wallet btc spend test seed 32 bytes"))
	master, err := hd.MasterKey(seed)
	if err != nil { seed.Wipe(); t.Fatal(err) }
	accountPath, err := hd.BTCAccountPath(domain.ProfileTest)
	if err != nil { master.Wipe(); seed.Wipe(); t.Fatal(err) }
	account, err := master.DerivePath(accountPath)
	if err != nil { master.Wipe(); seed.Wipe(); t.Fatal(err) }
	xpub, err := account.PublicKey().Serialize(domain.ProfileTest)
	account.Wipe()
	if err != nil { master.Wipe(); seed.Wipe(); t.Fatal(err) }
	deriver := HDAddressDeriver{}
	receive, receivePath, err := deriver.DeriveAddress(xpub, domain.ProfileTest, domain.ChainReceive, 0)
	if err != nil { master.Wipe(); seed.Wipe(); t.Fatal(err) }
	recipient, _, err := deriver.DeriveAddress(xpub, domain.ProfileTest, domain.ChainReceive, 1)
	if err != nil { master.Wipe(); seed.Wipe(); t.Fatal(err) }
	change, changePath, err := deriver.DeriveAddress(xpub, domain.ProfileTest, domain.ChainChange, 0)
	if err != nil { master.Wipe(); seed.Wipe(); t.Fatal(err) }
	master.Wipe()
	return seed, xpub, receive+"|"+receivePath, recipient+"|"+change+"|"+changePath
}

func TestSpendDraftChecksRBFAndBIP143Digest(t *testing.T) {
	seed, xpub, inputData, outputData := testSpendKeys(t)
	defer seed.Wipe()
	inputParts := strings.Split(inputData, "|")
	outputParts := strings.Split(outputData, "|")
	d := spendDraft{
		Version:btcDraftVersion, Profile:domain.ProfileTest, WalletID:"wallet-1", Xpub:xpub,
		To:outputParts[0], Amount:9000, Fee:1000,
		Inputs:[]spendInput{{TxID:strings.Repeat("11",32),Vout:2,Value:10000,Address:inputParts[0],Path:inputParts[1]}},
		Outputs:[]spendOutput{{Address:outputParts[0],Value:9000}},
	}
	raw, err := serializeUnsigned(d)
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(raw[42:46], []byte{0xfd,0xff,0xff,0xff}) { t.Fatalf("input sequence = %x, want RBF 0xfffffffd",raw[42:46]) }
	d.UnsignedTx=hex.EncodeToString(raw)
	if err:=validateSpendDraft(d,HDAddressDeriver{});err!=nil{t.Fatalf("validateSpendDraft: %v",err)}
	reqs,err:=digestRequests(d,raw,HDAddressDeriver{})
	if err!=nil{t.Fatal(err)}
	if len(reqs)!=1 || reqs[0].Network!=domain.NetworkBitcoin || reqs[0].Path!=inputParts[1] || reqs[0].ExpectedAddress!=inputParts[0] {
		t.Fatalf("unexpected digest request: %+v",reqs)
	}
	changed:=d
	changed.Outputs=[]spendOutput{{Address:outputParts[0],Value:8999}}
	changed.Fee=1001
	changedRaw,err:=serializeUnsigned(changed);if err!=nil{t.Fatal(err)}
	changed.UnsignedTx=hex.EncodeToString(changedRaw)
	changedReqs,err:=digestRequests(changed,changedRaw,HDAddressDeriver{});if err!=nil{t.Fatal(err)}
	if reqs[0].Digest==changedReqs[0].Digest{t.Fatal("BIP143 digest did not change when output amount changed")}
}

func TestSpendDraftRejectsForeignChangeAddress(t *testing.T) {
	seed,xpub,inputData,outputData:=testSpendKeys(t);defer seed.Wipe()
	inputParts:=strings.Split(inputData,"|");outputParts:=strings.Split(outputData,"|")
	d:=spendDraft{Version:btcDraftVersion,Profile:domain.ProfileTest,WalletID:"wallet-1",Xpub:xpub,To:outputParts[0],Amount:9000,Fee:1000,
		Inputs:[]spendInput{{TxID:strings.Repeat("22",32),Vout:0,Value:10000,Address:inputParts[0],Path:inputParts[1]}},
		Outputs:[]spendOutput{{Address:outputParts[0],Value:9000},{Address:outputParts[1],Value:1,Path:outputParts[2]}}}
	if err:=validateSpendDraft(d,HDAddressDeriver{});err==nil{t.Fatal("expected dust change rejection")}
	d.Outputs[1].Value=1000
	d.Outputs[1].Address=outputParts[0]
	if err:=validateSpendDraft(d,HDAddressDeriver{});err==nil{t.Fatal("expected foreign change address rejection")}
}

func TestFinalizeAssemblesAndVerifiesP2WPKHWitness(t *testing.T) {
	seed,xpub,inputData,outputData:=testSpendKeys(t)
	defer seed.Wipe()
	inputParts:=strings.Split(inputData,"|");outputParts:=strings.Split(outputData,"|")
	d:=spendDraft{Version:btcDraftVersion,Profile:domain.ProfileTest,WalletID:"wallet-1",Xpub:xpub,To:outputParts[0],Amount:9000,Fee:1000,
		Inputs:[]spendInput{{TxID:strings.Repeat("33",32),Vout:1,Value:10000,Address:inputParts[0],Path:inputParts[1]}},
		Outputs:[]spendOutput{{Address:outputParts[0],Value:9000}}}
	raw,err:=serializeUnsigned(d);if err!=nil{t.Fatal(err)}
	d.UnsignedTx=hex.EncodeToString(raw)
	digests,err:=digestRequests(d,raw,HDAddressDeriver{});if err!=nil{t.Fatal(err)}
	master,err:=hd.MasterKey(seed);if err!=nil{t.Fatal(err)}
	defer master.Wipe()
	path,err:=hd.ParsePath(inputParts[1]);if err!=nil{t.Fatal(err)}
	key,err:=master.DerivePath(path);if err!=nil{t.Fatal(err)}
	defer key.Wipe()
	sig,err:=secp256k1.Sign(key.Secret().Expose(),digests[0].Digest[:]);if err!=nil{t.Fatal(err)}
	pub:=key.PublicKey().Compressed()
	var signed ports.Signature
	copy(signed.Sig[:32],sig.R[:]);copy(signed.Sig[32:64],sig.S[:]);signed.Sig[64]=sig.RecID;signed.PubKey=pub[:]
	draftBytes,err:=jsonMarshalDraft(d);if err!=nil{t.Fatal(err)}
	engine:=&SpendEngine{derive:HDAddressDeriver{},profile:domain.ProfileTest}
	prepared := ports.Prepared{Network:domain.NetworkBitcoin,Asset:domain.BTC,Draft:draftBytes}
	result,err:=engine.Finalize(prepared,[]ports.Signature{signed})
	if err!=nil{t.Fatalf("Finalize: %v",err)}

	// A mathematically valid high-S counterpart must be rejected: Bitcoin
	// standard policy requires low-S signatures before a transaction is relayed.
	order, ok := new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)
	if !ok { t.Fatal("invalid secp256k1 order constant") }
	high := new(big.Int).Sub(order, new(big.Int).SetBytes(signed.Sig[32:64]))
	highSig := signed
	high.FillBytes(highSig.Sig[32:64])
	highSig.Sig[64] ^= 1
	if _, err := engine.Finalize(prepared, []ports.Signature{highSig}); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("high-S signature accepted, err = %v", err)
	}
	stripped,err:=stripWitness(result.Raw);if err!=nil{t.Fatal(err)}
	if !bytes.Equal(stripped,raw){t.Fatal("stripping witness did not recover unsigned transaction")}
	if !validTxID(string(result.TxID)){t.Fatalf("invalid txid %q",result.TxID)}
}

func jsonMarshalDraft(d spendDraft)([]byte,error){return json.Marshal(d)}


func TestReadCompactSizeRejectsNonCanonicalEncodings(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "fd encodes a one-byte value", raw: []byte{0xfd, 0xfc, 0x00}},
		{name: "fe encodes a two-byte value", raw: []byte{0xfe, 0xff, 0xff, 0x00, 0x00}},
		{name: "ff encodes a four-byte value", raw: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := readCompactSize(tt.raw); !errors.Is(err, ErrInvalidDraft) {
				t.Fatalf("readCompactSize(%x) error = %v, want ErrInvalidDraft", tt.raw, err)
			}
		})
	}
}

func TestReadCompactSizeAcceptsCanonicalBoundaries(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want uint64
	}{
		{name: "fd minimum", raw: []byte{0xfd, 0xfd, 0x00}, want: 0xfd},
		{name: "fe minimum", raw: []byte{0xfe, 0x00, 0x00, 0x01, 0x00}, want: 0x10000},
		{name: "ff minimum", raw: []byte{0xff, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}, want: 0x100000000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, used, err := readCompactSize(tt.raw)
			if err != nil || got != tt.want || used != len(tt.raw) {
				t.Fatalf("readCompactSize(%x) = %d, %d, %v; want %d, %d, nil", tt.raw, got, used, err, tt.want, len(tt.raw))
			}
		})
	}
}
