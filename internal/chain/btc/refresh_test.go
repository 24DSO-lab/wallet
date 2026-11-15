package btc

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/provider"
)

type fakeDeriver struct {
	addresses map[string]bool
}

func (d *fakeDeriver) DeriveAddress(_ string, _ domain.Profile, chain domain.AddressChain, index uint32) (string, string, error) {
	program := make([]byte, 20)
	program[0] = byte(index)
	if chain == domain.ChainChange { program[1] = 1 }
	address := testWitnessAddress("tb", program)
	if d.addresses == nil { d.addresses = make(map[string]bool) }
	d.addresses[address] = index == 0
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
