package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/24DSO-lab/wallet/internal/ports"
)

var _ ports.Store = (*DB)(nil)

const (
	defaultBusyTimeout = 5 * time.Second
	defaultReaders     = 4
	dirPerm            = 0o700
	filePerm           = 0o600
)

// Option настраивает Open.
type Option func(*options)

type options struct {
	log         *slog.Logger
	busyTimeout time.Duration
	readers     int
}

// WithLogger задаёт логгер. По умолчанию сообщения отбрасываются.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.log = l } }

// WithBusyTimeout задаёт, сколько соединение ждёт снятия блокировки файла.
func WithBusyTimeout(d time.Duration) Option { return func(o *options) { o.busyTimeout = d } }

// WithReaders задаёт число соединений для чтения (не меньше 1).
func WithReaders(n int) Option { return func(o *options) { o.readers = n } }

// DB это открытая база и реализация ports.Store.
type DB struct {
	*session // корневой доступ к репозиториям вне транзакции

	w    *sql.DB // единственный писатель
	r    *sql.DB // читатели, только чтение
	log  *slog.Logger
	path string
	opts options
}

// Open открывает базу по пути path (файл и каталог создаются при
// необходимости), применяет недостающие миграции и возвращает хранилище.
// Закрыть его должен вызывающий.
func Open(ctx context.Context, path string, opts ...Option) (*DB, error) {
	return open(ctx, path, mustLoadMigrations(), opts...)
}

func open(ctx context.Context, path string, migs []migration, opts ...Option) (*DB, error) {
	if path == "" {
		return nil, errors.New("store: empty database path")
	}
	o := options{log: slog.New(slog.NewTextHandler(io.Discard, nil)), busyTimeout: defaultBusyTimeout, readers: defaultReaders}
	for _, f := range opts {
		f(&o)
	}
	o.readers = max(o.readers, 1)

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	// База приватная: создаём файл с 0600 до того, как его создаст SQLite.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, filePerm) //nolint:gosec // путь задаёт приложение, не ввод
	if err != nil {
		return nil, fmt.Errorf("store: create database file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("store: create database file: %w", err)
	}

	w, err := sql.Open(driverName, dsn(path, o, kindWriter))
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	// Один писатель: SQLite всё равно сериализует записи, а очередь в
	// database/sql избавляет от SQLITE_BUSY между своими же горутинами.
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)

	if err := checkWriter(ctx, w); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := migrate(ctx, w, migs, len(migs), o.log); err != nil {
		_ = w.Close()
		return nil, err
	}

	r, err := sql.Open(driverName, dsn(path, o, kindReader))
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("store: open reader: %w", err)
	}
	r.SetMaxOpenConns(o.readers)
	r.SetMaxIdleConns(o.readers)
	r.SetConnMaxLifetime(0)

	d := &DB{w: w, r: r, log: o.log, path: path, opts: o}
	d.session = &session{db: d}
	return d, nil
}

// connKind это роль соединения: от неё зависят настройки.
type connKind int

const (
	kindWriter connKind = iota
	kindReader
	kindSnapshot
)

// dsn собирает строку подключения. Путь идёт без схемы file:, поэтому драйвер
// не разбирает его как URI и спецсимволы в пути (#, %, пробелы) безопасны.
func dsn(path string, o options, kind connKind) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", o.busyTimeout.Milliseconds()))
	q.Add("_pragma", "foreign_keys(1)")
	switch kind {
	case kindReader:
		q.Add("_pragma", "query_only(1)")
	case kindWriter:
		q.Add("_pragma", "journal_mode(WAL)")
		// Потеря подтверждённой записи при отключении питания хуже небольшой
		// задержки: pending_tx защищает от двойной отправки.
		q.Add("_pragma", "synchronous(FULL)")
		q.Set("_txlock", "immediate")
	case kindSnapshot:
		// Без query_only: SQLite считает VACUUM INTO записью и в режиме
		// «только чтение» его отвергает. База-источник при этом не меняется.
	}
	return path + "?" + q.Encode()
}

// checkWriter убеждается, что настройки соединения действительно применились:
// молчаливо выключенные внешние ключи ломают каскадное удаление.
func checkWriter(ctx context.Context, w *sql.DB) error {
	var fk int
	if err := w.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("store: check foreign_keys: %w", err)
	}
	if fk != 1 {
		return errors.New("store: foreign keys are not enabled")
	}
	var mode string
	if err := w.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("store: check journal_mode: %w", err)
	}
	if mode != "wal" {
		return fmt.Errorf("store: journal mode is %q, want wal", mode)
	}
	return nil
}

// Close закрывает оба пула. После Close хранилище непригодно; восстановление
// из копии требует закрытой базы.
func (d *DB) Close() error {
	return errors.Join(d.r.Close(), d.w.Close())
}

// Snapshot записывает согласованный снимок базы в новый файл dst средствами
// SQLite (VACUUM INTO). Приложение продолжает работать: снимок это одна
// читающая транзакция, писатель ей не мешает (WAL). Существующий dst не
// перезаписывается; при ошибке или отмене частичный файл удаляется.
func (d *DB) Snapshot(ctx context.Context, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("store: snapshot target already exists: %w", os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("store: snapshot target: %w", err)
	}
	// Отдельное временное соединение: писатель остаётся свободным, а
	// читатели работают с query_only.
	sdb, err := sql.Open(driverName, dsn(d.path, d.opts, kindSnapshot))
	if err != nil {
		return fmt.Errorf("store: snapshot: %w", err)
	}
	defer func() { _ = sdb.Close() }()
	sdb.SetMaxOpenConns(1)
	if _, err := sdb.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("store: snapshot: %w", err)
	}
	return nil
}

// querier это то, чем читают и *sql.DB, и *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// session это ports.Store либо над пулами (tx == nil), либо над одной
// транзакцией (внутри WithTx).
type session struct {
	db *DB
	tx *sql.Tx
}

func (s *session) reader() querier {
	if s.tx != nil {
		return s.tx
	}
	return s.db.r
}

// write выполняет fn в транзакции записи: в текущей, если мы внутри WithTx,
// иначе в новой, которую завершает сам.
func (s *session) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if s.tx != nil {
		return fn(s.tx)
	}
	return s.db.inTx(ctx, fn)
}

func (d *DB) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	done := false
	// Откат и при ошибке, и при панике в fn; после отмены ctx database/sql
	// откатывает сам, ErrTxDone здесь ожидаем.
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	done = true
	return nil
}

// WithTx выполняет fn в одной транзакции: при ошибке или панике всё
// откатывается. Вложенный вызов присоединяется к внешней транзакции.
func (s *session) WithTx(ctx context.Context, fn func(ctx context.Context, tx ports.Store) error) error {
	if s.tx != nil {
		return fn(ctx, s)
	}
	return s.db.inTx(ctx, func(tx *sql.Tx) error {
		return fn(ctx, &session{db: s.db, tx: tx})
	})
}

// Wallets и остальные методы отдают репозитории, привязанные к сессии.
func (s *session) Wallets() ports.WalletRepo           { return walletRepo{s} }
func (s *session) Addresses() ports.AddressRepo        { return addressRepo{s} }
func (s *session) Balances() ports.BalanceRepo         { return balanceRepo{s} }
func (s *session) UTXOs() ports.UTXORepo               { return utxoRepo{s} }
func (s *session) Transactions() ports.TransactionRepo { return txRepo{s} }
func (s *session) Settings() ports.SettingRepo         { return settingRepo{s} }
func (s *session) PendingTxs() ports.PendingTxRepo     { return pendingRepo{s} }
