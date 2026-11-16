package eth

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
)

const (
	hash1 = "0x1111111111111111111111111111111111111111111111111111111111111111"
	hash2 = "0x2222222222222222222222222222222222222222222222222222222222222222"
)

func txRow(hash, from, to, value, input string) string {
	return `{"blockNumber":"100","timeStamp":"1700000000","hash":"` + hash + `","from":"` + from +
		`","to":"` + to + `","value":"` + value + `","isError":"0","txreceipt_status":"1","input":"` + input +
		`","gasUsed":"21000","gasPrice":"2000000000"}`
}

func envelope(rows ...string) fakeReply {
	return fakeReply{body: `{"status":"1","message":"OK","result":[` + strings.Join(rows, ",") + `]}`}
}

func newTestEtherscan(t *testing.T, replies ...fakeReply) (*Etherscan, *fakeClient) {
	t.Helper()
	fc := &fakeClient{replies: replies}
	e, err := NewEtherscan(fc, ChainIDSepolia, "KEY123")
	if err != nil {
		t.Fatal(err)
	}
	return e, fc
}

func TestNormalTxsRequestAndParsing(t *testing.T) {
	e, fc := newTestEtherscan(t, envelope(txRow(hash1, addrB, addrA, "5000", "0x")))
	txs, err := e.NormalTxs(context.Background(), mustAddr(t, addrA))
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 {
		t.Fatalf("got %d txs", len(txs))
	}
	tx := txs[0]
	if tx.Hash != hash1 || tx.Value.Int64() != 5000 || tx.From != mustAddr(t, addrB) || !tx.HasTo ||
		tx.HasInput || tx.GasUsed != 21000 || tx.GasPrice.Int64() != 2_000_000_000 || tx.Block != 100 || tx.Time.Unix() != 1700000000 {
		t.Errorf("tx = %+v", tx)
	}

	q := fc.reqs[0].Query
	want := map[string]string{
		"chainid": strconv.FormatUint(ChainIDSepolia, 10), "module": "account", "action": "txlist",
		"address": strings.ToLower(addrA), "sort": "desc", "page": "1", "apikey": "KEY123",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("query %s = %q, want %q", k, q.Get(k), v)
		}
	}
	if m := fc.reqs[0].Method; m != "" && m != "GET" {
		t.Errorf("indexer reads must be GET (retryable), got %s", m)
	}
}

func TestNormalTxsKeepsTransferCalldataOnly(t *testing.T) {
	big := "0x" + strings.Repeat("ab", 200)
	e, _ := newTestEtherscan(t, envelope(
		txRow(hash1, addrA, USDCSepoliaContract, "0", "0x"+transferVector),
		txRow(hash2, addrA, USDCSepoliaContract, "0", big),
	))
	txs, err := e.NormalTxs(context.Background(), mustAddr(t, addrA))
	if err != nil {
		t.Fatal(err)
	}
	if !txs[0].HasInput || len(txs[0].Input) != transferCallLen {
		t.Errorf("transfer calldata lost: %+v", txs[0])
	}
	if !txs[1].HasInput || txs[1].Input != nil {
		t.Errorf("large calldata must be flagged but not kept: %+v", txs[1])
	}
}

func TestNoTransactionsIsEmptyNotError(t *testing.T) {
	e, _ := newTestEtherscan(t,
		fakeReply{body: `{"status":"0","message":"No transactions found","result":[]}`},
		fakeReply{body: `{"status":"0","message":"No records found","result":[]}`},
	)
	txs, err := e.NormalTxs(context.Background(), mustAddr(t, addrA))
	if err != nil || len(txs) != 0 {
		t.Errorf("txs %v err %v", txs, err)
	}
	tr, err := e.TokenTransfers(context.Background(), mustAddr(t, addrA), mustAddr(t, USDCSepoliaContract))
	if err != nil || len(tr) != 0 {
		t.Errorf("transfers %v err %v", tr, err)
	}
}

func TestIndexerRateLimitAndErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		code domain.ErrorCode
	}{
		{"rate limit", `{"status":"0","message":"NOTOK","result":"Max calls per sec rate limit reached (3/sec)"}`, domain.CodeProviderRateLimited},
		{"invalid key", `{"status":"0","message":"NOTOK","result":"Invalid API Key"}`, domain.CodeProviderBadResponse},
		{"html", `<html>`, domain.CodeProviderBadResponse},
		{"result not list", `{"status":"1","message":"OK","result":"x"}`, domain.CodeProviderBadResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newTestEtherscan(t, fakeReply{body: tt.body})
			_, err := e.NormalTxs(context.Background(), mustAddr(t, addrA))
			if domain.CodeOf(err) != tt.code {
				t.Errorf("code = %q, want %q (%v)", domain.CodeOf(err), tt.code, err)
			}
			if strings.Contains(err.Error(), "KEY123") {
				t.Error("error leaks the API key")
			}
		})
	}
}

func TestIndexerRejectsMalformedRows(t *testing.T) {
	bad := map[string]string{
		"short hash":       txRow("0x12", addrB, addrA, "1", "0x"),
		"hash not hex":     txRow("0x"+strings.Repeat("zz", 32), addrB, addrA, "1", "0x"),
		"bad from":         txRow(hash1, "0xabc", addrA, "1", "0x"),
		"negative value":   txRow(hash1, addrB, addrA, "-1", "0x"),
		"hex value":        txRow(hash1, addrB, addrA, "0x10", "0x"),
		"huge value":       txRow(hash1, addrB, addrA, strings.Repeat("9", 79), "0x"),
		"bad input":        txRow(hash1, addrB, addrA, "1", "0xzz"),
		"bad checksum":     txRow(hash1, strings.Replace(addrB, "fB69", "FB69", 1), addrA, "1", "0x"),
		"timestamp far":    strings.Replace(txRow(hash1, addrB, addrA, "1", "0x"), "1700000000", "99999999999999", 1),
		"block not number": strings.Replace(txRow(hash1, addrB, addrA, "1", "0x"), `"100"`, `"abc"`, 1),
	}
	for name, row := range bad {
		t.Run(name, func(t *testing.T) {
			e, _ := newTestEtherscan(t, envelope(row))
			_, err := e.NormalTxs(context.Background(), mustAddr(t, addrA))
			if domain.CodeOf(err) != domain.CodeProviderBadResponse {
				t.Errorf("err = %v, want provider_bad_response", err)
			}
		})
	}
}

func TestIndexerPagination(t *testing.T) {
	e, fc := newTestEtherscan(t, envelope(txRow(hash1, addrB, addrA, "1", "0x"), txRow(hash2, addrB, addrA, "2", "0x")), envelope())
	e.pageSize = 2
	txs, err := e.NormalTxs(context.Background(), mustAddr(t, addrA))
	if err != nil || len(txs) != 2 {
		t.Fatalf("txs %d err %v", len(txs), err)
	}
	if len(fc.reqs) != 2 || fc.reqs[1].Query.Get("page") != "2" {
		t.Errorf("a full page must trigger the next one, got %d requests", len(fc.reqs))
	}
}

func TestIndexerPageCap(t *testing.T) {
	full := envelope(txRow(hash1, addrB, addrA, "1", "0x"))
	e, fc := newTestEtherscan(t, full, full, full, full)
	e.pageSize, e.maxPages = 1, 3
	if _, err := e.NormalTxs(context.Background(), mustAddr(t, addrA)); err != nil {
		t.Fatal(err)
	}
	if len(fc.reqs) != 3 {
		t.Errorf("requests = %d, want the page cap 3", len(fc.reqs))
	}
}

func TestTokenTransfersParsing(t *testing.T) {
	row := `{"blockNumber":"101","timeStamp":"1700000100","hash":"` + hash1 + `","from":"` + addrB + `","to":"` + addrA +
		`","value":"2500000","contractAddress":"` + USDCSepoliaContract + `","tokenDecimal":"6"}`
	e, fc := newTestEtherscan(t, envelope(row))
	tr, err := e.TokenTransfers(context.Background(), mustAddr(t, addrA), mustAddr(t, USDCSepoliaContract))
	if err != nil || len(tr) != 1 {
		t.Fatalf("%v %v", tr, err)
	}
	if tr[0].Value.Int64() != 2_500_000 || tr[0].Contract != mustAddr(t, USDCSepoliaContract) || tr[0].To != mustAddr(t, addrA) {
		t.Errorf("transfer = %+v", tr[0])
	}
	if q := fc.reqs[0].Query; q.Get("action") != "tokentx" || q.Get("contractaddress") != strings.ToLower(USDCSepoliaContract) {
		t.Errorf("query = %v", q)
	}
}

func TestNewEtherscanRequiresClient(t *testing.T) {
	if _, err := NewEtherscan(nil, 1, ""); err == nil {
		t.Error("nil client accepted")
	}
	if _, err := New(domain.ProfileTest, nil, nil, Options{}); err == nil || errors.Is(err, nil) {
		t.Error("nil dependencies accepted")
	}
}

func TestParseDecimal(t *testing.T) {
	for _, s := range []string{"0", "1", "000", maxUint256.String()} {
		if _, err := parseDecimal(s); err != nil {
			t.Errorf("parseDecimal(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "-1", "+1", "1.5", "1e3", " 1", "0x1", "٣", strings.Repeat("9", 78)} {
		if _, err := parseDecimal(s); err == nil {
			t.Errorf("parseDecimal(%q) accepted", s)
		}
	}
}

func FuzzParseDecimal(f *testing.F) {
	for _, s := range []string{"0", "123", "-5", "", maxUint256.String()} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := parseDecimal(s)
		if err != nil {
			return
		}
		if n.Sign() < 0 || n.Cmp(maxUint256) > 0 {
			t.Fatalf("parseDecimal(%q) = %s out of range", s, n)
		}
	})
}

func FuzzDecodeEnvelope(f *testing.F) {
	f.Add([]byte(`{"status":"1","message":"OK","result":[]}`))
	f.Add([]byte(`{"status":"0","message":"NOTOK","result":"Max calls per sec"}`))
	f.Add([]byte(`{`))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = decodeEnvelope(body) // не должен паниковать
	})
}
