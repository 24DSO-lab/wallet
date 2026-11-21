package logging

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestRotatingFileRotatesAndKeepsBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "wallet.log")
	f, err := OpenRotating(path, 50, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	line := strings.Repeat("x", 30) + "\n" // 31 bytes: two lines do not fit in 50
	for i := 0; i < 5; i++ {
		if _, err := f.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{path, path + ".1", path + ".2"} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Fatal("more backups than configured")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("log permissions = %v, want 0600", info.Mode().Perm())
		}
	}
}

func TestRotatingFileIsSafeForConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.log")
	f, err := OpenRotating(path, 1<<20, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = f.Write([]byte("line\n"))
			}
		}()
	}
	wg.Wait()
	data, _ := os.ReadFile(path)
	if got := strings.Count(string(data), "line\n"); got != 400 {
		t.Fatalf("lines = %d, want 400", got)
	}
}

func TestRotatingFileRejectsBadSettings(t *testing.T) {
	if _, err := OpenRotating(filepath.Join(t.TempDir(), "x.log"), 0, 1); err == nil {
		t.Fatal("expected an error")
	}
}
