package backup

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/store"
	"github.com/24DSO-lab/wallet/internal/secret"
)

func bigAmount(t *testing.T, exp int) domain.Amount {
	t.Helper()
	a, err := domain.NewAmount(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// seed создаёт рабочие данные: настоящую БД с содержимым и keystore-файлы.
func seed(t *testing.T, dir string) *store.DB {
	t.Helper()
	db, err := store.Open(ctx, filepath.Join(dir, "wallet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	w := domain.Wallet{
		PublicWallet: domain.PublicWallet{ID: "w1", Name: "Основной", BTCXpub: "xpub-1", ETHAddress: "0xabc", TronAddress: "Tabc"},
		KeystoreID:   "ks1", CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(),
	}
	if err := db.Wallets().Create(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := db.Balances().Put(ctx, "w1", domain.ETH, domain.Balance{Confirmed: bigAmount(t, 30)}, time.UnixMilli(5)); err != nil {
		t.Fatal(err)
	}
	if err := db.Settings().Set(ctx, "lang", "ru"); err != nil {
		t.Fatal(err)
	}
	writeKeystore(t, dir, map[string]string{"ks1.json": "-secret-blob-1", "ks2.json": "-secret-blob-2"})
	return db
}

func TestRoundTripOnEmptyDirectory(t *testing.T) {
	src, dest, dst := t.TempDir(), t.TempDir(), t.TempDir()
	db := seed(t, src)
	svc := newService(t, src, db, func(c *Config) { c.MaxSchemaVersion = store.SchemaVersion() })

	res, err := svc.Create(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 3 {
		t.Errorf("Files = %d, want 3", res.Files)
	}

	restore := newService(t, dst, db, func(c *Config) { c.MaxSchemaVersion = store.SchemaVersion() })
	rr, err := restore.Restore(ctx, res.Path, secret.Bytes{})
	if err != nil {
		t.Fatal(err)
	}
	if rr.PreviousDir != "" {
		t.Errorf("clean install must have no previous dir, got %q", rr.PreviousDir)
	}

	got := openStore(t, dst)
	w, err := got.Wallets().Get(ctx, "w1")
	if err != nil || w.Name != "Основной" || w.BTCXpub != "xpub-1" || w.ETHAddress != "0xabc" || w.TronAddress != "Tabc" {
		t.Fatalf("wallet lost: %+v, %v", w, err)
	}
	if b, err := got.Balances().Get(ctx, "w1", domain.ETH); err != nil || !b.Balance.Confirmed.Equal(bigAmount(t, 30)) {
		t.Fatalf("balance lost: %v, %v", b.Balance.Confirmed, err)
	}
	if v, _ := got.Settings().Get(ctx, "lang"); v != "ru" {
		t.Errorf("setting lost: %q", v)
	}
	// keystore побайтно тот же.
	for _, name := range []string{"ks1.json", "ks2.json"} {
		a, _ := os.ReadFile(filepath.Join(src, "keystore", name))
		b, err := os.ReadFile(filepath.Join(dst, "keystore", name))
		if err != nil || string(a) != string(b) {
			t.Errorf("%s differs after restore (err %v)", name, err)
		}
	}
	// После восстановления нет временных каталогов.
	for _, e := range mustReadDir(t, dst) {
		if strings.HasPrefix(e, ".restore-") {
			t.Errorf("staging dir %s left behind", e)
		}
	}
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestRestoreOverExistingDataKeepsPrevious(t *testing.T) {
	src, dest, dst := t.TempDir(), t.TempDir(), t.TempDir()
	db := seed(t, src)
	res, err := newService(t, src, db, func(c *Config) { c.MaxSchemaVersion = store.SchemaVersion() }).Create(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	// Рабочие данные, которые будут заменены.
	if err := os.WriteFile(filepath.Join(dst, "wallet.db"), fakeDBBytes(1), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dst, "wallet.db-wal"), []byte("old-wal"), 0o600)
	writeKeystore(t, dst, map[string]string{"old.json": "-old"})

	rr, err := newService(t, dst, db, func(c *Config) { c.MaxSchemaVersion = store.SchemaVersion() }).Restore(ctx, res.Path, secret.Bytes{})
	if err != nil {
		t.Fatal(err)
	}
	if rr.PreviousDir == "" {
		t.Fatal("previous data must be kept")
	}
	if b, err := os.ReadFile(filepath.Join(rr.PreviousDir, "keystore", "old.json")); err != nil || string(b) != ksMagic+"-old" {
		t.Errorf("old keystore not preserved: %q, %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(rr.PreviousDir, "wallet.db-wal")); string(b) != "old-wal" {
		t.Error("old wal not preserved")
	}
	if _, err := os.Stat(filepath.Join(dst, "keystore", "old.json")); err == nil {
		t.Error("old keystore file still in working keystore")
	}
	if _, err := os.Stat(filepath.Join(dst, "wallet.db-wal")); err == nil {
		t.Error("stale wal next to the restored database")
	}
	if _, err := openStore(t, dst).Wallets().Get(ctx, "w1"); err != nil {
		t.Errorf("restored wallet: %v", err)
	}
}

func TestCreateOutputIsAtomicAndComplete(t *testing.T) {
	dir, dest := t.TempDir(), t.TempDir()
	writeKeystore(t, dir, map[string]string{"a.json": "-a", "b.json": "-b"})
	res, err := newService(t, dir, fakeDB{version: testSchema}).Create(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	names := mustReadDir(t, dest)
	sort.Strings(names)
	want := []string{"wallet-backup-20261115T103000Z.zip", "wallet-backup-20261115T103000Z.zip.sha256"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("dest contains %v, want exactly %v", names, want)
	}
	sum, err := fileSHA256(ctx, res.Path)
	if err != nil || sum != res.SHA256 {
		t.Fatalf("checksum: %s vs %s (%v)", sum, res.SHA256, err)
	}
	if got, err := readChecksumFile(res.ChecksumPath); err != nil || got != sum {
		t.Errorf("sidecar = %q, %v", got, err)
	}

	// Внутри ровно разрешённые записи: БД, keystore и манифест. Ничего лишнего,
	// в том числе парольной фразы, которой у пакета просто нет.
	zr, err := zip.OpenReader(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	var entries []string
	for _, f := range zr.File {
		entries = append(entries, f.Name)
	}
	sort.Strings(entries)
	if fmt.Sprint(entries) != fmt.Sprint([]string{"db/wallet.db", "keystore/a.json", "keystore/b.json", "manifest.json"}) {
		t.Errorf("entries = %v", entries)
	}

	// Второе создание в ту же секунду не затирает первое.
	res2, err := newService(t, dir, fakeDB{version: testSchema}).Create(ctx, dest)
	if err != nil || res2.Path == res.Path {
		t.Fatalf("second Create: %v, same path %v", err, res2.Path == res.Path)
	}
}

func TestCreateKeystoreRules(t *testing.T) {
	t.Run("missing keystore dir is allowed", func(t *testing.T) {
		res, err := newService(t, t.TempDir(), fakeDB{version: testSchema}).Create(ctx, t.TempDir())
		if err != nil || res.Files != 1 {
			t.Fatalf("%v, files %d", err, res.Files)
		}
	})
	t.Run("temp files are skipped", func(t *testing.T) {
		dir := t.TempDir()
		writeKeystore(t, dir, map[string]string{"a.json": "-a", ".hidden": "-x", "a.json.tmp": "-t"})
		res, err := newService(t, dir, fakeDB{version: testSchema}).Create(ctx, t.TempDir())
		if err != nil || res.Files != 2 {
			t.Fatalf("%v, files %d", err, res.Files)
		}
	})
	t.Run("invalid keystore file is refused", func(t *testing.T) {
		dir := t.TempDir()
		writeKeystore(t, dir, map[string]string{"a.json": "-a"})
		if err := os.WriteFile(filepath.Join(dir, "keystore", "junk.json"), []byte("not a keystore"), 0o600); err != nil {
			t.Fatal(err)
		}
		dest := t.TempDir()
		_, err := newService(t, dir, fakeDB{version: testSchema}).Create(ctx, dest)
		wantInvalid(t, err)
		if n := len(mustReadDir(t, dest)); n != 0 {
			t.Errorf("failed Create left %d files", n)
		}
	})
	t.Run("unsafe name is refused", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			// NUL.json на Windows это устройство: файл не создать, проверять нечего.
			// Правило имён покрыто TestClassifyEntry.
			t.Skip("reserved device names cannot be created on Windows")
		}
		dir := t.TempDir()
		writeKeystore(t, dir, map[string]string{"NUL.json": "-x"})
		if _, err := newService(t, dir, fakeDB{version: testSchema}).Create(ctx, t.TempDir()); err == nil {
			t.Error("expected error for a reserved device name")
		}
	})
	t.Run("password change in progress is refused", func(t *testing.T) {
		dir := t.TempDir()
		writeKeystore(t, dir, map[string]string{"a.json": "-a"})
		_ = os.Mkdir(filepath.Join(dir, "keystore", keystoreStageDir), 0o700)
		dest := t.TempDir()
		if _, err := newService(t, dir, fakeDB{version: testSchema}).Create(ctx, dest); !errors.Is(err, errKeystoreBusy) {
			t.Fatalf("err = %v, want errKeystoreBusy", err)
		}
		if n := len(mustReadDir(t, dest)); n != 0 {
			t.Errorf("failed Create left %d files", n)
		}
	})
	t.Run("subdirectory is refused", func(t *testing.T) {
		dir := t.TempDir()
		writeKeystore(t, dir, map[string]string{"a.json": "-a"})
		_ = os.Mkdir(filepath.Join(dir, "keystore", "sub"), 0o700)
		if _, err := newService(t, dir, fakeDB{version: testSchema}).Create(ctx, t.TempDir()); err == nil {
			t.Error("expected error for a non-regular entry")
		}
	})
}

func TestCreateCanceledLeavesNothing(t *testing.T) {
	dir, dest := t.TempDir(), t.TempDir()
	cctx, cancel := context.WithCancel(ctx)
	time.AfterFunc(20*time.Millisecond, cancel)
	block := make(chan struct{})
	_, err := newService(t, dir, fakeDB{block: block}).Create(cctx, dest)
	if !errors.Is(err, context.Canceled) || domain.CodeOf(err) != domain.CodeCanceled {
		t.Fatalf("err = %v (code %q)", err, domain.CodeOf(err))
	}
	if n := len(mustReadDir(t, dest)); n != 0 {
		t.Errorf("canceled Create left %v", mustReadDir(t, dest))
	}
}

func TestRestoreRejectsBrokenArchivesAndKeepsData(t *testing.T) {
	good := func(t *testing.T, dir string) string {
		t.Helper()
		p := filepath.Join(dir, "in.zip")
		craft(t, p, goodEntries(t, map[string]string{"a.json": "-a"}))
		return p
	}
	// Рабочие данные, которые ни одна неудачная попытка не должна затронуть.
	setup := func(t *testing.T) (*Service, string, map[string]string) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "wallet.db"), fakeDBBytes(2), 0o600)
		writeKeystore(t, dir, map[string]string{"keep.json": "-keep"})
		return newService(t, dir, fakeDB{version: testSchema}), dir, tree(t, dir)
	}

	cases := map[string]func(t *testing.T, in string){
		"flipped byte": func(t *testing.T, in string) {
			b, _ := os.ReadFile(in)
			b[len(b)/2] ^= 0xFF
			_ = os.WriteFile(in, b, 0o600) // сумма осталась прежней
		},
		"missing checksum": func(t *testing.T, in string) { _ = os.Remove(in + checksumExt) },
		"garbage checksum": func(t *testing.T, in string) { _ = os.WriteFile(in+checksumExt, []byte("nope"), 0o600) },
		"truncated": func(t *testing.T, in string) {
			b, _ := os.ReadFile(in)
			_ = os.WriteFile(in, b[:len(b)/2], 0o600)
			writeSum(t, in) // сумма верна, но архив обрезан
		},
		"not a zip": func(t *testing.T, in string) {
			_ = os.WriteFile(in, []byte("plain text, definitely not a zip"), 0o600)
			writeSum(t, in)
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			svc, dir, before := setup(t)
			in := good(t, t.TempDir())
			corrupt(t, in)
			_, err := svc.Restore(ctx, in, secret.Bytes{})
			wantInvalid(t, err)
			sameTree(t, tree(t, dir), before)
		})
	}
}

func TestRestoreRejectsTamperedContent(t *testing.T) {
	db := fakeDBBytes(testSchema)
	ks := []byte(ksMagic + "-a")
	okFiles := []FileEntry{
		{Path: dbEntry, Size: int64(len(db)), SHA256: shaHex(db)},
		{Path: keystorePrefix + "a.json", Size: int64(len(ks)), SHA256: shaHex(ks)},
	}
	entries := func(manifest []byte, extra ...rawEntry) []rawEntry {
		return append([]rawEntry{{name: dbEntry, data: db}, {name: keystorePrefix + "a.json", data: ks}, {name: manifestEntry, data: manifest}}, extra...)
	}
	with := func(f func(fe []FileEntry) []FileEntry) []byte {
		cp := append([]FileEntry(nil), okFiles...)
		return manifestJSON(t, testSchema, f(cp))
	}

	cases := map[string][]rawEntry{
		"manifest hash of db is wrong": entries(with(func(fe []FileEntry) []FileEntry { fe[0].SHA256 = shaHex([]byte("x")); return fe })),
		"manifest size is wrong":       entries(with(func(fe []FileEntry) []FileEntry { fe[1].Size++; return fe })),
		"keystore content swapped":     {{name: dbEntry, data: db}, {name: keystorePrefix + "a.json", data: []byte(ksMagic + "-EVIL")}, {name: manifestEntry, data: with(func(fe []FileEntry) []FileEntry { return fe })}},
		"manifest drops a file":        entries(with(func(fe []FileEntry) []FileEntry { return fe[:1] })),
		"manifest lists a ghost": entries(with(func(fe []FileEntry) []FileEntry {
			return append(fe, FileEntry{Path: keystorePrefix + "ghost.json", Size: 1, SHA256: shaHex([]byte("g"))})
		})),
		"manifest lists a path twice": entries(with(func(fe []FileEntry) []FileEntry { return append(fe, fe[1]) })),
		"entry not in manifest":       entries(manifestJSON(t, testSchema, okFiles), rawEntry{name: keystorePrefix + "extra.json", data: []byte(ksMagic)}),
		"no manifest":                 {{name: dbEntry, data: db}},
		"manifest is not json":        entries([]byte("{not json")),
		"manifest unknown field":      entries([]byte(`{"format":1,"schema_version":3,"files":[],"evil":true}`)),
		"manifest trailing data":      entries(append(manifestJSON(t, testSchema, okFiles), []byte(`{"x":1}`)...)),
		"unsupported format":          entries([]byte(`{"format":99,"schema_version":3,"files":[]}`)),
		"schema newer than build":     entries(manifestJSON(t, testSchema+1, okFiles)),
		"db header disagrees": {{name: dbEntry, data: fakeDBBytes(2)}, {name: keystorePrefix + "a.json", data: ks}, {name: manifestEntry, data: with(func(fe []FileEntry) []FileEntry {
			d := fakeDBBytes(2)
			fe[0].Size, fe[0].SHA256 = int64(len(d)), shaHex(d)
			return fe
		})}},
		"db is not sqlite": {{name: dbEntry, data: []byte("not sqlite at all, but long enough to pass: " + strings.Repeat("x", 100))}, {name: keystorePrefix + "a.json", data: ks}, {name: manifestEntry, data: with(func(fe []FileEntry) []FileEntry {
			d := []byte("not sqlite at all, but long enough to pass: " + strings.Repeat("x", 100))
			fe[0].Size, fe[0].SHA256 = int64(len(d)), shaHex(d)
			return fe
		})}},
		"keystore header invalid": {{name: dbEntry, data: db}, {name: keystorePrefix + "a.json", data: []byte("JUNK")}, {name: manifestEntry, data: with(func(fe []FileEntry) []FileEntry {
			fe[1].Size, fe[1].SHA256 = 4, shaHex([]byte("JUNK"))
			return fe
		})}},
	}
	for name, es := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeKeystore(t, dir, map[string]string{"keep.json": "-keep"})
			_ = os.WriteFile(filepath.Join(dir, "wallet.db"), fakeDBBytes(2), 0o600)
			before := tree(t, dir)
			in := filepath.Join(t.TempDir(), "in.zip")
			craft(t, in, es)
			_, err := newService(t, dir, fakeDB{version: testSchema}).Restore(ctx, in, secret.Bytes{})
			wantInvalid(t, err)
			sameTree(t, tree(t, dir), before)
		})
	}
}

func TestRestoreBlocksZipSlipLinksAndOddEntries(t *testing.T) {
	db := fakeDBBytes(testSchema)
	bad := []rawEntry{
		{name: "../evil.txt", data: []byte("x")},
		{name: "keystore/../../evil.txt", data: []byte("x")},
		{name: "keystore/../evil.json", data: []byte("x")},
		{name: "/etc/evil", data: []byte("x")},
		{name: `keystore\..\evil.json`, data: []byte("x")},
		{name: `..\evil`, data: []byte("x")},
		{name: "C:/evil", data: []byte("x")},
		{name: "keystore/sub/a.json", data: []byte("x")},
		{name: "keystore/", data: nil, mode: os.ModeDir | 0o755},
		{name: "keystore/link.json", data: []byte("/etc/passwd"), mode: os.ModeSymlink | 0o777},
		{name: "keystore/CON.json", data: []byte(ksMagic)},
		{name: "keystore/.hidden", data: []byte(ksMagic)},
		{name: "keystore/a.json ", data: []byte(ksMagic)},
		{name: "db/../wallet.db", data: db},
		{name: "wallet.db", data: db},
	}
	for _, e := range bad {
		t.Run(e.name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "data")
			_ = os.MkdirAll(dir, 0o700)
			writeKeystore(t, dir, map[string]string{"keep.json": "-keep"})
			before := tree(t, parent)
			in := filepath.Join(t.TempDir(), "in.zip")
			craft(t, in, append(goodEntries(t, nil), e))
			_, err := newService(t, dir, fakeDB{version: testSchema}).Restore(ctx, in, secret.Bytes{})
			wantInvalid(t, err)
			sameTree(t, tree(t, parent), before) // ничего не появилось ни внутри, ни рядом
		})
	}
}

func TestRestoreEnforcesLimits(t *testing.T) {
	run := func(t *testing.T, lim Limits, entries []rawEntry) error {
		dir := t.TempDir()
		in := filepath.Join(t.TempDir(), "in.zip")
		craft(t, in, entries)
		_, err := newService(t, dir, fakeDB{version: testSchema}, func(c *Config) { c.Limits = lim }).Restore(ctx, in, secret.Bytes{})
		return err
	}
	ks := map[string]string{"a.json": "-a", "b.json": "-b", "c.json": "-c"}
	t.Run("too many entries", func(t *testing.T) { wantInvalid(t, run(t, Limits{MaxFiles: 3}, goodEntries(t, ks))) })
	t.Run("db too big", func(t *testing.T) { wantInvalid(t, run(t, Limits{MaxDBSize: 1024}, goodEntries(t, nil))) })
	t.Run("keystore file too big", func(t *testing.T) {
		wantInvalid(t, run(t, Limits{MaxKeystoreFileSize: 3}, goodEntries(t, map[string]string{"a.json": "-a"})))
	})
	t.Run("total too big", func(t *testing.T) { wantInvalid(t, run(t, Limits{MaxTotalSize: 4100}, goodEntries(t, ks))) })
	t.Run("within limits passes", func(t *testing.T) {
		if err := run(t, Limits{MaxFiles: 10, MaxTotalSize: 1 << 20}, goodEntries(t, ks)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("bomb: data longer than the manifest says", func(t *testing.T) {
		db := fakeDBBytes(testSchema)
		long := append(append([]byte{}, db...), make([]byte, 1<<20)...)
		wantInvalid(t, run(t, Limits{}, []rawEntry{
			{name: dbEntry, data: long},
			{name: manifestEntry, data: manifestJSON(t, testSchema, []FileEntry{{Path: dbEntry, Size: int64(len(db)), SHA256: shaHex(db)}})},
		}))
	})
}

func TestRestoreCanceledBeforeSwapKeepsData(t *testing.T) {
	dir := t.TempDir()
	writeKeystore(t, dir, map[string]string{"keep.json": "-keep"})
	before := tree(t, dir)
	in := filepath.Join(t.TempDir(), "in.zip")
	craft(t, in, goodEntries(t, map[string]string{"a.json": "-a"}))
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err := newService(t, dir, fakeDB{version: testSchema}).Restore(cctx, in, secret.Bytes{})
	if !errors.Is(err, context.Canceled) || domain.CodeOf(err) != domain.CodeCanceled {
		t.Fatalf("err = %v", err)
	}
	sameTree(t, tree(t, dir), before)
}

// Сбой на любом шаге замены откатывает все предыдущие переименования.
func TestSwapRollsBackOnFailureAtEveryStep(t *testing.T) {
	for failAt := 1; failAt <= 4; failAt++ {
		t.Run(fmt.Sprint("fail at rename ", failAt), func(t *testing.T) {
			dir := t.TempDir()
			_ = os.WriteFile(filepath.Join(dir, "wallet.db"), fakeDBBytes(2), 0o600)
			_ = os.WriteFile(filepath.Join(dir, "wallet.db-wal"), []byte("wal"), 0o600)
			writeKeystore(t, dir, map[string]string{"keep.json": "-keep"})
			before := tree(t, dir)
			in := filepath.Join(t.TempDir(), "in.zip")
			craft(t, in, goodEntries(t, map[string]string{"a.json": "-a"}))

			svc := newService(t, dir, fakeDB{version: testSchema})
			var n atomic.Int32
			svc.rename = func(o, nw string) error {
				// Откат тоже вызывает rename; ломаем только первые попытки вперёд.
				if int(n.Add(1)) == failAt {
					return errors.New("injected failure")
				}
				return os.Rename(o, nw)
			}
			if _, err := svc.Restore(ctx, in, secret.Bytes{}); err == nil {
				t.Fatal("expected error")
			}
			sameTree(t, tree(t, dir), before)
		})
	}
}

// Снимок согласован, даже если приложение пишет в БД прямо во время копирования.
func TestBackupWhileWriting(t *testing.T) {
	src := t.TempDir()
	db := seed(t, src)
	svc := newService(t, src, db, func(c *Config) { c.MaxSchemaVersion = store.SchemaVersion() })

	var (
		stop    atomic.Bool
		wg      sync.WaitGroup
		writeEr atomic.Value
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; !stop.Load(); i++ {
			// Баланс и число записей истории меняются одной транзакцией: в
			// согласованном снимке они всегда равны.
			err := db.WithTx(ctx, func(c context.Context, tx storeTx) error {
				if err := tx.Transactions().Upsert(c, []domain.Tx{{
					WalletID: "w1", Asset: domain.BTC, TxID: domain.TxID(fmt.Sprint("t", i)), Direction: domain.DirectionIn,
					Amount: domain.AmountFromUint64(1), Fee: domain.AmountFromUint64(0), Status: domain.TxConfirmed, Time: time.UnixMilli(int64(i)),
				}}); err != nil {
					return err
				}
				return tx.Balances().Put(c, "w1", domain.BTC, domain.Balance{Confirmed: domain.AmountFromUint64(uint64(i))}, time.UnixMilli(int64(i)))
			})
			if err != nil {
				writeEr.Store(err)
				return
			}
		}
	}()

	var archives []string
	for range 6 {
		res, err := svc.Create(ctx, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		archives = append(archives, res.Path)
		time.Sleep(5 * time.Millisecond)
	}
	stop.Store(true)
	wg.Wait()
	if err, _ := writeEr.Load().(error); err != nil {
		t.Fatalf("writer failed during backup: %v", err)
	}

	for i, a := range archives {
		dst := t.TempDir()
		if _, err := newService(t, dst, db, func(c *Config) { c.MaxSchemaVersion = store.SchemaVersion() }).Restore(ctx, a, secret.Bytes{}); err != nil {
			t.Fatalf("restore %d: %v", i, err)
		}
		rs := openStore(t, dst)
		txs, err := rs.Transactions().List(ctx, "w1", domain.BTC)
		if err != nil {
			t.Fatal(err)
		}
		bal, err := rs.Balances().Get(ctx, "w1", domain.BTC)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			t.Fatal(err)
		}
		if got := bal.Balance.Confirmed.String(); got != fmt.Sprint(len(txs)) && (!errors.Is(err, domain.ErrNotFound) || len(txs) != 0) {
			t.Errorf("archive %d is inconsistent: balance %s, %d transactions", i, got, len(txs))
		}
	}
}

func TestNewValidatesConfig(t *testing.T) {
	ok := Config{DataDir: "d", DB: fakeDB{}, CheckKeystoreFile: checkKS, MaxSchemaVersion: 1}
	for name, mut := range map[string]func(*Config){
		"no data dir":   func(c *Config) { c.DataDir = "" },
		"no db":         func(c *Config) { c.DB = nil },
		"no checker":    func(c *Config) { c.CheckKeystoreFile = nil },
		"no max schema": func(c *Config) { c.MaxSchemaVersion = 0 },
		"db name path":  func(c *Config) { c.DBName = "a/b.db" },
		"ks name path":  func(c *Config) { c.KeystoreDirName = "../ks" },
	} {
		c := ok
		mut(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := New(ok); err != nil {
		t.Fatal(err)
	}
}
