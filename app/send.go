package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
)

func (a *App) PrepareSend(walletID, asset, recipient, amount string) (QuoteDTO, error) {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return QuoteDTO{}, err
	}
	if err := a.requireUnlocked(); err != nil {
		return QuoteDTO{}, err
	}
	da, err := parseAsset(asset)
	if err != nil {
		return QuoteDTO{}, err
	}
	info, _ := da.Info()
	units, err := domain.ParseDecimal(amount, info.Decimals)
	if err != nil {
		return QuoteDTO{}, a.fail(err)
	}
	intent := domain.SendIntent{Wallet: domain.PublicWallet{ID: walletID}, Asset: da, To: recipient, Amount: units}
	prepared, err := a.currentService().PrepareSend(a.context(), intent)
	if err != nil {
		return QuoteDTO{}, a.fail(err)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return QuoteDTO{}, errors.New("internal")
	}
	id := hex.EncodeToString(raw[:])
	a.mu.Lock()
	a.drafts[id] = pendingDraft{walletID: walletID, intent: intent, prepared: prepared, expires: time.Now().Add(10 * time.Minute)}
	a.mu.Unlock()
	return QuoteDTO{DraftID: id, Asset: asset, Network: string(prepared.Network), Recipient: prepared.Summary.To, Amount: prepared.Summary.Amount.FormatDecimal(info.Decimals), Fee: prepared.Fee.FormatDecimal(feeDecimals(prepared.FeeAsset, info.Decimals)), FeeAsset: string(prepared.FeeAsset), Total: prepared.Summary.Total.FormatDecimal(info.Decimals), Warnings: warningCodes(prepared.Warnings)}, nil
}

// PrepareRBF creates a fee-bump quote for an accepted, unconfirmed BTC send.
func (a *App) PrepareRBF(walletID, txid string) (QuoteDTO, error) {
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()
	a.transactionMu.RLock()
	defer a.transactionMu.RUnlock()
	if err := a.ready(); err != nil {
		return QuoteDTO{}, err
	}
	if err := a.requireUnlocked(); err != nil {
		return QuoteDTO{}, err
	}
	replacement, err := a.currentService().PrepareRBF(a.context(), walletID, domain.TxID(txid))
	if err != nil {
		return QuoteDTO{}, a.fail(err)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return QuoteDTO{}, errors.New("internal")
	}
	id := hex.EncodeToString(raw[:])
	a.mu.Lock()
	a.drafts[id] = pendingDraft{walletID: walletID, intent: replacement.Intent, prepared: replacement.Prepared, replacement: &replacement, expires: time.Now().Add(10 * time.Minute)}
	a.mu.Unlock()
	return QuoteDTO{DraftID: id, Asset: string(domain.BTC), Network: string(domain.NetworkBitcoin), Recipient: replacement.Prepared.Summary.To, Amount: replacement.Prepared.Summary.Amount.FormatDecimal(8), Fee: replacement.Prepared.Fee.FormatDecimal(8), FeeAsset: string(domain.BTC), Total: replacement.Prepared.Summary.Total.FormatDecimal(8), Warnings: warningCodes(replacement.Prepared.Warnings), ReplacesTxID: txid}, nil
}

func (a *App) ConfirmSend(draftID, password, passphrase string) (string, error) {
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
	a.mu.Lock()
	draft, ok := a.drafts[draftID]
	if ok && (time.Now().After(draft.expires) || draft.inFlight) {
		if time.Now().After(draft.expires) {
			delete(a.drafts, draftID)
		}
		ok = false
	} else if ok {
		// Single-flight: a second concurrent confirmation must not obtain the
		// same draft while signing is in progress.
		draft.inFlight = true
		a.drafts[draftID] = draft
	}
	a.mu.Unlock()
	if !ok {
		return "", errors.New("draft_expired_or_in_use")
	}
	var txid domain.TxID
	var err error
	secrets := ports.Secrets{Password: secret.FromString(password), Passphrase: secret.FromString(passphrase)}
	if draft.replacement != nil {
		txid, err = a.currentService().ConfirmRBF(a.context(), draft.walletID, *draft.replacement, secrets)
	} else {
		txid, err = a.currentService().ConfirmSend(a.context(), draft.walletID, draft.intent, draft.prepared, secrets)
	}
	a.mu.Lock()
	if err != nil && txid == "" && domain.CodeOf(err) != domain.CodeDuplicateSend {
		// Failures before persistence are safe to retry, except when another
		// unresolved transaction makes this draft stale.
		if current, exists := a.drafts[draftID]; exists {
			current.inFlight = false
			a.drafts[draftID] = current
		}
	} else {
		// A non-empty txid means the signed payload is durable in pending_tx.
		// Never reuse this draft: reconciliation, not another confirmation,
		// determines whether the network accepted the transaction.
		delete(a.drafts, draftID)
	}
	a.mu.Unlock()
	if err != nil {
		return "", a.fail(err)
	}
	return string(txid), nil
}
