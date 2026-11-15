package btc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
	"github.com/24DSO-lab/wallet/internal/ports"
)

const (
	btcDraftVersion = 1
	btcSequenceRBF  = uint32(0xfffffffd)
	btcSighashAll   = byte(0x01)
)

var (
	ErrInvalidDraft = errors.New("btc: invalid transaction draft")
	ErrInvalidSignature = errors.New("btc: invalid transaction signature")
)

// SpendEngine builds, verifies and finalizes native-SegWit P2WPKH transactions.
// It receives only public wallet data; signing remains inside ports.Keystore.
type SpendEngine struct {
	api *Esplora
	scanner *Scanner
	derive AddressDeriver
	profile domain.Profile
}

func NewSpendEngine(api *Esplora, scanner *Scanner, derive AddressDeriver) (*SpendEngine, error) {
	if api == nil || scanner == nil || derive == nil {
		return nil, errors.New("btc: Esplora, scanner and address deriver are required")
	}
	return &SpendEngine{api:api, scanner:scanner, derive:derive, profile:api.Profile()}, nil
}

type spendInput struct {
	TxID string `json:"txid"`
	Vout uint32 `json:"vout"`
	Value uint64 `json:"value"`
	Address string `json:"address"`
	Path string `json:"path"`
}
type spendOutput struct {
	Address string `json:"address"`
	Value uint64 `json:"value"`
	Path string `json:"path,omitempty"`
}
type spendDraft struct {
	Version int `json:"version"`
	Profile domain.Profile `json:"profile"`
	WalletID string `json:"wallet_id"`
	Xpub string `json:"xpub"`
	To string `json:"to"`
	Amount uint64 `json:"amount"`
	Fee uint64 `json:"fee"`
	Inputs []spendInput `json:"inputs"`
	Outputs []spendOutput `json:"outputs"`
	UnsignedTx string `json:"unsigned_tx"`
}

// Prepare refreshes UTXOs, selects coins deterministically, creates an RBF
// transaction and returns one BIP143 SIGHASH_ALL request per input.
func (e *SpendEngine) Prepare(ctx context.Context, in domain.SendIntent) (ports.Prepared, error) {
	if err := in.Validate(); err != nil { return ports.Prepared{}, err }
	if in.Asset != domain.BTC { return ports.Prepared{}, domain.NewError(domain.CodeInvalidInput, errors.New("btc: only BTC can be spent by this adapter")) }
	if err := ValidateAddress(e.profile, in.To); err != nil { return ports.Prepared{}, err }
\tif amount < DustThreshold { return ports.Prepared{}, domain.NewError(domain.CodeInvalidAmount, errors.New("btc: recipient output is dust")) }
	amount, ok := in.Amount.Uint64()
	if !ok || amount == 0 { return ports.Prepared{}, domain.NewError(domain.CodeInvalidAmount, errors.New("btc: amount outside satoshi range")) }

	snapshot, err := e.scanner.Refresh(ctx, in.Wallet, domain.BTC)
	if err != nil { return ports.Prepared{}, err }
	estimates, err := e.api.GetFeeEstimates(ctx)
	if err != nil { return ports.Prepared{}, err }
	rate, err := ConservativeFeeRate(estimates)
	if err != nil { return ports.Prepared{}, err }
	selection, err := SelectCoins(snapshot.UTXOs, in.Amount, rate)
	if err != nil { return ports.Prepared{}, err }

	addressByValue := make(map[string]domain.Address, len(snapshot.Addresses))
	for _, a := range snapshot.Addresses { addressByValue[a.Address] = a }
	inputs := make([]spendInput, 0, len(selection.UTXOs))
	for _, u := range selection.UTXOs {
		a, exists := addressByValue[u.Address]
		if !exists || a.WalletID != in.Wallet.ID { return ports.Prepared{}, fmt.Errorf("%w: selected input address is not in wallet scan", ErrInvalidDraft) }
		inputs = append(inputs, spendInput{TxID:string(u.TxID), Vout:u.Vout, Value:mustUint64(u.Amount), Address:u.Address, Path:a.Path})
	}
	outputs := []spendOutput{{Address:in.To, Value:amount}}
	if selection.HasChange {
		change, found := nextChangeAddress(snapshot.Addresses)
		if !found { return ports.Prepared{}, fmt.Errorf("%w: no unused change address", ErrInvalidDraft) }
		changeValue, ok := selection.Change.Uint64()
		if !ok || changeValue < DustThreshold { return ports.Prepared{}, ErrInvalidDraft }
		outputs = append(outputs, spendOutput{Address:change.Address, Value:changeValue, Path:change.Path})
	}
	draft := spendDraft{Version:btcDraftVersion, Profile:e.profile, WalletID:in.Wallet.ID, Xpub:in.Wallet.BTCXpub,
		To:in.To, Amount:amount, Fee:mustUint64(selection.Fee), Inputs:inputs, Outputs:outputs}
	raw, err := serializeUnsigned(draft)
	if err != nil { return ports.Prepared{}, err }
	draft.UnsignedTx = hex.EncodeToString(raw)
	encoded, err := json.Marshal(draft)
	if err != nil { return ports.Prepared{}, err }

	digests, err := digestRequests(draft, raw, e.derive)
	if err != nil { return ports.Prepared{}, err }
	fee := selection.Fee
	prepared := ports.Prepared{
		Network:domain.NetworkBitcoin, Asset:domain.BTC, Draft:encoded, Digests:digests,
		Fee:fee, FeeAsset:domain.BTC,
		Summary:ports.Summary{Network:domain.NetworkBitcoin, Asset:domain.BTC, To:in.To, Amount:in.Amount, Fee:fee, FeeAsset:domain.BTC},
	}
	total, _ := in.Amount.Add(fee)
	prepared.Summary.Total = total
	if selection.DustAddedToFee {
		prepared.Warnings = append(prepared.Warnings, domain.Warning{Code:domain.WarnDustAmount, Detail:"dust remainder is added to the miner fee or recipient output is below the standard P2WPKH threshold"})
	}
	return prepared, nil
}

func nextChangeAddress(addresses []domain.Address) (domain.Address, bool) {
	var best domain.Address
	found := false
	for _, a := range addresses {
		if a.Chain != domain.ChainChange || a.Used { continue }
		if !found || a.Index < best.Index { best, found = a, true }
	}
	return best, found
}

func mustUint64(a domain.Amount) uint64 { v, _ := a.Uint64(); return v }

// Verify deliberately decodes and validates Draft from scratch. It does not
// trust Prepared.Digests, Summary, Fee, or any other cached display field.
func (e *SpendEngine) Verify(p ports.Prepared, in domain.SendIntent) error {
	fail := func(err error) error { return domain.NewError(domain.CodeVerificationFailed, fmt.Errorf("%w: %v", ErrInvalidDraft, err)) }
	if p.Network != domain.NetworkBitcoin || p.Asset != domain.BTC || in.Asset != domain.BTC { return fail(errors.New("network or asset mismatch")) }
	if _, ok := in.Amount.Uint64(); !ok { return fail(errors.New("amount outside satoshi range")) }
	if err := in.Validate(); err != nil { return fail(err) }
	if err := ValidateAddress(e.profile, in.To); err != nil { return fail(err) }
	var d spendDraft
	dec := json.NewDecoder(bytes.NewReader(p.Draft))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil { return fail(err) }
	if dec.More() { return fail(errors.New("trailing draft data")) }
	canonical, err := json.Marshal(d)
	if err != nil || !bytes.Equal(canonical, p.Draft) { return fail(errors.New("non-canonical draft encoding")) }
	if d.Version != btcDraftVersion || d.Profile != e.profile || d.WalletID != in.Wallet.ID || d.Xpub != in.Wallet.BTCXpub ||
		d.To != in.To || d.Amount != mustUint64(in.Amount) || len(d.Inputs) == 0 || len(d.Outputs) == 0 {
		return fail(errors.New("draft does not match send intent"))
	}
	if len(d.Inputs) > 1000 || len(d.Outputs) > 2 { return fail(errors.New("transaction size limits exceeded")) }
	if err := validateSpendDraft(d, e.derive); err != nil { return fail(err) }
	return nil
}

func validateSpendDraft(d spendDraft, derive AddressDeriver) error {
	if err := ValidateAddress(d.Profile, d.To); err != nil { return err }
	if len(d.Outputs) == 0 || d.Outputs[0] != (spendOutput{Address:d.To, Value:d.Amount}) { return errors.New("recipient output mismatch") }
	if d.Amount < DustThreshold { /* explicit warning is carried by Prepare */ }
	inputTotal, outputTotal := new(big.Int), new(big.Int)
	seen := make(map[string]struct{}, len(d.Inputs))
	for _, in := range d.Inputs {
		if !validTxID(in.TxID) || in.Value == 0 || ValidateAddress(d.Profile, in.Address) != nil { return errors.New("invalid input metadata") }
		key := strings.ToLower(in.TxID)+":"+fmt.Sprint(in.Vout)
		if _, ok := seen[key]; ok { return errors.New("duplicate transaction input") }; seen[key]=struct{}{}
		path, err := hd.ParsePath(in.Path); if err != nil || hd.CheckPathForNetwork(domain.NetworkBitcoin, d.Profile, path) != nil { return errors.New("invalid BIP84 input path") }
		chain := domain.ChainReceive; if path[3] == 1 { chain = domain.ChainChange }
		address, gotPath, err := derive.DeriveAddress(d.Xpub, d.Profile, chain, path[4])
		if err != nil || address != in.Address || gotPath != in.Path { return errors.New("input address does not match xpub derivation") }
		inputTotal.Add(inputTotal, new(big.Int).SetUint64(in.Value))
	}
	for _, out := range d.Outputs {
		if err := ValidateAddress(d.Profile, out.Address); err != nil || out.Value < DustThreshold { return errors.New("invalid or dust output") }
		outputTotal.Add(outputTotal, new(big.Int).SetUint64(out.Value))
	}
	if len(d.Outputs) > 2 { return errors.New("unexpected output count") }
	if d.Outputs[0].Path != "" { return errors.New("recipient output has an unexpected derivation path") }
	if len(d.Outputs) == 2 {
		path, err := hd.ParsePath(d.Outputs[1].Path); if err != nil || hd.CheckPathForNetwork(domain.NetworkBitcoin, d.Profile, path) != nil || path[3] != 1 { return errors.New("invalid change derivation path") }
		changeAddress, gotPath, err := derive.DeriveAddress(d.Xpub, d.Profile, domain.ChainChange, path[4]); if err != nil || changeAddress != d.Outputs[1].Address || gotPath != d.Outputs[1].Path { return errors.New("change output does not match wallet xpub") }
	} else if len(d.Outputs) != 1 { return errors.New("unexpected output count") }
	if new(big.Int).Sub(inputTotal, outputTotal).Cmp(new(big.Int).SetUint64(d.Fee)) != 0 { return errors.New("input/output/fee arithmetic mismatch") }
	raw, err := hex.DecodeString(d.UnsignedTx); if err != nil { return err }
	want, err := serializeUnsigned(d); if err != nil { return err }
	if !bytes.Equal(raw, want) { return errors.New("unsigned transaction bytes differ from draft metadata") }
	return nil
}

func digestRequests(d spendDraft, raw []byte, derive AddressDeriver) ([]ports.DigestRequest, error) {
	_ = raw
	hashPrevouts, hashSequence, hashOutputs := sha256d(nil), sha256d(nil), sha256d(nil)
	var prevouts, sequences, outputs bytes.Buffer
	for _, in := range d.Inputs {
		txid, _ := hex.DecodeString(in.TxID)
		for i:=len(txid)-1;i>=0;i-- { prevouts.WriteByte(txid[i]) }
		_ = binary.Write(&prevouts, binary.LittleEndian, in.Vout)
		_ = binary.Write(&sequences, binary.LittleEndian, btcSequenceRBF)
	}
	for _, out := range d.Outputs {
		script, err := p2wpkhScript(out.Address, d.Profile); if err != nil { return nil, err }
		writeVarInt(&outputs, uint64(out.Value)); writeCompactSize(&outputs, uint64(len(script))); outputs.Write(script)
	}
	hashPrevouts, hashSequence, hashOutputs = sha256d(prevouts.Bytes()), sha256d(sequences.Bytes()), sha256d(outputs.Bytes())
	reqs := make([]ports.DigestRequest, len(d.Inputs))
	for i, in := range d.Inputs {
		txid, _ := hex.DecodeString(in.TxID)
		var pre bytes.Buffer
		_ = binary.Write(&pre, binary.LittleEndian, uint32(2))
		pre.Write(hashPrevouts); pre.Write(hashSequence)
		for j:=len(txid)-1;j>=0;j-- { pre.WriteByte(txid[j]) }
		_ = binary.Write(&pre, binary.LittleEndian, in.Vout)
		program, err := witnessProgram(in.Address, d.Profile); if err != nil { return nil, err }
		scriptCode := append([]byte{0x76,0xa9,0x14}, program...)
		scriptCode = append(scriptCode, 0x88,0xac)
		writeCompactSize(&pre, uint64(len(scriptCode))); pre.Write(scriptCode)
		_ = binary.Write(&pre, binary.LittleEndian, in.Value)
		_ = binary.Write(&pre, binary.LittleEndian, btcSequenceRBF)
		pre.Write(hashOutputs)
		_ = binary.Write(&pre, binary.LittleEndian, uint32(0))
		_ = binary.Write(&pre, binary.LittleEndian, uint32(btcSighashAll))
		digest := sha256d(pre.Bytes())
		path, err := hd.ParsePath(in.Path); if err != nil { return nil, err }
		chain := domain.ChainReceive; if path[3] == 1 { chain = domain.ChainChange }
		expected, gotPath, err := derive.DeriveAddress(d.Xpub, d.Profile, chain, path[4])
		if err != nil || expected != in.Address || gotPath != in.Path { return nil, ErrInvalidDraft }
		reqs[i] = ports.DigestRequest{Network:domain.NetworkBitcoin, Path:in.Path, ExpectedAddress:in.Address, Digest:digest}
	}
	return reqs,nil
}

func serializeUnsigned(d spendDraft) ([]byte,error) {
	var b bytes.Buffer
	_ = binary.Write(&b,binary.LittleEndian,uint32(2))
	writeCompactSize(&b,uint64(len(d.Inputs)))
	for _,in:=range d.Inputs {
		txid,err:=hex.DecodeString(in.TxID);if err!=nil||len(txid)!=32{return nil,ErrInvalidDraft}
		for i:=31;i>=0;i--{b.WriteByte(txid[i])}
		_ = binary.Write(&b,binary.LittleEndian,in.Vout)
		writeCompactSize(&b,0)
		_ = binary.Write(&b,binary.LittleEndian,btcSequenceRBF)
	}
	writeCompactSize(&b,uint64(len(d.Outputs)))
	for _,out:=range d.Outputs {
		script,err:=p2wpkhScript(out.Address,d.Profile);if err!=nil{return nil,err}
		_ = binary.Write(&b,binary.LittleEndian,out.Value)
		writeCompactSize(&b,uint64(len(script)));b.Write(script)
	}
	_ = binary.Write(&b,binary.LittleEndian,uint32(0))
	return b.Bytes(),nil
}

func writeCompactSize(b *bytes.Buffer,n uint64) {
	switch {case n<0xfd:b.WriteByte(byte(n));case n<=math.MaxUint16:b.WriteByte(0xfd);_ = binary.Write(b,binary.LittleEndian,uint16(n));case n<=math.MaxUint32:b.WriteByte(0xfe);_ = binary.Write(b,binary.LittleEndian,uint32(n));default:b.WriteByte(0xff);_ = binary.Write(b,binary.LittleEndian,n)}
}
func writeVarInt(b *bytes.Buffer,n uint64){_ = binary.Write(b,binary.LittleEndian,n)}
func sha256d(b []byte) [32]byte { first:=sha256.Sum256(b);return sha256.Sum256(first[:]) }
func witnessProgram(address string,profile domain.Profile)([]byte,error){if err:=ValidateAddress(profile,address);err!=nil{return nil,err};_,_,p,err:=decodeWitnessAddress(address);return p,err}
func p2wpkhScript(address string,profile domain.Profile)([]byte,error){p,err:=witnessProgram(address,profile);if err!=nil{return nil,err};return append([]byte{0x00,0x14},p...),nil}

// Finalize verifies each returned signature against its BIP143 digest and the
// public key derived from the xpub/path, then assembles the P2WPKH witness.
func (e *SpendEngine) Finalize(p ports.Prepared, sigs []ports.Signature) (ports.Signed,error) {
	var d spendDraft
	dec:=json.NewDecoder(bytes.NewReader(p.Draft));dec.DisallowUnknownFields()
	if err:=dec.Decode(&d);err!=nil{return ports.Signed{},ErrInvalidDraft}
	if err:=validateSpendDraft(d,e.derive);err!=nil{return ports.Signed{},err}
	raw,err:=hex.DecodeString(d.UnsignedTx);if err!=nil{return ports.Signed{},err}
	reqs,err:=digestRequests(d,raw,e.derive);if err!=nil{return ports.Signed{},err}
	if len(sigs)!=len(reqs){return ports.Signed{},ErrInvalidSignature}
	witnesses:=make([][][]byte,len(sigs))
	for i,sig:=range sigs{
		if len(sig.PubKey)!=33{return ports.Signed{},ErrInvalidSignature}
		path,err:=hd.ParsePath(d.Inputs[i].Path);if err!=nil{return ports.Signed{},err}
		account,profile,err:=hd.ParsePublicKey(d.Xpub);if err!=nil||profile!=d.Profile{return ports.Signed{},ErrInvalidDraft}
		branch,err:=account.Derive(path[3]);if err!=nil{return ports.Signed{},err}
		child,err:=branch.Derive(path[4]);if err!=nil{return ports.Signed{},err}
		expected:=child.Compressed()
		if !bytes.Equal(expected[:],sig.PubKey){return ports.Signed{},ErrInvalidSignature}
		point,err:=secp256k1.ParsePoint(sig.PubKey);if err!=nil||!secp256k1.Verify(point,reqs[i].Digest[:],sig.Sig[:32],sig.Sig[32:64]){return ports.Signed{},ErrInvalidSignature}
		der:=encodeDERSignature(sig.Sig[:32],sig.Sig[32:64])
		der=append(der,btcSighashAll)
		witnesses[i]=[][]byte{der,append([]byte(nil),sig.PubKey...)}
	}
	signedRaw,err:=serializeWitness(raw,witnesses);if err!=nil{return ports.Signed{},err}
	txidHash:=sha256d(raw)
	reversed:=make([]byte,32);for i:=range txidHash{reversed[i]=txidHash[31-i]}
	return ports.Signed{Network:domain.NetworkBitcoin,TxID:domain.TxID(hex.EncodeToString(reversed)),Raw:signedRaw},nil
}

func encodeDERSignature(r,s []byte)[]byte{
	integer:=func(v []byte)[]byte{for len(v)>1&&v[0]==0{v=v[1:]};out:=append([]byte(nil),v...);if out[0]&0x80!=0{out=append([]byte{0},out...)};return append([]byte{0x02,byte(len(out))},out...)}
	ri,si:=integer(r),integer(s);seq:=append(ri,si...);return append([]byte{0x30,byte(len(seq))},seq...)
}

func serializeWitness(unsigned []byte,witnesses [][][]byte)([]byte,error){
	if len(unsigned)<10{return nil,ErrInvalidDraft}
	var out bytes.Buffer
	out.Write(unsigned[:4]);out.WriteByte(0);out.WriteByte(1)
	pos:=4;n,used,err:=readCompactSize(unsigned[pos:]);if err!=nil||n!=uint64(len(witnesses)){return nil,ErrInvalidDraft};pos+=used
	// Copy serialized inputs and outputs (excluding version, marker/flag and locktime).
	inputStart:=pos
	for i:=uint64(0);i<n;i++{
		if pos+36>len(unsigned){return nil,ErrInvalidDraft};pos+=36
		scriptLen,u,e:=readCompactSize(unsigned[pos:]);if e!=nil{return nil,e};pos+=u+int(scriptLen)+4
		if pos>len(unsigned){return nil,ErrInvalidDraft}
	}
	outputStart:=pos
	out.Write(unsigned[inputStart:outputStart])
	outputCount,u,err:=readCompactSize(unsigned[pos:]);if err!=nil{return nil,err};pos+=u
	for i:=uint64(0);i<outputCount;i++{if pos+8>len(unsigned){return nil,ErrInvalidDraft};pos+=8;l,u,e:=readCompactSize(unsigned[pos:]);if e!=nil{return nil,e};pos+=u+int(l);if pos>len(unsigned){return nil,ErrInvalidDraft}}
	out.Write(unsigned[outputStart:pos])
	for _,w:=range witnesses{writeCompactSize(&out,uint64(len(w)));for _,item:=range w{writeCompactSize(&out,uint64(len(item)));out.Write(item)}}
	if pos+4!=len(unsigned){return nil,ErrInvalidDraft};out.Write(unsigned[pos:])
	return out.Bytes(),nil
}
func readCompactSize(b []byte)(uint64,int,error){if len(b)<1{return 0,0,ErrInvalidDraft};switch b[0]{case 0xfd:if len(b)<3{return 0,0,ErrInvalidDraft};return uint64(binary.LittleEndian.Uint16(b[1:3])),3,nil;case 0xfe:if len(b)<5{return 0,0,ErrInvalidDraft};return uint64(binary.LittleEndian.Uint32(b[1:5])),5,nil;case 0xff:if len(b)<9{return 0,0,ErrInvalidDraft};return binary.LittleEndian.Uint64(b[1:9]),9,nil;default:return uint64(b[0]),1,nil}}
