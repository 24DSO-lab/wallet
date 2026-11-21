package keystore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd"
	"github.com/24DSO-lab/wallet/internal/hd/hdtest"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
)

const (
	abandon    = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	zooMnem    = "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo wrong"
	pwd        = "correct-horse-PASSWORD-1"
	newPwd     = "battery-staple-NEWPWD-2"
	passphrase = "pp-SECRET-phrase-3"
)

// Известные адреса для abandon…about без парольной фразы (см. гейт в
// internal/hd): ETH m/44'/60'/0'/0/0 и TRON m/44'/195'/0'/0/0.
const (
	abandonETH  = "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"
	abandonTron = "TUEZSdKsoDHQMeZwihtdoBiN46zxhGWYdH"
)

func TestNewHardensExistingDirectoryPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permissions are governed by inherited ACLs")
	}
	dir := filepath.Join(t.TempDir(), "keystore")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Dir: dir, Profile: domain.ProfileTest, KDF: weak(), allowWeakKDF: true, Addresses: addrs(domain.ProfileTest)})
	if err != nil {
		t.Fatal(err)
	}
	_ = s
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("keystore directory permissions = %04o, want 0700", got)
	}
}

func weak() *Params { return &Params{Time: 1, MemoryKiB: 64, Threads: 1} }

func addrs(profile domain.Profile) map[domain.Network]AddressFunc {
	hrp := "bc"
	if profile == domain.ProfileTest {
		hrp = "tb"
	}
	return map[domain.Network]AddressFunc{
		domain.NetworkEthereum: hdtest.ETHAddress,
		domain.NetworkTron:     hdtest.TronAddress,
		domain.NetworkBitcoin:  func(pub []byte) (string, error) { return hdtest.P2WPKHFromPublicKey(hrp, pub) },
	}
}

type env struct {
	t    *testing.T
	s    *Store
	dir  string
	logs *bytes.Buffer
}

func newEnv(t *testing.T, profile domain.Profile) *env {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "keystore")
	logs := &bytes.Buffer{}
	s, err := New(Config{Dir: dir, Profile: profile, Addresses: addrs(profile), KDF: weak(), allowWeakKDF: true,
		Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, s: s, dir: dir, logs: logs}
}

func (e *env) reopen() *Store {
	e.t.Helper()
	s, err := New(Config{Dir: e.dir, Profile: domain.ProfileMain, Addresses: addrs(domain.ProfileMain), KDF: weak(), allowWeakKDF: true})
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func secrets(password, pass string) ports.Secrets {
	return ports.Secrets{Password: secret.FromString(password), Passphrase: secret.FromString(pass)}
}

func (e *env) save(id, mnemonic, password, pass string) ports.PublicKeys {
	e.t.Helper()
	pk, err := e.s.Save(context.Background(), id, secret.FromString(mnemonic), secrets(password, pass))
	if err != nil {
		e.t.Fatalf("Save(%s): %v", id, err)
	}
	return pk
}

func wantCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", code)
	}
	if got := domain.CodeOf(err); got != code {
		t.Fatalf("error code %s (%v), want %s", got, err, code)
	}
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSaveVerifyRevealRoundTrip(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	pk := e.save("w1", abandon, pwd, "")
	if pk.ETHAddress != abandonETH || pk.TronAddress != abandonTron || !strings.HasPrefix(pk.BTCXpub, "xpub") {
		t.Fatalf("unexpected public keys: %+v", pk)
	}
	ctx := context.Background()
	if err := e.s.Verify(ctx, secrets(pwd, "")); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	m, err := e.s.RevealSeed(ctx, "w1", secrets(pwd, ""))
	if err != nil || string(m.Expose()) != abandon {
		t.Fatalf("RevealSeed = %q, %v", m.Expose(), err)
	}
	m.Wipe()

	// Парольная фраза меняет адреса, но не файл: мнемоника в файле одна.
	pk2 := e.save("w2", abandon, pwd, passphrase)
	if pk2.ETHAddress == pk.ETHAddress || pk2.TronAddress == pk.TronAddress || pk2.BTCXpub == pk.BTCXpub {
		t.Error("passphrase did not change derived data")
	}
	m, _ = e.s.RevealSeed(ctx, "w2", secrets(pwd, "ignored"))
	if string(m.Expose()) != abandon {
		t.Error("stored mnemonic differs")
	}
}

func TestSaveNormalizesAndValidates(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("a", "  ABANDON "+strings.TrimPrefix(abandon, "abandon "), pwd, "")
	m, _ := e.s.RevealSeed(context.Background(), "a", secrets(pwd, ""))
	if string(m.Expose()) != abandon {
		t.Errorf("stored form %q is not canonical", m.Expose())
	}
	_, err := e.s.Save(context.Background(), "bad", secret.FromString(strings.Replace(abandon, "about", "abandon", 1)), secrets(pwd, ""))
	wantCode(t, err, domain.CodeInvalidInput)
	if exists(e.s.path("bad")) {
		t.Error("a file was written for an invalid mnemonic")
	}
	_, err = e.s.Save(context.Background(), "empty-pass", secret.FromString(abandon), secrets("", ""))
	wantCode(t, err, domain.CodeInvalidInput)
}

func TestInvalidIDs(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	ctx := context.Background()
	for _, id := range []string{"", "../x", "a/b", `a\b`, "x.y", ".hidden", strings.Repeat("a", 65), "a b", "é"} {
		_, err := e.s.Save(ctx, id, secret.FromString(abandon), secrets(pwd, ""))
		wantCode(t, err, domain.CodeInvalidInput)
		_, err = e.s.RevealSeed(ctx, id, secrets(pwd, ""))
		wantCode(t, err, domain.CodeInvalidInput)
		wantCode(t, e.s.Delete(ctx, id, secrets(pwd, "")), domain.CodeInvalidInput)
		_, err = e.s.Sign(ctx, id, secrets(pwd, ""), []ports.DigestRequest{{}})
		wantCode(t, err, domain.CodeInvalidInput)
	}
	entries, _ := os.ReadDir(filepath.Dir(e.dir))
	if len(entries) != 1 {
		t.Errorf("files appeared outside the keystore directory: %v", entries)
	}
}

func TestOnePasswordForAllWallets(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	_, err := e.s.Save(context.Background(), "w2", secret.FromString(zooMnem), secrets("another-password", ""))
	wantCode(t, err, domain.CodeWrongPassword)
	if exists(e.s.path("w2")) {
		t.Error("wallet with a different password was stored")
	}
	e.save("w2", zooMnem, pwd, "")
	_, err = e.s.Save(context.Background(), "w2", secret.FromString(zooMnem), secrets(pwd, ""))
	if !errors.Is(err, ErrExists) {
		t.Errorf("overwrite of an existing wallet: %v", err)
	}
}

func TestWrongPassword(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	before := readBytes(t, e.s.path("w1"))
	ctx := context.Background()
	bad := secrets("wrong-password", "")
	wantCode(t, e.s.Verify(ctx, bad), domain.CodeWrongPassword)
	_, err := e.s.RevealSeed(ctx, "w1", bad)
	wantCode(t, err, domain.CodeWrongPassword)
	wantCode(t, e.s.Delete(ctx, "w1", bad), domain.CodeWrongPassword)
	wantCode(t, e.s.Delete(ctx, "w1", secrets("", "")), domain.CodeWrongPassword)
	_, err = e.s.Sign(ctx, "w1", bad, []ports.DigestRequest{ethRequest(abandonETH)})
	wantCode(t, err, domain.CodeWrongPassword)
	wantCode(t, e.s.ChangePassword(ctx, secret.FromString("wrong-password"), secret.FromString(newPwd)), domain.CodeWrongPassword)
	if !bytes.Equal(before, readBytes(t, e.s.path("w1"))) {
		t.Error("a failed operation modified the file")
	}
}

func TestVerifyAndOpenMissing(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	ctx := context.Background()
	wantCode(t, e.s.Verify(ctx, secrets(pwd, "")), domain.CodeNotFound)
	_, err := e.s.RevealSeed(ctx, "nope", secrets(pwd, ""))
	wantCode(t, err, domain.CodeNotFound)
	wantCode(t, e.s.ChangePassword(ctx, secret.FromString(pwd), secret.FromString(newPwd)), domain.CodeNotFound)
}

func TestDelete(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	e.save("w2", zooMnem, pwd, "")
	if err := e.s.Delete(context.Background(), "w1", secrets(pwd, "")); err != nil {
		t.Fatal(err)
	}
	if exists(e.s.path("w1")) || !exists(e.s.path("w2")) {
		t.Error("wrong file removed")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(e.s.path("w2"))
		if info.Mode().Perm() != 0o600 {
			t.Errorf("file mode %v, want 0600", info.Mode().Perm())
		}
	}
}

func TestContextCancelled(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := e.s.Save(ctx, "w1", secret.FromString(abandon), secrets(pwd, ""))
	wantCode(t, err, domain.CodeCanceled)
	if exists(e.s.path("w1")) {
		t.Error("cancelled Save wrote a file")
	}
	dctx, dcancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer dcancel()
	wantCode(t, e.s.Verify(dctx, secrets(pwd, "")), domain.CodeTimeout)
}

// --- повреждения файла ---

func TestEveryByteFlipIsRejected(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	orig := readBytes(t, e.s.path("w1"))
	for i := range orig {
		for _, mask := range []byte{0x01, 0x80} {
			if mask == 0x80 && i >= 11 && i <= 13 {
				continue // старшие байты памяти KDF: не гоняем сотни МиБ
			}
			bad := append([]byte(nil), orig...)
			bad[i] ^= mask
			if err := os.WriteFile(e.s.path("w1"), bad, 0o600); err != nil {
				t.Fatal(err)
			}
			m, err := e.s.RevealSeed(context.Background(), "w1", secrets(pwd, ""))
			if err == nil {
				m.Wipe()
				t.Fatalf("byte %d mask %#x: modified file was accepted", i, mask)
			}
			if c := domain.CodeOf(err); c != domain.CodeKeystoreCorrupt && c != domain.CodeWrongPassword {
				t.Fatalf("byte %d: unexpected code %s", i, c)
			}
		}
	}
}

func TestTruncatedExtendedAndForeignFiles(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	orig := readBytes(t, e.s.path("w1"))
	cases := map[string][]byte{
		"empty":    {},
		"magic":    orig[:4],
		"cut tag":  orig[:len(orig)-1],
		"half":     orig[:len(orig)/2],
		"extended": append(append([]byte(nil), orig...), 0),
		"zeros":    make([]byte, len(orig)),
		"text":     []byte("this is not a keystore"),
		"huge":     bytes.Repeat([]byte{1}, maxFileSize+1),
	}
	for name, data := range cases {
		if err := os.WriteFile(e.s.path("w1"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := e.s.RevealSeed(context.Background(), "w1", secrets(pwd, ""))
		if domain.CodeOf(err) != domain.CodeKeystoreCorrupt {
			t.Errorf("%s: code %s (%v), want keystore_corrupt", name, domain.CodeOf(err), err)
		}
	}
}

func TestHeaderParametersAreAuthenticated(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	orig := readBytes(t, e.s.path("w1"))
	// Подмена числа проходов (1 -> 2) оставляет заголовок допустимым, но
	// ключ меняется, и AAD тоже: расшифровка обязана провалиться.
	bad := append([]byte(nil), orig...)
	bad[10] = 2
	_ = os.WriteFile(e.s.path("w1"), bad, 0o600)
	_, err := e.s.RevealSeed(context.Background(), "w1", secrets(pwd, ""))
	wantCode(t, err, domain.CodeWrongPassword)
	// Заголовок нового файла с параметрами старого KDF для другого пароля.
	h, _, _, err2 := parseFile(orig)
	if err2 != nil || h.params != *weak() || h.id != "w1" {
		t.Fatalf("header: %+v, %v", h, err2)
	}
}

func TestFileSwapBetweenWalletsIsRejected(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	e.save("w2", zooMnem, pwd, "")
	if err := os.WriteFile(e.s.path("w2"), readBytes(t, e.s.path("w1")), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := e.s.RevealSeed(context.Background(), "w2", secrets(pwd, ""))
	wantCode(t, err, domain.CodeKeystoreCorrupt)
}

func TestSymlinkIsNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	target := filepath.Join(t.TempDir(), "elsewhere")
	_ = os.WriteFile(target, readBytes(t, e.s.path("w1")), 0o600)
	_ = os.Remove(e.s.path("w1"))
	if err := os.Symlink(target, e.s.path("w1")); err != nil {
		t.Skip(err)
	}
	_, err := e.s.RevealSeed(context.Background(), "w1", secrets(pwd, ""))
	wantCode(t, err, domain.CodeKeystoreCorrupt)
	ids, _ := e.s.listIDs()
	if len(ids) != 0 {
		t.Errorf("symlink listed as a wallet: %v", ids)
	}
}

// --- параметры KDF ---

func TestKDFParametersBoundsAndFloor(t *testing.T) {
	dir := t.TempDir()
	for name, p := range map[string]Params{
		"below floor":  {Time: 1, MemoryKiB: 8 * 1024, Threads: 1},
		"memory floor": {Time: 2, MemoryKiB: 19*1024 - 1, Threads: 1},
		"too much":     {Time: 2, MemoryKiB: maxMemoryKiB + 1, Threads: 1},
		"no threads":   {Time: 2, MemoryKiB: 19 * 1024, Threads: 0},
	} {
		p := p
		if _, err := New(Config{Dir: dir, Profile: domain.ProfileMain, KDF: &p}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := New(Config{Dir: dir, Profile: domain.ProfileMain, KDF: &Params{Time: 2, MemoryKiB: 19 * 1024, Threads: 1}}); err != nil {
		t.Errorf("OWASP minimum rejected: %v", err)
	}
	if _, err := New(Config{Dir: dir, Profile: "nope"}); err == nil {
		t.Error("unknown profile accepted")
	}
	if _, err := New(Config{Profile: domain.ProfileMain}); err == nil {
		t.Error("empty dir accepted")
	}
}

func TestCalibrateStaysAboveFloor(t *testing.T) {
	p := Calibrate(time.Nanosecond) // ничего не укладывается: возвращается минимум
	if !p.atLeastFloor() || !p.withinReadBounds() || p.MemoryKiB != minMemoryKiB {
		t.Errorf("Calibrate(1ns) = %+v", p)
	}
	p = Calibrate(DefaultTarget)
	if !p.atLeastFloor() || p.MemoryKiB > calibrateMaxKiB {
		t.Errorf("Calibrate = %+v", p)
	}
}

func TestStrengthenedParametersApplyOnNextWrite(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	h, _, _, _ := parseFile(readBytes(t, e.s.path("w1")))
	if h.params != *weak() {
		t.Fatalf("initial params %+v", h.params)
	}
	// Новый процесс с более сильными параметрами читает старый файл как есть...
	stronger := Params{Time: 2, MemoryKiB: 128, Threads: 2}
	s, err := New(Config{Dir: e.dir, Profile: domain.ProfileMain, Addresses: addrs(domain.ProfileMain), KDF: &stronger, allowWeakKDF: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(context.Background(), secrets(pwd, "")); err != nil {
		t.Fatalf("old file with weaker parameters must open: %v", err)
	}
	// ...и записывает файл с новыми параметрами при смене пароля.
	if err := s.ChangePassword(context.Background(), secret.FromString(pwd), secret.FromString(newPwd)); err != nil {
		t.Fatal(err)
	}
	h, _, _, _ = parseFile(readBytes(t, s.path("w1")))
	if h.params != stronger {
		t.Errorf("params after ChangePassword %+v, want %+v", h.params, stronger)
	}
	if err := s.Verify(context.Background(), secrets(newPwd, "")); err != nil {
		t.Fatal(err)
	}
}

func TestFreshSaltAndNonceEveryWrite(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	e.save("w2", abandon, pwd, "")
	a, _, _, _ := parseFile(readBytes(t, e.s.path("w1")))
	b, _, _, _ := parseFile(readBytes(t, e.s.path("w2")))
	if a.salt == b.salt || a.nonce == b.nonce {
		t.Error("salt or nonce repeated for the same mnemonic and password")
	}
}

// --- смена пароля, откат и обрыв ---

func snapshot(t *testing.T, e *env, ids ...string) map[string][]byte {
	t.Helper()
	m := map[string][]byte{}
	for _, id := range ids {
		m[id] = readBytes(t, e.s.path(id))
	}
	return m
}

func assertPassword(t *testing.T, s *Store, ids []string, password string) {
	t.Helper()
	for _, id := range ids {
		m, err := s.RevealSeed(context.Background(), id, secrets(password, ""))
		if err != nil {
			t.Fatalf("wallet %s with password %q: %v", id, password, err)
		}
		m.Wipe()
	}
}

func twoWallets(t *testing.T) (*env, []string) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	e.save("w2", zooMnem, pwd, "")
	return e, []string{"w1", "w2"}
}

func TestChangePassword(t *testing.T) {
	e, ids := twoWallets(t)
	if err := e.s.ChangePassword(context.Background(), secret.FromString(pwd), secret.FromString(newPwd)); err != nil {
		t.Fatal(err)
	}
	assertPassword(t, e.s, ids, newPwd)
	_, err := e.s.RevealSeed(context.Background(), "w1", secrets(pwd, ""))
	wantCode(t, err, domain.CodeWrongPassword)
	if exists(e.s.stagePath()) {
		t.Error("staging directory left behind")
	}
	m, _ := e.s.RevealSeed(context.Background(), "w2", secrets(newPwd, ""))
	if string(m.Expose()) != zooMnem {
		t.Error("mnemonic changed")
	}
	wantCode(t, e.s.ChangePassword(context.Background(), secret.FromString(pwd), secret.Bytes{}), domain.CodeInvalidInput)
}

func TestChangePasswordWithOneBadFileChangesNothing(t *testing.T) {
	e, ids := twoWallets(t)
	before := snapshot(t, e, ids...)
	_ = os.WriteFile(e.s.path("w2"), []byte("garbage"), 0o600)
	before["w2"] = readBytes(t, e.s.path("w2"))
	err := e.s.ChangePassword(context.Background(), secret.FromString(pwd), secret.FromString(newPwd))
	wantCode(t, err, domain.CodeKeystoreCorrupt)
	for id, b := range before {
		if !bytes.Equal(b, readBytes(t, e.s.path(id))) {
			t.Errorf("%s modified", id)
		}
	}
	if exists(e.s.stagePath()) {
		t.Error("staging directory left behind")
	}
}

func TestChangePasswordRollsBackOnError(t *testing.T) {
	e, ids := twoWallets(t)
	before := snapshot(t, e, ids...)
	boom := errors.New("disk failure")
	e.s.faults.beforeRename = func(i int) error {
		if i == 1 {
			return boom
		}
		return nil
	}
	err := e.s.ChangePassword(context.Background(), secret.FromString(pwd), secret.FromString(newPwd))
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	for id, b := range before {
		if !bytes.Equal(b, readBytes(t, e.s.path(id))) {
			t.Errorf("%s was not restored", id)
		}
	}
	assertPassword(t, e.s, ids, pwd)
	if exists(e.s.stagePath()) {
		t.Error("staging directory left behind")
	}
}

func TestCrashRecovery(t *testing.T) {
	type tc struct {
		name     string
		inject   func(s *Store)
		wantPass string
	}
	calls := 0
	tests := []tc{
		{"crash before COMMIT is discarded", func(s *Store) {
			s.faults.afterTemp = func() error {
				calls++
				if calls == 5 { // 2 файла x (new, old) = 4 записи, пятая это COMMIT
					return errSimulatedCrash
				}
				return nil
			}
		}, pwd},
		{"crash right after COMMIT rolls forward", func(s *Store) {
			s.faults.afterCommit = func() error { return errSimulatedCrash }
		}, newPwd},
		{"crash between renames rolls forward", func(s *Store) {
			s.faults.beforeRename = func(i int) error {
				if i == 1 {
					return errSimulatedCrash
				}
				return nil
			}
		}, newPwd},
		{"crash during rollback rolls back", func(s *Store) {
			s.faults.beforeRename = func(i int) error {
				if i == 1 {
					return errors.New("disk failure")
				}
				return nil
			}
			s.faults.afterTemp = func() error {
				calls++
				if calls == 6 { // первая запись восстановления
					return errSimulatedCrash
				}
				return nil
			}
		}, pwd},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			calls = 0
			e, ids := twoWallets(t)
			c.inject(e.s)
			err := e.s.ChangePassword(context.Background(), secret.FromString(pwd), secret.FromString(newPwd))
			if err == nil {
				t.Fatal("the injected failure did not happen")
			}
			// Перезапуск: New обязан привести каталог в согласованное состояние.
			s := e.reopen()
			assertPassword(t, s, ids, c.wantPass)
			if exists(s.stagePath()) {
				t.Error("staging directory left behind after recovery")
			}
			entries, _ := os.ReadDir(e.dir)
			for _, en := range entries {
				if strings.HasPrefix(en.Name(), tmpPrefix) {
					t.Errorf("stale temp file %s", en.Name())
				}
			}
		})
	}
}

func TestInterruptedSaveLeavesNoWallet(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.s.faults.afterTemp = func() error { return errSimulatedCrash }
	_, err := e.s.Save(context.Background(), "w1", secret.FromString(abandon), secrets(pwd, ""))
	if err == nil {
		t.Fatal("expected the simulated crash")
	}
	if exists(e.s.path("w1")) {
		t.Fatal("a half-written wallet file is visible under its final name")
	}
	var temps int
	entries, _ := os.ReadDir(e.dir)
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), tmpPrefix) {
			temps++
		}
	}
	if temps != 1 {
		t.Fatalf("expected one leftover temp file, found %d", temps)
	}
	s := e.reopen()
	entries, _ = os.ReadDir(e.dir)
	if len(entries) != 0 {
		t.Errorf("New left stale files: %v", entries)
	}
	if _, err := s.Save(context.Background(), "w1", secret.FromString(abandon), secrets(pwd, "")); err != nil {
		t.Fatalf("Save after an interrupted write: %v", err)
	}
}

func TestFailedWriteKeepsExistingFile(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	before := readBytes(t, e.s.path("w1"))
	e.s.faults.afterTemp = func() error { return errors.New("no space left") }
	_, err := e.s.Save(context.Background(), "w2", secret.FromString(zooMnem), secrets(pwd, ""))
	wantCode(t, err, domain.CodeInternal)
	if !bytes.Equal(before, readBytes(t, e.s.path("w1"))) || exists(e.s.path("w2")) {
		t.Error("state changed after a failed write")
	}
	entries, _ := os.ReadDir(e.dir)
	if len(entries) != 1 {
		t.Errorf("temp files left after a handled failure: %v", entries)
	}
}

// --- подпись ---

func ethRequest(expected string) ports.DigestRequest {
	var d [32]byte
	for i := range d {
		d[i] = byte(i + 1)
	}
	return ports.DigestRequest{Network: domain.NetworkEthereum, Path: "m/44'/60'/0'/0/0", ExpectedAddress: expected, Digest: d}
}

// recoverPub восстанавливает ключ по подписи и v (0 или 1): так проверяется,
// что v соответствует настоящему подписанту, а не только форма подписи.
func recoverPub(t *testing.T, digest []byte, sig []byte) secp256k1.Point {
	t.Helper()
	n, _ := new(big.Int).SetString("fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141", 16)
	r, s, z := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:64]), new(big.Int).SetBytes(digest)
	rInv := new(big.Int).ModInverse(r, n)
	u1 := new(big.Int).Mul(z, rInv)
	u1.Neg(u1).Mod(u1, n)
	u2 := new(big.Int).Mul(s, rInv)
	u2.Mod(u2, n)
	rp, err := secp256k1.ParsePoint(append([]byte{0x02 | sig[64]}, sig[:32]...))
	if err != nil {
		t.Fatal(err)
	}
	a, err := secp256k1.ScalarBaseMult(u1.FillBytes(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := secp256k1.ScalarMult(rp, u2.FillBytes(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	q, err := secp256k1.Add(a, b)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func btcRequest(t *testing.T, xpub string, hrp string, chain, idx uint32, coin string, digestSeed byte) ports.DigestRequest {
	t.Helper()
	acct, _, err := hd.ParsePublicKey(xpub)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := acct.Derive(chain)
	leaf, _ := ch.Derive(idx)
	var d [32]byte
	for i := range d {
		d[i] = digestSeed + byte(i)
	}
	return ports.DigestRequest{
		Network:         domain.NetworkBitcoin,
		Path:            fmt.Sprintf("m/84'/%s'/0'/%d/%d", coin, chain, idx),
		ExpectedAddress: hdtest.P2WPKHAddress(hrp, leaf.Hash160()),
		Digest:          d,
	}
}

func TestSignAllNetworks(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	pk := e.save("w1", abandon, pwd, "")
	reqs := []ports.DigestRequest{
		ethRequest(pk.ETHAddress),
		{Network: domain.NetworkTron, Path: "m/44'/195'/0'/0/0", ExpectedAddress: pk.TronAddress, Digest: [32]byte{9, 9, 9}},
		btcRequest(t, pk.BTCXpub, "bc", 0, 0, "0", 1),
		btcRequest(t, pk.BTCXpub, "bc", 0, 0, "0", 50), // тот же адрес, другой дайджест (второй вход)
		btcRequest(t, pk.BTCXpub, "bc", 1, 7, "0", 90),
	}
	if reqs[2].ExpectedAddress != "bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu" {
		t.Fatalf("test setup: BIP84 first address is %s", reqs[2].ExpectedAddress)
	}
	sigs, err := e.s.Sign(context.Background(), "w1", secrets(pwd, ""), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != len(reqs) {
		t.Fatalf("%d signatures for %d requests", len(sigs), len(reqs))
	}
	for i, r := range reqs {
		sig := sigs[i]
		if len(sig.PubKey) != 33 || sig.Sig[64] > 1 || !secp256k1.IsLowS(sig.Sig[32:64]) {
			t.Errorf("request %d: malformed signature %x", i, sig)
		}
		pub, err := secp256k1.ParsePoint(sig.PubKey)
		if err != nil {
			t.Fatal(err)
		}
		if !secp256k1.Verify(pub, r.Digest[:], sig.Sig[:32], sig.Sig[32:64]) {
			t.Errorf("request %d: signature does not verify", i)
		}
		if !recoverPub(t, r.Digest[:], sig.Sig[:]).Equal(pub) {
			t.Errorf("request %d: recovery id %d does not recover the signer", i, sig.Sig[64])
		}
		u := pub.Uncompressed()
		addr, _ := addrs(domain.ProfileMain)[r.Network](u[:])
		if addr != r.ExpectedAddress {
			t.Errorf("request %d: signer address %s, expected %s", i, addr, r.ExpectedAddress)
		}
	}
	if sigs[2].Sig == sigs[3].Sig {
		t.Error("different digests produced the same signature")
	}
	// Детерминированность RFC 6979: повтор даёт те же байты.
	again, _ := e.s.Sign(context.Background(), "w1", secrets(pwd, ""), reqs[:1])
	if again[0].Sig != sigs[0].Sig {
		t.Error("signature is not deterministic")
	}
}

func TestSignTestProfile(t *testing.T) {
	e := newEnv(t, domain.ProfileTest)
	pk := e.save("w1", abandon, pwd, "")
	if !strings.HasPrefix(pk.BTCXpub, "tpub") {
		t.Fatalf("test profile xpub %s", pk.BTCXpub)
	}
	ok := btcRequest(t, pk.BTCXpub, "tb", 0, 0, "1", 3)
	if ok.ExpectedAddress != "tb1q6rz28mcfaxtmd6v789l9rrlrusdprr9pqcpvkl" {
		t.Fatalf("test setup: testnet BIP84 address %s", ok.ExpectedAddress)
	}
	if _, err := e.s.Sign(context.Background(), "w1", secrets(pwd, ""), []ports.DigestRequest{ok}); err != nil {
		t.Fatal(err)
	}
	// Путь основной сети в тестовом профиле отвергается, даже с «верным» адресом.
	mainPath := ok
	mainPath.Path = "m/84'/0'/0'/0/0"
	_, err := e.s.Sign(context.Background(), "w1", secrets(pwd, ""), []ports.DigestRequest{mainPath})
	wantCode(t, err, domain.CodeSigningRefused)
}

func TestSignRefusals(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	pk := e.save("w1", abandon, pwd, "")
	good := ethRequest(pk.ETHAddress)
	ctx := context.Background()
	sec := secrets(pwd, "")

	mutate := func(f func(r *ports.DigestRequest)) ports.DigestRequest {
		r := good
		f(&r)
		return r
	}
	cases := map[string][]ports.DigestRequest{
		"wrong address":     {mutate(func(r *ports.DigestRequest) { r.ExpectedAddress = "0x0000000000000000000000000000000000000001" })},
		"lowercase address": {mutate(func(r *ports.DigestRequest) { r.ExpectedAddress = strings.ToLower(pk.ETHAddress) })},
		"empty address":     {mutate(func(r *ports.DigestRequest) { r.ExpectedAddress = "" })},
		"other index":       {mutate(func(r *ports.DigestRequest) { r.Path = "m/44'/60'/0'/0/1" })},
		"tron path on eth":  {mutate(func(r *ports.DigestRequest) { r.Path = "m/44'/195'/0'/0/0" })},
		"network mismatch":  {mutate(func(r *ports.DigestRequest) { r.Network = domain.NetworkTron })},
		"unknown network":   {mutate(func(r *ports.DigestRequest) { r.Network = "dogecoin" })},
		"bad path":          {mutate(func(r *ports.DigestRequest) { r.Path = "m/44'/60'/0'/0/x" })},
		"hardened leaf":     {mutate(func(r *ports.DigestRequest) { r.Path = "m/44'/60'/0'/0/0'" })},
		"one bad in many":   {good, mutate(func(r *ports.DigestRequest) { r.ExpectedAddress = pk.TronAddress })},
	}
	for name, reqs := range cases {
		sigs, err := e.s.Sign(ctx, "w1", sec, reqs)
		if domain.CodeOf(err) != domain.CodeSigningRefused || sigs != nil {
			t.Errorf("%s: code %s, sigs %v (%v)", name, domain.CodeOf(err), sigs, err)
		}
	}
	for name, reqs := range map[string][]ports.DigestRequest{"none": nil, "too many": make([]ports.DigestRequest, maxSignRequests+1)} {
		if _, err := e.s.Sign(ctx, "w1", sec, reqs); domain.CodeOf(err) != domain.CodeInvalidInput {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSignRefusesWhenNoAddressFunction(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	pk := e.save("w1", abandon, pwd, "")
	s, err := New(Config{Dir: e.dir, Profile: domain.ProfileMain, KDF: weak(), allowWeakKDF: true,
		Addresses: map[domain.Network]AddressFunc{domain.NetworkEthereum: hdtest.ETHAddress}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Sign(context.Background(), "w1", secrets(pwd, ""), []ports.DigestRequest{
		{Network: domain.NetworkTron, Path: "m/44'/195'/0'/0/0", ExpectedAddress: pk.TronAddress}})
	wantCode(t, err, domain.CodeSigningRefused)
	_, err = s.Save(context.Background(), "w9", secret.FromString(zooMnem), secrets(pwd, ""))
	wantCode(t, err, domain.CodeInternal) // нет функции адреса TRON
	if exists(s.path("w9")) {
		t.Error("wallet stored although its addresses could not be computed")
	}
}

func TestPassphraseTypoRefusesToSign(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	pk := e.save("w1", abandon, pwd, passphrase)
	req := ethRequest(pk.ETHAddress)
	ctx := context.Background()
	if _, err := e.s.Sign(ctx, "w1", secrets(pwd, passphrase), []ports.DigestRequest{req}); err != nil {
		t.Fatalf("correct passphrase: %v", err)
	}
	for name, pass := range map[string]string{"typo": passphrase + "x", "missing": "", "case": strings.ToUpper(passphrase)} {
		sigs, err := e.s.Sign(ctx, "w1", secrets(pwd, pass), []ports.DigestRequest{req})
		if domain.CodeOf(err) != domain.CodeSigningRefused || sigs != nil {
			t.Errorf("%s: code %s, sigs %v", name, domain.CodeOf(err), sigs)
		}
	}
	// Кошелёк без парольной фразы тоже не подпишет чужим адресом с фразой.
	e.save("w2", abandon, pwd, "")
	_, err := e.s.Sign(ctx, "w2", secrets(pwd, passphrase), []ports.DigestRequest{ethRequest(abandonETH)})
	wantCode(t, err, domain.CodeSigningRefused)
}

// --- утечки ---

func TestNoSecretsInErrorsLogsAndFiles(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	pk := e.save("w1", abandon, pwd, passphrase)
	ctx := context.Background()
	var outputs []string
	collect := func(err error) {
		if err != nil {
			outputs = append(outputs, err.Error(), fmt.Sprintf("%v|%+v|%#v", err, err, err))
		}
	}
	bad := secrets("WRONG-"+pwd, "WRONG-"+passphrase)
	collect(e.s.Verify(ctx, bad))
	_, err := e.s.RevealSeed(ctx, "w1", bad)
	collect(err)
	collect(e.s.Delete(ctx, "w1", bad))
	collect(e.s.ChangePassword(ctx, bad.Password, secret.FromString(newPwd)))
	_, err = e.s.Sign(ctx, "w1", secrets(pwd, "WRONG-"+passphrase), []ports.DigestRequest{ethRequest(pk.ETHAddress)})
	collect(err)
	_, err = e.s.Save(ctx, "w2", secret.FromString(strings.Replace(abandon, "about", "abandon", 1)), secrets(pwd, passphrase))
	collect(err)
	_, err = e.s.Save(ctx, "w3", secret.FromString("zebra zebra qqqqnotaword"), secrets(pwd, passphrase))
	collect(err)
	_ = os.WriteFile(e.s.path("w1"), []byte("junk"), 0o600)
	_, err = e.s.RevealSeed(ctx, "w1", secrets(pwd, passphrase))
	collect(err)

	seedHex := func() string {
		seed, _ := hd.SeedFromMnemonic(secret.FromString(abandon), secret.FromString(passphrase))
		defer seed.Wipe()
		return fmt.Sprintf("%x", seed.Expose())
	}()
	all := strings.Join(outputs, "\n") + "\n" + e.logs.String()
	for _, needle := range []string{pwd, newPwd, passphrase, "WRONG-", "abandon", "zoo", "qqqqnotaword", seedHex} {
		if strings.Contains(all, needle) {
			t.Errorf("secret %q found in errors or logs:\n%s", needle, all)
		}
	}
	if e.logs.Len() == 0 {
		t.Error("expected some log output from successful operations")
	}
}

func TestFileHoldsNoPlaintext(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, passphrase)
	raw := readBytes(t, e.s.path("w1"))
	for _, needle := range []string{"abandon", "about", pwd, passphrase, "mnemonic"} {
		if bytes.Contains(raw, []byte(needle)) {
			t.Errorf("file contains %q", needle)
		}
	}
	if len(raw) != fixedHeaderLen+len("w1")+saltLen+nonceLen+payloadLen+tagLen {
		t.Errorf("file size %d depends on the content", len(raw))
	}
}

func TestSecretTypesDoNotPrint(t *testing.T) {
	m, _ := newEnv(t, domain.ProfileMain).s.NewMnemonic(12)
	if len(strings.Fields(string(m.Expose()))) != 12 {
		t.Error("NewMnemonic length")
	}
	if got := fmt.Sprintf("%v %s %+v", m, m, m); strings.Contains(got, strings.Fields(string(m.Expose()))[0]) {
		t.Errorf("mnemonic printed: %s", got)
	}
	if _, err := newEnv(t, domain.ProfileMain).s.NewMnemonic(13); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Errorf("13 words: %v", err)
	}
}

func TestConcurrentUse(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	pk := e.save("w1", abandon, pwd, "")
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 4; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); errs <- e.s.Verify(context.Background(), secrets(pwd, "")) }()
		go func() {
			defer wg.Done()
			_, err := e.s.Sign(context.Background(), "w1", secrets(pwd, ""), []ports.DigestRequest{ethRequest(pk.ETHAddress)})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			m, err := e.s.RevealSeed(context.Background(), "w1", secrets(pwd, ""))
			m.Wipe()
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

// --- фаззинг разбора файла ---

func FuzzParseFile(f *testing.F) {
	// Сид детерминированный: каждый процесс фаззера должен получить тот же файл.
	h := header{params: *weak(), id: "w1"}
	for i := range h.salt {
		h.salt[i] = byte(i + 1)
	}
	for i := range h.nonce {
		h.nonce[i] = byte(0xA0 + i)
	}
	file, err := sealFile(h, []byte(abandon), secret.FromString(pwd))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(file)
	f.Add([]byte{})
	f.Add([]byte("WLTK"))
	f.Add(file[:len(file)-1])
	f.Fuzz(func(t *testing.T, data []byte) {
		h, hb, ct, err := parseFile(data)
		if err != nil {
			return
		}
		// Принятая структура обязана собираться обратно в те же байты заголовка.
		if !bytes.Equal(h.marshal(), hb) || len(hb)+len(ct) != len(data) {
			t.Fatalf("header does not round trip: %x", data)
		}
		// Расшифровка дорогая при больших параметрах: пробуем только дешёвые.
		if h.params.MemoryKiB > 256 || h.params.Time > 2 {
			return
		}
		m, err := decryptFile(h.id, data, secret.FromString(pwd))
		if err == nil {
			defer m.Wipe()
			if !bytes.Equal(data, file) {
				t.Fatalf("a modified file was accepted: %x", data)
			}
		}
	})
}

func TestReservedWindowsNames(t *testing.T) {
	for _, id := range []string{"con", "CON", "Nul", "aux", "PRN", "com1", "LPT9", "com9"} {
		if validID(id) {
			t.Errorf("%q accepted", id)
		}
	}
	for _, id := range []string{"com0", "com10", "console", "lpt", "9", "a-b_C"} {
		if !validID(id) {
			t.Errorf("%q rejected", id)
		}
	}
}

func TestSaveNeverReplacesAnExistingFile(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	orig := readBytes(t, e.s.path("w1"))
	// Гонка: файл появился после проверки exists, но до записи.
	if err := e.s.writeNew(e.s.path("w1"), []byte("another file")); !errors.Is(err, ErrExists) {
		t.Fatalf("writeNew over an existing file: %v", err)
	}
	if !bytes.Equal(orig, readBytes(t, e.s.path("w1"))) {
		t.Fatal("existing wallet file was replaced")
	}
	entries, _ := os.ReadDir(e.dir)
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestHeaderBoundsAreCorruptNotWrongPassword(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	orig := readBytes(t, e.s.path("w1"))
	for name, edit := range map[string]func(b []byte){
		"time above max":    func(b []byte) { b[10] = maxTime + 1 },
		"time zero":         func(b []byte) { b[10] = 0 },
		"threads above max": func(b []byte) { b[15] = maxThreads + 1 },
		"memory above max":  func(b []byte) { b[11], b[12], b[13], b[14] = 0, 0x04, 0x00, 0x01 }, // 256 MiB + 1 KiB
	} {
		bad := append([]byte(nil), orig...)
		edit(bad)
		_ = os.WriteFile(e.s.path("w1"), bad, 0o600)
		_, err := e.s.RevealSeed(context.Background(), "w1", secrets(pwd, ""))
		if domain.CodeOf(err) != domain.CodeKeystoreCorrupt {
			t.Errorf("%s: code %s (%v)", name, domain.CodeOf(err), err)
		}
	}
}

// Сбой после COMMIT не оставляет смесь файлов: следующая операция того же
// процесса сначала доводит смену пароля, а не читает наполовину замененный набор.
func TestInterruptedChangeIsFinishedBeforeNextOperation(t *testing.T) {
	e, ids := twoWallets(t)
	e.s.faults.afterCommit = func() error { return errors.New("disk failure after commit") }
	if err := e.s.ChangePassword(context.Background(), secret.FromString(pwd), secret.FromString(newPwd)); err == nil {
		t.Fatal("expected an error")
	}
	e.s.faults.afterCommit = nil
	// Следующий вызов видит согласованное состояние (новый пароль), без перезапуска.
	assertPassword(t, e.s, ids, newPwd)
	if exists(e.s.stagePath()) {
		t.Error("staging directory left behind")
	}
	// И повторная смена пароля не затирает журнал: стартует с чистого состояния.
	if err := e.s.ChangePassword(context.Background(), secret.FromString(newPwd), secret.FromString(pwd)); err != nil {
		t.Fatal(err)
	}
	assertPassword(t, e.s, ids, pwd)
}

func TestReadFileRejectsOversizeWithoutReadingIt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.keystore")
	if err := os.WriteFile(p, make([]byte, maxFileSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFile(p); !errors.Is(err, errCorrupt) {
		t.Errorf("got %v", err)
	}
}

func TestPublicKeysRederivesWhatSaveReturned(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	saved := e.save("w1", abandon, pwd, "")
	savedWithPassphrase := e.save("w2", abandon, pwd, passphrase)
	ctx := context.Background()

	got, err := e.s.PublicKeys(ctx, "w1", secrets(pwd, ""))
	if err != nil {
		t.Fatalf("PublicKeys: %v", err)
	}
	if got != saved {
		t.Fatalf("PublicKeys = %+v, want %+v", got, saved)
	}

	got, err = e.s.PublicKeys(ctx, "w2", secrets(pwd, passphrase))
	if err != nil || got != savedWithPassphrase {
		t.Fatalf("PublicKeys with passphrase = %+v, %v; want %+v", got, err, savedWithPassphrase)
	}
}

func TestPublicKeysWrongPassphraseGivesDifferentData(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	saved := e.save("w1", abandon, pwd, passphrase)

	got, err := e.s.PublicKeys(context.Background(), "w1", secrets(pwd, ""))
	if err != nil {
		t.Fatalf("PublicKeys: %v", err)
	}
	if got.ETHAddress == saved.ETHAddress || got.TronAddress == saved.TronAddress || got.BTCXpub == saved.BTCXpub {
		t.Fatal("a wrong passphrase must not reproduce the saved public data")
	}
}

func TestPublicKeysRejectsBadInput(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	ctx := context.Background()

	_, err := e.s.PublicKeys(ctx, "w1", secrets("wrong password", ""))
	wantCode(t, err, domain.CodeWrongPassword)
	_, err = e.s.PublicKeys(ctx, "../w1", secrets(pwd, ""))
	wantCode(t, err, domain.CodeInvalidInput)
	_, err = e.s.PublicKeys(ctx, "missing", secrets(pwd, ""))
	wantCode(t, err, domain.CodeNotFound)
}
