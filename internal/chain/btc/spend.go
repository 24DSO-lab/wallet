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
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
	"github.com/24DSO-lab/wallet/internal/ports"
)

const (
	btcDraftVersion = 1
	btcSequenceRBF  = uint32(0xfffffffd)
	btcSighashAll   = byte(0x01)
	DefaultIncrementalRelayFeeRate = uint64(1) // sat/vB; Bitcoin Core default policy, override for other node policies.
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
	incrementalRelayFeeRate uint64
}

func NewSpendEngine(api *Esplora, scanner *Scanner, derive AddressDeriver) (*SpendEngine, error) {
	if api == nil || scanner == nil || derive == nil {
		return nil, errors.New("btc: Esplora, scanner and address deriver are required")
	}
	if scanner.profile != api.Profile() || scanner.api != api { return nil, errors.New("btc: scanner and provider configuration differ") }
	return &SpendEngine{api:api, scanner:scanner, derive:derive, profile:api.Profile(), incrementalRelayFeeRate:DefaultIncrementalRelayFeeRate}, nil
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
	amount, ok := in.Amount.Uint64()
	if !ok || amount == 0 { return ports.Prepared{}, domain.NewError(domain.CodeInvalidAmount, errors.New("btc: amount outside satoshi range")) }
	if amount < DustThreshold { return ports.Prepared{}, domain.NewError(domain.CodeInvalidAmount, errors.New("btc: recipient output is dust")) }

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
		prepared.Warnings = append(prepared.Warnings, domain.Warning{Code:domain.WarnDustAmount, Detail:"sub-dust remainder is added to the miner fee"})
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
	fail := func(err error) error { return domain.NewError(domain.CodeVerificationFailed, fmt.Errorf("%w: %w", ErrInvalidDraft, err)) }
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
	if err := e.verifyLiveUTXOs(d); err != nil { return fail(err) }
	raw, err := hex.DecodeString(d.UnsignedTx)
	if err != nil { return fail(err) }
	expectedDigests, err := digestRequests(d, raw, e.derive)
	if err != nil { return fail(err) }
	if len(p.Digests) != len(expectedDigests) { return fail(errors.New("digest request count mismatch")) }
	for i := range expectedDigests {
		if p.Digests[i] != expectedDigests[i] { return fail(fmt.Errorf("digest request %d differs from transaction", i)) }
	}
	fee := domain.AmountFromUint64(d.Fee)
	amount := domain.AmountFromUint64(d.Amount)
	total, err := amount.Add(fee)
	if err != nil { return fail(err) }
	if !p.Fee.Equal(fee) || p.FeeAsset != domain.BTC ||
		p.Summary.Network != domain.NetworkBitcoin || p.Summary.Asset != domain.BTC ||
		p.Summary.To != d.To || !p.Summary.Amount.Equal(amount) ||
		!p.Summary.Fee.Equal(fee) || p.Summary.FeeAsset != domain.BTC ||
		!p.Summary.Total.Equal(total) {
		return fail(errors.New("prepared display summary differs from transaction draft"))
	}
	return nil
}

func validateSpendDraft(d spendDraft, derive AddressDeriver) error {
	if d.Version != btcDraftVersion || d.Fee == 0 || d.WalletID == "" || d.Xpub == "" { return ErrInvalidDraft }
	if err := ValidateAddress(d.Profile, d.To); err != nil { return err }
	if len(d.Outputs) == 0 || d.Outputs[0] != (spendOutput{Address:d.To, Value:d.Amount}) { return errors.New("recipient output mismatch") }
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
		pre.Write(hashPrevouts[:]); pre.Write(hashSequence[:])
		for j:=len(txid)-1;j>=0;j-- { pre.WriteByte(txid[j]) }
		_ = binary.Write(&pre, binary.LittleEndian, in.Vout)
		program, err := witnessProgram(in.Address, d.Profile); if err != nil { return nil, err }
		scriptCode := append([]byte{0x76,0xa9,0x14}, program...)
		scriptCode = append(scriptCode, 0x88,0xac)
		writeCompactSize(&pre, uint64(len(scriptCode))); pre.Write(scriptCode)
		_ = binary.Write(&pre, binary.LittleEndian, in.Value)
		_ = binary.Write(&pre, binary.LittleEndian, btcSequenceRBF)
		pre.Write(hashOutputs[:])
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
	if p.Network != domain.NetworkBitcoin || p.Asset != domain.BTC { return ports.Signed{}, ErrInvalidDraft }
	var d spendDraft
	dec:=json.NewDecoder(bytes.NewReader(p.Draft));dec.DisallowUnknownFields()
	if err:=dec.Decode(&d);err!=nil{return ports.Signed{},ErrInvalidDraft}
	if d.Profile != e.profile { return ports.Signed{}, ErrInvalidDraft }
	canonical,err:=json.Marshal(d);if err!=nil||!bytes.Equal(canonical,p.Draft){return ports.Signed{},ErrInvalidDraft}
	if err:=validateSpendDraft(d,e.derive);err!=nil{return ports.Signed{},err}
	raw,err:=hex.DecodeString(d.UnsignedTx);if err!=nil{return ports.Signed{},err}
	reqs,err:=digestRequests(d,raw,e.derive);if err!=nil{return ports.Signed{},err}
	if len(sigs)!=len(reqs){return ports.Signed{},ErrInvalidSignature}
	witnesses:=make([][][]byte,len(sigs))
	for i,sig:=range sigs{
		if len(sig.PubKey)!=33 || sig.Sig[64] > 1 {return ports.Signed{},ErrInvalidSignature}
		path,err:=hd.ParsePath(d.Inputs[i].Path);if err!=nil{return ports.Signed{},err}
		account,profile,err:=hd.ParsePublicKey(d.Xpub);if err!=nil||profile!=d.Profile{return ports.Signed{},ErrInvalidDraft}
		branch,err:=account.Derive(path[3]);if err!=nil{return ports.Signed{},err}
		child,err:=branch.Derive(path[4]);if err!=nil{return ports.Signed{},err}
		expected:=child.Compressed()
		if !bytes.Equal(expected[:],sig.PubKey){return ports.Signed{},ErrInvalidSignature}
		point,err:=secp256k1.ParsePoint(sig.PubKey)
		if err!=nil || !secp256k1.IsLowS(sig.Sig[32:64]) ||
			!secp256k1.Verify(point,reqs[i].Digest[:],sig.Sig[:32],sig.Sig[32:64]) {
			return ports.Signed{},ErrInvalidSignature
		}
		der:=encodeDERSignature(sig.Sig[:32],sig.Sig[32:64])
		der=append(der,btcSighashAll)
		witnesses[i]=[][]byte{der,append([]byte(nil),sig.PubKey...)}
	}
	signedRaw,err:=serializeWitness(raw,witnesses);if err!=nil{return ports.Signed{},err}
	txidHash:=sha256d(raw)
	reversed:=make([]byte,32);for i:=range txidHash{reversed[i]=txidHash[31-i]}
	return ports.Signed{Network:domain.NetworkBitcoin,TxID:domain.TxID(hex.EncodeToString(reversed)),Raw:signedRaw},nil
}

func derLength(n int) byte {
	// DER integers from a 32-byte scalar are at most 33 bytes.
	//nolint:gosec // bounded by the fixed-size secp256k1 scalar.
	return byte(n)
}

func encodeDERSignature(r,s []byte)[]byte{
	integer:=func(v []byte)[]byte{for len(v)>1&&v[0]==0{v=v[1:]};out:=append([]byte(nil),v...);if out[0]&0x80!=0{out=append([]byte{0},out...)};return append([]byte{0x02,derLength(len(out))},out...)}
	ri,si:=integer(r),integer(s);seq:=append(ri,si...);return append([]byte{0x30,derLength(len(seq))},seq...)
}

func serializeWitness(unsigned []byte, witnesses [][][]byte) ([]byte, error) {
	if len(unsigned) < 10 {
		return nil, ErrInvalidDraft
	}
	var out bytes.Buffer
	out.Write(unsigned[:4])
	out.WriteByte(0)
	out.WriteByte(1)

	pos := 4
	inputCount, used, err := readCompactSize(unsigned[pos:])
	if err != nil || inputCount != uint64(len(witnesses)) {
		return nil, ErrInvalidDraft
	}
	writeCompactSize(&out, inputCount)
	pos += used

	inputStart := pos
	for i := uint64(0); i < inputCount; i++ {
		if pos+36 > len(unsigned) {
			return nil, ErrInvalidDraft
		}
		pos += 36
		scriptLen, n, err := readCompactSize(unsigned[pos:])
		if err != nil || n+4 > len(unsigned)-pos {
			return nil, ErrInvalidDraft
		}
		size, ok := checkedSliceSize(scriptLen, len(unsigned)-pos-n-4)
		if !ok {
			return nil, ErrInvalidDraft
		}
		pos += n + size + 4
	}
	outputStart := pos
	out.Write(unsigned[inputStart:outputStart])

	outputCount, used, err := readCompactSize(unsigned[pos:])
	if err != nil {
		return nil, ErrInvalidDraft
	}
	pos += used
	for i := uint64(0); i < outputCount; i++ {
		if pos+8 > len(unsigned) {
			return nil, ErrInvalidDraft
		}
		pos += 8
		scriptLen, n, err := readCompactSize(unsigned[pos:])
		if err != nil || n > len(unsigned)-pos {
			return nil, ErrInvalidDraft
		}
		size, ok := checkedSliceSize(scriptLen, len(unsigned)-pos-n)
		if !ok {
			return nil, ErrInvalidDraft
		}
		pos += n + size
	}
	out.Write(unsigned[outputStart:pos])

	for _, witness := range witnesses {
		writeCompactSize(&out, uint64(len(witness)))
		for _, item := range witness {
			writeCompactSize(&out, uint64(len(item)))
			out.Write(item)
		}
	}
	if pos+4 != len(unsigned) {
		return nil, ErrInvalidDraft
	}
	out.Write(unsigned[pos:])
	return out.Bytes(), nil
}

// checkedSliceSize converts a CompactSize length only after proving it fits
// in the remaining slice and in int.
func checkedSliceSize(n uint64, remaining int) (int, bool) {
	if remaining < 0 {
		return 0, false
	}
	//nolint:gosec // remaining is non-negative and bounded by len(slice).
	if n > uint64(remaining) {
		return 0, false
	}
	//nolint:gosec // n <= remaining <= len(slice), so it fits in int.
	return int(n), true
}

func readCompactSize(b []byte) (uint64, int, error) {
	if len(b) < 1 {
		return 0, 0, ErrInvalidDraft
	}
	switch b[0] {
	case 0xfd:
		if len(b) < 3 {
			return 0, 0, ErrInvalidDraft
		}
		n := uint64(binary.LittleEndian.Uint16(b[1:3]))
		if n < 0xfd {
			return 0, 0, ErrInvalidDraft
		}
		return n, 3, nil
	case 0xfe:
		if len(b) < 5 {
			return 0, 0, ErrInvalidDraft
		}
		n := binary.LittleEndian.Uint32(b[1:5])
		if n <= math.MaxUint16 {
			return 0, 0, ErrInvalidDraft
		}
		return uint64(n), 5, nil
	case 0xff:
		if len(b) < 9 {
			return 0, 0, ErrInvalidDraft
		}
		n := binary.LittleEndian.Uint64(b[1:9])
		if n <= math.MaxUint32 {
			return 0, 0, ErrInvalidDraft
		}
		return n, 9, nil
	default:
		return uint64(b[0]), 1, nil
	}
}


// NewAdapter constructs the complete Bitcoin ChainAdapter using the shared
// Esplora provider and the repository's public-only BIP32 implementation.
func NewAdapter(api *Esplora) (*SpendEngine, error) {
	return NewAdapterWithIncrementalRelayFeeRate(api, DefaultIncrementalRelayFeeRate)
}

// NewAdapterWithIncrementalRelayFeeRate constructs the Bitcoin adapter with
// the incremental relay fee policy of the target node, expressed in sat/vB.
func NewAdapterWithIncrementalRelayFeeRate(api *Esplora, rate uint64) (*SpendEngine, error) {
	scanner, err := NewHDScanner(api)
	if err != nil { return nil, err }
	engine, err := NewSpendEngine(api, scanner, HDAddressDeriver{})
	if err != nil { return nil, err }
	if err := engine.SetIncrementalRelayFeeRate(rate); err != nil { return nil, err }
	return engine, nil
}

var _ ports.ChainAdapter = (*SpendEngine)(nil)

func (e *SpendEngine) Network() domain.Network { return domain.NetworkBitcoin }

func (e *SpendEngine) ValidateAddress(address string) error {
	return ValidateAddress(e.profile, address)
}

func (e *SpendEngine) AddressFromPublicKey([]byte) (string, error) {
	return "", errors.New("btc: addresses must be derived from the account xpub")
}

// ReceiveAddress uses the lightweight receive-only BIP84 scan.
func (e *SpendEngine) ReceiveAddress(ctx context.Context, wallet domain.PublicWallet, asset domain.Asset) (domain.Address, error) {
	if asset != domain.BTC {
		return domain.Address{}, domain.NewError(domain.CodeInvalidInput, errors.New("btc: unsupported receive asset"))
	}
	return e.scanner.ReceiveAddress(ctx, wallet)
}

func (e *SpendEngine) Refresh(ctx context.Context, wallet domain.PublicWallet, asset domain.Asset) (ports.Snapshot, error) {
	return e.scanner.Refresh(ctx, wallet, asset)
}

// Broadcast verifies the locally computed txid before the one-shot Esplora
// POST. An ambiguous network failure is intentionally not retried.
func (e *SpendEngine) Broadcast(ctx context.Context, signed ports.Signed) (domain.TxID, error) {
	if signed.Network != domain.NetworkBitcoin || !validTxID(string(signed.TxID)) {
		return "", domain.NewError(domain.CodeInvalidInput, errors.New("btc: invalid signed transaction metadata"))
	}
	stripped, err := stripWitness(signed.Raw)
	if err != nil { return "", err }
	hash := sha256d(stripped)
	reversed := make([]byte, 32)
	for i := range hash { reversed[i] = hash[31-i] }
	localID := strings.ToLower(hex.EncodeToString(reversed))
	if localID != strings.ToLower(string(signed.TxID)) {
		return "", domain.NewError(domain.CodeVerificationFailed, errors.New("btc: signed transaction id mismatch"))
	}
	remoteID, err := e.api.BroadcastRaw(ctx, signed.Raw)
	if err != nil { return "", err }
	if remoteID != localID {
		return "", domain.NewError(domain.CodeProviderBadResponse, errors.New("btc: provider returned a different transaction id"))
	}
	return domain.TxID(localID), nil
}

func (e *SpendEngine) Status(ctx context.Context, id domain.TxID) (ports.TxStatus, error) {
	if !validTxID(string(id)) { return ports.TxStatus{}, domain.NewError(domain.CodeInvalidInput, ErrInvalidTxID) }
	status, err := e.api.GetTransactionStatus(ctx, string(id))
	if err != nil { return ports.TxStatus{}, err }
	if !status.Confirmed { return ports.TxStatus{State:domain.TxPending, Known:true}, nil }
	if status.BlockHeight == 0 { return ports.TxStatus{}, ErrMalformedResponse }
	tip, err := e.api.GetTipHeight(ctx)
	if err != nil { return ports.TxStatus{}, err }
	if status.BlockHeight > tip { return ports.TxStatus{}, ErrMalformedResponse }
	depth := tip - status.BlockHeight + 1
	if depth > uint64(^uint32(0)) { depth = uint64(^uint32(0)) }
	return ports.TxStatus{State:domain.TxConfirmed, Known:true, BlockHeight:status.BlockHeight, Confirmations:uint32(depth)}, nil
}

// stripWitness parses a SegWit serialization and returns the canonical
// non-witness serialization used for txid calculation.
func stripWitness(raw []byte) ([]byte, error) {
	if len(raw) < 10 || raw[4] != 0 || raw[5] != 1 {
		return nil, ErrInvalidDraft
	}
	pos := 6
	inputCount, used, err := readCompactSize(raw[pos:])
	if err != nil || inputCount == 0 || inputCount > 1000 {
		return nil, ErrInvalidDraft
	}
	pos += used
	var base bytes.Buffer
	base.Write(raw[:4])
	writeCompactSize(&base, inputCount)
	for i := uint64(0); i < inputCount; i++ {
		start := pos
		if pos+36 > len(raw) {
			return nil, ErrInvalidDraft
		}
		pos += 36
		scriptLen, n, err := readCompactSize(raw[pos:])
		if err != nil || n+4 > len(raw)-pos {
			return nil, ErrInvalidDraft
		}
		size, ok := checkedSliceSize(scriptLen, len(raw)-pos-n-4)
		if !ok {
			return nil, ErrInvalidDraft
		}
		pos += n + size + 4
		base.Write(raw[start:pos])
	}
	outputCount, used, err := readCompactSize(raw[pos:])
	if err != nil || outputCount == 0 || outputCount > 1000 {
		return nil, ErrInvalidDraft
	}
	pos += used
	writeCompactSize(&base, outputCount)
	for i := uint64(0); i < outputCount; i++ {
		start := pos
		if pos+8 > len(raw) {
			return nil, ErrInvalidDraft
		}
		pos += 8
		scriptLen, n, err := readCompactSize(raw[pos:])
		if err != nil || n > len(raw)-pos {
			return nil, ErrInvalidDraft
		}
		size, ok := checkedSliceSize(scriptLen, len(raw)-pos-n)
		if !ok {
			return nil, ErrInvalidDraft
		}
		pos += n + size
		base.Write(raw[start:pos])
	}
	for i := uint64(0); i < inputCount; i++ {
		items, n, err := readCompactSize(raw[pos:])
		if err != nil || items > 100 {
			return nil, ErrInvalidDraft
		}
		pos += n
		for j := uint64(0); j < items; j++ {
			size, n, err := readCompactSize(raw[pos:])
			if err != nil || n > len(raw)-pos {
				return nil, ErrInvalidDraft
			}
			itemSize, ok := checkedSliceSize(size, len(raw)-pos-n)
			if !ok {
				return nil, ErrInvalidDraft
			}
			pos += n + itemSize
		}
	}
	if pos+4 != len(raw) {
		return nil, ErrInvalidDraft
	}
	base.Write(raw[pos:])
	return base.Bytes(), nil
}

// verifyLiveUTXOs checks that each claimed prevout is still listed as unspent
// by Esplora. Verify has no context parameter in ChainAdapter, so this bounded
// lookup uses its own deadline and caches one query per wallet address.
func (e *SpendEngine) verifyLiveUTXOs(d spendDraft) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	byAddress := make(map[string]map[string]uint64)
	for _, input := range d.Inputs {
		known, ok := byAddress[input.Address]
		if !ok {
			utxos, err := e.api.GetUTXOs(ctx, input.Address)
			if err != nil { return fmt.Errorf("btc: verify prevout availability: %w", err) }
			known = make(map[string]uint64, len(utxos))
			for _, u := range utxos {
				key := strings.ToLower(u.TxID)+":"+fmt.Sprint(u.Vout)
				known[key] = u.Value
			}
			byAddress[input.Address] = known
		}
		key := strings.ToLower(input.TxID)+":"+fmt.Sprint(input.Vout)
		if value, exists := known[key]; !exists || value != input.Value {
			return errors.New("input prevout is missing, spent or has a different value")
		}
	}
	return nil
}


// SetIncrementalRelayFeeRate configures the node's BIP125 incremental relay
// fee policy in sat/vB. It must be set from the actual target node policy;
// one sat/vB is only Bitcoin Core's default, not a network-wide guarantee.
func (e *SpendEngine) SetIncrementalRelayFeeRate(rate uint64) error {
	if rate == 0 || rate > 1000000 {
		return errors.New("btc: incremental relay fee rate is outside supported range")
	}
	e.incrementalRelayFeeRate = rate
	return nil
}

// replacementVSize conservatively accounts for ECDSA witness signatures that
// may be up to two bytes larger per input than those in the original tx.
func replacementVSize(raw, stripped []byte, inputs int) (uint64, error) {
	vsize := virtualSize(raw, stripped)
	if vsize == 0 || inputs <= 0 || uint64(inputs) > (^uint64(0)-vsize)/2 {
		return 0, errors.New("btc: invalid replacement virtual size")
	}
	return vsize + uint64(inputs)*2, nil
}
