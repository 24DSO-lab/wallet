package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/24DSO-lab/wallet/internal/domain"
)

type txRepo struct{ s *session }

func validateTx(t domain.Tx) error {
	switch {
	case t.WalletID == "":
		return invalid("tx wallet id %w", errEmpty)
	case t.Asset == "":
		return invalid("tx asset %w", errEmpty)
	case t.TxID == "":
		return invalid("tx id %w", errEmpty)
	case !t.Status.Valid():
		return invalid("tx status %q is unknown", string(t.Status))
	}
	switch t.Direction {
	case domain.DirectionIn, domain.DirectionOut, domain.DirectionSelf:
	default:
		return invalid("tx direction %q is unknown", string(t.Direction))
	}
	return nil
}

// Upsert идемпотентен по (WalletID, Asset, TxID): повторное обновление истории
// не создаёт дублей, а меняет статус, высоту блока и время существующей
// записи. Её ID сохраняется, поле Tx.ID на входе игнорируется. Побеждает
// последняя запись: хранилище не решает, какой статус «правильнее».
func (r txRepo) Upsert(ctx context.Context, txs []domain.Tx) error {
	if len(txs) == 0 {
		return nil
	}
	heights := make([]int64, len(txs))
	for i, t := range txs {
		if err := validateTx(t); err != nil {
			return err
		}
		h, err := height(t.BlockHeight)
		if err != nil {
			return err
		}
		heights[i] = h
	}
	return r.s.write(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO tx (id, wallet_id, asset, txid, direction, amount, fee, fee_asset,
			                 status, block_height, ts, counterparty)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (wallet_id, asset, txid) DO UPDATE SET
			   direction = excluded.direction, amount = excluded.amount, fee = excluded.fee,
			   fee_asset = excluded.fee_asset, status = excluded.status,
			   block_height = excluded.block_height, ts = excluded.ts,
			   counterparty = excluded.counterparty`)
		if err != nil {
			return fmt.Errorf("store: upsert transactions: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		for i, t := range txs {
			id, err := newID()
			if err != nil {
				return err
			}
			if _, err := stmt.ExecContext(ctx, id, t.WalletID, t.Asset, t.TxID, t.Direction,
				t.Amount.String(), t.Fee.String(), t.FeeAsset, t.Status, heights[i],
				toDBTime(t.Time), t.Counterparty); err != nil {
				if isForeignKey(err) {
					return notFound(fmt.Sprintf("wallet %q", t.WalletID))
				}
				return fmt.Errorf("store: upsert transactions: %w", err)
			}
		}
		return nil
	})
}

const txColumns = `id, wallet_id, asset, txid, direction, amount, fee, fee_asset, status, block_height, ts, counterparty`

func scanTx(r rowScanner) (domain.Tx, error) {
	var (
		t           domain.Tx
		amount, fee string
		blockHeight int64
		ts          int64
	)
	if err := r.Scan(&t.ID, &t.WalletID, &t.Asset, &t.TxID, &t.Direction, &amount, &fee,
		&t.FeeAsset, &t.Status, &blockHeight, &ts, &t.Counterparty); err != nil {
		return domain.Tx{}, err
	}
	var err error
	if t.Amount, err = parseAmount(amount); err != nil {
		return domain.Tx{}, err
	}
	if t.Fee, err = parseAmount(fee); err != nil {
		return domain.Tx{}, err
	}
	t.BlockHeight, t.Time = uint64(blockHeight), fromDBTime(ts) //nolint:gosec // CHECK (block_height >= 0)
	return t, nil
}

// List возвращает историю кошелька, новые первыми. Пустой asset означает все активы.
func (r txRepo) List(ctx context.Context, walletID string, asset domain.Asset) ([]domain.Tx, error) {
	var sb strings.Builder
	sb.WriteString(`SELECT ` + txColumns + ` FROM tx WHERE wallet_id = ?`)
	args := []any{walletID}
	if asset != "" {
		sb.WriteString(` AND asset = ?`)
		args = append(args, asset)
	}
	sb.WriteString(` ORDER BY ts DESC, block_height DESC, txid DESC`)

	rows, err := r.s.reader().QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("store: list transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Tx
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list transactions: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list transactions: %w", err)
	}
	return out, nil
}

func (r txRepo) Get(ctx context.Context, walletID string, asset domain.Asset, id domain.TxID) (domain.Tx, error) {
	t, err := scanTx(r.s.reader().QueryRowContext(ctx,
		`SELECT `+txColumns+` FROM tx WHERE wallet_id = ? AND asset = ? AND txid = ?`, walletID, asset, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Tx{}, notFound(fmt.Sprintf("transaction %q", string(id)))
	}
	if err != nil {
		return domain.Tx{}, fmt.Errorf("store: get transaction: %w", err)
	}
	return t, nil
}
