package processlock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
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


// TestProcessLockHelper is the child-process side of the cross-process test.
func TestProcessLockHelper(t *testing.T) {
	path := os.Getenv("WALLET_PROCESS_LOCK_TEST_PATH")
	if path == "" {
		return
	}
	lock, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if _, err := fmt.Fprintln(os.Stdout, "locked"); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestAcquireExcludesAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProcessLockHelper$")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), "WALLET_PROCESS_LOCK_TEST_PATH="+path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	ready, err := reader.ReadString('\n')
	if err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		t.Fatalf("child process did not acquire the lock: %v", err)
	}
	if ready != "locked\n" {
		_ = stdin.Close()
		_ = cmd.Wait()
		t.Fatalf("child process response = %q, want locked", ready)
	}
	second, acquireErr := Acquire(path)
	if second != nil {
		_ = second.Close()
	}
	if !errors.Is(acquireErr, ErrAlreadyRunning) {
		_ = stdin.Close()
		_ = cmd.Wait()
		t.Fatalf("parent Acquire error = %v, want ErrAlreadyRunning", acquireErr)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child process: %v", err)
	}
}
