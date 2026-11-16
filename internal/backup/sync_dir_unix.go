//go:build !windows

package backup

import (
	"fmt"
	"os"
)

// syncDir flushes directory metadata so successful renames survive a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // directory is selected by the backup service
	if err != nil {
		return fmt.Errorf("open directory: %w", err)
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return fmt.Errorf("sync directory: %w", err)
	}
	if err := d.Close(); err != nil {
		return fmt.Errorf("close directory: %w", err)
	}
	return nil
}
