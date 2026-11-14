package domain

import (
	"errors"
	"fmt"
)

// SendIntent это намерение пользователя отправить средства: что, кому и
// сколько. Адаптеры сверяют с ним черновик транзакции (ChainAdapter.Verify),
// поэтому это единственный источник правды о том, что хотел пользователь.
type SendIntent struct {
	Wallet PublicWallet
	Asset  Asset
	To     string // адрес получателя целиком, как его ввёл пользователь
	Amount Amount // в минимальных единицах Asset
}

// Validate проверяет то, что не зависит от сети: актив кошелька, непустой
// получатель, ненулевая сумма. Формат адреса проверяет валидатор сети актива
// (ChainAdapter.ValidateAddress), здесь его проверить нельзя.
func (in SendIntent) Validate() error {
	if !in.Asset.IsWalletAsset() {
		return NewError(CodeInvalidInput, fmt.Errorf("%w: %q", ErrUnknownAsset, string(in.Asset)))
	}
	if in.Wallet.ID == "" {
		return NewError(CodeInvalidInput, errors.New("wallet is not set"))
	}
	if in.To == "" {
		return NewError(CodeInvalidAddress, errors.New("recipient is empty"))
	}
	if in.Amount.IsZero() {
		return NewError(CodeInvalidAmount, errors.New("amount is zero"))
	}
	return nil
}
