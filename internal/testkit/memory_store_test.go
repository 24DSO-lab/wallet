package testkit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

func TestMemoryStoreWithTxRollbackAndCommit(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	wallet := domain.Wallet{PublicWallet: domain.PublicWallet{ID: "w1", Name: "before"}}
	if err := store.Wallets().Create(ctx, wallet); err != nil {
		t.Fatal(err)
	}

	rollback := errors.New("rollback")
	err := store.WithTx(ctx, func(ctx context.Context, tx ports.Store) error {
		if err := tx.Wallets().Rename(ctx, "w1", "discarded"); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("WithTx error = %v, want rollback", err)
	}
	got, err := store.Wallets().Get(ctx, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "before" {
		t.Fatalf("rollback leaked name %q", got.Name)
	}

	if err := store.WithTx(ctx, func(ctx context.Context, tx ports.Store) error {
		return tx.Wallets().Rename(ctx, "w1", "committed")
	}); err != nil {
		t.Fatal(err)
	}
	got, err = store.Wallets().Get(ctx, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "committed" {
		t.Fatalf("commit name = %q", got.Name)
	}
}

func TestMemoryStorePendingTxCopiesRawBytes(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	raw := []byte{1, 2, 3}
	p := ports.PendingTx{TxID: "tx1", WalletID: "w1", Asset: domain.BTC, Network: domain.NetworkBitcoin, Raw: raw, CreatedAt: time.Unix(1, 0)}
	if err := store.PendingTxs().Put(ctx, p); err != nil {
		t.Fatal(err)
	}
	raw[0] = 9

	got, err := store.PendingTxs().Get(ctx, "tx1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Raw[0] != 1 {
		t.Fatalf("stored raw bytes aliased input: %v", got.Raw)
	}
	got.Raw[1] = 9
	again, err := store.PendingTxs().Get(ctx, "tx1")
	if err != nil {
		t.Fatal(err)
	}
	if again.Raw[1] != 2 {
		t.Fatalf("returned raw bytes aliased stored data: %v", again.Raw)
	}
}

func TestMemoryStoreSettingsHideSensitiveValues(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.Settings().Set(ctx, "provider.api_key", "private"); err != nil {
		t.Fatal(err)
	}
	if err := store.Settings().Set(ctx, "theme", "dark"); err != nil {
		t.Fatal(err)
	}
	all, err := store.Settings().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := all["provider.api_key"]; ok {
		t.Fatal("sensitive setting leaked through List")
	}
	if all["theme"] != "dark" {
		t.Fatalf("theme = %q", all["theme"])
	}
	value, err := store.Settings().Get(ctx, "provider.api_key")
	if err != nil {
		t.Fatal(err)
	}
	if value != "private" {
		t.Fatal("Get did not return stored value")
	}
}
