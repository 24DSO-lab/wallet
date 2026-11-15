package store

import (
	stdctx "context"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
)

var ctx = stdctx.Background()

func openTest(t *testing.T) *DB {
	t.Helper()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "wallet.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func amt(t *testing.T, s string) domain.Amount {
	t.Helper()
	a, err := domain.ParseUnits(s)
	if err != nil {
		t.Fatalf("ParseUnits(%q): %v", s, err)
	}
	return a
}

// wei возвращает 10^n: заведомо больше int64 при n >= 19.
func wei(t *testing.T, n int) domain.Amount {
	t.Helper()
	a, err := domain.NewAmount(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func ms(sec int64) time.Time { return time.UnixMilli(sec * 1000).UTC() }

func mkWallet(t *testing.T, d *DB, id string, order int) domain.Wallet {
	t.Helper()
	w := domain.Wallet{
		PublicWallet: domain.PublicWallet{
			ID: id, Name: "Wallet " + id, BTCXpub: "tpub-" + id,
			ETHAddress: "0xeth-" + id, TronAddress: "Ttron-" + id,
		},
		KeystoreID: "ks-" + id, CreatedAt: ms(1000 + int64(order)), SortOrder: order,
	}
	if err := d.Wallets().Create(ctx, w); err != nil {
		t.Fatalf("Create wallet %s: %v", id, err)
	}
	return w
}

func count(t *testing.T, d *DB, table string) int {
	t.Helper()
	var n int
	// table это константа теста, не ввод.
	if err := d.r.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func withCancel() (stdctx.Context, stdctx.CancelFunc) { return stdctx.WithCancel(ctx) }
