package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Ошибки разбора и арифметики Amount. Тексты не содержат введённых значений.
var (
	ErrAmountEmpty     = errors.New("domain: amount is empty")
	ErrAmountSyntax    = errors.New("domain: amount has invalid syntax")
	ErrAmountNegative  = errors.New("domain: amount is negative")
	ErrAmountPrecision = errors.New("domain: amount has more fractional digits than the asset allows")
	ErrAmountOverflow  = errors.New("domain: amount exceeds the maximum")
	ErrAmountDecimals  = errors.New("domain: decimals out of range")
)

const (
	// maxDecimals это наибольшее число знаков, для которого 10^decimals
	// помещается в Amount (2^256 ≈ 1,16·10^77).
	maxDecimals = 77

	// maxInputLen ограничивает длину разбираемой строки, чтобы ввод не
	// заставлял выделять память. Наибольшее число занимает 78 цифр.
	maxInputLen = 160
)

// Amount это неотрицательная сумма в минимальных единицах актива (сатоши,
// wei, минимальная доля токена).
//
// Правила:
//   - значение целое, неотрицательное и не больше 2^256-1: это предел uint256,
//     в который помещаются суммы ERC-20 и TRC-20;
//   - нулевое значение Amount{} это 0;
//   - в БД и в JSON сумма это строка десятичных цифр в минимальных единицах,
//     JSON-число принимается только как ошибка;
//   - значение неизменяемо: методы возвращают новую сумму.
//
// Перевод в человекочитаемый вид (ParseDecimal, FormatDecimal) выполняется
// только на границе интерфейса.
type Amount struct {
	v big.Int
}

// maxValue возвращает 2^256-1.
func maxValue() *big.Int {
	m := new(big.Int).Lsh(big.NewInt(1), 256)
	return m.Sub(m, big.NewInt(1))
}

// MaxAmount возвращает наибольшую допустимую сумму, 2^256-1.
func MaxAmount() Amount {
	return Amount{v: *maxValue()}
}

// NewAmount создаёт сумму из big.Int, копируя значение.
func NewAmount(v *big.Int) (Amount, error) {
	if v == nil {
		return Amount{}, ErrAmountEmpty
	}
	if v.Sign() < 0 {
		return Amount{}, ErrAmountNegative
	}
	if v.Cmp(maxValue()) > 0 {
		return Amount{}, ErrAmountOverflow
	}
	return Amount{v: *new(big.Int).Set(v)}, nil
}

// AmountFromUint64 создаёт сумму из uint64.
func AmountFromUint64(u uint64) Amount {
	return Amount{v: *new(big.Int).SetUint64(u)}
}

// isDigits сообщает, что s непусто и состоит только из ASCII-цифр. Unicode-цифры
// и знаки не проходят намеренно.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// fromDigits разбирает строку ASCII-цифр и проверяет верхнюю границу.
func fromDigits(digits string) (Amount, error) {
	v, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return Amount{}, ErrAmountSyntax
	}
	if v.Cmp(maxValue()) > 0 {
		return Amount{}, ErrAmountOverflow
	}
	return Amount{v: *v}, nil
}

// ParseUnits разбирает сумму в минимальных единицах: каноническую строку цифр
// без знака, пробелов и ведущих нулей (кроме самого "0"). Это формат БД и
// JSON, он однозначен: String возвращает ровно то, что принял ParseUnits.
func ParseUnits(s string) (Amount, error) {
	switch {
	case s == "":
		return Amount{}, ErrAmountEmpty
	case s[0] == '-':
		return Amount{}, ErrAmountNegative
	case len(s) > maxInputLen, !isDigits(s), len(s) > 1 && s[0] == '0':
		return Amount{}, ErrAmountSyntax
	}
	return fromDigits(s)
}

// ParseDecimal разбирает сумму, введённую человеком ("1.5"), в минимальные
// единицы актива с заданным числом знаков. Правила строгие, ничто не
// округляется и не угадывается:
//   - только ASCII-цифры и одна точка, без знаков "+", пробелов, запятых,
//     экспоненты и шестнадцатеричной записи; перед точкой и после неё
//     должна быть хотя бы одна цифра;
//   - знаков после точки не больше decimals, иначе ErrAmountPrecision,
//     даже если лишние цифры нули;
//   - ведущие нули допускаются ("007.5"), результат от них не зависит;
//   - результат не больше 2^256-1.
func ParseDecimal(s string, decimals uint8) (Amount, error) {
	if decimals > maxDecimals {
		return Amount{}, ErrAmountDecimals
	}
	switch {
	case s == "":
		return Amount{}, ErrAmountEmpty
	case s[0] == '-':
		return Amount{}, ErrAmountNegative
	case len(s) > maxInputLen:
		return Amount{}, ErrAmountSyntax
	}

	whole, frac, hasDot := strings.Cut(s, ".")
	if !isDigits(whole) {
		return Amount{}, ErrAmountSyntax
	}
	if hasDot {
		if !isDigits(frac) {
			return Amount{}, ErrAmountSyntax
		}
		if len(frac) > int(decimals) {
			return Amount{}, ErrAmountPrecision
		}
	}
	return fromDigits(whole + frac + strings.Repeat("0", int(decimals)-len(frac)))
}

// String возвращает сумму в минимальных единицах: каноническую строку цифр,
// формат БД и JSON. Это не человекочитаемый вид, для него есть FormatDecimal.
func (a Amount) String() string {
	return a.v.String()
}

// FormatDecimal возвращает сумму в виде десятичной дроби с заданным числом
// знаков: без лишних нулей в конце и без точки у целого числа ("1.5", "2", "0").
// Результат разбирается ParseDecimal в ту же сумму. Для decimals больше 77
// возвращает минимальные единицы без дроби.
func (a Amount) FormatDecimal(decimals uint8) string {
	digits := a.v.String()
	if decimals == 0 || decimals > maxDecimals {
		return digits
	}
	d := int(decimals)
	if len(digits) <= d {
		digits = strings.Repeat("0", d-len(digits)+1) + digits
	}
	whole, frac := digits[:len(digits)-d], strings.TrimRight(digits[len(digits)-d:], "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}

// BigInt возвращает копию значения: изменение результата не затрагивает Amount.
func (a Amount) BigInt() *big.Int {
	return new(big.Int).Set(&a.v)
}

// Uint64 возвращает значение, если оно помещается в uint64.
func (a Amount) Uint64() (uint64, bool) {
	if !a.v.IsUint64() {
		return 0, false
	}
	return a.v.Uint64(), true
}

// Cmp сравнивает суммы: -1, 0 или 1.
func (a Amount) Cmp(b Amount) int {
	return a.v.Cmp(&b.v)
}

// Equal сообщает, что суммы равны.
func (a Amount) Equal(b Amount) bool {
	return a.Cmp(b) == 0
}

// IsZero сообщает, что сумма равна нулю.
func (a Amount) IsZero() bool {
	return a.v.Sign() == 0
}

// Add возвращает a+b или ErrAmountOverflow, если сумма выходит за 2^256-1.
func (a Amount) Add(b Amount) (Amount, error) {
	sum := new(big.Int).Add(&a.v, &b.v)
	if sum.Cmp(maxValue()) > 0 {
		return Amount{}, ErrAmountOverflow
	}
	return Amount{v: *sum}, nil
}

// Sub возвращает a-b или ErrAmountNegative, если b больше a.
func (a Amount) Sub(b Amount) (Amount, error) {
	diff := new(big.Int).Sub(&a.v, &b.v)
	if diff.Sign() < 0 {
		return Amount{}, ErrAmountNegative
	}
	return Amount{v: *diff}, nil
}

// MarshalJSON кодирует сумму JSON-строкой в минимальных единицах.
func (a Amount) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

// UnmarshalJSON принимает только JSON-строку канонического вида. Число и null
// отклоняются: число могло пройти через float, а null не должен незаметно
// превращаться в ноль.
func (a *Amount) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '"' {
		return fmt.Errorf("domain: amount must be a JSON string: %w", ErrAmountSyntax)
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("domain: amount must be a JSON string: %w", ErrAmountSyntax)
	}
	parsed, err := ParseUnits(s)
	if err != nil {
		return err
	}
	*a = parsed
	return nil
}
