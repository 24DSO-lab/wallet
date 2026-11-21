package app

import (
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
)

// ListWallets returns public wallet metadata and cached balances.
func (a *App) ListWallets() ([]WalletDTO, error) {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return nil, err
	}
	if err := a.requireUnlocked(); err != nil {
		return nil, err
	}
	wallets, err := a.currentService().ListWallets(a.context())
	if err != nil {
		return nil, a.fail(err)
	}
	out := make([]WalletDTO, 0, len(wallets))
	for _, w := range wallets {
		balances, err := a.currentService().GetBalances(a.context(), w.ID)
		if err != nil {
			return nil, a.fail(err)
		}
		out = append(out, walletDTO(w, balances))
	}
	return out, nil
}

// ChangePassword re-encrypts all wallet keystore entries with a new password.
func (a *App) ChangePassword(oldPassword, newPassword string) error {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	if err := a.currentService().ChangePassword(a.context(), secret.FromString(oldPassword), secret.FromString(newPassword)); err != nil {
		return a.fail(err)
	}
	a.touch()
	return nil
}

// RevealSeed exposes a wallet mnemonic only for this explicit, password-verified
// request. The returned secret buffer is wiped before crossing the Wails boundary.
func (a *App) RevealSeed(id, password, passphrase string) (string, error) {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return "", err
	}
	if err := a.requireUnlocked(); err != nil {
		return "", err
	}
	mnemonic, err := a.currentService().RevealSeed(a.context(), id, ports.Secrets{
		Password: secret.FromString(password), Passphrase: secret.FromString(passphrase),
	})
	if err != nil {
		return "", a.fail(err)
	}
	defer mnemonic.Wipe()
	a.touch()
	return string(mnemonic.Expose()), nil
}

func (a *App) RenameWallet(id, name string) error {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if err := a.currentService().RenameWallet(a.context(), id, name); err != nil {
		return a.fail(err)
	}
	return nil
}

func (a *App) DeleteWallet(id, confirmedName, password, passphrase string) error {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if err := a.currentService().DeleteWallet(a.context(), id, confirmedName, ports.Secrets{Password: secret.FromString(password), Passphrase: secret.FromString(passphrase)}); err != nil {
		return a.fail(err)
	}
	return nil
}
