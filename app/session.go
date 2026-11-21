package app

import (
	"errors"
	"time"

	"github.com/24DSO-lab/wallet/internal/secret"
)

// KeepAlive records explicit foreground user activity. Background refreshes
// and polling only call requireUnlocked and therefore cannot postpone locking.
func (a *App) KeepAlive() error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	a.touch()
	return nil
}

// GetSession returns non-secret UI session state.
func (a *App) GetSession() (SessionDTO, error) {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return SessionDTO{}, err
	}
	wallets, err := a.currentService().ListWallets(a.context())
	if err != nil {
		return SessionDTO{}, a.fail(err)
	}
	a.mu.Lock()
	if len(wallets) == 0 {
		a.locked = false // first-run setup has no password-protected keystore yet
	}
	dto := SessionDTO{Locked: a.locked, Profile: string(a.profile), Timeout: int(a.lockTimeout / time.Minute)}
	a.mu.Unlock()
	return dto, nil
}

// Unlock verifies the supplied password; it is never retained by App.
func (a *App) Unlock(password string) error {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return err
	}
	if password == "" {
		return errors.New("invalid_input")
	}
	if err := a.currentService().VerifyPassword(a.context(), secret.FromString(password)); err != nil {
		return a.fail(err)
	}
	a.mu.Lock()
	a.locked = false
	a.mu.Unlock()
	a.touch()
	return nil
}

// Lock immediately blocks operations requiring an unlocked session and drops
// all cached unsigned drafts. It does not alter encrypted keystore files.
func (a *App) Lock() {
	// Wait for sensitive operations to finish, but do not wait for unrelated
	// UI dialogs such as the native backup destination picker.
	a.transactionMu.Lock()
	defer a.transactionMu.Unlock()
	a.lockSessionState()
}

// lockSessionState updates session state while the caller owns profileMu when
// synchronization with transaction preparation/confirmation is required.
func (a *App) lockSessionState() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.locked = true
	if a.lockTimer != nil {
		a.lockTimer.Stop()
		a.lockTimer = nil
	}
	clear(a.drafts)
	a.pendingMnemonic.Wipe()
	a.pendingWalletName = ""
	a.pendingChallengePositions = nil
	a.pendingWalletExpires = time.Time{}
	if a.pendingWalletInFlight {
		a.pendingWalletCancelRequested = true
	}
}
