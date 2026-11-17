package tron

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/provider"
)

// stubClient отдаёт заранее заданные ответы по пути и запоминает запросы.
type stubClient struct {
	routes map[string]stubReply
	reqs   []provider.Request
}

type stubReply struct {
	body string
	err  error
}

func (s *stubClient) Do(_ context.Context, r provider.Request) (*provider.Response, error) {
	s.reqs = append(s.reqs, r)
	key := r.Path
	if r.Query.Get("only_unconfirmed") == "true" {
		key += "|unconfirmed"
	} else if r.Query.Get("only_confirmed") == "true" {
		key += "|confirmed"
	}
	if cursor := r.Query.Get("fingerprint"); cursor != "" {
		key += "|" + cursor
	}
	rep, ok := s.routes[key]
	if !ok {
		rep, ok = s.routes[r.Path]
	}
	if !ok {
		return nil, domain.NewError(domain.CodeProviderBadResponse, nil)
	}
	if rep.err != nil {
		return nil, rep.err
	}
	return &provider.Response{Status: 200, Body: []byte(rep.body)}, nil
}

func grid(routes map[string]stubReply) (*TronGrid, *stubClient) {
	s := &stubClient{routes: routes}
	g, _ := NewTronGrid(s)
	return g, s
}

var blockID42 = "000000000000002a" + strings.Repeat("ab", 24)

func TestNowBlock(t *testing.T) {
	ok := `{"blockID":"` + blockID42 + `","block_header":{"raw_data":{"number":42,"timestamp":1700000000000}}}`
	g, _ := grid(map[string]stubReply{"/wallet/getnowblock": {body: ok}})
	b, err := g.NowBlock(context.Background())
	if err != nil || b.Number != 42 || b.ID[8] != 0xab || b.Timestamp.UnixMilli() != 1700000000000 {
		t.Fatalf("%+v %v", b, err)
	}
	for name, body := range map[string]string{
		"number mismatch": strings.Replace(ok, `"number":42`, `"number":43`, 1),
		"short id":        strings.Replace(ok, blockID42, "00ab", 1),
		"no timestamp":    strings.Replace(ok, `"timestamp":1700000000000`, `"timestamp":0`, 1),
		"error body":      `{"Error":"boom"}`,
		"not json":        `<html>`,
		"empty object":    `{}`,
	} {
		g, _ := grid(map[string]stubReply{"/wallet/getnowblock": {body: body}})
		if _, err := g.NowBlock(context.Background()); domain.CodeOf(err) != domain.CodeProviderBadResponse {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAccountAndResources(t *testing.T) {
	me, _ := ParseAddress(otherAddr)
	g, s := grid(map[string]stubReply{
		"/v1/accounts/" + otherAddr:  {body: `{"data":[{"balance":123456789}],"success":true}`},
		"/wallet/getaccountresource": {body: `{"freeNetLimit":600,"freeNetUsed":100,"NetLimit":1000,"NetUsed":1500,"EnergyLimit":5000,"EnergyUsed":200}`},
	})
	bal, exists, err := g.Account(context.Background(), me)
	if err != nil || bal != 123456789 || !exists {
		t.Fatalf("%v %v %v", bal, exists, err)
	}
	r, err := g.Resources(context.Background(), me)
	if err != nil || r.FreeBandwidth != 500 || r.StakedBandwidth != 0 || r.Energy != 4800 { // NetUsed > NetLimit не уходит в минус
		t.Fatalf("%+v %v", r, err)
	}
	if !s.reqs[1].Idempotent {
		t.Error("read must be idempotent")
	}
	empty, _ := grid(map[string]stubReply{"/v1/accounts/" + otherAddr: {body: `{"data":[],"success":true}`}})
	if bal, exists, err := empty.Account(context.Background(), me); err != nil || bal != 0 || exists {
		t.Errorf("empty account: %v %v %v", bal, exists, err)
	}
}

func TestConstantCalls(t *testing.T) {
	me, _ := ParseAddress(otherAddr)
	usdt, _ := usdtContract(domain.ProfileMain)
	word := strings.Repeat("0", 56) + "00989680" // 10 000 000
	g, s := grid(map[string]stubReply{"/wallet/triggerconstantcontract": {
		body: `{"result":{"result":true},"energy_used":29631,"constant_result":["` + word + `"]}`}})
	bal, err := g.TRC20Balance(context.Background(), usdt, me)
	if err != nil || bal.Cmp(big.NewInt(10_000_000)) != 0 {
		t.Fatalf("%v %v", bal, err)
	}
	var req map[string]any
	_ = json.Unmarshal(s.reqs[0].Body, &req)
	if req["function_selector"] != "balanceOf(address)" || req["contract_address"] != USDTContractMain ||
		req["parameter"] != strings.Repeat("0", 24)+hex.EncodeToString(me[1:]) {
		t.Errorf("balanceOf request %v", req)
	}
	data, _ := transferCalldata(me, big.NewInt(1))
	e, err := g.EstimateEnergy(context.Background(), me, usdt, data)
	if err != nil || e != 29631 {
		t.Fatalf("%v %v", e, err)
	}
	_ = json.Unmarshal(s.reqs[1].Body, &req)
	if req["function_selector"] != "transfer(address,uint256)" || req["parameter"] != hex.EncodeToString(data[4:]) {
		t.Errorf("estimate request %v", req)
	}
	for name, body := range map[string]string{
		"revert":       `{"result":{"result":false},"energy_used":1}`,
		"no result":    `{"result":{"result":true},"energy_used":1,"constant_result":[]}`,
		"short result": `{"result":{"result":true},"energy_used":1,"constant_result":["00"]}`,
	} {
		g, _ := grid(map[string]stubReply{"/wallet/triggerconstantcontract": {body: body}})
		if _, err := g.TRC20Balance(context.Background(), usdt, me); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	zero, _ := grid(map[string]stubReply{"/wallet/triggerconstantcontract": {body: `{"result":{"result":true},"energy_used":0}`}})
	if _, err := zero.EstimateEnergy(context.Background(), me, usdt, data); err == nil {
		t.Error("zero energy estimate accepted")
	}
	if _, err := g.EstimateEnergy(context.Background(), me, usdt, data[:10]); err == nil {
		t.Error("short calldata accepted")
	}
}

func TestTransfers(t *testing.T) {
	me, _ := ParseAddress(otherAddr)
	usdt, _ := usdtContract(domain.ProfileMain)
	tx := func(id byte, token string) string {
		return `{"transaction_id":"` + hex.EncodeToString(bytes32(id)) + `","token_info":{"address":"` + token + `"},` +
			`"block_timestamp":1700000000000,"from":"` + USDTContractMain + `","to":"` + otherAddr + `","type":"Transfer","value":"5000000"}`
	}
	path := "/v1/accounts/" + otherAddr + "/transactions/trc20"
	g, s := grid(map[string]stubReply{path: {body: `{"data":[` + tx(1, USDTContractMain) + `,` + tx(2, otherAddr) + `],"success":true}`}})
	got, err := g.TRC20Transfers(context.Background(), usdt, me, 500)
	if err != nil {
		t.Fatal(err)
	}
	// Один и тот же ответ на оба запроса (confirmed и all): дубль не удваивается, чужой токен отброшен.
	if len(got) != 1 || !got[0].Confirmed || got[0].Value.String() != "5000000" || got[0].Time.UnixMilli() != 1700000000000 {
		t.Fatalf("%+v", got)
	}
	if s.reqs[0].Query.Get("limit") != "200" || s.reqs[0].Query.Get("contract_address") != USDTContractMain ||
		s.reqs[0].Query.Get("only_confirmed") != "true" || s.reqs[1].Query.Get("only_unconfirmed") != "true" {
		t.Errorf("query %v / %v", s.reqs[0].Query, s.reqs[1].Query)
	}
	for name, body := range map[string]string{
		"bad value":   `{"data":[` + strings.Replace(tx(1, USDTContractMain), `"5000000"`, `"-5"`, 1) + `]}`,
		"float value": `{"data":[` + strings.Replace(tx(1, USDTContractMain), `"5000000"`, `"5.5"`, 1) + `]}`,
		"bad address": `{"data":[` + strings.Replace(tx(1, USDTContractMain), `"to":"`+otherAddr, `"to":"Tbad`, 1) + `]}`,
		"bad txid":    `{"data":[` + strings.Replace(tx(1, USDTContractMain), hex.EncodeToString(bytes32(1)), "zz", 1) + `]}`,
	} {
		g, _ := grid(map[string]stubReply{path: {body: body}})
		if _, err := g.TRC20Transfers(context.Background(), usdt, me, 10); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestTRC20TransfersPaginatesConfirmedAndUnconfirmed(t *testing.T) {
	me, _ := ParseAddress(otherAddr)
	contract, _ := usdtContract(domain.ProfileMain)
	path := "/v1/accounts/" + otherAddr + "/transactions/trc20"
	row := func(id byte) string {
		return `{"transaction_id":"` + hex.EncodeToString(bytes32(id)) + `","token_info":{"address":"` + USDTContractMain + `"},"block_timestamp":1700000000000,"from":"` + USDTContractMain + `","to":"` + otherAddr + `","type":"Transfer","value":"5000000"}`
	}
	g, s := grid(map[string]stubReply{
		path + "|confirmed": {body: `{"data":[` + row(1) + `],"meta":{"fingerprint":"next-confirmed"}}`},
		path + "|confirmed|next-confirmed": {body: `{"data":[` + row(2) + `]}`},
		path + "|unconfirmed": {body: `{"data":[` + row(3) + `]}`},
	})
	got, err := g.TRC20Transfers(context.Background(), contract, me, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || !got[0].Confirmed || !got[1].Confirmed || got[2].Confirmed {
		t.Fatalf("paginated transfers = %+v", got)
	}
	if len(s.reqs) != 3 || s.reqs[1].Query.Get("fingerprint") != "next-confirmed" ||
		s.reqs[2].Query.Get("only_unconfirmed") != "true" {
		t.Fatalf("pagination requests = %+v", s.reqs)
	}
}

func TestTRC20TransfersRejectsPaginationLoopAndTruncation(t *testing.T) {
	me, _ := ParseAddress(otherAddr)
	contract, _ := usdtContract(domain.ProfileMain)
	path := "/v1/accounts/" + otherAddr + "/transactions/trc20"
	row := `{"data":[],"meta":{"fingerprint":"same"}}`
	g, _ := grid(map[string]stubReply{
		path + "|confirmed": {body: row},
		path + "|confirmed|same": {body: row},
	})
	if _, err := g.TRC20Transfers(context.Background(), contract, me, 200); domain.CodeOf(err) != domain.CodeProviderBadResponse {
		t.Fatalf("repeated cursor error = %v", err)
	}

	routes := map[string]stubReply{}
	cursor := ""
	for page := 0; page < maxTransferHistoryPages; page++ {
		key := path + "|confirmed"
		if cursor != "" {
			key += "|" + cursor
		}
		next := "cursor-" + strconv.Itoa(page+1)
		routes[key] = stubReply{body: `{"data":[],"meta":{"fingerprint":"` + next + `"}}`}
		cursor = next
	}
	g, _ = grid(routes)
	if _, err := g.TRC20Transfers(context.Background(), contract, me, 200); domain.CodeOf(err) != domain.CodeProviderBadResponse {
		t.Fatalf("page cap error = %v", err)
	}
}

func TestPricesBroadcastTxInfo(t *testing.T) {
	g, s := grid(map[string]stubReply{
		"/wallet/getchainparameters": {body: `{"chainParameter":[{"key":"getEnergyFee","value":420},{"key":"getTransactionFee","value":1000},{"key":"x","value":1}]}`},
	})
	p, err := g.Prices(context.Background())
	if err != nil || p.EnergyFee != 420 || p.BandwidthFee != 1000 {
		t.Fatalf("%+v %v", p, err)
	}
	for name, body := range map[string]string{
		"missing": `{"chainParameter":[]}`,
		"absurd":  `{"chainParameter":[{"key":"getEnergyFee","value":99999999},{"key":"getTransactionFee","value":1000}]}`,
	} {
		g, _ := grid(map[string]stubReply{"/wallet/getchainparameters": {body: body}})
		if _, err := g.Prices(context.Background()); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	id := hex.EncodeToString(bytes32(7))
	for _, c := range []struct {
		name, body string
		want       BroadcastResult
	}{
		{"ok", `{"result":true,"txid":"` + strings.ToUpper(id) + `"}`, BroadcastResult{OK: true, TxID: id}},
		{"dup", `{"code":"DUP_TRANSACTION_ERROR","message":"6475702074782"}`, BroadcastResult{Duplicate: true, Code: "DUP_TRANSACTION_ERROR"}},
		{"sig", `{"code":"SIGERROR","message":"61626364"}`, BroadcastResult{Code: "SIGERROR"}},
		{"hostile code", `{"code":"<script>alert(1)</script>"}`, BroadcastResult{Code: "UNKNOWN"}},
	} {
		g, s := grid(map[string]stubReply{"/wallet/broadcasthex": {body: c.body}})
		got, err := g.Broadcast(context.Background(), []byte{1, 2, 3})
		if err != nil || got != c.want {
			t.Errorf("%s: %+v %v", c.name, got, err)
		}
		if s.reqs[0].Idempotent {
			t.Errorf("%s: broadcast must not be marked idempotent", c.name)
		}
		if !strings.Contains(string(s.reqs[0].Body), `"transaction":"010203"`) {
			t.Errorf("%s: body %s", c.name, s.reqs[0].Body)
		}
	}
	_ = s

	// gettransactioninfobyid + свежий блок.
	routes := map[string]stubReply{
		"/wallet/gettransactioninfobyid": {body: `{"id":"` + id + `","blockNumber":40,"fee":13000000,"receipt":{"result":"SUCCESS"}}`},
		"/wallet/getnowblock":            {body: `{"blockID":"` + blockID42 + `","block_header":{"raw_data":{"number":42,"timestamp":1700000000000}}}`},
	}
	g2, _ := grid(routes)
	info, err := g2.TxInfo(context.Background(), id)
	if err != nil || !info.Found || !info.Success || info.BlockNumber != 40 || info.FeeSun != 13_000_000 || info.CurrentTip != 42 {
		t.Fatalf("%+v %v", info, err)
	}
	routes["/wallet/gettransactioninfobyid"] = stubReply{body: `{"id":"` + id + `","blockNumber":40,"fee":9000000,"result":"FAILED","receipt":{"result":"OUT_OF_ENERGY"}}`}
	if info, _ := g2.TxInfo(context.Background(), id); !info.Found || info.Success {
		t.Errorf("failed tx reported as success: %+v", info)
	}
	routes["/wallet/gettransactioninfobyid"] = stubReply{body: `{}`}
	if info, err := g2.TxInfo(context.Background(), id); err != nil || info.Found {
		t.Errorf("not found: %+v %v", info, err)
	}
	routes["/wallet/gettransactioninfobyid"] = stubReply{body: `{"id":"` + hex.EncodeToString(bytes32(9)) + `","blockNumber":40}`}
	if _, err := g2.TxInfo(context.Background(), id); err == nil {
		t.Error("info of another transaction accepted")
	}
	if _, err := g2.TxInfo(context.Background(), "nothex"); err == nil {
		t.Error("bad id accepted")
	}
}

// Сквозная проверка с настоящим provider.HTTPClient: 429 превращается в код
// ограничения частоты, а не в «плохой ответ».
func TestRateLimitThroughRealClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c, err := provider.New(provider.Options{Endpoints: []string{srv.URL}, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	g, _ := NewTronGrid(c)
	_, err = g.NowBlock(context.Background())
	if domain.CodeOf(err) != domain.CodeProviderRateLimited {
		t.Fatalf("got %v", err)
	}
	if _, err := NewTronGrid(nil); err == nil {
		t.Error("nil client accepted")
	}
}

func TestForbiddenFrequencyLimitIsRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"Success":false,"Error":"The key exceeds the frequency limit(15), and the query server is suspended for 30s","StatusCode":403}`))
	}))
	defer srv.Close()
	c, _ := provider.New(provider.Options{Endpoints: []string{srv.URL}, MaxAttempts: 1})
	g, _ := NewTronGrid(c)
	me, _ := ParseAddress(otherAddr)
	if _, _, err := g.Account(context.Background(), me); domain.CodeOf(err) != domain.CodeProviderRateLimited {
		t.Fatalf("GET: %v", err)
	}
	if _, err := g.NowBlock(context.Background()); domain.CodeOf(err) != domain.CodeProviderRateLimited {
		t.Fatalf("POST: %v", err)
	}
	// Другой 403 остаётся обычной ошибкой провайдера.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv2.Close()
	c2, _ := provider.New(provider.Options{Endpoints: []string{srv2.URL}, MaxAttempts: 1})
	g2, _ := NewTronGrid(c2)
	if _, err := g2.NowBlock(context.Background()); domain.CodeOf(err) != domain.CodeProviderBadResponse {
		t.Fatalf("plain 403: %v", err)
	}
}
