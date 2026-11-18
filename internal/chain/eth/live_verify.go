package eth

import (
	"context"
	"errors"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

// VerifyLive rechecks the account's pending nonce at confirmation time.
// A prepared transaction becomes stale if another send from the same account
// was accepted after its quote was created; signing it could replace that send.
func (a *Adapter) VerifyLive(ctx context.Context, p ports.Prepared, in domain.SendIntent) error {
	if err := a.Verify(p, in); err != nil {
		return err
	}
	tx, err := parseUnsigned(p.Draft)
	if err != nil {
		return domain.NewError(domain.CodeVerificationFailed, err)
	}
	address, _, err := parseAddress(in.Wallet.ETHAddress)
	if err != nil {
		return domain.NewError(domain.CodeInvalidInput, err)
	}
	nonce, err := a.rpc.PendingNonce(ctx, address)
	if err != nil {
		return err
	}
	if tx.Nonce != nonce {
		return domain.NewError(domain.CodeDraftExpired, errors.New("account nonce changed after the transaction was prepared"))
	}
	return nil
}
