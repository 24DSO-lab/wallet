package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/secret"
)

func TestRollbackRestoresPreviousProfileAndRetainsFailedRestore(t *testing.T) {
	dir := t.TempDir()
	previous := filepath.Join(dir, "pre-restore-test")
	if err := os.MkdirAll(filepath.Join(previous, "keystore"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(previous, "wallet.db"), []byte("old-db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(previous, "keystore", "wallet.keystore"), []byte("old-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "keystore"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wallet.db"), []byte("new-db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keystore", "wallet.keystore"), []byte("new-key"), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := newService(t, dir, fakeDB{version: testSchema})
	if err := svc.Rollback(previous); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(dir, "wallet.db"):                         "old-db",
		filepath.Join(dir, "keystore", "wallet.keystore"): "old-key",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	foundFailed := false
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "failed-restore-") {
			continue
		}
		foundFailed = true
		got, err := os.ReadFile(filepath.Join(dir, entry.Name(), "wallet.db"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "new-db" {
			t.Fatalf("failed restore data = %q", got)
		}
	}
	if !foundFailed {
		t.Fatal("failed restored files were not retained")
	}
	if _, err := os.Stat(previous); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous directory still exists: %v", err)
	}
}

func TestRestoreVerifiesPasswordBeforeReplacingWorkingData(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "keystore"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wallet.db"), fakeDBBytes(testSchema), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keystore", "existing.json"), []byte(ksMagic+"-existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := tree(t, dir)
	archive := filepath.Join(t.TempDir(), "backup.zip")
	craft(t, archive, goodEntries(t, map[string]string{"restored.json": "-restored"}))
	wrongPassword := errors.New("wrong password")
	svc := newService(t, dir, fakeDB{version: testSchema}, func(c *Config) {
		c.VerifyKeystorePassword = func(context.Context, string, secret.Bytes) error {
			return wrongPassword
		}
	})
	if _, err := svc.Restore(ctx, archive, secret.FromString("incorrect")); !errors.Is(err, wrongPassword) {
		t.Fatalf("Restore error = %v, want wrong password", err)
	}
	sameTree(t, tree(t, dir), before)
}
