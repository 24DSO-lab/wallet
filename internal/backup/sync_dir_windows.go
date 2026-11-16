//go:build windows

package backup

// syncDir is intentionally a no-op on Windows: Go does not expose a portable
// directory-handle flush operation. File contents are still flushed before
// publication; directory-entry durability is therefore best-effort on Windows.
func syncDir(string) error { return nil }
