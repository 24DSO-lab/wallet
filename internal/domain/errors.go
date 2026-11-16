package domain

import (
	"errors"
	"fmt"
)

// ErrNotFound возвращают репозитории и сервисы, когда записи нет. Оборачивайте
// его с контекстом через %w и проверяйте через errors.Is.
var ErrNotFound = errors.New("domain: not found")

// ErrorCode это стабильный код ошибки. Интерфейс показывает код, а не текст
// ошибки, чтобы подробности (адреса провайдеров, ответы серверов) не
// доходили до экрана.
type ErrorCode string

// Коды ошибок. Список только растёт: удаление и переименование это ломающее
// изменение контракта.
const (
	CodeInternal            ErrorCode = "internal"
	CodeCanceled            ErrorCode = "canceled"
	CodeTimeout             ErrorCode = "timeout"
	CodeNotFound            ErrorCode = "not_found"
	CodeLocked              ErrorCode = "locked"
	CodeWrongPassword       ErrorCode = "wrong_password"
	CodeWalletLimit         ErrorCode = "wallet_limit_reached"
	CodeInvalidInput        ErrorCode = "invalid_input"
	CodeInvalidAddress      ErrorCode = "invalid_address"
	CodeInvalidAmount       ErrorCode = "invalid_amount"
	CodeWrongNetwork        ErrorCode = "wrong_network"
	CodeInsufficientFunds   ErrorCode = "insufficient_funds"
	CodeInsufficientFee     ErrorCode = "insufficient_fee_coin"
	CodeVerificationFailed  ErrorCode = "verification_failed" // черновик не совпал с намерением
	CodeSigningRefused      ErrorCode = "signing_refused"     // адрес по пути деривации не совпал с ожидаемым
	CodeDraftExpired        ErrorCode = "draft_expired"
	CodeDuplicateSend       ErrorCode = "duplicate_send"
	CodeProviderUnavailable ErrorCode = "provider_unavailable"
	CodeProviderRateLimited ErrorCode = "provider_rate_limited"
	CodeProviderBadResponse ErrorCode = "provider_bad_response"
	CodeBroadcastRejected   ErrorCode = "broadcast_rejected"
	CodeBroadcastUnknown    ErrorCode = "broadcast_unknown" // подписанная транзакция сохранена, результат отправки требует сверки
	CodeKeystoreCorrupt     ErrorCode = "keystore_corrupt"
	CodeBackupInvalid       ErrorCode = "backup_invalid"
)

// AppError это ошибка с кодом для интерфейса. Cause хранит исходную причину
// для логов и проверок errors.Is и errors.As; текст причины в интерфейс не
// передаётся, только Code.
//
// Создатель ошибки отвечает за то, чтобы в Cause не было секретов.
type AppError struct {
	Code  ErrorCode
	Cause error
}

// NewError создаёт ошибку с кодом и причиной (причина может быть nil).
func NewError(code ErrorCode, cause error) error {
	return &AppError{Code: code, Cause: cause}
}

// Error возвращает код и, если есть, текст причины.
func (e *AppError) Error() string {
	if e.Cause == nil {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

// Unwrap возвращает причину для errors.Is и errors.As.
func (e *AppError) Unwrap() error { return e.Cause }

// Is сопоставляет ошибки по коду: errors.Is(err, &AppError{Code: CodeLocked})
// истинно для любой цепочки с таким кодом, причина в сравнении не участвует.
func (e *AppError) Is(target error) bool {
	t, ok := target.(*AppError)
	return ok && t.Code == e.Code
}

// CodeOf возвращает код ближайшего AppError в цепочке err. Для nil результат
// пустой, для ошибки без AppError это CodeInternal.
func CodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var ae *AppError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return CodeInternal
}

// WarningCode это стабильный код предупреждения на экране отправки. Список
// только растёт.
type WarningCode string

// Коды предупреждений (ADR, раздел «Функции», отправка).
const (
	WarnInsufficientFeeCoin WarningCode = "insufficient_fee_coin" // нет ETH для USDC или TRX для USDT
	WarnInsufficientEnergy  WarningCode = "insufficient_energy"   // TRON: не хватает energy или bandwidth
	WarnDustAmount          WarningCode = "dust_amount"           // BTC: сумма или сдача ниже порога пыли
	WarnNewAddress          WarningCode = "new_address"           // первая отправка на этот адрес
	WarnSelfAddress         WarningCode = "self_address"          // адрес совпадает с собственным
	WarnNoChecksumAddress   WarningCode = "no_checksum_address"   // ETH: адрес в одном регистре, контрольной суммы нет
	WarnHighFee             WarningCode = "high_fee"              // комиссия заметно выше обычной
)

// Warning это предупреждение, которое показывается до подписи. Detail это
// короткое пояснение для логов и подсказки, экран строится по Code.
type Warning struct {
	Code   WarningCode
	Detail string
}
