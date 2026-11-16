package store

import (
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func rawDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(driverName, dsn(filepath.Join(t.TempDir(), "m.db"), options{busyTimeout: defaultBusyTimeout}, kindWriter))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func version(t *testing.T, db *sql.DB) int {
	t.Helper()
	v, err := userVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	ms, err := loadMigrations(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || SchemaVersion() != len(ms) {
		t.Fatalf("SchemaVersion = %d, migrations = %d", SchemaVersion(), len(ms))
	}
}

func TestMigrateFromScratch(t *testing.T) {
	d := openTest(t)
	if got, want := version(t, d.w), SchemaVersion(); got != want {
		t.Fatalf("user_version = %d, want %d", got, want)
	}
	for _, tbl := range []string{"wallet", "address", "setting", "balance", "utxo", "tx", "pending_tx"} {
		if !tableExists(t, d.w, tbl) {
			t.Errorf("table %s is missing", tbl)
		}
	}
	// Таблица swap остаётся на холде; миграция 4 расширяет только pending_tx.
	if tableExists(t, d.w, "swap") {
		t.Error("swap table must not exist while swap remains on hold")
	}
}

func TestMigrateStepByStep(t *testing.T) {
	migs := mustLoadMigrations()
	db := rawDB(t)
	wantTables := [][]string{
		{"wallet", "address", "setting"},
		{"balance", "utxo", "tx"},
		{"pending_tx"},
		{"pending_tx"},
	}
	if len(migs) != len(wantTables) {
		t.Fatalf("test knows %d stages, found %d migrations", len(wantTables), len(migs))
	}
	for step := 1; step <= len(migs); step++ {
		if err := migrate(ctx, db, migs, step, discard); err != nil {
			t.Fatalf("migrate to %d: %v", step, err)
		}
		if got := version(t, db); got != step {
			t.Fatalf("after step %d user_version = %d", step, got)
		}
		for _, tbl := range wantTables[step-1] {
			if !tableExists(t, db, tbl) {
				t.Errorf("step %d: table %s is missing", step, tbl)
			}
		}
		// Повторный запуск того же шага ничего не меняет.
		if err := migrate(ctx, db, migs, step, discard); err != nil {
			t.Fatalf("repeat step %d: %v", step, err)
		}
	}
}

func TestMigrateKeepsDataAcrossStages(t *testing.T) {
	migs := mustLoadMigrations()
	db := rawDB(t)
	if err := migrate(ctx, db, migs, 1, discard); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO wallet (id, name, created_at, keystore_id) VALUES ('w1','n',1,'k')`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, migs, len(migs), discard); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM wallet`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("wallet rows after migration = %d, err %v", n, err)
	}
}

func TestMigrateFailureRollsBack(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0001_ok.sql":  {Data: []byte(`CREATE TABLE a (x INTEGER);`)},
		"m/0002_bad.sql": {Data: []byte(`CREATE TABLE b (x INTEGER); INSERT INTO nonexistent VALUES (1);`)},
	}
	migs, err := loadMigrations(fsys, "m")
	if err != nil {
		t.Fatal(err)
	}
	db := rawDB(t)
	if err := migrate(ctx, db, migs, 2, discard); err == nil {
		t.Fatal("expected error from broken migration")
	}
	if got := version(t, db); got != 1 {
		t.Errorf("user_version = %d, want 1 (failed migration must not be recorded)", got)
	}
	if tableExists(t, db, "b") {
		t.Error("table b from the failed migration must be rolled back")
	}
	if !tableExists(t, db, "a") {
		t.Error("table a from the successful migration must stay")
	}
}

func TestMigrateRejectsNewerSchema(t *testing.T) {
	db := rawDB(t)
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	err := migrate(ctx, db, mustLoadMigrations(), SchemaVersion(), discard)
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("err = %v, want ErrSchemaTooNew", err)
	}
}

func TestLoadMigrationsRejectsGapsAndBadNames(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"gap":      {"m/0001_a.sql": {Data: []byte("")}, "m/0003_c.sql": {Data: []byte("")}},
		"no start": {"m/0002_b.sql": {Data: []byte("")}},
		"bad name": {"m/init.sql": {Data: []byte("")}},
		"short":    {"m/1_a.sql": {Data: []byte("")}},
	} {
		if _, err := loadMigrations(fsys, "m"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// Схема не должна знать про конкретные активы, сети и число кошельков, а
// деньги не хранятся числами с плавающей точкой.
func TestSchemaIsGenericAndHasNoReal(t *testing.T) {
	d := openTest(t)
	rows, err := d.w.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE sql IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		up := strings.ToUpper(ddl)
		for _, bad := range []string{" REAL", " FLOAT", " DOUBLE", "NUMERIC", "'BTC'", "'ETH'", "'USDC'", "'USDT'", "'BITCOIN'", "'ETHEREUM'", "'TRON'"} {
			if strings.Contains(up, bad) {
				t.Errorf("%s: schema contains %q", name, bad)
			}
		}
	}
}

func TestSchemaAcceptsArbitraryAssetNetworkAndManyWallets(t *testing.T) {
	d := openTest(t)
	for i := range 10 { // не 1-3: число кошельков схеме безразлично
		mkWallet(t, d, string(rune('a'+i)), i)
	}
	if n := count(t, d, "wallet"); n != 10 {
		t.Fatalf("wallets = %d", n)
	}
	if _, err := d.w.ExecContext(ctx, `INSERT INTO balance (wallet_id, asset, confirmed, pending, updated_at) VALUES ('a','XYZ','1','0',1)`); err != nil {
		t.Fatalf("unknown asset must be accepted by the schema: %v", err)
	}
	if _, err := d.w.ExecContext(ctx, `INSERT INTO address (wallet_id, network, address, path, chain, idx) VALUES ('a','newnet','x','m',  'receive',0)`); err != nil {
		t.Fatalf("unknown network must be accepted by the schema: %v", err)
	}
}

func TestAmountColumnsRejectNonDigits(t *testing.T) {
	d := openTest(t)
	mkWallet(t, d, "w", 0)
	for _, bad := range []string{"", "-1", "1.5", "1e18", "0x10", " 1", "١٢"} {
		_, err := d.w.ExecContext(ctx, `INSERT INTO balance (wallet_id, asset, confirmed, pending, updated_at) VALUES ('w','A',?, '0', 1)`, bad)
		if err == nil {
			t.Errorf("amount %q was accepted by the schema", bad)
		}
	}
}

func TestOpenSetsPragmasAndPrivateMode(t *testing.T) {
	d := openTest(t)
	var fk int
	if err := d.w.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Errorf("writer foreign_keys = %d, err %v", fk, err)
	}
	if err := d.r.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Errorf("reader foreign_keys = %d, err %v", fk, err)
	}
	// Читатель не должен уметь писать.
	if _, err := d.r.ExecContext(ctx, `INSERT INTO setting (key, value) VALUES ('k','v')`); err == nil {
		t.Error("reader connection accepted a write")
	}
}

func TestReopenKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.db")
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Settings().Set(ctx, "lang", "ru"); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if v, err := d.Settings().Get(ctx, "lang"); err != nil || v != "ru" {
		t.Fatalf("lang = %q, err %v", v, err)
	}
}
