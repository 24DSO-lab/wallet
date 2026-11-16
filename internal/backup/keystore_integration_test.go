package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd/hdtest"
	"github.com/24DSO-lab/wallet/internal/keystore"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
	"github.com/24DSO-lab/wallet/internal/store"
)

// Круг с настоящим keystore Dev 2: копия содержит его файлы как есть, после
// восстановления пароль подходит, а seed тот же. Backup пароля не видит.
func TestRoundTripWithRealKeystore(t *testing.T) {
	// Параметры не ниже минимума OWASP, иначе keystore.New их не примет.
	kdf := &keystore.Params{Time: 2, MemoryKiB: 19 * 1024, Threads: 1}
	newKS := func(dir string) *keystore.Store {
		t.Helper()
		ks, err := keystore.New(keystore.Config{
			Dir: filepath.Join(dir, "keystore"), Profile: domain.ProfileTest, KDF: kdf,
			Addresses: map[domain.Network]keystore.AddressFunc{
				domain.NetworkEthereum: hdtest.ETHAddress,
				domain.NetworkTron:     hdtest.TronAddress,
				domain.NetworkBitcoin:  func(pub []byte) (string, error) { return hdtest.P2WPKHFromPublicKey("tb", pub) },
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return ks
	}
	const password = "correct horse battery staple 42"
	secrets := func(pw string) ports.Secrets { return ports.Secrets{Password: secret.FromString(pw)} }

	src, dest, dst := t.TempDir(), t.TempDir(), t.TempDir()
	ks := newKS(src)
	words, err := ks.NewMnemonic(12)
	if err != nil {
		t.Fatal(err)
	}
	seed := string(words.Expose())
	if _, err := ks.Save(ctx, "w1", words, secrets(password)); err != nil {
		t.Fatal(err)
	}
	db := seed0(t, src)

	real := func(c *Config) {
		c.CheckKeystoreFile = keystore.CheckFile
		c.MaxSchemaVersion = store.SchemaVersion()
		c.VerifyKeystorePassword = func(ctx context.Context, dir string, password secret.Bytes) error {
			staged, err := keystore.New(keystore.Config{
				Dir: dir, Profile: domain.ProfileTest, KDF: kdf,
				Addresses: map[domain.Network]keystore.AddressFunc{
					domain.NetworkEthereum: hdtest.ETHAddress,
					domain.NetworkTron:     hdtest.TronAddress,
					domain.NetworkBitcoin:  func(pub []byte) (string, error) { return hdtest.P2WPKHFromPublicKey("tb", pub) },
				},
			})
			if err != nil {
				return err
			}
			return staged.Verify(ctx, ports.Secrets{Password: password})
		}
	}
	res, err := newService(t, src, db, real).Create(ctx, dest)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Files != 2 { // БД и один keystore-файл
		t.Errorf("Files = %d", res.Files)
	}
	passwordBytes := secret.FromString(password)
	defer passwordBytes.Wipe()
	if _, err := newService(t, dst, db, real).Restore(ctx, res.Path, passwordBytes); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Файл побайтно тот же.
	a, _ := os.ReadFile(filepath.Join(src, "keystore", "w1.keystore"))
	b, err := os.ReadFile(filepath.Join(dst, "keystore", "w1.keystore"))
	if err != nil || string(a) != string(b) {
		t.Fatalf("keystore file differs after restore (err %v)", err)
	}

	restored := newKS(dst)
	if err := restored.Verify(ctx, secrets(password)); err != nil {
		t.Fatalf("Verify after restore: %v", err)
	}
	if err := restored.Verify(ctx, secrets("wrong password")); err == nil {
		t.Error("wrong password accepted after restore")
	}
	got, err := restored.RevealSeed(ctx, "w1", secrets(password))
	if err != nil || string(got.Expose()) != seed {
		t.Errorf("seed changed after restore: %v", err)
	}
}

// CheckFile Dev 2 отвергает чужой и переименованный файл, а значит и копию с таким файлом.
func TestCreateRefusesBrokenRealKeystoreFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "keystore"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keystore", "w1.keystore"), []byte("not a keystore"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := newService(t, dir, fakeDB{version: testSchema}, func(c *Config) { c.CheckKeystoreFile = keystore.CheckFile })
	_, err := svc.Create(ctx, t.TempDir())
	if domain.CodeOf(err) == "" {
		t.Fatalf("expected a coded error, got %v", err)
	}
}

// seed0 открывает настоящую БД с одним кошельком.
func seed0(t *testing.T, dir string) *store.DB {
	t.Helper()
	db, err := store.Open(ctx, filepath.Join(dir, "wallet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	w := domain.Wallet{PublicWallet: domain.PublicWallet{ID: "w1", Name: "n"}, KeystoreID: "w1"}
	if err := db.Wallets().Create(ctx, w); err != nil {
		t.Fatal(err)
	}
	return db
}
