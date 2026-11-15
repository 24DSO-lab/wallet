package store

import (
	"errors"
	"slices"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

func TestWalletCRUD(t *testing.T) {
	d := openTest(t)
	w := mkWallet(t, d, "w1", 0)

	got, err := d.Wallets().Get(ctx, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if got != w {
		t.Fatalf("Get = %+v\nwant %+v", got, w)
	}

	if err := d.Wallets().Rename(ctx, "w1", "Новое имя"); err != nil {
		t.Fatal(err)
	}
	if got, _ = d.Wallets().Get(ctx, "w1"); got.Name != "Новое имя" {
		t.Errorf("name = %q", got.Name)
	}

	dup := w
	if err := d.Wallets().Create(ctx, dup); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Errorf("duplicate Create: code = %q, err %v", domain.CodeOf(err), err)
	}

	if err := d.Wallets().Delete(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Wallets().Get(ctx, "w1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get after Delete: %v", err)
	}
}

func TestWalletNotFound(t *testing.T) {
	d := openTest(t)
	for name, err := range map[string]error{
		"rename": d.Wallets().Rename(ctx, "nope", "x"),
		"delete": d.Wallets().Delete(ctx, "nope"),
	} {
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
}

func TestWalletCreateValidates(t *testing.T) {
	d := openTest(t)
	if err := d.Wallets().Create(ctx, domain.Wallet{KeystoreID: "k"}); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Errorf("empty id: %v", err)
	}
	if err := d.Wallets().Create(ctx, domain.Wallet{PublicWallet: domain.PublicWallet{ID: "x"}}); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Errorf("empty keystore id: %v", err)
	}
}

func TestWalletListOrder(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "c", 2)
	mkWallet(t, d, "a", 0)
	mkWallet(t, d, "b", 1)
	ws, err := d.Wallets().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, w := range ws {
		ids = append(ids, w.ID)
	}
	if !slices.Equal(ids, []string{"a", "b", "c"}) {
		t.Fatalf("order = %v", ids)
	}
	if ws[0].ETHAddress != "0xeth-a" || ws[2].TronAddress != "Ttron-c" {
		t.Errorf("addresses not assembled: %+v", ws)
	}
}

func TestWalletDeleteCascades(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	mkWallet(t, d, "keep", 1)
	for _, id := range []string{"w", "keep"} {
		if err := d.Balances().Put(ctx, id, domain.BTC, domain.Balance{Confirmed: amt(t, "5")}, ms(1)); err != nil {
			t.Fatal(err)
		}
		if err := d.UTXOs().Replace(ctx, id, []domain.UTXO{{TxID: "t", Vout: 0, Amount: amt(t, "1")}}); err != nil {
			t.Fatal(err)
		}
		if err := d.Transactions().Upsert(ctx, []domain.Tx{sampleTx(t, id, "t1")}); err != nil {
			t.Fatal(err)
		}
		if err := d.PendingTxs().Put(ctx, ports.PendingTx{TxID: domain.TxID("p-" + id), WalletID: id, Asset: domain.BTC, Network: domain.NetworkBitcoin, Raw: []byte{1}, CreatedAt: ms(1)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Wallets().Delete(ctx, "w"); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"address", "balance", "utxo", "tx", "pending_tx"} {
		var n int
		if err := d.r.QueryRowContext(ctx, "SELECT count(*) FROM "+tbl+" WHERE wallet_id = 'w'").Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d rows of the deleted wallet remain (err %v)", tbl, n, err)
		}
		if err := d.r.QueryRowContext(ctx, "SELECT count(*) FROM "+tbl+" WHERE wallet_id = 'keep'").Scan(&n); err != nil || n == 0 {
			t.Errorf("%s: rows of the other wallet vanished (err %v)", tbl, err)
		}
	}
}

func TestAddressUpsertIdempotent(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	base := []domain.Address{
		{WalletID: "w", Network: domain.NetworkBitcoin, Address: "bc1a", Path: "m/84'/0'/0'/0/0", Chain: domain.ChainReceive, Index: 0},
		{WalletID: "w", Network: domain.NetworkBitcoin, Address: "bc1b", Path: "m/84'/0'/0'/0/1", Chain: domain.ChainReceive, Index: 1},
		{WalletID: "w", Network: domain.NetworkBitcoin, Address: "bc1c", Path: "m/84'/0'/0'/1/0", Chain: domain.ChainChange, Index: 0},
	}
	for range 3 {
		if err := d.Addresses().Upsert(ctx, base); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.Addresses().List(ctx, "w", domain.NetworkBitcoin)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d addresses after repeated upsert, want 3", len(got))
	}
	// receive до change, внутри цепочки по индексу.
	if got[0].Address != "bc1a" || got[1].Address != "bc1b" || got[2].Address != "bc1c" {
		t.Errorf("order: %+v", got)
	}
}

func TestAddressUsedOnlyGrows(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	a := domain.Address{WalletID: "w", Network: domain.NetworkBitcoin, Address: "bc1a", Path: "p", Chain: domain.ChainReceive, Used: true}
	if err := d.Addresses().Upsert(ctx, []domain.Address{a}); err != nil {
		t.Fatal(err)
	}
	a.Used = false
	if err := d.Addresses().Upsert(ctx, []domain.Address{a}); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Addresses().List(ctx, "w", domain.NetworkBitcoin)
	if len(got) != 1 || !got[0].Used {
		t.Fatalf("used flag was lowered: %+v", got)
	}
}

func TestAddressUnknownWalletAndFilter(t *testing.T) {
	d := openTest(t)
	err := d.Addresses().Upsert(ctx, []domain.Address{{WalletID: "ghost", Network: "n", Address: "a"}})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown wallet: %v", err)
	}
	mkWallet(t, d, "w", 0)
	all, _ := d.Addresses().List(ctx, "w", "")
	eth, _ := d.Addresses().List(ctx, "w", domain.NetworkEthereum)
	if len(all) != 2 || len(eth) != 1 || eth[0].Address != "0xeth-w" || eth[0].Path != pathETH {
		t.Errorf("all=%+v eth=%+v", all, eth)
	}
}

func TestAddressBatchIsAtomic(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	err := d.Addresses().Upsert(ctx, []domain.Address{
		{WalletID: "w", Network: "n", Address: "ok"},
		{WalletID: "w", Network: "", Address: "bad"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got, _ := d.Addresses().List(ctx, "w", "n"); len(got) != 0 {
		t.Errorf("partial batch was saved: %+v", got)
	}
}

func TestSettings(t *testing.T) {
	d := openTest(t)
	s := d.Settings()
	if _, err := s.Get(ctx, "lang"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get missing: %v", err)
	}
	for k, v := range map[string]string{
		"lang": "ru", "lock_timeout": "300",
		"provider.eth_rpc.api_key": "SECRET-1", "provider.tron.apikey": "SECRET-2", "token": "SECRET-3",
	} {
		if err := s.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Set(ctx, "lang", "en"); err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all["lang"] != "en" || all["lock_timeout"] != "300" {
		t.Errorf("List = %v", all)
	}
	for k, v := range all {
		if v == "SECRET-1" || v == "SECRET-2" || v == "SECRET-3" {
			t.Errorf("List leaked sensitive key %q", k)
		}
	}
	if v, err := s.Get(ctx, "provider.eth_rpc.api_key"); err != nil || v != "SECRET-1" {
		t.Errorf("Get sensitive = %q, %v", v, err)
	}
	if err := s.Set(ctx, "", "x"); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Errorf("empty key: %v", err)
	}
}

func TestIsSensitiveSetting(t *testing.T) {
	for k, want := range map[string]bool{
		"lang": false, "lock_timeout": false, "profile": false, "provider.btc.url": false,
		"provider.eth_rpc.api_key": true, "ETH_API_KEY": true, "apikey": true, "api-key": true,
		"auth_token": true, "wallet.passphrase": true,
	} {
		if got := IsSensitiveSetting(k); got != want {
			t.Errorf("IsSensitiveSetting(%q) = %v, want %v", k, got, want)
		}
	}
}
