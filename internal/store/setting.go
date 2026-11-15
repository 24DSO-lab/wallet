package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
)

// sensitiveKey выделяет ключи настроек, значения которых нельзя отдавать при
// общем чтении: ключи API провайдеров и всё, что похоже на секрет. Правило
// намеренно широкое: лишний скрытый ключ дешевле утёкшего.
var sensitiveKey = regexp.MustCompile(`(?i)(api[_.\-]?key|secret|token|passw|passphrase|seed|mnemonic|private|credential|auth)`)

// IsSensitiveSetting сообщает, что значение ключа не отдаётся через List.
// Экспортирована, чтобы сервисы называли ключи в соответствии с правилом.
func IsSensitiveSetting(key string) bool { return sensitiveKey.MatchString(key) }

type settingRepo struct{ s *session }

func (r settingRepo) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := r.s.reader().QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", notFound(fmt.Sprintf("setting %q", key))
	}
	if err != nil {
		return "", fmt.Errorf("store: get setting: %w", err)
	}
	return v, nil
}

func (r settingRepo) Set(ctx context.Context, key, value string) error {
	if key == "" {
		return invalid("setting key %w", errEmpty)
	}
	return r.s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO setting (key, value) VALUES (?, ?)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
		if err != nil {
			return fmt.Errorf("store: set setting: %w", err)
		}
		return nil
	})
}

// List возвращает настройки без чувствительных ключей. Фильтр применяется
// здесь, а не в вызывающем коде, чтобы значение не покидало слой хранения.
func (r settingRepo) List(ctx context.Context) (map[string]string, error) {
	rows, err := r.s.reader().QueryContext(ctx, `SELECT key, value FROM setting ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("store: list settings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("store: list settings: %w", err)
		}
		if !IsSensitiveSetting(k) {
			out[k] = v
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list settings: %w", err)
	}
	return out, nil
}
