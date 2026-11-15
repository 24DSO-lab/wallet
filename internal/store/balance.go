package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

type balanceRepo struct{ s *session }

// Put заменяет закэшированный баланс актива кошелька. Баланс это кэш: источник
// истины всегда сеть, поэтому история изменений не хранится.
func (r balanceRepo) Put(ctx context.Context, walletID string, a domain.Asset, b domain.Balance, at time.Time) error {
	if walletID == "" || a == "" {
		return invalid("balance needs wallet id and asset")
	}
	return r.s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO balance (wallet_id, asset, confirmed, pending, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (wallet_id, asset) DO UPDATE SET
			   confirmed = excluded.confirmed, pending = excluded.pending, updated_at = excluded.updated_at`,
			walletID, a, b.Confirmed.String(), b.Pending.String(), toDBTime(at))
		if err != nil {
			if isForeignKey(err) {
				return notFound(fmt.Sprintf("wallet %q", walletID))
			}
			return fmt.Errorf("store: put balance: %w", err)
		}
		return nil
	})
}

func (r balanceRepo) Get(ctx context.Context, walletID string, a domain.Asset) (ports.BalanceRecord, error) {
	var confirmed, pending string
	var at int64
	err := r.s.reader().QueryRowContext(ctx,
		`SELECT confirmed, pending, updated_at FROM balance WHERE wallet_id = ? AND asset = ?`,
		walletID, a).Scan(&confirmed, &pending, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.BalanceRecord{}, notFound(fmt.Sprintf("balance of %q for wallet %q", string(a), walletID))
	}
	if err != nil {
		return ports.BalanceRecord{}, fmt.Errorf("store: get balance: %w", err)
	}
	var rec ports.BalanceRecord
	if rec.Balance.Confirmed, err = parseAmount(confirmed); err != nil {
		return ports.BalanceRecord{}, err
	}
	if rec.Balance.Pending, err = parseAmount(pending); err != nil {
		return ports.BalanceRecord{}, err
	}
	rec.UpdatedAt = fromDBTime(at)
	return rec, nil
}
