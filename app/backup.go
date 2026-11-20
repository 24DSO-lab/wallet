package app

import (
	"errors"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/24DSO-lab/wallet/internal/secret"
)

// RestoreBackup opens a native file picker, restores the selected archive,
// then verifies its keystore password before unlocking the restored profile.
func (a *App) RestoreBackup(password string) (bool, error) {
	if err := a.ready(); err != nil {
		return false, err
	}
	if err := a.requireUnlocked(); err != nil {
		return false, err
	}
	if a.restoreBackup == nil {
		return false, errors.New("restore_not_configured")
	}
	profileAtStart := a.activeProfile()
	path, err := wailsruntime.OpenFileDialog(a.context(), wailsruntime.OpenDialogOptions{
		Title:   "Restore wallet backup",
		Filters: []wailsruntime.FileFilter{{DisplayName: "Wallet backup (*.zip)", Pattern: "*.zip"}},
	})
	if err != nil {
		return false, safeError(err)
	}
	if path == "" {
		return false, nil
	}
	a.profileMu.Lock()
	defer a.profileMu.Unlock()
	if a.activeProfile() != profileAtStart {
		return false, errors.New("profile_changed_during_restore_selection")
	}
	if err := a.requireUnlocked(); err != nil {
		return false, err
	}
	unlockTransaction, err := a.lockForRestore()
	if err != nil {
		return false, err
	}
	defer unlockTransaction()
	a.lockSessionState()
	passwordBytes := secret.FromString(password)
	defer passwordBytes.Wipe()
	restored, restoreErr := a.restoreBackup(a.context(), path, passwordBytes)
	if restored != nil {
		a.mu.Lock()
		a.svc = restored
		a.mu.Unlock()
	}
	if restoreErr != nil {
		return false, safeError(restoreErr)
	}
	if restored == nil {
		return false, errors.New("restore_failed")
	}
	a.mu.Lock()
	a.locked = false
	a.mu.Unlock()
	a.touch()
	return true, nil
}

// lockForRestore acquires the transaction lock and revalidates session state.
// The caller must already hold profileMu exclusively. Lock may run while the
// native file picker is open, so the pre-picker check is not sufficient.
func (a *App) lockForRestore() (func(), error) {
	a.transactionMu.Lock()
	unlock := a.transactionMu.Unlock
	if err := a.ready(); err != nil {
		unlock()
		return nil, err
	}
	if err := a.requireUnlocked(); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

// lockForBackup keeps the session unlocked for the complete backup operation.
// It is acquired only after the native directory picker returns, so Lock never
// waits for a user-controlled dialog.
func (a *App) lockForBackup() (func(), error) {
	a.transactionMu.RLock()
	unlock := a.transactionMu.RUnlock
	if err := a.ready(); err != nil {
		unlock()
		return nil, err
	}
	if err := a.requireUnlocked(); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

func (a *App) CreateBackup(password string) (bool, error) {
	// Keep password verification and backup creation bound to the same profile,
	// even while the native directory picker is open.
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	if err := a.ready(); err != nil {
		return false, err
	}
	if err := a.requireUnlocked(); err != nil {
		return false, err
	}
	if err := a.currentService().VerifyPassword(a.context(), secret.FromString(password)); err != nil {
		return false, safeError(err)
	}
	a.mu.Lock()
	create := a.createBackup
	a.mu.Unlock()
	if create == nil {
		return false, errors.New("backup_unavailable")
	}
	destination, err := wailsruntime.OpenDirectoryDialog(a.context(), wailsruntime.OpenDialogOptions{
		Title: "Choose wallet backup folder",
	})
	if err != nil {
		return false, safeError(err)
	}
	if destination == "" {
		return false, nil
	}
	unlockTransaction, err := a.lockForBackup()
	if err != nil {
		return false, err
	}
	defer unlockTransaction()
	if err := create(a.context(), destination); err != nil {
		return false, safeError(err)
	}
	a.touch()
	return true, nil
}
