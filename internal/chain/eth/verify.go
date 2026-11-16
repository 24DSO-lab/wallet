package eth

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

// ErrVerify это корень всех отказов Verify; причина вложена через %w.
var ErrVerify = errors.New("eth: draft does not match the send intent")

func verifyFail(format string, args ...any) error {
	return domain.NewError(domain.CodeVerificationFailed, fmt.Errorf("%w: "+format, append([]any{ErrVerify}, args...)...))
}

// checkFeeLimits применяет потолки газа и комиссии (fees.go).
func checkFeeLimits(tx dynTx, isUSDC bool) error {
	minGas := uint64(gasTransferETH)
	switch {
	case tx.Gas < minGas || tx.Gas > gasCap(isUSDC):
		return fmt.Errorf("%w: gas limit %d is outside [%d, %d]", ErrVerify, tx.Gas, minGas, gasCap(isUSDC))
	case tx.MaxFee.Sign() <= 0 || tx.MaxFee.Cmp(maxFeeCeiling) > 0:
		return fmt.Errorf("%w: maxFeePerGas %s wei is outside (0, %s]", ErrVerify, tx.MaxFee, maxFeeCeiling)
	case tx.MaxPriorityFee.Cmp(tx.MaxFee) > 0:
		return fmt.Errorf("%w: maxPriorityFeePerGas exceeds maxFeePerGas", ErrVerify)
	}
	return nil
}

// Verify разбирает байты черновика сам и сверяет с намерением получателя,
// сумму, сеть, комиссию и отправителя. Поля Prepared, кроме Draft, служат
// только для показа и сверяются с тем, что прочитано из байтов: экран
// подтверждения не должен показывать то, чего нет в подписываемой транзакции.
func (a *Adapter) Verify(p ports.Prepared, in domain.SendIntent) error {
	if err := in.Validate(); err != nil {
		return err
	}
	if in.Asset != domain.ETH && in.Asset != domain.USDC {
		return verifyFail("asset %q is not an Ethereum asset", string(in.Asset))
	}
	if p.Network != domain.NetworkEthereum || p.Asset != in.Asset {
		return verifyFail("prepared network or asset differs from the intent")
	}
	if err := ValidateAddress(in.To); err != nil {
		return err
	}
	to, _, _ := parseAddress(in.To)
	own, _, err := parseAddress(in.Wallet.ETHAddress)
	if err != nil || own == (Address{}) {
		return verifyFail("wallet has no valid Ethereum address")
	}

	tx, err := parseUnsigned(p.Draft)
	if err != nil {
		return verifyFail("%v", err)
	}
	if tx.ChainID != a.params.chainID {
		return verifyFail("chain id %d, profile %q needs %d", tx.ChainID, string(a.params.profile), a.params.chainID)
	}

	isUSDC := in.Asset == domain.USDC
	amount := in.Amount.BigInt()
	if isUSDC {
		if tx.To != a.params.usdc {
			return verifyFail("transaction goes to %s, not to the USDC contract", tx.To)
		}
		if tx.Value.Sign() != 0 {
			return verifyFail("a token transfer must not carry ETH")
		}
		recipient, got, err := decodeTransfer(tx.Data)
		if err != nil {
			return verifyFail("%v", err)
		}
		if recipient != to {
			return verifyFail("token recipient %s differs from the intent", recipient)
		}
		if got.Cmp(amount) != 0 {
			return verifyFail("token amount %s differs from the intent %s", got, amount)
		}
	} else {
		if tx.To != to {
			return verifyFail("recipient %s differs from the intent", tx.To)
		}
		if tx.Value.Cmp(amount) != 0 {
			return verifyFail("value %s differs from the intent %s", tx.Value, amount)
		}
		if len(tx.Data) != 0 {
			return verifyFail("an ETH transfer must not carry calldata")
		}
	}
	if err := checkFeeLimits(tx, isUSDC); err != nil {
		return domain.NewError(domain.CodeVerificationFailed, err)
	}
	maxCost := new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas), tx.MaxFee)

	// Запрос подписи: ровно один, на дайджест этих байтов, от адреса кошелька.
	if len(p.Digests) != 1 {
		return verifyFail("expected one digest request, got %d", len(p.Digests))
	}
	d := p.Digests[0]
	if d.Network != domain.NetworkEthereum || d.Path != derivationPath {
		return verifyFail("signing request has a foreign network or derivation path")
	}
	if signer, _, err := parseAddress(d.ExpectedAddress); err != nil || signer != own {
		return verifyFail("signing request is for an address other than the wallet's")
	}
	if d.Digest != keccak256(p.Draft) {
		return verifyFail("digest does not match the draft")
	}

	// Показанное пользователю совпадает с прочитанным.
	if p.FeeAsset != domain.ETH || p.Fee.BigInt().Cmp(maxCost) != 0 {
		return verifyFail("shown fee differs from gas limit times maxFeePerGas")
	}
	s := p.Summary
	if s.Network != domain.NetworkEthereum || s.Asset != in.Asset || s.FeeAsset != domain.ETH ||
		s.Amount.BigInt().Cmp(amount) != 0 || s.Fee.BigInt().Cmp(maxCost) != 0 {
		return verifyFail("summary differs from the transaction")
	}
	if shown, _, err := parseAddress(s.To); err != nil || shown != to {
		return verifyFail("summary recipient differs from the intent")
	}
	wantTotal := new(big.Int)
	if !isUSDC {
		wantTotal.Add(amount, maxCost)
	}
	if s.Total.BigInt().Cmp(wantTotal) != 0 {
		return verifyFail("summary total differs from amount plus fee")
	}
	return nil
}
