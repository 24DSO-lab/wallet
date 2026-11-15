package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// notFound оборачивает domain.ErrNotFound с описанием записи.
func notFound(what string) error {
	return fmt.Errorf("store: %s: %w", what, domain.ErrNotFound)
}

// invalid это ошибка входных данных: вызывающий передал то, что нельзя сохранить.
func invalid(format string, a ...any) error {
	return domain.NewError(domain.CodeInvalidInput, fmt.Errorf("store: "+format, a...))
}

// Время хранится целым числом миллисекунд Unix в UTC.
func toDBTime(t time.Time) int64    { return t.UnixMilli() }
func fromDBTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// parseAmount разбирает сумму из БД. Повреждённое значение это ошибка, а не ноль.
func parseAmount(s string) (domain.Amount, error) {
	a, err := domain.ParseUnits(s)
	if err != nil {
		return domain.Amount{}, fmt.Errorf("store: corrupt amount in database: %w", err)
	}
	return a, nil
}

// height переводит высоту блока в int64 колонки SQLite.
func height(h uint64) (int64, error) {
	if h > math.MaxInt64 {
		return 0, invalid("block height %d is out of range", h)
	}
	return int64(h), nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// newID возвращает случайный идентификатор записи: 128 бит в hex.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("store: generate id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

var errEmpty = errors.New("must not be empty")
