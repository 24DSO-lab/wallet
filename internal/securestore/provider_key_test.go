package securestore

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProviderKeyStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.key")
	const want = "test-etherscan-key-not-a-real-credential"
	if err := SaveProviderKey(path, want); err != nil {
		if runtime.GOOS != "windows" && errors.Is(err, ErrUnavailable) {
			return
		}
		t.Fatalf("SaveProviderKey: %v", err)
	}
	got, err := LoadProviderKey(path)
	if err != nil {
		t.Fatalf("LoadProviderKey: %v", err)
	}
	if got != want {
		t.Fatalf("LoadProviderKey() = %q, want stored value", got)
	}
	if err := DeleteProviderKey(path); err != nil {
		t.Fatalf("DeleteProviderKey: %v", err)
	}
	got, err = LoadProviderKey(path)
	if err != nil {
		t.Fatalf("LoadProviderKey after delete: %v", err)
	}
	if got != "" {
		t.Fatalf("LoadProviderKey after delete = %q, want empty", got)
	}
}
