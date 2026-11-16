package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/store"
	"github.com/24DSO-lab/wallet/internal/secret"
)

var ctx = context.Background()

const testSchema = 3

// ksMagic это «заголовок» тестового keystore-файла; настоящий разбор делает Dev 2.
const ksMagic = "WKS1"

func checkKS(name string, data []byte) error {
	if !bytes.HasPrefix(data, []byte(ksMagic)) {
		return errors.New("bad keystore header")
	}
	return nil
}

// fakeDB пишет минимальный файл с заголовком SQLite и заданной версией схемы.
type fakeDB struct {
	version uint32
	block   <-chan struct{} // если задан, Snapshot ждёт отмены
}

func (f fakeDB) Snapshot(ctx context.Context, dst string) error {
	if f.block != nil {
		<-ctx.Done()
		return ctx.Err()
	}
	return os.WriteFile(dst, fakeDBBytes(f.version), 0o600)
}

func fakeDBBytes(version uint32) []byte {
	b := make([]byte, 4096)
	copy(b, sqliteMagic)
	binary.BigEndian.PutUint32(b[60:], version)
	return b
}

func newService(t *testing.T, dir string, db Snapshotter, mut ...func(*Config)) *Service {
	t.Helper()
	cfg := Config{
		DataDir: dir, DB: db, CheckKeystoreFile: checkKS, MaxSchemaVersion: testSchema,
		VerifyKeystorePassword: func(context.Context, string, secret.Bytes) error { return nil },
		Now: func() time.Time { return time.Date(2026, 11, 15, 10, 30, 0, 0, time.UTC) },
	}
	for _, m := range mut {
		m(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeKeystore(t *testing.T, dataDir string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(dataDir, "keystore")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(ksMagic+body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// tree возвращает путь -> содержимое всех файлов каталога: для проверки «ничего не изменилось».
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("tree changed: got %v, want %v", keys(got), keys(want))
		return
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s changed or missing", k)
		}
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// rawEntry это запись для рукодельного архива.
type rawEntry struct {
	name string
	data []byte
	mode os.FileMode
}

// craft пишет архив из заданных записей без проверок и сразу кладёт рядом верную сумму,
// чтобы дальше работали именно проверки содержимого, а не суммы.
func craft(t *testing.T, path string, entries []rawEntry) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	writeSum(t, path)
}

func writeSum(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if err := os.WriteFile(path+checksumExt, []byte(hex.EncodeToString(sum[:])+"  "+filepath.Base(path)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func shaHex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// goodEntries возвращает корректный набор записей с верным манифестом.
func goodEntries(t *testing.T, ks map[string]string) []rawEntry {
	t.Helper()
	db := fakeDBBytes(testSchema)
	files := []FileEntry{{Path: dbEntry, Size: int64(len(db)), SHA256: shaHex(db)}}
	out := []rawEntry{{name: dbEntry, data: db}}
	for name, body := range ks {
		d := []byte(ksMagic + body)
		files = append(files, FileEntry{Path: keystorePrefix + name, Size: int64(len(d)), SHA256: shaHex(d)})
		out = append(out, rawEntry{name: keystorePrefix + name, data: d})
	}
	return append(out, rawEntry{name: manifestEntry, data: manifestJSON(t, testSchema, files)})
}

func manifestJSON(t *testing.T, schema int, files []FileEntry) []byte {
	t.Helper()
	b, err := json.Marshal(Manifest{Format: FormatVersion, CreatedAt: time.Unix(0, 0).UTC(), SchemaVersion: schema, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func wantInvalid(t *testing.T, err error) {
	t.Helper()
	if domain.CodeOf(err) != domain.CodeBackupInvalid {
		t.Fatalf("code = %q, want %q; err: %v", domain.CodeOf(err), domain.CodeBackupInvalid, err)
	}
}

// openStore открывает БД из восстановленных данных: доказывает, что копия жива.
func openStore(t *testing.T, dir string) *store.DB {
	t.Helper()
	d, err := store.Open(ctx, filepath.Join(dir, "wallet.db"))
	if err != nil {
		t.Fatalf("open restored store: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func osWriteFile(p string, b []byte) error { return os.WriteFile(p, b, 0o600) }

// storeTx это ports.Store: короткий алиас для сигнатур в тестах.
type storeTx = ports.Store
