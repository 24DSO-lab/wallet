package btc

import (
	"context"
	"errors"
	"net/http"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/provider"
)

type recordingProvider struct {
	requests  []provider.Request
	response  provider.Response
	responses []provider.Response
	next      int
	err       error
}

func (p *recordingProvider) Do(_ context.Context, req provider.Request) (*provider.Response, error) {
	p.requests = append(p.requests, req)
	if p.err != nil {
		return nil, p.err
	}
	if p.next < len(p.responses) {
		response := p.responses[p.next]
		p.next++
		return &response, nil
	}
	return &p.response, nil
}

func validTestAddress() string {
	program := make([]byte, 20)
	for i := range program {
		program[i] = byte(i + 1)
	}
	return testWitnessAddress("tb", program)
}

func TestEsploraGetAddress(t *testing.T) {
	p := &recordingProvider{response: provider.Response{Status: http.StatusOK, Body: []byte(`{
		"chain_stats":{"funded_txo_sum":1200,"spent_txo_sum":200},
		"mempool_stats":{"funded_txo_sum":300,"spent_txo_sum":100}
	}`)}}
	api, err := NewEsplora(p, domain.ProfileTest)
	if err != nil { t.Fatal(err) }
	got, err := api.GetAddress(context.Background(), validTestAddress())
	if err != nil { t.Fatal(err) }
	if got.ChainStats.Funded != 1200 || got.ChainStats.Spent != 200 ||
		got.MempoolStats.Funded != 300 || got.MempoolStats.Spent != 100 {
		t.Fatalf("unexpected address stats: %+v", got)
	}
	if len(p.requests) != 1 || p.requests[0].Path == "" {
		t.Fatalf("unexpected requests: %+v", p.requests)
	}
}

func TestEsploraGetAddressRejectsIncompleteStats(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{name: "missing chain stats", body: `{"mempool_stats":{"funded_txo_sum":0,"spent_txo_sum":0}}`},
		{name: "missing mempool stats", body: `{"chain_stats":{"funded_txo_sum":0,"spent_txo_sum":0}}`},
		{name: "missing funded counter", body: `{"chain_stats":{"spent_txo_sum":0},"mempool_stats":{"funded_txo_sum":0,"spent_txo_sum":0}}`},
		{name: "null spent counter", body: `{"chain_stats":{"funded_txo_sum":0,"spent_txo_sum":null},"mempool_stats":{"funded_txo_sum":0,"spent_txo_sum":0}}`},
		{name: "empty object", body: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &recordingProvider{response: provider.Response{Status: http.StatusOK, Body: []byte(tc.body)}}
			api, _ := NewEsplora(p, domain.ProfileTest)
			if _, err := api.GetAddress(context.Background(), validTestAddress()); !errors.Is(err, ErrMalformedResponse) {
				t.Fatalf("expected malformed response, got %v", err)
			}
		})
	}
}

func TestEsploraGetUTXOsRejectsMalformedTxID(t *testing.T) {
	p := &recordingProvider{response: provider.Response{Body: []byte(`[{"txid":"bad","vout":0,"value":42,"status":{"confirmed":false}}]`)}}
	api, _ := NewEsplora(p, domain.ProfileTest)
	_, err := api.GetUTXOs(context.Background(), validTestAddress())
	if !errors.Is(err, ErrMalformedResponse) {
		t.Fatalf("expected malformed response, got %v", err)
	}
}

func TestEsploraGetUTXOsRejectsIncompleteOrDuplicateEntries(t *testing.T) {
	txid := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name, body string
	}{
		{name: "missing status", body: `[{"txid":"`+txid+`","vout":0,"value":42}]`},
		{name: "missing confirmed flag", body: `[{"txid":"`+txid+`","vout":0,"value":42,"status":{}}]`},
		{name: "null confirmed flag", body: `[{"txid":"`+txid+`","vout":0,"value":42,"status":{"confirmed":null}}]`},
		{name: "confirmed without block metadata", body: `[{"txid":"`+txid+`","vout":0,"value":42,"status":{"confirmed":true}}]`},
		{name: "duplicate outpoint", body: `[{"txid":"`+txid+`","vout":0,"value":42,"status":{"confirmed":false}},{"txid":"`+txid+`","vout":0,"value":42,"status":{"confirmed":false}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &recordingProvider{response: provider.Response{Status: http.StatusOK, Body: []byte(tc.body)}}
			api, _ := NewEsplora(p, domain.ProfileTest)
			if _, err := api.GetUTXOs(context.Background(), validTestAddress()); !errors.Is(err, ErrMalformedResponse) {
				t.Fatalf("expected malformed response, got %v", err)
			}
		})
	}
}

func TestEsploraGetTransactionsPagination(t *testing.T) {
	p := &recordingProvider{response: provider.Response{Body: []byte("[]")}}
	api, _ := NewEsplora(p, domain.ProfileTest)
	txid := strings.Repeat("ab", 32)
	_, err := api.GetTransactions(context.Background(), validTestAddress(), txid)
	if err != nil { t.Fatal(err) }
	want := "/address/" + validTestAddress() + "/txs/chain/" + txid
	if p.requests[0].Path != want {
		t.Fatalf("path = %q, want %q", p.requests[0].Path, want)
	}
}

func TestEsploraBroadcastIsNotMarkedIdempotent(t *testing.T) {
	p := &recordingProvider{response: provider.Response{Status: http.StatusOK, Body: []byte(strings.Repeat("cd", 32))}}
	api, _ := NewEsplora(p, domain.ProfileTest)
	got, err := api.BroadcastRaw(context.Background(), []byte{1, 2, 3})
	if err != nil { t.Fatal(err) }
	if got != strings.Repeat("cd", 32) { t.Fatalf("txid = %q", got) }
	if len(p.requests) != 1 || p.requests[0].Method != http.MethodPost || p.requests[0].Idempotent {
		t.Fatalf("broadcast request must be a single non-idempotent POST: %+v", p.requests)
	}
	if !reflect.DeepEqual(p.requests[0].Body, []byte{1, 2, 3}) {
		t.Fatalf("body mismatch: %x", p.requests[0].Body)
	}
}

func TestEsploraPropagatesProviderError(t *testing.T) {
	want := errors.New("offline")
	p := &recordingProvider{err: want}
	api, _ := NewEsplora(p, domain.ProfileTest)
	_, err := api.GetFeeEstimates(context.Background())
	if !errors.Is(err, want) { t.Fatalf("error = %v, want %v", err, want) }
}

func TestNewEsploraValidatesConfiguration(t *testing.T) {
	if _, err := NewEsplora(nil, domain.ProfileTest); err == nil {
		t.Fatal("nil provider must fail")
	}
	if _, err := NewEsplora(&recordingProvider{}, domain.Profile("other")); err == nil {
		t.Fatal("invalid profile must fail")
	}
}


func TestConservativeFeeRate(t *testing.T) {
	got, err := ConservativeFeeRate(FeeEstimates{"1": 12.5, "3": 8.25, "6": 5})
	if err != nil { t.Fatal(err) }
	if got != 8.25 { t.Fatalf("rate = %v, want 8.25", got) }

	got, err = ConservativeFeeRate(FeeEstimates{"6": 5, "12": 3})
	if err != nil { t.Fatal(err) }
	if got != 5 { t.Fatalf("fallback rate = %v, want fastest target's 5", got) }

	if _, err := ConservativeFeeRate(FeeEstimates{"3": math.NaN()}); !errors.Is(err, ErrMalformedResponse) {
		t.Fatalf("NaN estimate must be rejected, got %v", err)
	}
}

func TestEsploraHasSpentOutputsFailsClosedOnIncompleteProviderData(t *testing.T) {
	txid := strings.Repeat("cd", 32)
	transaction := `{"txid":"` + txid + `","vout":[{},{}]}`
	spentTxID := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name string
		body string
		want bool
		wantErr bool
	}{
		{name: "all outputs unspent", body: `[{"spent":false},{"spent":false}]`},
		{name: "spent output", body: `[{"spent":false},{"spent":true,"txid":"` + spentTxID + `","status":{"confirmed":false}}]`, want: true},
		{name: "empty response", body: `[]`, wantErr: true},
		{name: "truncated response", body: `[{"spent":false}]`, wantErr: true},
		{name: "missing spent flag", body: `[{"spent":false},{}]`, wantErr: true},
		{name: "null spent flag", body: `[{"spent":false},{"spent":null}]`, wantErr: true},
		{name: "contradictory unspent entry", body: `[{"spent":false},{"spent":false,"txid":"` + spentTxID + `"}]`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &recordingProvider{responses: []provider.Response{
				{Status: http.StatusOK, Body: []byte(transaction)},
				{Status: http.StatusOK, Body: []byte(tc.body)},
			}}
			api, _ := NewEsplora(p, domain.ProfileTest)
			got, err := api.HasSpentOutputs(context.Background(), txid)
			if tc.wantErr {
				if !errors.Is(err, ErrMalformedResponse) {
					t.Fatalf("expected malformed response, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("HasSpentOutputs = %v, want %v", got, tc.want)
			}
			if len(p.requests) != 2 || p.requests[0].Path != "/tx/"+txid ||
				p.requests[1].Path != "/tx/"+txid+"/outspends" {
				t.Fatalf("unexpected requests: %+v", p.requests)
			}
		})
	}
}

func TestEsploraHasSpentOutputsRejectsTransactionWithoutOutputs(t *testing.T) {
	txid := strings.Repeat("cd", 32)
	p := &recordingProvider{response: provider.Response{Status: http.StatusOK,
		Body: []byte(`{"txid":"` + txid + `","vout":[]}`)}}
	api, _ := NewEsplora(p, domain.ProfileTest)
	if _, err := api.HasSpentOutputs(context.Background(), txid); !errors.Is(err, ErrMalformedResponse) {
		t.Fatalf("expected malformed transaction response, got %v", err)
	}
	if len(p.requests) != 1 {
		t.Fatalf("outspends queried for malformed transaction: %+v", p.requests)
	}
}
