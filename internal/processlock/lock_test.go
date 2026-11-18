package processlock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquireExcludesSecondProcessLockAndAllowsReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Acquire error = %v, want ErrAlreadyRunning", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}
