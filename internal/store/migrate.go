package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrSchemaTooNew означает, что база создана более новой версией программы:
// молча работать с ней нельзя, а откатывать схему мы не умеем.
var ErrSchemaTooNew = errors.New("store: database schema is newer than this build")

// migration это один файл вида 0001_name.sql.
type migration struct {
	version int
	name    string
	sql     string
}

// SchemaVersion возвращает версию схемы, которую создаёт эта сборка. Нужна
// резервной копии: архив с более новой схемой восстанавливать нельзя.
func SchemaVersion() int { return len(mustLoadMigrations()) }

func mustLoadMigrations() []migration {
	ms, err := loadMigrations(migrationsFS, "migrations")
	if err != nil {
		panic(err) // встроенные файлы проверяются тестом
	}
	return ms
}

// loadMigrations читает и проверяет файлы: имена NNNN_name.sql, номера
// подряд с единицы без пропусков и повторов.
func loadMigrations(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		num, name, ok := strings.Cut(strings.TrimSuffix(e.Name(), ".sql"), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v < 1 || len(num) != 4 {
			return nil, fmt.Errorf("store: bad migration file name %q", e.Name())
		}
		body, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: read migration %q: %w", e.Name(), err)
		}
		out = append(out, migration{version: v, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("store: migrations are not contiguous at version %d", m.version)
		}
	}
	return out, nil
}

func userVersion(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}
	return v, nil
}

// migrate применяет миграции с номерами до target включительно. Каждая идёт в
// своей транзакции вместе с записью user_version: либо применилась целиком,
// либо база осталась на прежней версии. Соединение писателя берёт блокировку
// сразу (_txlock=immediate), поэтому два процесса не применят одну миграцию
// дважды: проигравший перечитывает версию внутри транзакции.
func migrate(ctx context.Context, db *sql.DB, ms []migration, target int, log *slog.Logger) error {
	cur, err := userVersion(ctx, db)
	if err != nil {
		return err
	}
	if cur > len(ms) {
		return fmt.Errorf("%w: schema %d, build knows %d", ErrSchemaTooNew, cur, len(ms))
	}
	for _, m := range ms {
		if m.version > target {
			break
		}
		if err := applyOne(ctx, db, m); err != nil {
			return err
		}
		log.Debug("store: migration applied", "version", m.version, "name", m.name)
	}
	return nil
}

func applyOne(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: migration %d: begin: %w", m.version, err)
	}
	defer func() { _ = tx.Rollback() }()

	cur, err := userVersion(ctx, tx)
	if err != nil {
		return err
	}
	if cur >= m.version {
		return nil // уже применена, например другим процессом
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("store: migration %d (%s): %w", m.version, m.name, err)
	}
	// PRAGMA не принимает параметры; значение это наше целое число.
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(m.version)); err != nil {
		return fmt.Errorf("store: migration %d: set version: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migration %d: commit: %w", m.version, err)
	}
	return nil
}
