package eth

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

// Prepare собирает неподписанную транзакцию типа 2 без ключей. Draft это байты
// 0x02 || rlp(...), дайджест это Keccak-256 от них.
//
// Показанная комиссия это худший случай, газ на maxFeePerGas: узел спишет
// меньше (реальную цену газа и использованный газ), но проверка баланса и
// потолки работают по максимуму.
func (a *Adapter) Prepare(ctx context.Context, in domain.SendIntent) (ports.Prepared, error) {
	if err := in.Validate(); err != nil {
		return ports.Prepared{}, err
	}
	if in.Asset != domain.ETH && in.Asset != domain.USDC {
		return ports.Prepared{}, domain.NewError(domain.CodeInvalidInput, fmt.Errorf("eth: unsupported asset %q", string(in.Asset)))
	}
	if err := ValidateAddress(in.To); err != nil {
		return ports.Prepared{}, err
	}
	to, _, _ := parseAddress(in.To)
	own, _, err := parseAddress(in.Wallet.ETHAddress)
	if err != nil || own == (Address{}) {
		return ports.Prepared{}, domain.NewError(domain.CodeInvalidInput, errors.New("eth: wallet has no valid Ethereum address"))
	}
	if err := a.ensureChain(ctx); err != nil {
		return ports.Prepared{}, err
	}
	isUSDC := in.Asset == domain.USDC
	amount := in.Amount.BigInt()

	// Цель вызова, значение и calldata зависят от актива.
	tx := dynTx{ChainID: a.params.chainID, To: to, Value: amount}
	if isUSDC {
		data, err := encodeTransfer(to, amount)
		if err != nil {
			return ports.Prepared{}, domain.NewError(domain.CodeInvalidAmount, err)
		}
		tx.To, tx.Value, tx.Data = a.params.usdc, new(big.Int), data
	}

	// Баланс токена проверяется до оценки газа: перевод больше баланса на
	// узле откатывается, и оценка упала бы с невнятной ошибкой провайдера.
	if isUSDC {
		raw, err := a.rpc.Call(ctx, a.params.usdc, encodeBalanceOf(own))
		if err != nil {
			return ports.Prepared{}, err
		}
		have, err := decodeWord(raw)
		if err != nil {
			return ports.Prepared{}, badResponse(err)
		}
		if have.Cmp(amount) < 0 {
			return ports.Prepared{}, domain.NewError(domain.CodeInsufficientFunds, errors.New("eth: USDC balance is below the amount"))
		}
	}

	if tx.Nonce, err = a.rpc.PendingNonce(ctx, own); err != nil {
		return ports.Prepared{}, err
	}
	quote, err := quoteFees(ctx, a.rpc)
	if err != nil {
		return ports.Prepared{}, err
	}
	estimate, err := a.rpc.EstimateGas(ctx, CallMsg{From: own, To: tx.To, Value: tx.Value, Data: tx.Data})
	if err != nil {
		return ports.Prepared{}, err
	}
	tx.Gas = paddedGas(estimate, isUSDC)
	tx.MaxPriorityFee, tx.MaxFee = quote.maxPriority, quote.maxFee

	// Потолки применяются уже здесь: черновик, который не пройдёт Verify,
	// пользователю не показывается.
	if err := checkFeeLimits(tx, isUSDC); err != nil {
		return ports.Prepared{}, domain.NewError(domain.CodeVerificationFailed, err)
	}

	maxCost := new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas), tx.MaxFee)
	ethBalance, err := a.rpc.Balance(ctx, own)
	if err != nil {
		return ports.Prepared{}, err
	}
	warnings := addressWarnings(in.To)
	if to == own {
		warnings = append(warnings, domain.Warning{Code: domain.WarnSelfAddress, Detail: "recipient is your own address"})
	}
	if isUSDC {
		if ethBalance.Cmp(maxCost) < 0 {
			warnings = append(warnings, domain.Warning{Code: domain.WarnInsufficientFeeCoin, Detail: "not enough ETH to pay for gas"})
		}
	} else if ethBalance.Cmp(new(big.Int).Add(amount, maxCost)) < 0 {
		return ports.Prepared{}, domain.NewError(domain.CodeInsufficientFunds, errors.New("eth: balance is below the amount plus the maximum fee"))
	}
	if quote.nextBase.Cmp(highBaseFee) > 0 {
		warnings = append(warnings, domain.Warning{Code: domain.WarnHighFee, Detail: "network base fee is unusually high"})
	}

	fee, err := domain.NewAmount(maxCost)
	if err != nil {
		return ports.Prepared{}, domain.NewError(domain.CodeInternal, err)
	}
	sum := ports.Summary{
		Network: domain.NetworkEthereum, Asset: in.Asset, To: to.String(), Amount: in.Amount,
		Fee: fee, FeeAsset: domain.ETH,
	}
	if !isUSDC {
		if sum.Total, err = in.Amount.Add(fee); err != nil {
			return ports.Prepared{}, domain.NewError(domain.CodeInvalidAmount, err)
		}
	}

	draft := tx.unsigned()
	return ports.Prepared{
		Network: domain.NetworkEthereum,
		Asset:   in.Asset,
		Draft:   draft,
		Digests: []ports.DigestRequest{{
			Network:         domain.NetworkEthereum,
			Path:            derivationPath,
			ExpectedAddress: own.String(),
			Digest:          keccak256(draft),
		}},
		Fee:      fee,
		FeeAsset: domain.ETH,
		Summary:  sum,
		Warnings: warnings,
	}, nil
}
