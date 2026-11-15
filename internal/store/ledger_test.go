package store

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

func sampleTx(t *testing.T, wallet, id string) domain.Tx {
	t.Helper()
	return domain.Tx{
		WalletID: wallet, Asset: domain.BTC, TxID: domain.TxID(id), Direction: domain.DirectionIn,
		Amount: amt(t, "1000"), Fee: amt(t, "10"), FeeAsset: domain.BTC, Status: domain.TxPending,
		Time: ms(5000), Counterparty: "bc1x",
	}
}

func TestBalanceBeyondInt64(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	// 10^30 wei = 10^12 ETH, заведомо не помещается в int64; верхняя граница uint256 тоже.
	b := domain.Balance{Confirmed: wei(t, 30), Pending: domain.MaxAmount()}
	if err := d.Balances().Put(ctx, "w", domain.ETH, b, ms(7)); err != nil {
		t.Fatal(err)
	}
	got, err := d.Balances().Get(ctx, "w", domain.ETH)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Balance.Confirmed.Equal(b.Confirmed) || !got.Balance.Pending.Equal(b.Pending) {
		t.Fatalf("balance changed: %v / %v", got.Balance.Confirmed, got.Balance.Pending)
	}
	if !got.UpdatedAt.Equal(ms(7)) {
		t.Errorf("UpdatedAt = %v", got.UpdatedAt)
	}
	// В базе лежит текст, а не число: 10^30 не потерял ни одной цифры.
	var raw string
	if err := d.r.QueryRowContext(ctx, `SELECT confirmed FROM balance`).Scan(&raw); err != nil || raw != wei(t, 30).String() {
		t.Errorf("stored %q, err %v", raw, err)
	}
	var typ string
	if err := d.r.QueryRowContext(ctx, `SELECT typeof(confirmed) FROM balance`).Scan(&typ); err != nil || typ != "text" {
		t.Errorf("typeof = %q, err %v", typ, err)
	}
}

func TestBalanceOverwriteAndMissing(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	if _, err := d.Balances().Get(ctx, "w", domain.BTC); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	for _, v := range []string{"1", "2"} {
		if err := d.Balances().Put(ctx, "w", domain.BTC, domain.Balance{Confirmed: amt(t, v)}, ms(1)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := d.Balances().Get(ctx, "w", domain.BTC)
	if got.Balance.Confirmed.String() != "2" || count(t, d, "balance") != 1 {
		t.Errorf("not overwritten: %v rows=%d", got.Balance.Confirmed, count(t, d, "balance"))
	}
	if err := d.Balances().Put(ctx, "ghost", domain.BTC, domain.Balance{}, ms(1)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown wallet: %v", err)
	}
}

func TestUTXOReplace(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	mkWallet(t, d, "other", 1)
	u := func(tx string, v uint32, a string, spent bool) domain.UTXO {
		return domain.UTXO{TxID: domain.TxID(tx), Vout: v, Address: "bc1" + tx, Amount: amt(t, a), Confirmations: 3, Spent: spent}
	}
	if err := d.UTXOs().Replace(ctx, "other", []domain.UTXO{u("o", 0, "9", false)}); err != nil {
		t.Fatal(err)
	}
	if err := d.UTXOs().Replace(ctx, "w", []domain.UTXO{u("b", 1, "5", false), u("a", 0, "7", true), u("a", 2, "1", false)}); err != nil {
		t.Fatal(err)
	}
	got, err := d.UTXOs().List(ctx, "w")
	if err != nil || len(got) != 3 {
		t.Fatalf("List = %+v, %v", got, err)
	}
	if got[0].TxID != "a" || got[0].Vout != 0 || !got[0].Spent || got[0].WalletID != "w" || got[2].TxID != "b" {
		t.Errorf("order or fields: %+v", got)
	}
	// Новое сканирование заменяет набор целиком, чужой кошелёк не затронут.
	if err := d.UTXOs().Replace(ctx, "w", []domain.UTXO{u("c", 0, "2", false)}); err != nil {
		t.Fatal(err)
	}
	got, _ = d.UTXOs().List(ctx, "w")
	if len(got) != 1 || got[0].TxID != "c" {
		t.Errorf("after replace: %+v", got)
	}
	if o, _ := d.UTXOs().List(ctx, "other"); len(o) != 1 {
		t.Errorf("other wallet changed: %+v", o)
	}
	if err := d.UTXOs().Replace(ctx, "w", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = d.UTXOs().List(ctx, "w"); len(got) != 0 {
		t.Errorf("empty replace left %+v", got)
	}
}

func TestUTXOValidation(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	if err := d.UTXOs().Replace(ctx, "ghost", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown wallet: %v", err)
	}
	if err := d.UTXOs().Replace(ctx, "w", []domain.UTXO{{WalletID: "x", TxID: "t"}}); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Errorf("foreign wallet id: %v", err)
	}
	// Ошибка во входе не должна стереть прежний набор.
	if err := d.UTXOs().Replace(ctx, "w", []domain.UTXO{{TxID: "t", Amount: amt(t, "1")}}); err != nil {
		t.Fatal(err)
	}
	if err := d.UTXOs().Replace(ctx, "w", []domain.UTXO{{TxID: ""}}); err == nil {
		t.Fatal("expected error")
	}
	if got, _ := d.UTXOs().List(ctx, "w"); len(got) != 1 {
		t.Errorf("failed Replace changed data: %+v", got)
	}
}

func TestTransactionUpsertIdempotentAndUpdates(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	tx := sampleTx(t, "w", "abc")
	for range 3 {
		if err := d.Transactions().Upsert(ctx, []domain.Tx{tx}); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, d, "tx"); n != 1 {
		t.Fatalf("rows = %d after repeated upsert", n)
	}
	first, _ := d.Transactions().Get(ctx, "w", domain.BTC, "abc")
	if first.ID == "" {
		t.Fatal("Store must assign an ID")
	}

	tx.Status, tx.BlockHeight, tx.Time = domain.TxConfirmed, 850_000, ms(6000)
	if err := d.Transactions().Upsert(ctx, []domain.Tx{tx}); err != nil {
		t.Fatal(err)
	}
	second, _ := d.Transactions().Get(ctx, "w", domain.BTC, "abc")
	if second.ID != first.ID {
		t.Errorf("ID changed on update: %q -> %q", first.ID, second.ID)
	}
	if second.Status != domain.TxConfirmed || second.BlockHeight != 850_000 || !second.Time.Equal(ms(6000)) {
		t.Errorf("not updated: %+v", second)
	}
	// Тот же txid в другом активе или кошельке это другая запись.
	other := tx
	other.Asset = domain.ETH
	if err := d.Transactions().Upsert(ctx, []domain.Tx{other}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, d, "tx"); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
}

func TestTransactionAmountsBeyondInt64(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	tx := sampleTx(t, "w", "big")
	tx.Asset, tx.FeeAsset = domain.ETH, domain.ETH
	tx.Amount, tx.Fee = domain.MaxAmount(), wei(t, 19)
	if err := d.Transactions().Upsert(ctx, []domain.Tx{tx}); err != nil {
		t.Fatal(err)
	}
	got, err := d.Transactions().Get(ctx, "w", domain.ETH, "big")
	if err != nil || !got.Amount.Equal(tx.Amount) || !got.Fee.Equal(tx.Fee) {
		t.Fatalf("amount/fee changed: %v %v %v", got.Amount, got.Fee, err)
	}
}

func TestTransactionListOrderAndFilter(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	mkWallet(t, d, "o", 1)
	mk := func(w string, a domain.Asset, id string, sec int64) domain.Tx {
		x := sampleTx(t, w, id)
		x.Asset, x.Time = a, ms(sec)
		return x
	}
	if err := d.Transactions().Upsert(ctx, []domain.Tx{
		mk("w", domain.BTC, "old", 100), mk("w", domain.ETH, "mid", 200), mk("w", domain.BTC, "new", 300), mk("o", domain.BTC, "foreign", 400),
	}); err != nil {
		t.Fatal(err)
	}
	all, _ := d.Transactions().List(ctx, "w", "")
	btc, _ := d.Transactions().List(ctx, "w", domain.BTC)
	ids := func(ts []domain.Tx) (s []string) {
		for _, x := range ts {
			s = append(s, string(x.TxID))
		}
		return
	}
	if got := ids(all); len(got) != 3 || got[0] != "new" || got[1] != "mid" || got[2] != "old" {
		t.Errorf("all = %v", got)
	}
	if got := ids(btc); len(got) != 2 || got[0] != "new" || got[1] != "old" {
		t.Errorf("btc = %v", got)
	}
	if _, err := d.Transactions().Get(ctx, "w", domain.BTC, "foreign"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("other wallet's tx visible: %v", err)
	}
}

func TestTransactionValidation(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	good := sampleTx(t, "w", "ok")
	mut := func(f func(*domain.Tx)) domain.Tx { x := good; f(&x); return x }
	for name, tx := range map[string]domain.Tx{
		"no wallet":   mut(func(x *domain.Tx) { x.WalletID = "" }),
		"no asset":    mut(func(x *domain.Tx) { x.Asset = "" }),
		"no txid":     mut(func(x *domain.Tx) { x.TxID = "" }),
		"bad status":  mut(func(x *domain.Tx) { x.Status = "weird" }),
		"bad dir":     mut(func(x *domain.Tx) { x.Direction = "sideways" }),
		"huge height": mut(func(x *domain.Tx) { x.BlockHeight = math.MaxUint64 }),
	} {
		if err := d.Transactions().Upsert(ctx, []domain.Tx{good, tx}); domain.CodeOf(err) != domain.CodeInvalidInput {
			t.Errorf("%s: code = %q, err %v", name, domain.CodeOf(err), err)
		}
	}
	if n := count(t, d, "tx"); n != 0 {
		t.Errorf("invalid batch saved %d rows", n)
	}
	if err := d.Transactions().Upsert(ctx, []domain.Tx{sampleTx(t, "ghost", "x")}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown wallet: %v", err)
	}
}

func TestPendingTx(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	p := ports.PendingTx{TxID: "tx1", WalletID: "w", Asset: domain.USDT, Network: domain.NetworkTron, Raw: []byte{0, 1, 2, 255}, CreatedAt: ms(10)}
	if err := d.PendingTxs().Put(ctx, p); err != nil {
		t.Fatal(err)
	}
	// Повторный Put идемпотентен и не двигает исходное время.
	p2 := p
	p2.CreatedAt = ms(99)
	if err := d.PendingTxs().Put(ctx, p2); err != nil {
		t.Fatal(err)
	}
	got, err := d.PendingTxs().Get(ctx, "tx1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Raw) != string(p.Raw) || got.Network != p.Network || got.Asset != p.Asset || !got.CreatedAt.Equal(ms(10)) {
		t.Errorf("Get = %+v", got)
	}
	if n := count(t, d, "pending_tx"); n != 1 {
		t.Errorf("rows = %d", n)
	}
	second := p
	second.TxID, second.CreatedAt = "tx0", ms(5)
	if err := d.PendingTxs().Put(ctx, second); err != nil {
		t.Fatal(err)
	}
	list, _ := d.PendingTxs().List(ctx)
	if len(list) != 2 || list[0].TxID != "tx0" {
		t.Errorf("List order: %+v", list)
	}
	if err := d.PendingTxs().Delete(ctx, "tx1"); err != nil {
		t.Fatal(err)
	}
	if err := d.PendingTxs().Delete(ctx, "tx1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second Delete: %v", err)
	}
	if _, err := d.PendingTxs().Get(ctx, "tx1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get deleted: %v", err)
	}
	bad := p
	bad.Raw = nil
	if err := d.PendingTxs().Put(ctx, bad); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Errorf("empty raw: %v", err)
	}
	ghost := p
	ghost.WalletID = "ghost"
	if err := d.PendingTxs().Put(ctx, ghost); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown wallet: %v", err)
	}
}

func TestTimeRoundTripIsUTCMillis(t *testing.T) {
	d := openTest(t)
	w := mkWallet(t, d, "w", 0)
	in := time.Date(2026, 11, 15, 12, 30, 45, 123_456_789, time.FixedZone("x", 3*3600))
	if err := d.Balances().Put(ctx, w.ID, domain.BTC, domain.Balance{}, in); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Balances().Get(ctx, w.ID, domain.BTC)
	if want := in.UTC().Truncate(time.Millisecond); !got.UpdatedAt.Equal(want) || got.UpdatedAt.Location() != time.UTC {
		t.Errorf("UpdatedAt = %v, want %v UTC", got.UpdatedAt, want)
	}
}
