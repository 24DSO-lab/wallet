package btc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/provider"
)

type fakeDeriver struct {
	mu sync.Mutex
	addresses map[string]bool
}

func (d *fakeDeriver) DeriveAddress(_ string, _ domain.Profile, chain domain.AddressChain, index uint32) (string, string, error) {
	program := make([]byte, 20)
	program[0] = byte(index)
	if chain == domain.ChainChange { program[1] = 1 }
	address := testWitnessAddress("tb", program)
	d.mu.Lock()
	if d.addresses == nil { d.addresses = make(map[string]bool) }
	d.addresses[address] = index == 0
	d.mu.Unlock()
	return address, fmt.Sprintf("m/84'/1'/0'/%d/%d", chainIndex(chain), index), nil
}

func chainIndex(chain domain.AddressChain) uint32 {
	if chain == domain.ChainChange { return 1 }
	return 0
}

type refreshProvider struct {
	used map[string]bool
}

func (p *refreshProvider) Do(_ context.Context, req provider.Request) (*provider.Response, error) {
	body := ""
	switch {
	case req.Path == "/blocks/tip/height":
		body = "100"
	case strings.HasSuffix(req.Path, "/utxo"):
		body = "[]"
	case strings.HasSuffix(req.Path, "/txs"):
		body = "[]"
	case strings.HasPrefix(req.Path, "/address/"):
		address := strings.TrimPrefix(req.Path, "/address/")
		if p.used[address] {
			body = `{"chain_stats":{"funded_txo_sum":10000,"spent_txo_sum":0},"mempool_stats":{"funded_txo_sum":0,"spent_txo_sum":0}}`
		} else {
			body = `{"chain_stats":{"funded_txo_sum":0,"spent_txo_sum":0},"mempool_stats":{"funded_txo_sum":0,"spent_txo_sum":0}}`
		}
	default:
		return nil, fmt.Errorf("unexpected path %s", req.Path)
	}
	return &provider.Response{Status: http.StatusOK, Body: []byte(body)}, nil
}

func TestScannerRefreshScansBothChainsToGapLimit(t *testing.T) {
	deriver := &fakeDeriver{}
	p := &refreshProvider{used: make(map[string]bool)}
	// Build the same first receive/change addresses as the test deriver.
	for _, chain := range []domain.AddressChain{domain.ChainReceive, domain.ChainChange} {
		program := make([]byte, 20)
		if chain == domain.ChainChange { program[1] = 1 }
		p.used[testWitnessAddress("tb", program)] = true
	}
	api, err := NewEsplora(p, domain.ProfileTest)
	if err != nil { t.Fatal(err) }
	scanner, err := NewScanner(api, deriver)
	if err != nil { t.Fatal(err) }

	got, err := scanner.Refresh(context.Background(), domain.PublicWallet{ID: "wallet-1", BTCXpub: "tpub-test"}, domain.BTC)
	if err != nil { t.Fatal(err) }
	if got.Balance.Confirmed.String() != "20000" || !got.Balance.Pending.IsZero() {
		t.Fatalf("unexpected balance: %+v", got.Balance)
	}
	if len(got.Addresses) != 2*(AddressGapLimit+1) {
		t.Fatalf("scanned %d addresses, want %d", len(got.Addresses), 2*(AddressGapLimit+1))
	}
	if !got.Addresses[0].Used || got.Addresses[1].Used {
		t.Fatalf("used flags incorrect: %+v", got.Addresses[:2])
	}
	if got.Asset != domain.BTC || got.FeeAsset != domain.BTC {
		t.Fatalf("unexpected assets: %+v", got)
	}
}


type concurrentRefreshProvider struct {
	*refreshProvider
	mu     sync.Mutex
	active int
	max    int
}

func (p *concurrentRefreshProvider) Do(ctx context.Context, req provider.Request) (*provider.Response, error) {
	if strings.HasPrefix(req.Path, "/address/") && !strings.HasSuffix(req.Path, "/utxo") && !strings.HasSuffix(req.Path, "/txs") {
		p.mu.Lock()
		p.active++
		if p.active > p.max {
			p.max = p.active
		}
		p.mu.Unlock()
		time.Sleep(15 * time.Millisecond)
		defer func() {
			p.mu.Lock()
			p.active--
			p.mu.Unlock()
		}()
	}
	return p.refreshProvider.Do(ctx, req)
}

func TestScannerRefreshAddressLookupsAreBoundedAndConcurrent(t *testing.T) {
	base := &refreshProvider{used: make(map[string]bool)}
	p := &concurrentRefreshProvider{refreshProvider: base}
	api, err := NewEsplora(p, domain.ProfileTest)
	if err != nil {
		t.Fatal(err)
	}
	scanner, err := NewScanner(api, &fakeDeriver{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Refresh(context.Background(), domain.PublicWallet{ID: "wallet-1", BTCXpub: "tpub-test"}, domain.BTC); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	max := p.max
	p.mu.Unlock()
	if max <= 1 {
		t.Fatalf("address lookups were not concurrent: max concurrency = %d", max)
	}
	if max > 2*maxConcurrentAddressLookups {
		t.Fatalf("address lookup concurrency = %d, want <= %d across both chains", max, 2*maxConcurrentAddressLookups)
	}
}

func TestScannerRefreshStopsAtExactGapLimitWithinBatch(t *testing.T) {
	used := make(map[string]bool)
	for _, chain := range []domain.AddressChain{domain.ChainReceive, domain.ChainChange} {
		for _, index := range []byte{0, AddressGapLimit + 1} {
			program := make([]byte, 20)
			program[0] = index
			if chain == domain.ChainChange {
				program[1] = 1
			}
			used[testWitnessAddress("tb", program)] = true
		}
	}
	api, err := NewEsplora(&refreshProvider{used: used}, domain.ProfileTest)
	if err != nil {
		t.Fatal(err)
	}
	scanner, err := NewScanner(api, &fakeDeriver{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := scanner.Refresh(context.Background(), domain.PublicWallet{ID: "wallet-1", BTCXpub: "tpub-test"}, domain.BTC)
	if err != nil {
		t.Fatal(err)
	}
	want := 2 * (AddressGapLimit + 1)
	if len(got.Addresses) != want {
		t.Fatalf("scanned %d addresses, want exactly %d", len(got.Addresses), want)
	}
	for _, address := range got.Addresses {
		if address.Index > AddressGapLimit {
			t.Fatalf("address beyond exact gap boundary was retained: index %d", address.Index)
		}
	}
}

func TestScannerReceiveAddressUsesReceiveOnlyGapScan(t *testing.T) {
	used := make(map[string]bool)
	program := make([]byte, 20)
	used[testWitnessAddress("tb", program)] = true // index 0 was already used
	api, err := NewEsplora(&refreshProvider{used: used}, domain.ProfileTest)
	if err != nil {
		t.Fatal(err)
	}
	scanner, err := NewScanner(api, &fakeDeriver{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := scanner.ReceiveAddress(context.Background(), domain.PublicWallet{ID: "wallet-1", BTCXpub: "tpub-test"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Index != 1 || got.Chain != domain.ChainReceive || got.Used {
		t.Fatalf("unexpected next receive address: %+v", got)
	}
	if got.Path != "m/84'/1'/0'/0/1" {
		t.Fatalf("unexpected derivation path: %s", got.Path)
	}
}


func TestScannerReceiveAddressUsesSlidingWindowAndOrderedGapLimit(t *testing.T) {
	used := make(map[string]bool)
	// Index 2 is used, although later requests may complete before it.
	program := make([]byte, 20)
	program[0] = 2
	used[testWitnessAddress("tb", program)] = true
	base := &refreshProvider{used: used}
	p := &concurrentRefreshProvider{refreshProvider: base}
	api, err := NewEsplora(p, domain.ProfileTest)
	if err != nil { t.Fatal(err) }
	scanner, err := NewScanner(api, &fakeDeriver{})
	if err != nil { t.Fatal(err) }

	got, err := scanner.ReceiveAddress(context.Background(), domain.PublicWallet{ID: "wallet-1", BTCXpub: "tpub-test"})
	if err != nil { t.Fatal(err) }
	if got.Index != 3 || got.Used {
		t.Fatalf("got receive index %d, want first unused index 3 after used index 2", got.Index)
	}
	p.mu.Lock()
	max := p.max
	p.mu.Unlock()
	if max < 2 || max > maxConcurrentAddressLookups {
		t.Fatalf("sliding window concurrency = %d, want 2..%d", max, maxConcurrentAddressLookups)
	}
}

func TestScannerRefreshErrorIncludesAddressContext(t *testing.T) {
	wantErr := errors.New("explorer unavailable")
	p := &failingAddressProvider{refreshProvider: &refreshProvider{used: map[string]bool{}}, err: wantErr}
	api, err := NewEsplora(p, domain.ProfileTest)
	if err != nil { t.Fatal(err) }
	scanner, err := NewScanner(api, &fakeDeriver{})
	if err != nil { t.Fatal(err) }
	_, err = scanner.Refresh(context.Background(), domain.PublicWallet{ID: "wallet-1", BTCXpub: "tpub-test"}, domain.BTC)
	if !errors.Is(err, wantErr) { t.Fatalf("error %v does not wrap provider error", err) }
	if !strings.Contains(err.Error(), "chain=receive index=0") {
		t.Fatalf("error lacks address context: %v", err)
	}
}

type failingAddressProvider struct {
	*refreshProvider
	err error
}

func (p *failingAddressProvider) Do(ctx context.Context, req provider.Request) (*provider.Response, error) {
	if strings.HasPrefix(req.Path, "/address/") && !strings.HasSuffix(req.Path, "/utxo") && !strings.HasSuffix(req.Path, "/txs") {
		return nil, p.err
	}
	return p.refreshProvider.Do(ctx, req)
}

func TestScannerRefreshRejectsMissingXpubAndWrongAsset(t *testing.T) {
	api, _ := NewEsplora(&refreshProvider{used: map[string]bool{}}, domain.ProfileTest)
	scanner, _ := NewScanner(api, &fakeDeriver{})
	if _, err := scanner.Refresh(context.Background(), domain.PublicWallet{ID: "w"}, domain.BTC); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Fatalf("missing xpub error = %v", err)
	}
	if _, err := scanner.Refresh(context.Background(), domain.PublicWallet{ID: "w", BTCXpub: "tpub"}, domain.ETH); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Fatalf("wrong asset error = %v", err)
	}
}


type concurrentDetailProvider struct {
	*refreshProvider
	mu sync.Mutex
	active int
	max int
}

func (p *concurrentDetailProvider) Do(ctx context.Context, req provider.Request) (*provider.Response, error) {
	if strings.HasSuffix(req.Path, "/utxo") || strings.HasSuffix(req.Path, "/txs") {
		p.mu.Lock()
		p.active++
		if p.active > p.max {
			p.max = p.active
		}
		p.mu.Unlock()
		time.Sleep(15 * time.Millisecond)
		defer func() {
			p.mu.Lock()
			p.active--
			p.mu.Unlock()
		}()
	}
	return p.refreshProvider.Do(ctx, req)
}

func TestScannerRefreshFetchesUsedAddressDetailsConcurrently(t *testing.T) {
	used := make(map[string]bool)
	for _, chain := range []domain.AddressChain{domain.ChainReceive, domain.ChainChange} {
		for index := byte(0); index < 4; index++ {
			program := make([]byte, 20)
			program[0] = index
			if chain == domain.ChainChange {
				program[1] = 1
			}
			used[testWitnessAddress("tb", program)] = true
		}
	}
	p := &concurrentDetailProvider{refreshProvider: &refreshProvider{used: used}}
	api, err := NewEsplora(p, domain.ProfileTest)
	if err != nil {
		t.Fatal(err)
	}
	scanner, err := NewScanner(api, &fakeDeriver{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Refresh(context.Background(), domain.PublicWallet{ID: "wallet-1", BTCXpub: "tpub-test"}, domain.BTC); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	max := p.max
	p.mu.Unlock()
	if max < 2 {
		t.Fatalf("used-address detail requests were not concurrent: max concurrency = %d", max)
	}
	if max > 4*maxConcurrentAddressLookups {
		t.Fatalf("detail request concurrency = %d, want <= %d", max, 4*maxConcurrentAddressLookups)
	}
}

func TestTransactionForWalletRejectsConfirmedWithoutBlockMetadata(t *testing.T) {
	for _, status := range []EsploraTxStatus{
		{Confirmed: true, BlockTime: 1_700_000_000},
		{Confirmed: true, BlockHeight: 100},
	} {
		_, err := transactionForWallet("w", EsploraTx{
			TxID: strings.Repeat("d", 64), Status: status,
			Vout: []EsploraOutput{{ScriptPubKeyAddress: "bc1-wallet", Value: 100}},
		}, map[string]struct{}{"bc1-wallet": {}})
		if !errors.Is(err, ErrMalformedResponse) {
			t.Fatalf("confirmed status %+v accepted without complete block metadata: %v", status, err)
		}
	}
}

func TestTransactionForWalletDirections(t *testing.T) {
	ours := map[string]struct{}{"bc1-wallet": {}}
	tests := []struct {
		name string
		tx EsploraTx
		direction domain.Direction
		amount string
		fee string
		counterparty string
	}{
		{
			name: "incoming",
			tx: EsploraTx{TxID: strings.Repeat("a", 64), Fee: 200,
				Vin: []EsploraInput{{Prevout: &struct {
					ScriptPubKeyAddress string `json:"scriptpubkey_address"`
					Value uint64 `json:"value"`
				}{ScriptPubKeyAddress: "external", Value: 1200}}},
				Vout: []EsploraOutput{{ScriptPubKeyAddress: "bc1-wallet", Value: 1000}},
			},
			direction: domain.DirectionIn, amount: "1000", fee: "0", counterparty: "external",
		},
		{
			name: "outgoing",
			tx: EsploraTx{TxID: strings.Repeat("b", 64), Fee: 200,
				Vin: []EsploraInput{
					{Prevout: &struct {
						ScriptPubKeyAddress string `json:"scriptpubkey_address"`
						Value uint64 `json:"value"`
					}{ScriptPubKeyAddress: "external-coinput", Value: 5000}},
					{Prevout: &struct {
						ScriptPubKeyAddress string `json:"scriptpubkey_address"`
						Value uint64 `json:"value"`
					}{ScriptPubKeyAddress: "bc1-wallet", Value: 10000}},
				},
				Vout: []EsploraOutput{
					{ScriptPubKeyAddress: "recipient", Value: 6000},
					{ScriptPubKeyAddress: "bc1-wallet", Value: 3800},
				},
			},
			direction: domain.DirectionOut, amount: "6000", fee: "200", counterparty: "recipient",
		},
		{
			name: "self",
			tx: EsploraTx{TxID: strings.Repeat("c", 64), Fee: 200,
				Vin: []EsploraInput{{Prevout: &struct {
					ScriptPubKeyAddress string `json:"scriptpubkey_address"`
					Value uint64 `json:"value"`
				}{ScriptPubKeyAddress: "bc1-wallet", Value: 10000}}},
				Vout: []EsploraOutput{{ScriptPubKeyAddress: "bc1-wallet", Value: 9800}},
			},
			direction: domain.DirectionSelf, amount: "9800", fee: "200", counterparty: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := transactionForWallet("w", tt.tx, ours)
			if err != nil { t.Fatal(err) }
			if got.Direction != tt.direction || got.Amount.String() != tt.amount ||
				got.Fee.String() != tt.fee || got.Counterparty != tt.counterparty {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
