package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/24DSO-lab/wallet/internal/domain"
)

type addressRepo struct{ s *session }

// upsertAddress вставляет адрес или обновляет существующий по ключу
// (wallet_id, network, address). Флаг used только растёт: адрес, на котором
// были операции, не становится «неиспользованным» из-за неполного ответа
// провайдера.
func upsertAddress(ctx context.Context, tx *sql.Tx, a domain.Address) error {
	if a.WalletID == "" || a.Network == "" || a.Address == "" {
		return invalid("address needs wallet id, network and address")
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO address (wallet_id, network, address, path, chain, idx, used)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (wallet_id, network, address) DO UPDATE SET
		   path = excluded.path, chain = excluded.chain, idx = excluded.idx,
		   used = MAX(used, excluded.used)`,
		a.WalletID, a.Network, a.Address, a.Path, a.Chain, a.Index, boolInt(a.Used))
	if err != nil {
		if isForeignKey(err) {
			return notFound(fmt.Sprintf("wallet %q", a.WalletID))
		}
		return fmt.Errorf("store: upsert address: %w", err)
	}
	return nil
}

// Upsert идемпотентен по (WalletID, Network, Address); весь набор пишется в
// одной транзакции. Адрес несуществующего кошелька это ErrNotFound.
func (r addressRepo) Upsert(ctx context.Context, addrs []domain.Address) error {
	if len(addrs) == 0 {
		return nil
	}
	return r.s.write(ctx, func(tx *sql.Tx) error {
		for _, a := range addrs {
			if err := upsertAddress(ctx, tx, a); err != nil {
				return err
			}
		}
		return nil
	})
}

// List возвращает адреса кошелька в сети: сначала receive, затем change, по
// индексу. Пустая сеть означает все сети кошелька.
func (r addressRepo) List(ctx context.Context, walletID string, n domain.Network) ([]domain.Address, error) {
	var sb strings.Builder
	sb.WriteString(`SELECT wallet_id, network, address, path, chain, idx, used FROM address WHERE wallet_id = ?`)
	args := []any{walletID}
	if n != "" {
		sb.WriteString(` AND network = ?`)
		args = append(args, n)
	}
	sb.WriteString(` ORDER BY network, CASE chain WHEN 'receive' THEN 0 ELSE 1 END, chain, idx, address`)

	rows, err := r.s.reader().QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("store: list addresses: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Address
	for rows.Next() {
		var (
			a    domain.Address
			used int
		)
		if err := rows.Scan(&a.WalletID, &a.Network, &a.Address, &a.Path, &a.Chain, &a.Index, &used); err != nil {
			return nil, fmt.Errorf("store: list addresses: %w", err)
		}
		a.Used = used != 0
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list addresses: %w", err)
	}
	return out, nil
}
