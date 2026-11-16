package store

import (
	stdctx "context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

func TestWithTxCommit(t *testing.T) {
	d := openTest(t)
	err := d.WithTx(ctx, func(ctx2 stdctx.Context, tx ports.Store) error {
		if err := tx.Wallets().Create(ctx2, domain.Wallet{PublicWallet: domain.PublicWallet{ID: "w", Name: "n"}, KeystoreID: "k", CreatedAt: ms(1)}); err != nil {
			return err
		}
		// Внутри транзакции записанное уже видно, снаружи ещё нет.
		if _, err := tx.Wallets().Get(ctx2, "w"); err != nil {
			return fmt.Errorf("read own write: %w", err)
		}
		if _, err := d.Wallets().Get(ctx2, "w"); !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("uncommitted write is visible outside: %w", err)
		}
		return tx.Settings().Set(ctx2, "k", "v")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Wallets().Get(ctx, "w"); err != nil {
		t.Errorf("committed wallet missing: %v", err)
	}
	if v, _ := d.Settings().Get(ctx, "k"); v != "v" {
		t.Errorf("setting = %q", v)
	}
}

func TestWithTxRollbackOnError(t *testing.T) {
	d := openTest(t)
	boom := errors.New("boom")
	err := d.WithTx(ctx, func(ctx2 stdctx.Context, tx ports.Store) error {
		if err := tx.Wallets().Create(ctx2, domain.Wallet{PublicWallet: domain.PublicWallet{ID: "w"}, KeystoreID: "k"}); err != nil {
			return err
		}
		if err := tx.Settings().Set(ctx2, "k", "v"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := count(t, d, "wallet") + count(t, d, "setting"); n != 0 {
		t.Errorf("rollback left %d rows", n)
	}
	// Хранилище после отката рабочее.
	if err := d.Settings().Set(ctx, "after", "ok"); err != nil {
		t.Errorf("store unusable after rollback: %v", err)
	}
}

func TestWithTxRollbackOnPanic(t *testing.T) {
	d := openTest(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was swallowed")
			}
		}()
		_ = d.WithTx(ctx, func(ctx2 stdctx.Context, tx ports.Store) error {
			_ = tx.Settings().Set(ctx2, "k", "v")
			panic("oops")
		})
	}()
	if n := count(t, d, "setting"); n != 0 {
		t.Errorf("panic left %d rows", n)
	}
	if err := d.Settings().Set(ctx, "after", "ok"); err != nil {
		t.Errorf("store unusable after panic: %v", err)
	}
}

func TestWithTxRollsBackOnConstraintMidway(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	err := d.WithTx(ctx, func(ctx2 stdctx.Context, tx ports.Store) error {
		if err := tx.Balances().Put(ctx2, "w", domain.BTC, domain.Balance{Confirmed: amt(t, "1")}, ms(1)); err != nil {
			return err
		}
		return tx.Balances().Put(ctx2, "ghost", domain.BTC, domain.Balance{}, ms(1)) // нарушает внешний ключ
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := d.Balances().Get(ctx, "w", domain.BTC); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("first write survived the rollback: %v", err)
	}
}

func TestWithTxNestedJoinsOuter(t *testing.T) {
	d := openTest(t)
	boom := errors.New("boom")
	err := d.WithTx(ctx, func(ctx2 stdctx.Context, tx ports.Store) error {
		if err := tx.WithTx(ctx2, func(ctx3 stdctx.Context, inner ports.Store) error {
			return inner.Settings().Set(ctx3, "inner", "1")
		}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if n := count(t, d, "setting"); n != 0 {
		t.Errorf("nested write survived outer rollback: %d rows", n)
	}
}

func TestWithTxCanceledContext(t *testing.T) {
	d := openTest(t)
	cctx, cancel := withCancel()
	cancel()
	if err := d.WithTx(cctx, func(c stdctx.Context, tx ports.Store) error { return tx.Settings().Set(c, "k", "v") }); err == nil {
		t.Fatal("expected error for canceled context")
	}
	if err := d.Settings().Set(ctx, "k", "v"); err != nil {
		t.Errorf("store unusable after canceled tx: %v", err)
	}
}

// Читатели не блокируются писателем и не получают «database is locked».
func TestConcurrentReadsDuringWrites(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)

	const writes = 150
	var (
		wg     sync.WaitGroup
		stop   atomic.Bool
		reads  atomic.Int64
		failed atomic.Value
	)
	fail := func(err error) { failed.CompareAndSwap(nil, err) }

	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if _, err := d.Wallets().List(ctx); err != nil {
					fail(fmt.Errorf("List: %w", err))
					return
				}
				if _, err := d.Transactions().List(ctx, "w", ""); err != nil {
					fail(fmt.Errorf("tx List: %w", err))
					return
				}
				reads.Add(1)
			}
		}()
	}
	for i := range writes {
		tx := sampleTx(t, "w", fmt.Sprintf("tx-%04d", i))
		if err := d.WithTx(ctx, func(c stdctx.Context, s ports.Store) error {
			if err := s.Transactions().Upsert(c, []domain.Tx{tx}); err != nil {
				return err
			}
			return s.Balances().Put(c, "w", domain.BTC, domain.Balance{Confirmed: amt(t, fmt.Sprint(i))}, ms(int64(i)))
		}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if err, _ := failed.Load().(error); err != nil {
		t.Fatal(err)
	}
	if reads.Load() == 0 {
		t.Error("readers made no progress")
	}
	if got, _ := d.Transactions().List(ctx, "w", ""); len(got) != writes {
		t.Errorf("history has %d rows, want %d", len(got), writes)
	}
}

// Параллельные писатели ставятся в очередь, ни одна запись не теряется.
func TestConcurrentWriters(t *testing.T) {
	d := openTest(t)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- d.Settings().Set(ctx, fmt.Sprintf("k%02d", i), "v")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, d, "setting"); n != 40 {
		t.Errorf("settings = %d, want 40", n)
	}
}
