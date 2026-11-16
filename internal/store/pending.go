package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

type pendingRepo struct{ s *session }

func scanPending(r rowScanner) (ports.PendingTx, error) {
	var (
		p  ports.PendingTx
		at int64
	)
	var accepted int
	if err := r.Scan(&p.TxID, &p.WalletID, &p.Asset, &p.Network, &p.Raw, &p.Draft, &accepted, &p.ReplacesTxID, &at); err != nil {
		return ports.PendingTx{}, err
	}
	p.BroadcastAccepted = accepted == 1
	p.CreatedAt = fromDBTime(at)
	return p, nil
}

// Put сохраняет подписанную транзакцию до подтверждения сетью. Повторный Put
// того же TxID не создаёт дубль и не сдвигает исходное CreatedAt.
func (r pendingRepo) Put(ctx context.Context, p ports.PendingTx) error {
	switch {
	case p.TxID == "":
		return invalid("pending tx id %w", errEmpty)
	case p.WalletID == "" || p.Asset == "" || p.Network == "":
		return invalid("pending tx needs wallet id, asset and network")
	case len(p.Raw) == 0:
		return invalid("pending tx raw %w", errEmpty)
	}
	return r.s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO pending_tx (txid, wallet_id, asset, network, raw, draft, broadcast_accepted, replaces_txid, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (txid) DO UPDATE SET
			   wallet_id = excluded.wallet_id, asset = excluded.asset,
			   network = excluded.network, raw = excluded.raw, draft = excluded.draft,
			   broadcast_accepted = excluded.broadcast_accepted, replaces_txid = excluded.replaces_txid`,
			p.TxID, p.WalletID, p.Asset, p.Network, p.Raw, p.Draft, boolInt(p.BroadcastAccepted), p.ReplacesTxID, toDBTime(p.CreatedAt))
		if err != nil {
			if isForeignKey(err) {
				return notFound(fmt.Sprintf("wallet %q", p.WalletID))
			}
			return fmt.Errorf("store: put pending tx: %w", err)
		}
		return nil
	})
}

func (r pendingRepo) Get(ctx context.Context, id domain.TxID) (ports.PendingTx, error) {
	p, err := scanPending(r.s.reader().QueryRowContext(ctx,
		`SELECT txid, wallet_id, asset, network, raw, draft, broadcast_accepted, replaces_txid, created_at FROM pending_tx WHERE txid = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ports.PendingTx{}, notFound(fmt.Sprintf("pending tx %q", string(id)))
	}
	if err != nil {
		return ports.PendingTx{}, fmt.Errorf("store: get pending tx: %w", err)
	}
	return p, nil
}

// List возвращает неподтверждённые транзакции от старых к новым.
func (r pendingRepo) List(ctx context.Context) ([]ports.PendingTx, error) {
	rows, err := r.s.reader().QueryContext(ctx,
		`SELECT txid, wallet_id, asset, network, raw, draft, broadcast_accepted, replaces_txid, created_at FROM pending_tx ORDER BY created_at, txid`)
	if err != nil {
		return nil, fmt.Errorf("store: list pending txs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ports.PendingTx
	for rows.Next() {
		p, err := scanPending(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list pending txs: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list pending txs: %w", err)
	}
	return out, nil
}

// Delete удаляет запись; отсутствующая это ErrNotFound.
func (r pendingRepo) Delete(ctx context.Context, id domain.TxID) error {
	return r.s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM pending_tx WHERE txid = ?`, id)
		return affectedOne(res, err, "delete pending tx", fmt.Sprintf("pending tx %q", string(id)))
	})
}
