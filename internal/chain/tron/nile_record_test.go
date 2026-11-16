//go:build nilerecord

package tron

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/logging"
	"github.com/24DSO-lab/wallet/internal/provider"
)

// recordingClient сохраняет каждый обмен в testdata/nile/call_NN.json.
type recordingClient struct {
	next provider.Client
	n    int
}

func (r *recordingClient) Do(ctx context.Context, req provider.Request) (*provider.Response, error) {
	resp, err := r.next.Do(ctx, req)
	if resp != nil {
		m := req.Method
		if m == "" {
			m = "GET"
		}
		r.n++
		b, _ := json.MarshalIndent(recordedCall{Method: m, Path: req.Path, Query: req.Query.Encode(),
			ReqBody: string(req.Body), Status: resp.Status, Body: string(resp.Body)}, "", "  ")
		_ = os.WriteFile(filepath.Join(nileDir, fmt.Sprintf("call_%02d.json", r.n)), b, 0o644)
	}
	return resp, err
}

// TestRecordNile снимает ответы настоящего Nile для TestReplayNile.
//
//	TRON_NILE_OWNER=T... TRON_NILE_TO=T... [TRON_NILE_API_KEY=...] \
//	  go test -tags nilerecord -run TestRecordNile ./internal/chain/tron/
//
// Нужен адрес с USDT и TRX из крана Nile. Ключи не пишутся в файлы: в записях
// только тела запросов и ответов, заголовков нет.
func TestRecordNile(t *testing.T) {
	owner, to := os.Getenv("TRON_NILE_OWNER"), os.Getenv("TRON_NILE_TO")
	if owner == "" || to == "" {
		t.Skip("задайте TRON_NILE_OWNER и TRON_NILE_TO")
	}
	key := os.Getenv("TRON_NILE_API_KEY")
	opts := provider.Options{Endpoints: []string{"https://nile.trongrid.io"}, MinInterval: 150 * time.Millisecond,
		Redactor: logging.NewRedactor(key)}
	if key != "" {
		opts.Header = http.Header{"TRON-PRO-API-KEY": {key}}
	}
	hc, err := provider.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old, _ := filepath.Glob(filepath.Join(nileDir, "call_*.json"))
	for _, f := range old {
		_ = os.Remove(f)
	}
	g, _ := NewTronGrid(&recordingClient{next: hc})
	ctx := context.Background()
	o, err := ParseAddress(owner)
	if err != nil {
		t.Fatal(err)
	}
	contract, _ := usdtContract(domain.ProfileTest)
	for name, run := range map[string]func() error{
		"NowBlock":  func() error { _, err := g.NowBlock(ctx); return err },
		"Account":   func() error { _, _, err := g.Account(ctx, o); return err },
		"Resources": func() error { _, err := g.Resources(ctx, o); return err },
		"Prices":    func() error { _, err := g.Prices(ctx); return err },
		"Balance":   func() error { _, err := g.TRC20Balance(ctx, contract, o); return err },
		"Transfers": func() error { _, err := g.TRC20Transfers(ctx, contract, o, 20); return err },
	} {
		if err := run(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Prepare добавляет запрос оценки energy.
	blk, _ := g.NowBlock(ctx)
	a, _ := New(g, domain.ProfileTest, WithClock(func() time.Time { return blk.Timestamp }))
	in := domain.SendIntent{Wallet: domain.PublicWallet{ID: "nile", TronAddress: owner}, Asset: domain.USDT, To: to, Amount: domain.AmountFromUint64(1)}
	if _, err := a.Prepare(ctx, in); err != nil {
		t.Logf("Prepare не записан (нужны USDT и TRX на адресе): %v", err)
	}
	meta, _ := json.Marshal(nileMeta{Owner: owner, To: to})
	if err := os.WriteFile(filepath.Join(nileDir, "meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
}
