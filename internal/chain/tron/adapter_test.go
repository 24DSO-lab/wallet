package tron

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
	"github.com/24DSO-lab/wallet/internal/ports"
)

var testNow = time.UnixMilli(1_700_000_000_500)

// fakeAPI это API с настраиваемыми ответами; вызовы и подмены видны тестам.
type fakeAPI struct {
	block     BlockRef
	trx       uint64
	exists    bool
	res       Resources
	usdt      *big.Int
	transfers []Transfer
	energy    uint64
	prices    Prices
	info      map[string]TxInfo
	accountErr error
	transfersErr error
	bcast     BroadcastResult
	bcastErr  error
	sent      []byte
	err       error // возвращается всеми методами, если задана
}

func newFake() *fakeAPI {
	var id [32]byte
	id[7] = 0x2A // номер блока 42
	for i := 8; i < 32; i++ {
		id[i] = byte(i)
	}
	return &fakeAPI{
		block: BlockRef{Number: 42, ID: id, Timestamp: testNow}, trx: 50_000_000, exists: true,
		res: Resources{Bandwidth: 1000, Energy: 100_000}, usdt: big.NewInt(10_000_000),
		energy: 30_000, prices: Prices{EnergyFee: 420, BandwidthFee: 1000}, info: map[string]TxInfo{},
	}
}

func (f *fakeAPI) NowBlock(context.Context) (BlockRef, error)                 { return f.block, f.err }
func (f *fakeAPI) Account(context.Context, Addr) (uint64, bool, error) { if f.accountErr != nil { return 0, false, f.accountErr }; return f.trx, f.exists, f.err }
func (f *fakeAPI) Resources(context.Context, Addr) (Resources, error)         { return f.res, f.err }
func (f *fakeAPI) TRC20Balance(context.Context, Addr, Addr) (*big.Int, error) { return f.usdt, f.err }
func (f *fakeAPI) TRC20Transfers(context.Context, Addr, Addr, int) ([]Transfer, error) {
	if f.transfersErr != nil { return nil, f.transfersErr }
	return f.transfers, f.err
}
func (f *fakeAPI) EstimateEnergy(context.Context, Addr, Addr, []byte) (uint64, error) {
	return f.energy, f.err
}
func (f *fakeAPI) Prices(context.Context) (Prices, error) { return f.prices, f.err }
func (f *fakeAPI) Broadcast(_ context.Context, tx []byte) (BroadcastResult, error) {
	f.sent = tx
	return f.bcast, f.bcastErr
}
func (f *fakeAPI) TxInfo(_ context.Context, id string) (TxInfo, error) { return f.info[id], f.err }

// Тестовый ключ: скаляр 1 (публичный ключ G), только для тестов.
var testPriv = func() []byte { k := make([]byte, 32); k[31] = 1; return k }()

func testWallet(t *testing.T) domain.PublicWallet {
	t.Helper()
	pt, err := secp256k1.ScalarBaseMult(testPriv)
	if err != nil {
		t.Fatal(err)
	}
	u := pt.Uncompressed()
	addr, err := addrFromPublicKey(u[:])
	if err != nil {
		t.Fatal(err)
	}
	return domain.PublicWallet{ID: "w1", Name: "t", TronAddress: addr.String()}
}

const otherAddr = "TUEZSdKsoDHQMeZwihtdoBiN46zxhGWYdH"

func testIntent(t *testing.T, amount int64) domain.SendIntent {
	return domain.SendIntent{Wallet: testWallet(t), Asset: domain.USDT, To: otherAddr, Amount: domain.AmountFromUint64(uint64(amount))}
}

func newAdapter(t *testing.T, f *fakeAPI) *Adapter {
	t.Helper()
	a, err := New(f, domain.ProfileMain, WithClock(func() time.Time { return testNow }))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func hasWarn(p ports.Prepared, c domain.WarningCode) bool {
	for _, w := range p.Warnings {
		if w.Code == c {
			return true
		}
	}
	return false
}

func TestPrepareHappyPath(t *testing.T) {
	f, in := newFake(), testIntent(t, 2_500_000)
	a := newAdapter(t, f)
	p, err := a.Prepare(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ParseRawTx(p.Draft)
	if err != nil {
		t.Fatal(err)
	}
	if raw.RefBlockBytes != [2]byte{0, 42} || raw.RefBlockHash != [8]byte{8, 9, 10, 11, 12, 13, 14, 15} {
		t.Errorf("ref block: %x %x", raw.RefBlockBytes, raw.RefBlockHash)
	}
	// 30000 energy * 420 sun * 120% = 15 120 000 sun.
	if raw.FeeLimit != 15_120_000 {
		t.Errorf("fee_limit %d", raw.FeeLimit)
	}
	if raw.Expiration-raw.Timestamp != DraftWindow.Milliseconds() {
		t.Error("window")
	}
	if p.Digests[0].Digest != TxIDOf(p.Draft) || p.Digests[0].Path != TronPath || p.Digests[0].ExpectedAddress != in.Wallet.TronAddress {
		t.Error("digest request")
	}
	if !p.Fee.IsZero() || len(p.Warnings) != 0 { // energy и bandwidth хватает
		t.Errorf("fee %s warnings %v", p.Fee, p.Warnings)
	}
	if p.Summary.To != otherAddr || !p.Summary.Total.IsZero() || p.FeeAsset != domain.TRX {
		t.Errorf("summary %+v", p.Summary)
	}
}

func TestPrepareWarnings(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(f *fakeAPI)
		warn   []domain.WarningCode
		noWarn []domain.WarningCode
	}{
		{"no energy, enough TRX", func(f *fakeAPI) { f.res.Energy = 0; f.trx = 50_000_000 },
			[]domain.WarningCode{domain.WarnInsufficientEnergy}, []domain.WarningCode{domain.WarnInsufficientFeeCoin}},
		{"no TRX at all", func(f *fakeAPI) { f.res.Energy = 0; f.trx = 0 },
			[]domain.WarningCode{domain.WarnInsufficientEnergy, domain.WarnInsufficientFeeCoin}, nil},
		{"TRX below burn", func(f *fakeAPI) { f.res.Energy = 0; f.trx = 1_000_000 },
			[]domain.WarningCode{domain.WarnInsufficientFeeCoin}, nil},
		{"high fee (recipient without USDT)", func(f *fakeAPI) { f.res.Energy = 0; f.energy = 131_000; f.trx = 100_000_000 },
			[]domain.WarningCode{domain.WarnHighFee}, nil},
		{"unactivated sender", func(f *fakeAPI) { f.exists = false; f.trx = 0; f.res = Resources{} },
			[]domain.WarningCode{domain.WarnInsufficientFeeCoin}, nil},
	}
	for _, c := range cases {
		f := newFake()
		c.setup(f)
		p, err := newAdapter(t, f).Prepare(context.Background(), testIntent(t, 1_000_000))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for _, w := range c.warn {
			if !hasWarn(p, w) {
				t.Errorf("%s: missing warning %s (got %v)", c.name, w, p.Warnings)
			}
		}
		for _, w := range c.noWarn {
			if hasWarn(p, w) {
				t.Errorf("%s: unexpected warning %s", c.name, w)
			}
		}
	}
	// Отправка самому себе.
	f, in := newFake(), testIntent(t, 1)
	in.To = in.Wallet.TronAddress
	p, err := newAdapter(t, f).Prepare(context.Background(), in)
	if err != nil || !hasWarn(p, domain.WarnSelfAddress) {
		t.Errorf("self: %v %v", err, p.Warnings)
	}
}

func TestPrepareErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("insufficient USDT", func(t *testing.T) {
		f := newFake()
		f.usdt = big.NewInt(5)
		_, err := newAdapter(t, f).Prepare(ctx, testIntent(t, 6))
		if domain.CodeOf(err) != domain.CodeInsufficientFunds {
			t.Fatal(err)
		}
	})
	t.Run("fee above ceiling", func(t *testing.T) {
		f := newFake()
		f.energy = 300_000 // 300k*420*1.2 = 151 TRX
		_, err := newAdapter(t, f).Prepare(ctx, testIntent(t, 1))
		if !errors.Is(err, ErrFeeAboveCeiling) {
			t.Fatal(err)
		}
	})
	t.Run("bad provider prices", func(t *testing.T) {
		f := newFake()
		f.prices.EnergyFee = 0
		if _, err := newAdapter(t, f).Prepare(ctx, testIntent(t, 1)); domain.CodeOf(err) != domain.CodeProviderBadResponse {
			t.Fatal(err)
		}
	})
	t.Run("provider failure passes through", func(t *testing.T) {
		f := newFake()
		f.err = domain.NewError(domain.CodeProviderRateLimited, nil)
		if _, err := newAdapter(t, f).Prepare(ctx, testIntent(t, 1)); domain.CodeOf(err) != domain.CodeProviderRateLimited {
			t.Fatal(err)
		}
	})
	t.Run("assets and addresses", func(t *testing.T) {
		a := newAdapter(t, newFake())
		in := testIntent(t, 1)
		for name, mut := range map[string]func(*domain.SendIntent){
			"TRX":         func(i *domain.SendIntent) { i.Asset = domain.TRX },
			"USDC":        func(i *domain.SendIntent) { i.Asset = domain.USDC },
			"eth address": func(i *domain.SendIntent) { i.To = "0x9858EfFD232B4033E47d90003D41EC34EcaEda94" },
			"zero amount": func(i *domain.SendIntent) { i.Amount = domain.Amount{} },
		} {
			c := in
			mut(&c)
			if _, err := a.Prepare(ctx, c); err == nil {
				t.Errorf("%s accepted", name)
			}
		}
		c := in
		c.Asset = domain.USDC
		if _, err := a.Prepare(ctx, c); domain.CodeOf(err) != domain.CodeWrongNetwork {
			t.Errorf("USDC code %v", domain.CodeOf(err))
		}
	})
}

// Подмена черновика после Prepare: каждая правка должна ловиться Verify.
func TestVerifyCatchesTampering(t *testing.T) {
	f, in := newFake(), testIntent(t, 2_500_000)
	a := newAdapter(t, f)
	p, err := a.Prepare(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Verify(p, in); err != nil {
		t.Fatalf("honest draft rejected: %v", err)
	}
	base, _ := ParseRawTx(p.Draft)
	other, _ := ParseAddress(otherAddr)
	evil := Addr{0x41, 0xEE, 0xEE}
	usdtMain, _ := usdtContract(domain.ProfileMain)
	usdtNile, _ := usdtContract(domain.ProfileTest)

	// resign пересчитывает дайджест, как это сделал бы злоумышленник, чтобы
	// проверить сами поля, а не только расхождение дайджеста.
	resign := func(r RawTx) ports.Prepared {
		q := p
		q.Draft = r.Marshal()
		q.Digests = []ports.DigestRequest{p.Digests[0]}
		q.Digests[0].Digest = TxIDOf(q.Draft)
		return q
	}
	withData := func(to Addr, amt int64) RawTx {
		r := base
		r.Data, _ = transferCalldata(to, big.NewInt(amt))
		return r
	}
	mut := func(f func(*RawTx)) ports.Prepared { r := base; f(&r); return resign(r) }

	cases := map[string]ports.Prepared{
		"recipient":        resign(withData(evil, 2_500_000)),
		"amount up":        resign(withData(other, 2_500_001)),
		"amount x10":       resign(withData(other, 25_000_000)),
		"contract main->x": mut(func(r *RawTx) { r.Contract = evil }),
		"contract nile":    mut(func(r *RawTx) { r.Contract = usdtNile }),
		"sender":           mut(func(r *RawTx) { r.Owner = evil }),
		"fee_limit huge":   mut(func(r *RawTx) { r.FeeLimit = MaxFeeLimitSun + 1 }),
		"fee_limit zero":   mut(func(r *RawTx) { r.FeeLimit = 0 }),
		"fee_limit neg":    mut(func(r *RawTx) { r.FeeLimit = -1 }),
		"expiry far":       mut(func(r *RawTx) { r.Expiration = r.Timestamp + int64(time.Hour/time.Millisecond) }),
		"expiry before ts": mut(func(r *RawTx) { r.Expiration = r.Timestamp - 1 }),
		"ts in future": mut(func(r *RawTx) {
			r.Timestamp += int64(time.Hour / time.Millisecond)
			r.Expiration += int64(time.Hour / time.Millisecond)
		}),
		"approve instead": mut(func(r *RawTx) { r.Data[0], r.Data[1], r.Data[2], r.Data[3] = 0x09, 0x5e, 0xa7, 0xb3 }),
		"digest of other": func() ports.Prepared {
			q := p
			q.Digests = []ports.DigestRequest{p.Digests[0]}
			q.Digests[0].Digest[0] ^= 1
			return q
		}(),
		"digest path": func() ports.Prepared {
			q := p
			q.Digests = []ports.DigestRequest{p.Digests[0]}
			q.Digests[0].Path = "m/44'/195'/0'/0/1"
			return q
		}(),
		"digest address": func() ports.Prepared {
			q := p
			q.Digests = []ports.DigestRequest{p.Digests[0]}
			q.Digests[0].ExpectedAddress = otherAddr
			return q
		}(),
		"two digests": func() ports.Prepared {
			q := p
			q.Digests = append(append([]ports.DigestRequest{}, p.Digests...), p.Digests[0])
			return q
		}(),
		"summary to":     func() ports.Prepared { q := p; q.Summary.To = evil.String(); return q }(),
		"summary amount": func() ports.Prepared { q := p; q.Summary.Amount = domain.AmountFromUint64(1); return q }(),
		"summary fee":    func() ports.Prepared { q := p; q.Summary.Fee = domain.AmountFromUint64(7); return q }(),
		"fee asset":      func() ports.Prepared { q := p; q.FeeAsset = domain.ETH; return q }(),
		"displayed fee": func() ports.Prepared {
			q := p
			q.Fee = domain.AmountFromUint64(1 << 40)
			q.Summary.Fee = q.Fee
			return q
		}(),
		"network": func() ports.Prepared { q := p; q.Network = domain.NetworkEthereum; return q }(),
		"memo appended": func() ports.Prepared {
			q := p
			q.Draft = append(append([]byte{}, p.Draft...), pbAppendBytes(nil, 10, []byte("x"))...)
			q.Digests[0].Digest = TxIDOf(q.Draft)
			return q
		}(),
	}
	_ = usdtMain
	for name, q := range cases {
		err := a.Verify(q, in)
		if err == nil {
			t.Errorf("%s: tampering not detected", name)
			continue
		}
		if c := domain.CodeOf(err); c != domain.CodeVerificationFailed && c != domain.CodeDraftExpired {
			t.Errorf("%s: code %q (%v)", name, c, err)
		}
	}

	// Подмена намерения вместо черновика: тот же черновик, другой получатель/сумма.
	in2 := in
	in2.To = evil.String()
	if err := a.Verify(p, in2); err == nil {
		t.Error("intent recipient change accepted")
	}
	in3 := in
	in3.Amount = domain.AmountFromUint64(1)
	if err := a.Verify(p, in3); err == nil {
		t.Error("intent amount change accepted")
	}
	in4 := in
	in4.Wallet.TronAddress = otherAddr
	if err := a.Verify(p, in4); err == nil {
		t.Error("other wallet accepted")
	}

	// Срок вышел.
	late, _ := New(f, domain.ProfileMain, WithClock(func() time.Time { return testNow.Add(DraftWindow + time.Second) }))
	if err := late.Verify(p, in); domain.CodeOf(err) != domain.CodeDraftExpired {
		t.Errorf("expired draft: %v", err)
	}
	// Тестовый профиль: контракт main в черновике ему чужой.
	nile, _ := New(f, domain.ProfileTest, WithClock(func() time.Time { return testNow }))
	if err := nile.Verify(p, in); err == nil {
		t.Error("main contract accepted on test profile")
	}
}

func signDraft(t *testing.T, draft []byte, priv []byte) ports.Signature {
	t.Helper()
	d := TxIDOf(draft)
	sg, err := secp256k1.Sign(priv, d[:])
	if err != nil {
		t.Fatal(err)
	}
	pt, _ := secp256k1.ScalarBaseMult(priv)
	c := pt.Compressed()
	var out ports.Signature
	copy(out.Sig[:32], sg.R[:])
	copy(out.Sig[32:64], sg.S[:])
	out.Sig[64] = sg.RecID
	out.PubKey = c[:]
	return out
}

func TestFinalizeBroadcastStatus(t *testing.T) {
	ctx := context.Background()
	f, in := newFake(), testIntent(t, 1_000_000)
	a := newAdapter(t, f)
	p, err := a.Prepare(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	sig := signDraft(t, p.Draft, testPriv)
	s, err := a.Finalize(p, []ports.Signature{sig})
	if err != nil {
		t.Fatal(err)
	}
	id := TxIDOf(p.Draft)
	if string(s.TxID) != hex.EncodeToString(id[:]) {
		t.Fatal("txid")
	}
	raw, gotSig, err := splitSigned(s.Raw)
	if err != nil || string(raw) != string(p.Draft) || string(gotSig) != string(sig.Sig[:]) {
		t.Fatalf("signed layout: %v", err)
	}

	f.bcast = BroadcastResult{OK: true, TxID: string(s.TxID)}
	got, err := a.Broadcast(ctx, s)
	if err != nil || got != s.TxID || string(f.sent) != string(s.Raw) {
		t.Fatalf("broadcast: %v %v", got, err)
	}

	// Отказы Finalize.
	other := make([]byte, 32)
	other[31] = 2
	bad := map[string][]ports.Signature{
		"none":        nil,
		"two":         {sig, sig},
		"other key":   {signDraft(t, p.Draft, other)},
		"other draft": {signDraft(t, append(append([]byte{}, p.Draft...), 0), testPriv)},
		"bad v":       func() []ports.Signature { s := sig; s.Sig[64] = 2; return []ports.Signature{s} }(),
		"bad r":       func() []ports.Signature { s := sig; s.Sig[0] ^= 1; return []ports.Signature{s} }(),
		"high s": func() []ports.Signature {
			s := sig
			n, _ := new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)
			hs := new(big.Int).Sub(n, new(big.Int).SetBytes(s.Sig[32:64]))
			hs.FillBytes(s.Sig[32:64])
			return []ports.Signature{s}
		}(),
		"uncompressed key": func() []ports.Signature {
			s := sig
			pt, _ := secp256k1.ParsePoint(s.PubKey)
			u := pt.Uncompressed()
			s.PubKey = u[:]
			return []ports.Signature{s}
		}(),
	}
	for name, sigs := range bad {
		if _, err := a.Finalize(p, sigs); domain.CodeOf(err) != domain.CodeSigningRefused {
			t.Errorf("finalize %s: %v", name, err)
		}
	}

	// Отказы Broadcast.
	f.bcast = BroadcastResult{OK: false, Code: "SIGERROR"}
	if _, err := a.Broadcast(ctx, s); domain.CodeOf(err) != domain.CodeBroadcastRejected {
		t.Errorf("rejected: %v", err)
	}
	f.bcast = BroadcastResult{Duplicate: true, Code: "DUP_TRANSACTION_ERROR"}
	if got, err := a.Broadcast(ctx, s); err != nil || got != s.TxID {
		t.Errorf("duplicate must be accepted: %v %v", got, err)
	}
	f.bcast = BroadcastResult{OK: true, TxID: hex.EncodeToString(make([]byte, 32))}
	if _, err := a.Broadcast(ctx, s); domain.CodeOf(err) != domain.CodeProviderBadResponse {
		t.Errorf("foreign txid: %v", err)
	}
	wrong := s
	wrong.TxID = "00"
	if _, err := a.Broadcast(ctx, wrong); err == nil {
		t.Error("txid mismatch accepted")
	}
	wrong = s
	wrong.Network = domain.NetworkEthereum
	if _, err := a.Broadcast(ctx, wrong); domain.CodeOf(err) != domain.CodeWrongNetwork {
		t.Error("wrong network accepted")
	}

	// Status.
	idStr := domain.TxID(s.TxID)
	check := func(info TxInfo, want domain.TxStatus, known bool) {
		t.Helper()
		f.info[string(idStr)] = info
		st, err := a.Status(ctx, idStr)
		if err != nil || st.State != want || st.Known != known {
			t.Errorf("status %+v: got %+v %v, want state=%v known=%v", info, st, err, want, known)
		}
	}
	check(TxInfo{}, domain.TxPending, false)
	check(TxInfo{Found: true, Success: true, BlockNumber: 100, CurrentTip: 100}, domain.TxPending, true)
	check(TxInfo{Found: true, Success: true, BlockNumber: 100, CurrentTip: 100 + SolidityDepth - 1}, domain.TxConfirmed, true)
	check(TxInfo{Found: true, Success: false, BlockNumber: 100, CurrentTip: 101}, domain.TxFailed, true)
	if _, err := a.Status(ctx, "zz"); err == nil {
		t.Error("bad id accepted")
	}
}

func TestRefreshUSDT(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	w := testWallet(t)
	me := w.TronAddress
	id1, id2, id3 := hex.EncodeToString(bytes32(1)), hex.EncodeToString(bytes32(2)), hex.EncodeToString(bytes32(3))
	f.transfers = []Transfer{
		{TxID: id1, From: otherAddr, To: me, Value: domain.AmountFromUint64(5_000_000), Time: testNow, Confirmed: true},
		{TxID: id2, From: me, To: otherAddr, Value: domain.AmountFromUint64(1_000_000), Time: testNow, Confirmed: true},
		{TxID: id3, From: me, To: me, Value: domain.AmountFromUint64(1), Time: testNow, Confirmed: false},
		{TxID: "ff", From: otherAddr, To: otherAddr, Value: domain.AmountFromUint64(9), Time: testNow, Confirmed: true}, // чужой
	}
	f.info[id1] = TxInfo{Found: true, Success: true, BlockNumber: 90, CurrentTip: 200}
	f.info[id2] = TxInfo{Found: true, Success: true, BlockNumber: 95, FeeSun: 13_000_000, CurrentTip: 200}
	snap, err := newAdapter(t, f).Refresh(ctx, w, domain.USDT)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Balance.Confirmed.String() != "10000000" || !snap.Balance.Pending.IsZero() {
		t.Errorf("balance %v", snap.Balance)
	}
	if snap.FeeAsset != domain.TRX || snap.FeeBalance.String() != "50000000" {
		t.Errorf("fee balance %v %v", snap.FeeAsset, snap.FeeBalance)
	}
	if len(snap.History) != 3 {
		t.Fatalf("history %d (foreign transfer must be dropped)", len(snap.History))
	}
	in, out, self := snap.History[0], snap.History[1], snap.History[2]
	if in.Direction != domain.DirectionIn || in.Counterparty != otherAddr || in.Status != domain.TxConfirmed || in.BlockHeight != 90 || !in.Fee.IsZero() {
		t.Errorf("in %+v", in)
	}
	if out.Direction != domain.DirectionOut || out.Fee.String() != "13000000" || out.FeeAsset != domain.TRX || out.Counterparty != otherAddr {
		t.Errorf("out %+v", out)
	}
	if self.Direction != domain.DirectionSelf || self.Status != domain.TxPending || self.Counterparty != "" {
		t.Errorf("self %+v", self)
	}
}

func TestRefreshUSDTKeepsTokenBalanceWhenAuxiliaryProvidersFail(t *testing.T) {
	boom := domain.NewError(domain.CodeProviderRateLimited, errors.New("rate limited"))
	f := newFake(); f.transfersErr, f.accountErr = boom, boom
	snap, err := newAdapter(t, f).Refresh(context.Background(), testWallet(t), domain.USDT)
	if err != nil { t.Fatalf("refresh should preserve valid USDT balance: %v", err) }
	if snap.Balance.Confirmed.String() != "10000000" { t.Fatalf("USDT balance = %s", snap.Balance.Confirmed) }
	if snap.HistoryError != string(domain.CodeProviderRateLimited) || len(snap.History) != 0 { t.Fatalf("history = %+v", snap) }
	if snap.FeeBalanceError != string(domain.CodeProviderRateLimited) { t.Fatalf("fee balance error = %q", snap.FeeBalanceError) }
}

func bytes32(b byte) []byte { x := make([]byte, 32); x[31] = b; return x }

func TestRefreshTRXAndErrors(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	a := newAdapter(t, f)
	snap, err := a.Refresh(ctx, testWallet(t), domain.TRX)
	if err != nil || snap.Balance.Confirmed.String() != "50000000" || len(snap.History) != 0 {
		t.Fatalf("%v %+v", err, snap)
	}
	if _, err := a.Refresh(ctx, testWallet(t), domain.ETH); domain.CodeOf(err) != domain.CodeWrongNetwork {
		t.Errorf("ETH: %v", err)
	}
	if _, err := a.Refresh(ctx, domain.PublicWallet{ID: "x"}, domain.USDT); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Errorf("no address: %v", err)
	}
	f.err = domain.NewError(domain.CodeProviderUnavailable, nil)
	if _, err := a.Refresh(ctx, testWallet(t), domain.USDT); domain.CodeOf(err) != domain.CodeProviderUnavailable {
		t.Errorf("provider failure: %v", err)
	}
	// Пустой аккаунт: нулевые балансы без ошибки.
	g := newFake()
	g.exists, g.trx, g.usdt = false, 0, big.NewInt(0)
	snap, err = newAdapter(t, g).Refresh(ctx, testWallet(t), domain.USDT)
	if err != nil || !snap.Balance.Confirmed.IsZero() || !snap.FeeBalance.IsZero() {
		t.Errorf("empty account: %v %+v", err, snap)
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := New(nil, domain.ProfileMain); err == nil {
		t.Error("nil api")
	}
	if _, err := New(newFake(), "bogus"); err == nil {
		t.Error("bad profile")
	}
	a := newAdapter(t, newFake())
	if a.Network() != domain.NetworkTron {
		t.Error("network")
	}
}
