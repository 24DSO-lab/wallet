package tron

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/provider"
)

// Записанные ответы Nile лежат в testdata/nile/*.json. Их снимает тест
// TestRecordNile (nile_record_test.go, тег nilerecord): см. README, раздел
// «Запись эталона». Без записей воспроизведение пропускается.

const nileDir = "testdata/nile"

type nileMeta struct {
	Owner string `json:"owner"`
	To    string `json:"to"`
}

type recordedCall struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Query   string `json:"query"`
	ReqBody string `json:"req_body"`
	Status  int    `json:"status"`
	Body    string `json:"body"`
}

// replayClient отдаёт записанные ответы по (метод, путь, запрос, тело).
type replayClient struct{ calls []recordedCall }

func (r *replayClient) Do(_ context.Context, req provider.Request) (*provider.Response, error) {
	m := req.Method
	if m == "" {
		m = "GET"
	}
	for _, c := range r.calls {
		if c.Method == m && c.Path == req.Path && c.Query == req.Query.Encode() && c.ReqBody == string(req.Body) {
			return &provider.Response{Status: c.Status, Body: []byte(c.Body)}, nil
		}
	}
	return nil, errors.New("replay: no recorded response for " + m + " " + req.Path)
}

func loadNile(t *testing.T) (*replayClient, nileMeta) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(nileDir, "call_*.json"))
	raw, err := os.ReadFile(filepath.Join(nileDir, "meta.json"))
	if err != nil || len(files) == 0 {
		t.Skip("нет записанных ответов Nile: снять их тестом TestRecordNile (см. README)")
	}
	var meta nileMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	rc := &replayClient{}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		var c recordedCall
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		rc.calls = append(rc.calls, c)
	}
	return rc, meta
}

// Воспроизведение на настоящих ответах Nile: клиент должен их разобрать, а
// собранный черновик пройти Verify.
func TestReplayNile(t *testing.T) {
	rc, meta := loadNile(t)
	g, _ := NewTronGrid(rc)
	ctx := context.Background()
	owner, err := ParseAddress(meta.Owner)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := g.NowBlock(ctx)
	if err != nil {
		t.Fatalf("NowBlock: %v", err)
	}
	if _, _, err := g.Account(ctx, owner); err != nil {
		t.Fatalf("Account: %v", err)
	}
	if _, err := g.Resources(ctx, owner); err != nil {
		t.Fatalf("Resources: %v", err)
	}
	if _, err := g.Prices(ctx); err != nil {
		t.Fatalf("Prices: %v", err)
	}
	contract, _ := usdtContract(domain.ProfileTest)
	bal, err := g.TRC20Balance(ctx, contract, owner)
	if err != nil {
		t.Fatalf("TRC20Balance: %v", err)
	}
	if _, err := g.TRC20Transfers(ctx, contract, owner, 20); err != nil {
		t.Fatalf("TRC20Transfers: %v", err)
	}
	if bal.Sign() == 0 {
		t.Skip("на записанном адресе нет USDT: Prepare не воспроизводится")
	}
	a, _ := New(g, domain.ProfileTest, WithClock(func() time.Time { return blk.Timestamp }))
	in := domain.SendIntent{Wallet: domain.PublicWallet{ID: "nile", TronAddress: meta.Owner}, Asset: domain.USDT, To: meta.To, Amount: domain.AmountFromUint64(1)}
	p, err := a.Prepare(ctx, in)
	if err != nil {
		t.Fatalf("Prepare on recorded Nile data: %v", err)
	}
	if err := a.Verify(p, in); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// Подмена на настоящем черновике по-прежнему ловится.
	bad := p
	bad.Draft = bytes.Replace(p.Draft, owner[1:], make([]byte, 20), 1)
	if err := a.Verify(bad, in); err == nil {
		t.Error("tampered real draft accepted")
	}
}

// Записанная настоящая отправка (testdata/nile/send.json: raw_data, подпись,
// txid из Nile) должна разбираться и давать тот же txid.
func TestNileSentTransaction(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(nileDir, "send.json"))
	if err != nil {
		t.Skip("нет testdata/nile/send.json: записать результат гейта «отправка USDT в Nile»")
	}
	var s struct {
		RawData string `json:"raw_data_hex"`
		TxID    string `json:"txid"`
		Owner   string `json:"owner"`
		To      string `json:"to"`
		Amount  string `json:"amount"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	raw, err := hexDecode(s.RawData)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseRawTx(raw)
	if err != nil {
		t.Fatalf("real Nile raw_data rejected by strict parser: %v", err)
	}
	if id := TxIDOf(raw); hexString(id[:]) != s.TxID {
		t.Fatalf("txid %x != recorded %s", id, s.TxID)
	}
	if r.Owner.String() != s.Owner {
		t.Error("owner")
	}
	to, amt, err := parseTransferCalldata(r.Data)
	if err != nil || to.String() != s.To || amt.String() != s.Amount {
		t.Errorf("calldata: %v %v %v", to, amt, err)
	}
}
