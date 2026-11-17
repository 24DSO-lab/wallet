package eth

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// fakeRPC это RPC на заготовленных значениях.
type fakeRPC struct {
	mu       sync.Mutex // фейк вызывается из нескольких горутин
	chainID  uint64
	chainErr error
	balance  *big.Int
	balErr   error
	callOut  []byte
	callErr  error
	calls    []string

	chainCalls int

	head    uint64
	nonce   uint64
	nonceEr error
	gas     uint64
	gasErr  error
	gasMsg  *CallMsg
	fees    FeeHistory
	feesErr error

	sendErr   error
	sendHash  string
	sentRaw   [][]byte
	receipt   *Receipt
	receiptEr error
	known     bool
	knownErr  error
	headErr   error
}

func (f *fakeRPC) ChainID(context.Context) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chainCalls++
	return f.chainID, f.chainErr
}
func (f *fakeRPC) BlockNumber(context.Context) (uint64, error) { return f.head, f.headErr }
func (f *fakeRPC) Balance(context.Context, Address) (*big.Int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "balance")
	return f.balance, f.balErr
}
func (f *fakeRPC) Call(_ context.Context, to Address, _ []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "call:"+to.String())
	return f.callOut, f.callErr
}
func (f *fakeRPC) PendingNonce(context.Context, Address) (uint64, error) { return f.nonce, f.nonceEr }
func (f *fakeRPC) EstimateGas(_ context.Context, m CallMsg) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gasMsg = &m
	return f.gas, f.gasErr
}
func (f *fakeRPC) FeeHistory(context.Context, int, []float64) (FeeHistory, error) {
	return f.fees, f.feesErr
}
func (f *fakeRPC) SendRawTransaction(_ context.Context, raw []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentRaw = append(f.sentRaw, raw)
	return f.sendHash, f.sendErr
}
func (f *fakeRPC) TransactionReceipt(context.Context, string) (*Receipt, error) {
	return f.receipt, f.receiptEr
}
func (f *fakeRPC) TransactionKnown(context.Context, string) (bool, error) { return f.known, f.knownErr }

// fakeIndexer это Indexer на заготовленных значениях.
type fakeIndexer struct {
	normal    []IndexedTx
	normalErr error
	transfers []IndexedTransfer
	transErr  error
}

func (f *fakeIndexer) NormalTxs(context.Context, Address) ([]IndexedTx, error) {
	return f.normal, f.normalErr
}
func (f *fakeIndexer) TokenTransfers(context.Context, Address, Address) ([]IndexedTransfer, error) {
	return f.transfers, f.transErr
}

var fixedNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func wallet() domain.PublicWallet {
	return domain.PublicWallet{ID: "w1", Name: "main", ETHAddress: addrA}
}

func newAdapter(t *testing.T, rpc *fakeRPC, idx *fakeIndexer) *Adapter {
	t.Helper()
	a, err := New(domain.ProfileTest, rpc, idx, Options{Now: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func word(n int64) []byte { return new(big.Int).SetInt64(n).FillBytes(make([]byte, 32)) }

func normalTx(hash string, from, to Address, value int64, input bool) IndexedTx {
	return IndexedTx{
		Hash: hash, From: from, To: to, HasTo: true, Value: big.NewInt(value), HasInput: input,
		Block: 100, Time: time.Unix(1700000000, 0).UTC(), GasUsed: 21000, GasPrice: big.NewInt(2_000_000_000),
	}
}

func transfer(hash string, from, to Address, value int64, t0 int64) IndexedTransfer {
	return IndexedTransfer{
		Hash: hash, Contract: mustAddr2(USDCSepoliaContract), From: from, To: to, Value: big.NewInt(value),
		Block: 101, Time: time.Unix(t0, 0).UTC(),
	}
}

func mustAddr2(s string) Address { a, _, _ := parseAddress(s); return a }

func TestRefreshEmptyWallet(t *testing.T) {
	rpc := &fakeRPC{chainID: ChainIDSepolia, balance: big.NewInt(0), callOut: word(0)}
	a := newAdapter(t, rpc, &fakeIndexer{})
	for _, asset := range []domain.Asset{domain.ETH, domain.USDC} {
		snap, err := a.Refresh(context.Background(), wallet(), asset)
		if err != nil {
			t.Fatalf("%s: %v", asset, err)
		}
		if !snap.Balance.Confirmed.IsZero() || len(snap.History) != 0 || snap.Asset != asset || !snap.FetchedAt.Equal(fixedNow) {
			t.Errorf("%s: snapshot = %+v", asset, snap)
		}
	}
}

func TestRefreshETH(t *testing.T) {
	own, other := mustAddr2(addrA), mustAddr2(addrB)
	rpc := &fakeRPC{chainID: ChainIDSepolia, balance: big.NewInt(1_500_000_000_000_000_000)}
	idx := &fakeIndexer{normal: []IndexedTx{
		normalTx(hash1, other, own, 5000, false), // входящий
		func() IndexedTx { // исходящий, неуспешный
			tx := normalTx(hash2, own, other, 7000, false)
			tx.Failed = true
			tx.Time = tx.Time.Add(time.Hour)
			return tx
		}(),
		normalTx("0x"+"33"+hash1[4:], own, mustAddr2(USDCSepoliaContract), 0, true), // перевод USDC: не в истории ETH
		normalTx("0x"+"44"+hash1[4:], other, own, 0, false),                         // входящий нулевой: спам
		normalTx("0x"+"55"+hash1[4:], other, other, 99, false),                      // чужая запись
		normalTx(hash1, other, own, 5000, false),                                    // дубликат
	}}
	snap, err := newAdapter(t, rpc, idx).Refresh(context.Background(), wallet(), domain.ETH)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Balance.Confirmed.String() != "1500000000000000000" {
		t.Errorf("balance = %s", snap.Balance.Confirmed)
	}
	if len(snap.History) != 2 {
		t.Fatalf("history = %+v", snap.History)
	}
	out, in := snap.History[0], snap.History[1] // новые первыми
	if out.TxID != domain.TxID(hash2) || out.Direction != domain.DirectionOut || out.Status != domain.TxFailed ||
		out.Amount.String() != "7000" || out.Fee.String() != "42000000000000" || out.Counterparty != other.String() || out.FeeAsset != domain.ETH {
		t.Errorf("outgoing = %+v", out)
	}
	if in.TxID != domain.TxID(hash1) || in.Direction != domain.DirectionIn || in.Status != domain.TxConfirmed ||
		in.Amount.String() != "5000" || !in.Fee.IsZero() || in.Counterparty != other.String() {
		t.Errorf("incoming = %+v", in)
	}
	if len(rpc.calls) != 1 || rpc.calls[0] != "balance" {
		t.Errorf("ETH refresh must only read the balance, got %v", rpc.calls)
	}
}

func TestRefreshUSDC(t *testing.T) {
	own, other := mustAddr2(addrA), mustAddr2(addrB)
	rpc := &fakeRPC{chainID: ChainIDSepolia, balance: big.NewInt(3_000_000_000_000_000), callOut: word(12_500_000)}
	idx := &fakeIndexer{
		transfers: []IndexedTransfer{
			transfer(hash1, other, own, 2_000_000, 1700000000), // входящий
			transfer(hash2, own, other, 500_000, 1700000500),   // исходящий
		},
		normal: []IndexedTx{
			normalTx(hash1, other, own, 0, true), // комиссию платил не мы
			normalTx(hash2, own, mustAddr2(USDCSepoliaContract), 0, true),
		},
	}
	snap, err := newAdapter(t, rpc, idx).Refresh(context.Background(), wallet(), domain.USDC)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Balance.Confirmed.String() != "12500000" || snap.FeeBalance.String() != "3000000000000000" || snap.FeeAsset != domain.ETH {
		t.Errorf("snapshot = %+v", snap)
	}
	if len(snap.History) != 2 {
		t.Fatalf("history = %+v", snap.History)
	}
	out, in := snap.History[0], snap.History[1]
	if out.TxID != domain.TxID(hash2) || out.Direction != domain.DirectionOut || out.Amount.String() != "500000" ||
		out.Fee.String() != "42000000000000" || out.Counterparty != other.String() || out.Asset != domain.USDC {
		t.Errorf("outgoing = %+v", out)
	}
	if in.Direction != domain.DirectionIn || in.Amount.String() != "2000000" || !in.Fee.IsZero() {
		t.Errorf("incoming = %+v", in)
	}
	if len(rpc.calls) != 2 {
		t.Fatalf("USDC refresh RPC calls = %v, want balanceOf and ETH balance", rpc.calls)
	}
	seen := map[string]bool{}
	for _, call := range rpc.calls {
		seen[call] = true
	}
	if !seen["call:"+USDCSepoliaContract] || !seen["balance"] {
		t.Errorf("USDC refresh RPC calls = %v, want pinned balanceOf and ETH balance", rpc.calls)
	}
}

func TestUSDCHistoryIgnoresForeignTokenAndStrangers(t *testing.T) {
	own, other := mustAddr2(addrA), mustAddr2(addrB)
	foreign := transfer(hash1, other, own, 1_000_000, 1700000000)
	foreign.Contract = other // подменённый контракт в ответе индексатора
	stranger := transfer(hash2, other, other, 1_000_000, 1700000000)
	h := usdcHistory("w1", own, mustAddr2(USDCSepoliaContract), []IndexedTransfer{foreign, stranger}, nil)
	if len(h) != 0 {
		t.Errorf("foreign records accepted: %+v", h)
	}
}

func TestUSDCHistoryAggregatesPerHash(t *testing.T) {
	own, other := mustAddr2(addrA), mustAddr2(addrB)
	contract := mustAddr2(USDCSepoliaContract)
	in := func(v int64) IndexedTransfer { return transfer(hash1, other, own, v, 1700000000) }
	out := func(v int64) IndexedTransfer { return transfer(hash1, own, other, v, 1700000000) }

	tests := []struct {
		name string
		tr   []IndexedTransfer
		dir  domain.Direction
		amt  string
	}{
		{"two incoming", []IndexedTransfer{in(3), in(4)}, domain.DirectionIn, "7"},
		{"net outgoing", []IndexedTransfer{in(3), out(10)}, domain.DirectionOut, "7"},
		{"net incoming", []IndexedTransfer{in(10), out(3)}, domain.DirectionIn, "7"},
		{"balanced", []IndexedTransfer{in(5), out(5)}, domain.DirectionSelf, "5"},
		{"to self", []IndexedTransfer{transfer(hash1, own, own, 9, 1700000000)}, domain.DirectionSelf, "9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := usdcHistory("w1", own, contract, tt.tr, nil)
			if len(h) != 1 || h[0].Direction != tt.dir || h[0].Amount.String() != tt.amt {
				t.Errorf("history = %+v", h)
			}
		})
	}
}

func TestRefreshSourceFailures(t *testing.T) {
	boom := domain.NewError(domain.CodeProviderUnavailable, errors.New("down"))
	ok := &fakeRPC{chainID: ChainIDSepolia, balance: big.NewInt(1), callOut: word(1)}

	tests := []struct {
		name  string
		asset domain.Asset
		rpc   *fakeRPC
		idx   *fakeIndexer
		code  domain.ErrorCode
	}{
		{"eth history down", domain.ETH, ok, &fakeIndexer{normalErr: boom}, domain.CodeProviderUnavailable},
		{"usdc transfers down", domain.USDC, ok, &fakeIndexer{transErr: boom}, domain.CodeProviderUnavailable},
		{"usdc fee source down", domain.USDC, ok, &fakeIndexer{normalErr: boom}, domain.CodeProviderUnavailable},
		{"rate limited", domain.USDC, ok, &fakeIndexer{transErr: domain.NewError(domain.CodeProviderRateLimited, errors.New("429"))}, domain.CodeProviderRateLimited},
		{"balance down", domain.ETH, &fakeRPC{chainID: ChainIDSepolia, balErr: boom}, &fakeIndexer{}, domain.CodeProviderUnavailable},
		{"balanceOf down", domain.USDC, &fakeRPC{chainID: ChainIDSepolia, callErr: boom}, &fakeIndexer{}, domain.CodeProviderUnavailable},
		{"balanceOf short answer", domain.USDC, &fakeRPC{chainID: ChainIDSepolia, balance: big.NewInt(1), callOut: []byte{1}}, &fakeIndexer{}, domain.CodeProviderBadResponse},
		{"balanceOf empty answer (no contract)", domain.USDC, &fakeRPC{chainID: ChainIDSepolia, balance: big.NewInt(1), callOut: nil}, &fakeIndexer{}, domain.CodeProviderBadResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap, err := newAdapter(t, tt.rpc, tt.idx).Refresh(context.Background(), wallet(), tt.asset)
			if err == nil {
				t.Fatal("failure of a source was swallowed")
			}
			if domain.CodeOf(err) != tt.code {
				t.Errorf("code = %q, want %q (%v)", domain.CodeOf(err), tt.code, err)
			}
			if len(snap.History) != 0 || !snap.Balance.Confirmed.IsZero() {
				t.Errorf("partial data returned beside the error: %+v", snap)
			}
		})
	}
}

func TestRefreshRejectsWrongChain(t *testing.T) {
	rpc := &fakeRPC{chainID: ChainIDMainnet, balance: big.NewInt(1)} // профиль test, а узел отвечает main
	a := newAdapter(t, rpc, &fakeIndexer{})
	_, err := a.Refresh(context.Background(), wallet(), domain.ETH)
	if domain.CodeOf(err) != domain.CodeWrongNetwork {
		t.Fatalf("err = %v, want wrong_network", err)
	}
	if len(rpc.calls) != 0 {
		t.Errorf("balance was requested from the wrong chain: %v", rpc.calls)
	}
}

func TestEnsureChainCachesSuccessOnly(t *testing.T) {
	rpc := &fakeRPC{chainID: ChainIDSepolia, chainErr: errors.New("net"), balance: big.NewInt(0)}
	a := newAdapter(t, rpc, &fakeIndexer{})
	if _, err := a.Refresh(context.Background(), wallet(), domain.ETH); err == nil {
		t.Fatal("chain id error swallowed")
	}
	rpc.chainErr = nil
	for range 3 {
		if _, err := a.Refresh(context.Background(), wallet(), domain.ETH); err != nil {
			t.Fatal(err)
		}
	}
	if rpc.chainCalls != 2 {
		t.Errorf("chainId calls = %d, want 2 (failure retried, success cached)", rpc.chainCalls)
	}
}

func TestRefreshInputValidation(t *testing.T) {
	a := newAdapter(t, &fakeRPC{chainID: ChainIDSepolia, balance: big.NewInt(0)}, &fakeIndexer{})
	w := wallet()
	tests := map[string]struct {
		w     domain.PublicWallet
		asset domain.Asset
	}{
		"bitcoin asset":  {w, domain.BTC},
		"usdt asset":     {w, domain.USDT},
		"empty address":  {domain.PublicWallet{ID: "w1"}, domain.ETH},
		"bad checksum":   {domain.PublicWallet{ID: "w1", ETHAddress: "0x5AAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"}, domain.ETH},
		"zero address":   {domain.PublicWallet{ID: "w1", ETHAddress: "0x0000000000000000000000000000000000000000"}, domain.ETH},
		"no wallet id":   {domain.PublicWallet{ETHAddress: addrA}, domain.ETH},
		"tron as ethere": {domain.PublicWallet{ID: "w1", ETHAddress: "TQrY8tryqsYVCYS3MFbtffiPp2ccyn4STm"}, domain.ETH},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := a.Refresh(context.Background(), tt.w, tt.asset)
			if domain.CodeOf(err) != domain.CodeInvalidInput {
				t.Errorf("err = %v, want invalid_input", err)
			}
		})
	}
}

func TestNewRejectsUnknownProfile(t *testing.T) {
	if _, err := New(domain.Profile("x"), &fakeRPC{}, &fakeIndexer{}, Options{}); err == nil {
		t.Error("unknown profile accepted")
	}
}

func TestAdapterBasics(t *testing.T) {
	a := newAdapter(t, &fakeRPC{}, &fakeIndexer{})
	if a.Network() != domain.NetworkEthereum {
		t.Errorf("network = %s", a.Network())
	}
	if err := a.ValidateAddress("nope"); domain.CodeOf(err) != domain.CodeInvalidAddress {
		t.Errorf("ValidateAddress: %v", err)
	}
	if _, err := a.AddressFromPublicKey([]byte{1}); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Errorf("AddressFromPublicKey: %v", err)
	}
}
