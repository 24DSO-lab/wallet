package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
)

func TestSnapshotIsConsistentAndOpenable(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	if err := d.Balances().Put(ctx, "w", domain.ETH, domain.Balance{Confirmed: wei(t, 25)}, ms(3)); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "snap.db")
	if err := d.Snapshot(ctx, dst); err != nil {
		t.Fatal(err)
	}
	// Снимок самодостаточный (без -wal) и открывается как обычная база.
	snap, err := Open(ctx, dst)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = snap.Close() }()
	if got, err := snap.Balances().Get(ctx, "w", domain.ETH); err != nil || !got.Balance.Confirmed.Equal(wei(t, 25)) {
		t.Errorf("balance in snapshot: %v, %v", got.Balance.Confirmed, err)
	}
	if v, err := snap.Wallets().Get(ctx, "w"); err != nil || v.ETHAddress != "0xeth-w" {
		t.Errorf("wallet in snapshot: %+v, %v", v, err)
	}
	if v := version(t, snap.w); v != SchemaVersion() {
		t.Errorf("snapshot user_version = %d", v)
	}
}

func TestSnapshotRefusesExistingTarget(t *testing.T) {
	d := openTest(t)
	dst := filepath.Join(t.TempDir(), "snap.db")
	if err := os.WriteFile(dst, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.Snapshot(ctx, dst); !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v, want ErrExist", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "precious" {
		t.Error("existing file was overwritten")
	}
}

func TestSnapshotCanceledLeavesNothing(t *testing.T) {
	d := openTest(t)
	cctx, cancel := withCancel()
	cancel()
	dst := filepath.Join(t.TempDir(), "snap.db")
	if err := d.Snapshot(cctx, dst); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("partial snapshot left behind: %v", err)
	}
}
