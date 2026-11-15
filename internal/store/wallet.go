package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// Пути деривации одного адреса ETH и TRON фиксирует ADR (раздел «Принятые
// решения»); адрес кошелька хранится в таблице address, а Wallet собирается из неё.
const (
	pathETH  = "m/44'/60'/0'/0/0"
	pathTron = "m/44'/195'/0'/0/0"
)

type walletRepo struct{ s *session }

// walletSelect читает кошелёк вместе с адресами ETH и TRON одним запросом, то
// есть из одного снимка. Параметры: ?1 сеть ETH, ?2 сеть TRON.
const walletSelect = `
SELECT w.id, w.name, w.btc_xpub, w.keystore_id, w.created_at, w.sort_order,
  COALESCE((SELECT a.address FROM address a
            WHERE a.wallet_id = w.id AND a.network = ?1 AND a.chain = 'receive' AND a.idx = 0
            ORDER BY a.address LIMIT 1), ''),
  COALESCE((SELECT a.address FROM address a
            WHERE a.wallet_id = w.id AND a.network = ?2 AND a.chain = 'receive' AND a.idx = 0
            ORDER BY a.address LIMIT 1), '')
FROM wallet w`

type rowScanner interface{ Scan(dest ...any) error }

func scanWallet(r rowScanner) (domain.Wallet, error) {
	var (
		w       domain.Wallet
		created int64
	)
	err := r.Scan(&w.ID, &w.Name, &w.BTCXpub, &w.KeystoreID, &created, &w.SortOrder, &w.ETHAddress, &w.TronAddress)
	w.CreatedAt = fromDBTime(created)
	return w, err
}

// Create сохраняет кошелёк. Если заданы ETHAddress и TronAddress, они
// записываются как адреса кошелька (индекс 0, цепочка receive) в той же
// транзакции: так Get и List вернут их обратно. ID должен быть уникален.
func (r walletRepo) Create(ctx context.Context, w domain.Wallet) error {
	switch {
	case w.ID == "":
		return invalid("wallet id %w", errEmpty)
	case w.KeystoreID == "":
		return invalid("wallet keystore id %w", errEmpty)
	}
	return r.s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO wallet (id, name, created_at, keystore_id, btc_xpub, sort_order)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			w.ID, w.Name, toDBTime(w.CreatedAt), w.KeystoreID, w.BTCXpub, w.SortOrder)
		if err != nil {
			if isUnique(err) {
				return domain.NewError(domain.CodeInvalidInput, fmt.Errorf("store: wallet %q already exists", w.ID))
			}
			return fmt.Errorf("store: create wallet: %w", err)
		}
		for _, a := range []struct {
			net  domain.Network
			addr string
			path string
		}{
			{domain.NetworkEthereum, w.ETHAddress, pathETH},
			{domain.NetworkTron, w.TronAddress, pathTron},
		} {
			if a.addr == "" {
				continue
			}
			if err := upsertAddress(ctx, tx, domain.Address{
				WalletID: w.ID, Network: a.net, Address: a.addr, Path: a.path, Chain: domain.ChainReceive,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r walletRepo) Get(ctx context.Context, id string) (domain.Wallet, error) {
	w, err := scanWallet(r.s.reader().QueryRowContext(ctx, walletSelect+` WHERE w.id = ?3`,
		domain.NetworkEthereum, domain.NetworkTron, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Wallet{}, notFound(fmt.Sprintf("wallet %q", id))
	}
	if err != nil {
		return domain.Wallet{}, fmt.Errorf("store: get wallet: %w", err)
	}
	return w, nil
}

// List возвращает кошельки по SortOrder, затем по времени создания и id.
func (r walletRepo) List(ctx context.Context) ([]domain.Wallet, error) {
	rows, err := r.s.reader().QueryContext(ctx,
		walletSelect+` ORDER BY w.sort_order, w.created_at, w.id`,
		domain.NetworkEthereum, domain.NetworkTron)
	if err != nil {
		return nil, fmt.Errorf("store: list wallets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Wallet
	for rows.Next() {
		w, err := scanWallet(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list wallets: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list wallets: %w", err)
	}
	return out, nil
}

func (r walletRepo) Rename(ctx context.Context, id, name string) error {
	return r.s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE wallet SET name = ? WHERE id = ?`, name, id)
		return affectedOne(res, err, "rename wallet", fmt.Sprintf("wallet %q", id))
	})
}

// Delete удаляет кошелёк и всё, что к нему относится (внешние ключи с
// каскадом): адреса, балансы, UTXO, историю, неподтверждённые транзакции.
func (r walletRepo) Delete(ctx context.Context, id string) error {
	return r.s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM wallet WHERE id = ?`, id)
		return affectedOne(res, err, "delete wallet", fmt.Sprintf("wallet %q", id))
	})
}

// affectedOne превращает «затронуто 0 строк» в ErrNotFound.
func affectedOne(res sql.Result, err error, op, what string) error {
	if err != nil {
		return fmt.Errorf("store: %s: %w", op, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: %s: %w", op, err)
	}
	if n == 0 {
		return notFound(what)
	}
	return nil
}
