package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Rollback restores the pre-restore files retained by Restore after the
// restored profile failed to open. The failed restored files are retained in
// a separate directory so a failed recovery never destroys either copy.
func (s *Service) Rollback(previousDir string) error {
	data := filepath.Clean(s.cfg.DataDir)
	previousDir = filepath.Clean(previousDir)
	if filepath.Dir(previousDir) != data || !strings.HasPrefix(filepath.Base(previousDir), "pre-restore-") {
		return errors.New("backup: invalid previous data directory")
	}
	for _, name := range []string{s.cfg.DBName, s.cfg.KeystoreDirName} {
		if _, err := os.Lstat(filepath.Join(previousDir, name)); err != nil {
			return fmt.Errorf("backup: previous data is incomplete: %w", err)
		}
	}

	failedDir, err := os.MkdirTemp(data, "failed-restore-")
	if err != nil {
		return fmt.Errorf("backup: create failed-restore directory: %w", err)
	}
	type move struct{ from, to string }
	var moved []move
	movePath := func(from, to string) error {
		if err := s.rename(from, to); err != nil {
			return err
		}
		moved = append(moved, move{from: from, to: to})
		return nil
	}
	rollbackMoves := func(cause error) error {
		var rollbackErrs []error
		for i := len(moved) - 1; i >= 0; i-- {
			m := moved[i]
			if err := s.rename(m.to, m.from); err != nil {
				rollbackErrs = append(rollbackErrs, err)
			}
		}
		if len(rollbackErrs) != 0 {
			return errors.Join(append([]error{
				fmt.Errorf("backup: rollback previous profile failed; previous data: %s; failed data: %s: %w", previousDir, failedDir, cause),
			}, rollbackErrs...)...)
		}
		_ = os.Remove(failedDir)
		return fmt.Errorf("backup: rollback previous profile failed; working files were returned to their pre-rollback state: %w", cause)
	}

	for _, name := range []string{s.cfg.DBName, s.cfg.DBName + "-wal", s.cfg.DBName + "-shm", s.cfg.KeystoreDirName} {
		active := filepath.Join(data, name)
		if _, err := os.Lstat(active); err == nil {
			if err := movePath(active, filepath.Join(failedDir, name)); err != nil {
				return rollbackMoves(err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return rollbackMoves(err)
		}
	}
	for _, name := range []string{s.cfg.DBName, s.cfg.DBName + "-wal", s.cfg.DBName + "-shm", s.cfg.KeystoreDirName} {
		previous := filepath.Join(previousDir, name)
		if _, err := os.Lstat(previous); err == nil {
			if err := movePath(previous, filepath.Join(data, name)); err != nil {
				return rollbackMoves(err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return rollbackMoves(err)
		}
	}
	syncDir(data)
	if err := os.Remove(previousDir); err != nil {
		return fmt.Errorf("backup: previous profile restored but could not remove empty recovery directory: %w", err)
	}
	// failedDir deliberately remains as a recovery artifact.
	return nil
}
