package eth

import (
	"context"
	"errors"
	"math/big"

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

	maxFee := new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas), tx.MaxFee)
	balance, err := a.rpc.Balance(ctx, address)
	if err != nil {
		return err
	}
	if balance == nil || balance.Sign() < 0 {
		return domain.NewError(domain.CodeProviderBadResponse, errors.New("eth: live balance response is invalid"))
	}
	isUSDC := in.Asset == domain.USDC
	if isUSDC {
		if balance.Cmp(maxFee) < 0 {
			return domain.NewError(domain.CodeInsufficientFunds, errors.New("ETH balance is below the current maximum transaction fee"))
		}
		tokenData, err := a.rpc.Call(ctx, a.params.usdc, encodeBalanceOf(address))
		if err != nil {
			return err
		}
		tokenBalance, err := decodeWord(tokenData)
		if err != nil {
			return badResponse(err)
		}
		if tokenBalance.Cmp(in.Amount.BigInt()) < 0 {
			return domain.NewError(domain.CodeInsufficientFunds, errors.New("USDC balance changed after transaction preparation"))
		}
	} else {
		required := new(big.Int).Add(in.Amount.BigInt(), maxFee)
		if balance.Cmp(required) < 0 {
			return domain.NewError(domain.CodeInsufficientFunds, errors.New("ETH balance is below the amount plus the current maximum fee"))
		}
	}
	return nil
}
