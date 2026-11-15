package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/24DSO-lab/wallet/internal/domain"
)

type utxoRepo struct{ s *session }

// Replace заменяет весь набор UTXO кошелька результатом свежего сканирования
// одной транзакцией: читатель видит либо старый набор, либо новый, но не
// пустой набор посередине. Одинаковые (TxID, Vout) во входе схлопываются, последний побеждает.
func (r utxoRepo) Replace(ctx context.Context, walletID string, utxos []domain.UTXO) error {
	if walletID == "" {
		return invalid("utxo wallet id %w", errEmpty)
	}
	for _, u := range utxos {
		if u.WalletID != "" && u.WalletID != walletID {
			return invalid("utxo %s:%d belongs to another wallet", u.TxID, u.Vout)
		}
		if u.TxID == "" {
			return invalid("utxo txid %w", errEmpty)
		}
	}
	return r.s.write(ctx, func(tx *sql.Tx) error {
		// Кошелёк должен существовать, иначе пустой Replace молча «удалил бы» ничего.
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM wallet WHERE id = ?`, walletID).Scan(&n); err != nil {
			return fmt.Errorf("store: replace utxos: %w", err)
		}
		if n == 0 {
			return notFound(fmt.Sprintf("wallet %q", walletID))
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM utxo WHERE wallet_id = ?`, walletID); err != nil {
			return fmt.Errorf("store: replace utxos: %w", err)
		}
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO utxo (wallet_id, txid, vout, address, amount, confirmations, spent)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (wallet_id, txid, vout) DO UPDATE SET
			   address = excluded.address, amount = excluded.amount,
			   confirmations = excluded.confirmations, spent = excluded.spent`)
		if err != nil {
			return fmt.Errorf("store: replace utxos: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		for _, u := range utxos {
			if _, err := stmt.ExecContext(ctx, walletID, u.TxID, u.Vout, u.Address,
				u.Amount.String(), u.Confirmations, boolInt(u.Spent)); err != nil {
				return fmt.Errorf("store: replace utxos: %w", err)
			}
		}
		return nil
	})
}

// List возвращает UTXO кошелька по (TxID, Vout), включая потраченные.
func (r utxoRepo) List(ctx context.Context, walletID string) ([]domain.UTXO, error) {
	rows, err := r.s.reader().QueryContext(ctx,
		`SELECT txid, vout, address, amount, confirmations, spent FROM utxo
		 WHERE wallet_id = ? ORDER BY txid, vout`, walletID)
	if err != nil {
		return nil, fmt.Errorf("store: list utxos: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.UTXO
	for rows.Next() {
		var (
			u      domain.UTXO
			amount string
			spent  int
		)
		if err := rows.Scan(&u.TxID, &u.Vout, &u.Address, &amount, &u.Confirmations, &spent); err != nil {
			return nil, fmt.Errorf("store: list utxos: %w", err)
		}
		if u.Amount, err = parseAmount(amount); err != nil {
			return nil, err
		}
		u.WalletID, u.Spent = walletID, spent != 0
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list utxos: %w", err)
	}
	return out, nil
}
