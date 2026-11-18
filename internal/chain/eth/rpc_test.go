package eth

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/provider"
)

// fakeClient это provider.Client с записанными ответами. Каждый запрос
// сохраняется, ответы выдаются по очереди.
type fakeClient struct {
	reqs    []provider.Request
	replies []fakeReply
}

type fakeReply struct {
	body string
	err  error
}

func (f *fakeClient) Do(_ context.Context, req provider.Request) (*provider.Response, error) {
	f.reqs = append(f.reqs, req)
	if len(f.replies) == 0 {
		return nil, errors.New("fakeClient: no reply left")
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	if r.err != nil {
		return nil, r.err
	}
	return &provider.Response{Status: 200, Body: []byte(r.body)}, nil
}

func ok(result string) fakeReply {
	return fakeReply{body: `{"jsonrpc":"2.0","id":1,"result":` + result + `}`}
}

func rpcFail(code int, msg string) fakeReply {
	b, _ := json.Marshal(msg)
	return fakeReply{body: `{"jsonrpc":"2.0","id":1,"error":{"code":` + itoa(code) + `,"message":` + string(b) + `}}`}
}

func itoa(n int) string { return big.NewInt(int64(n)).String() }

func newTestRPC(t *testing.T, replies ...fakeReply) (*JSONRPC, *fakeClient) {
	t.Helper()
	fc := &fakeClient{replies: replies}
	c, err := NewJSONRPC(fc)
	if err != nil {
		t.Fatal(err)
	}
	return c, fc
}

// request разбирает последний записанный запрос.
func (f *fakeClient) last(t *testing.T) (method string, params []json.RawMessage, req provider.Request) {
	t.Helper()
	req = f.reqs[len(f.reqs)-1]
	var body struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	return body.Method, body.Params, req
}

const (
	addrA = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	addrB = "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
)

func mustAddr(t testing.TB, s string) Address {
	t.Helper()
	a, _, err := parseAddress(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestBalanceRequestAndResult(t *testing.T) {
	c, fc := newTestRPC(t, ok(`"0xde0b6b3a7640000"`))
	got, err := c.Balance(context.Background(), mustAddr(t, addrA))
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "1000000000000000000" {
		t.Errorf("balance = %s, want 1 ETH in wei", got)
	}
	method, params, req := fc.last(t)
	if method != "eth_getBalance" || len(params) != 2 {
		t.Fatalf("method %s with %d params", method, len(params))
	}
	if string(params[0]) != `"`+strings.ToLower(addrA)+`"` || string(params[1]) != `"latest"` {
		t.Errorf("params = %s, %s", params[0], params[1])
	}
	if !req.Idempotent {
		t.Error("balance is a read and must allow retries")
	}
}

func TestSendRawTransactionIsNeverRetryable(t *testing.T) {
	const h = "0x" + "11111111111111111111111111111111111111111111111111111111111111aa"
	c, fc := newTestRPC(t, ok(`"`+h+`"`))
	got, err := c.SendRawTransaction(context.Background(), []byte{0x02, 0x01})
	if err != nil || got != h {
		t.Fatalf("hash %q, err %v", got, err)
	}
	method, params, req := fc.last(t)
	if method != "eth_sendRawTransaction" || string(params[0]) != `"0x0201"` {
		t.Errorf("method %s params %s", method, params)
	}
	if req.Idempotent {
		t.Error("sending must not be marked idempotent: a repeat could double-send")
	}
}

func TestSendRawTransactionErrors(t *testing.T) {
	tests := []struct {
		name  string
		reply fakeReply
		code  domain.ErrorCode
		known bool
	}{
		{"already known", rpcFail(-32000, "already known"), "", true},
		{"geth known transaction", rpcFail(-32000, "known transaction: 0xabc"), "", true},
		{"nonce too low", rpcFail(-32000, "nonce too low"), domain.CodeBroadcastRejected, false},
		{"underpriced", rpcFail(-32000, "replacement transaction underpriced"), domain.CodeBroadcastRejected, false},
		{"insufficient funds", rpcFail(-32000, "insufficient funds for gas * price + value"), domain.CodeBroadcastRejected, false},
		{"rate limited by code", rpcFail(-32005, "daily request count exceeded"), domain.CodeProviderRateLimited, false},
		{"rate limited by text", rpcFail(-32000, "Too Many Requests"), domain.CodeProviderRateLimited, false},
		{"provider down", fakeReply{err: domain.NewError(domain.CodeProviderUnavailable, errors.New("boom"))}, domain.CodeProviderUnavailable, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestRPC(t, tt.reply)
			_, err := c.SendRawTransaction(context.Background(), []byte{0x02})
			if err == nil {
				t.Fatal("no error")
			}
			if got := errors.Is(err, ErrAlreadyKnown); got != tt.known {
				t.Errorf("ErrAlreadyKnown = %v, want %v (%v)", got, tt.known, err)
			}
			if !tt.known {
				if code := domain.CodeOf(err); code != tt.code {
					t.Errorf("code = %q, want %q (%v)", code, tt.code, err)
				}
			}
		})
	}
}

func TestReadErrorsMapToCodes(t *testing.T) {
	c, _ := newTestRPC(t, rpcFail(-32005, "rate limit reached"), rpcFail(-32601, "method not found"))
	if _, err := c.BlockNumber(context.Background()); domain.CodeOf(err) != domain.CodeProviderRateLimited {
		t.Errorf("rate limit: %v", err)
	}
	if _, err := c.BlockNumber(context.Background()); domain.CodeOf(err) != domain.CodeProviderBadResponse {
		t.Errorf("method not found: %v", err)
	}
}

func TestMalformedResponses(t *testing.T) {
	tests := []struct {
		name  string
		reply fakeReply
	}{
		{"not json", fakeReply{body: "<html>502</html>"}},
		{"foreign id", fakeReply{body: `{"jsonrpc":"2.0","id":7,"result":"0x1"}`}},
		{"no result", fakeReply{body: `{"jsonrpc":"2.0","id":1}`}},
		{"result is not a string", ok(`{"a":1}`)},
		{"quantity without prefix", ok(`"1"`)},
		{"quantity too long", ok(`"0x` + strings.Repeat("f", 65) + `"`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestRPC(t, tt.reply)
			_, err := c.BlockNumber(context.Background())
			if domain.CodeOf(err) != domain.CodeProviderBadResponse {
				t.Errorf("err = %v, want provider_bad_response", err)
			}
		})
	}
}

func TestBlockNumberRejectsValueAbove64Bits(t *testing.T) {
	c, _ := newTestRPC(t, ok(`"0x`+strings.Repeat("f", 17)+`"`))
	if _, err := c.BlockNumber(context.Background()); domain.CodeOf(err) != domain.CodeProviderBadResponse {
		t.Errorf("err = %v", err)
	}
}

func TestTransactionReceipt(t *testing.T) {
	const hash = "0x1111111111111111111111111111111111111111111111111111111111111111"
	c, _ := newTestRPC(t,
		ok(`null`),
		ok(`{"transactionHash":"`+hash+`","status":"0x1","blockNumber":"0x10","gasUsed":"0x5208"}`),
		ok(`{"transactionHash":"`+hash+`","status":"0x0","blockNumber":"0x11"}`),
		ok(`{"transactionHash":"`+hash+`","blockNumber":"0x11"}`),
		ok(`{"transactionHash":"`+hash+`","status":"0x2","blockNumber":"0x11"}`),
		ok(`{"status":"0x1","blockNumber":"0x11"}`),
		ok(`{"transactionHash":"0x2222222222222222222222222222222222222222222222222222222222222222","status":"0x1","blockNumber":"0x11"}`),
	)
	ctx := context.Background()
	if r, err := c.TransactionReceipt(ctx, hash); r != nil || err != nil {
		t.Fatalf("pending: %v %v", r, err)
	}
	if r, err := c.TransactionReceipt(ctx, hash); err != nil || r.Status != 1 || r.BlockNumber != 16 {
		t.Fatalf("success: %+v %v", r, err)
	}
	if r, err := c.TransactionReceipt(ctx, hash); err != nil || r.Status != 0 || r.BlockNumber != 17 {
		t.Fatalf("reverted: %+v %v", r, err)
	}
	for _, name := range []string{"missing status", "status 2", "missing transaction hash", "foreign transaction hash"} {
		if _, err := c.TransactionReceipt(ctx, hash); domain.CodeOf(err) != domain.CodeProviderBadResponse {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTransactionKnown(t *testing.T) {
	const hash = "0x1111111111111111111111111111111111111111111111111111111111111111"
	c, _ := newTestRPC(t,
		ok(`null`),
		ok(`{"hash":"`+hash+`"}`),
		ok(`{}`),
		ok(`{"hash":"0x2222222222222222222222222222222222222222222222222222222222222222"}`),
		ok(`{"hash":"0xabc"}`),
	)
	if known, err := c.TransactionKnown(context.Background(), hash); known || err != nil {
		t.Errorf("unknown tx: %v %v", known, err)
	}
	if known, err := c.TransactionKnown(context.Background(), hash); !known || err != nil {
		t.Errorf("known tx: %v %v", known, err)
	}
	for _, name := range []string{"missing hash", "foreign hash", "malformed hash"} {
		if known, err := c.TransactionKnown(context.Background(), hash); known || domain.CodeOf(err) != domain.CodeProviderBadResponse {
			t.Errorf("%s: known=%v err=%v", name, known, err)
		}
	}
}

func TestFeeHistory(t *testing.T) {
	c, fc := newTestRPC(t,
		ok(`{"oldestBlock":"0x64","baseFeePerGas":["0x3b9aca00","0x3b9aca01","0x3b9aca02"],"gasUsedRatio":[0.5,0.5],"reward":[["0x3b9aca00"],["0x77359400"]]}`),
		ok(`{"oldestBlock":"0x64","baseFeePerGas":["0x1","0x2"],"reward":[["0x1","0x2"]]}`),
		ok(`{"oldestBlock":"0x64","baseFeePerGas":[],"reward":[]}`),
		ok(`{"oldestBlock":"0x64","baseFeePerGas":["0x1"],"reward":[["0x1"]]}`),
		ok(`{"oldestBlock":"0x64","baseFeePerGas":["0x1","0x2"],"reward":[]}`),
	)
	fh, err := c.FeeHistory(context.Background(), 2, []float64{50})
	if err != nil {
		t.Fatal(err)
	}
	if fh.OldestBlock != 100 || len(fh.BaseFee) != 3 || len(fh.Reward) != 2 || fh.Reward[1][0].Int64() != 2_000_000_000 {
		t.Errorf("fee history = %+v", fh)
	}
	method, params, _ := fc.last(t)
	if method != "eth_feeHistory" || string(params[0]) != `"0x2"` || string(params[1]) != `"latest"` || string(params[2]) != `[50]` {
		t.Errorf("request = %s %s", method, params)
	}
	if _, err := c.FeeHistory(context.Background(), 1, []float64{50}); domain.CodeOf(err) != domain.CodeProviderBadResponse {
		t.Errorf("reward row of the wrong width: %v", err)
	}
	if _, err := c.FeeHistory(context.Background(), 1, []float64{50}); domain.CodeOf(err) != domain.CodeProviderBadResponse {
		t.Errorf("empty baseFeePerGas: %v", err)
	}
	if _, err := c.FeeHistory(context.Background(), 1, []float64{50}); domain.CodeOf(err) != domain.CodeProviderBadResponse {
		t.Errorf("truncated baseFeePerGas: %v", err)
	}
	if _, err := c.FeeHistory(context.Background(), 1, []float64{50}); domain.CodeOf(err) != domain.CodeProviderBadResponse {
		t.Errorf("missing reward row: %v", err)
	}
	if _, err := c.FeeHistory(context.Background(), 0, nil); err == nil {
		t.Error("zero block count accepted")
	}
}

func TestCallAndEstimateGasRequests(t *testing.T) {
	c, fc := newTestRPC(t, ok(`"0x`+strings.Repeat("00", 31)+`2a"`), ok(`"0x"`), ok(`"0x5208"`), ok(`"0xzz"`))
	ctx := context.Background()
	out, err := c.Call(ctx, mustAddr(t, addrA), []byte{0x70, 0xa0, 0x82, 0x31})
	if err != nil || len(out) != 32 || out[31] != 0x2a {
		t.Fatalf("call = %x, %v", out, err)
	}
	_, params, _ := fc.last(t)
	if string(params[0]) != `{"data":"0x70a08231","to":"`+strings.ToLower(addrA)+`"}` {
		t.Errorf("eth_call params = %s", params[0])
	}
	if out, err := c.Call(ctx, mustAddr(t, addrA), nil); err != nil || len(out) != 0 {
		t.Errorf("empty data: %x, %v", out, err)
	}
	gas, err := c.EstimateGas(ctx, CallMsg{From: mustAddr(t, addrA), To: mustAddr(t, addrB), Value: big.NewInt(255)})
	if err != nil || gas != 21000 {
		t.Fatalf("gas = %d, %v", gas, err)
	}
	_, params, _ = fc.last(t)
	if !strings.Contains(string(params[0]), `"value":"0xff"`) || strings.Contains(string(params[0]), `"data"`) {
		t.Errorf("estimateGas params = %s", params[0])
	}
	if _, err := c.EstimateGas(ctx, CallMsg{From: mustAddr(t, addrA), To: mustAddr(t, addrB)}); domain.CodeOf(err) != domain.CodeProviderBadResponse {
		t.Errorf("bad quantity: %v", err)
	}
}

func TestParseQuantity(t *testing.T) {
	good := map[string]string{"0x0": "0", "0x1": "1", "0xff": "255", "0xFF": "255", "0x00ff": "255"}
	for in, want := range good {
		n, err := parseQuantity(in)
		if err != nil || n.String() != want {
			t.Errorf("parseQuantity(%q) = %v, %v; want %s", in, n, err, want)
		}
	}
	max := "0x" + strings.Repeat("f", 64)
	if n, err := parseQuantity(max); err != nil || n.BitLen() != 256 {
		t.Errorf("2^256-1 rejected: %v", err)
	}
	for _, in := range []string{"", "0x", "0", "ff", "0Xff", "0xg1", "0x-1", "0x 1", "0x" + strings.Repeat("f", 65)} {
		if _, err := parseQuantity(in); err == nil {
			t.Errorf("parseQuantity(%q) accepted", in)
		}
	}
	if got := hexQuantity(big.NewInt(0)); got != "0x0" {
		t.Errorf("hexQuantity(0) = %s", got)
	}
}

func TestCleanMessage(t *testing.T) {
	got := cleanMessage("line1\nline2\x00" + strings.Repeat("x", 500))
	if strings.ContainsAny(got, "\n\x00") || len([]rune(got)) != 200 {
		t.Errorf("cleanMessage kept control characters or exceeded the limit: %q", got)
	}
}

func FuzzParseQuantity(f *testing.F) {
	for _, s := range []string{"0x0", "0xff", "", "0x", "0x" + strings.Repeat("f", 64)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := parseQuantity(s)
		if err != nil {
			return
		}
		if n.Sign() < 0 || n.BitLen() > 256 {
			t.Fatalf("parseQuantity(%q) = %s is out of range", s, n)
		}
		if back, err := parseQuantity(hexQuantity(n)); err != nil || back.Cmp(n) != 0 {
			t.Fatalf("round trip of %s failed: %v", n, err)
		}
	})
}
