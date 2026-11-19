// Package keystore хранит seed-фразы кошельков под паролем и подписывает
// дайджесты. Это единственный пакет, который видит seed и приватные ключи.
//
// Зона Dev 2 (docs/TEAM-PROMPTS.md, раздел 2). Файл кошелька описан в
// format.go; параметры Argon2id хранятся в заголовке и входят в AAD.
//
// Порядок подписи (A2): сервис вызывает адаптер (Prepare, Verify), затем
// Sign с дайджестами из Prepared. Sign для каждого запроса выводит ключ по
// пути, превращает его в адрес сети функцией из Config.Addresses и сверяет с
// ExpectedAddress; при любом несовпадении не подписывается ничего.
//
// Функции адресов получают несжатый ключ в 65 байт для всех сетей. Для ETH
// и TRON это AddressFromPublicKey адаптеров. Для Bitcoin адаптер по
// контракту AddressFromPublicKey возвращает ошибку, поэтому при сборке
// приложения для сети bitcoin нужна отдельная функция, которая строит адрес
// P2WPKH (bc1q или tb1q по профилю) из 65-байтного ключа: её предоставляет
// сторона Dev 4. Пока функции для сети нет, Sign по ней отказывает.
package keystore

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
)

// maxSignRequests ограничивает число дайджестов в одном вызове Sign.
const maxSignRequests = 1024

// ErrExists возвращается, если файл кошелька с таким идентификатором уже есть.
var ErrExists = errors.New("keystore: wallet file already exists")

// AddressFunc превращает несжатый публичный ключ (65 байт) в адрес сети.
type AddressFunc func(uncompressedPub []byte) (string, error)

// Config это зависимости keystore.
type Config struct {
	Dir       string // каталог keystore профиля (A4)
	Profile   domain.Profile
	Addresses map[domain.Network]AddressFunc
	Logger    *slog.Logger // необязателен; секреты в лог не попадают
	// KDF задаёт параметры для новых файлов. Если nil, они подбираются под
	// машину (Calibrate) при первой записи. Ниже минимума OWASP не допускаются.
	KDF *Params

	allowWeakKDF bool // только для тестов пакета
}

// Store реализует ports.Keystore поверх файлов в каталоге.
type Store struct {
	dir       string
	profile   domain.Profile
	addresses map[domain.Network]AddressFunc
	log       *slog.Logger

	mu             sync.Mutex // операции с файлами и паролем выполняются по одной
	recoverPending bool       // после неудачной смены пароля остался каталог .pwchange
	kdf            *Params
	kdfOnce        sync.Once
	faults         faults
}

// faults это точки отказа для тестов; в рабочем коде все nil.
type faults struct {
	afterTemp    func() error
	afterCommit  func() error
	beforeRename func(i int) error
}

var _ ports.Keystore = (*Store)(nil)

// New открывает каталог keystore (создаёт с правами 0700), удаляет остатки
// прерванных записей и доводит прерванную смену пароля.
func New(cfg Config) (*Store, error) {
	if !cfg.Profile.Valid() {
		return nil, errors.New("keystore: unknown network profile")
	}
	if cfg.Dir == "" {
		return nil, errors.New("keystore: directory is required")
	}
	if cfg.KDF != nil && !cfg.allowWeakKDF && !cfg.KDF.acceptable() {
		return nil, errors.New("keystore: KDF parameters are below the OWASP minimum or out of range")
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("keystore: create directory: %w", err)
	}
	if runtime.GOOS != "windows" {
		//nolint:gosec // 0700 is intentional: private keystore directory permissions.
		if err := os.Chmod(cfg.Dir, 0o700); err != nil {
			return nil, fmt.Errorf("keystore: secure directory permissions: %w", err)
		}
	}
	s := &Store{
		dir:       cfg.Dir,
		profile:   cfg.Profile,
		addresses: cfg.Addresses,
		log:       cfg.Logger,
		kdf:       cfg.KDF,
	}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s.removeStaleTemps()
	if err := s.recoverStage(); err != nil {
		return nil, fmt.Errorf("keystore: recover interrupted password change: %w", err)
	}
	return s, nil
}

// writeParams возвращает параметры для новых файлов; подбор выполняется один раз.
func (s *Store) writeParams() Params {
	s.kdfOnce.Do(func() {
		if s.kdf == nil {
			p := Calibrate(DefaultTarget)
			s.kdf = &p
			s.log.Info("keystore KDF calibrated", "time", p.Time, "memory_kib", p.MemoryKiB, "threads", p.Threads)
		}
	})
	return *s.kdf
}

// NewMnemonic создаёт мнемонику из 12 или 24 слов.
func (s *Store) NewMnemonic(words int) (secret.Bytes, error) {
	m, err := hd.NewMnemonic(words)
	if err != nil {
		return secret.Bytes{}, domain.NewError(domain.CodeInvalidInput, err)
	}
	return m, nil
}

// Save шифрует мнемонику в файл кошелька и возвращает публичные данные. Файл
// существующего кошелька не перезаписывается. Пароль у всех кошельков один
// (A3): если файлы уже есть, пароль сверяется с одним из них.
func (s *Store) Save(ctx context.Context, id string, mnemonic secret.Bytes, sec ports.Secrets) (ports.PublicKeys, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return ports.PublicKeys{}, err
	}

	if !validID(id) {
		return ports.PublicKeys{}, domain.NewError(domain.CodeInvalidInput, errors.New("keystore: invalid wallet id"))
	}
	if sec.Password.IsEmpty() {
		return ports.PublicKeys{}, domain.NewError(domain.CodeInvalidInput, errors.New("keystore: password is empty"))
	}
	if err := ctxError(ctx); err != nil {
		return ports.PublicKeys{}, err
	}
	if exists(s.path(id)) {
		return ports.PublicKeys{}, domain.NewError(domain.CodeInvalidInput, ErrExists)
	}
	ids, err := s.listIDs()
	if err != nil {
		return ports.PublicKeys{}, domain.NewError(domain.CodeInternal, err)
	}
	if len(ids) > 0 {
		m, err := s.open(ids[0], sec.Password)
		if err != nil {
			return ports.PublicKeys{}, err
		}
		m.Wipe()
		if err := ctxError(ctx); err != nil {
			return ports.PublicKeys{}, err
		}
	}

	canon, err := hd.NormalizeMnemonic(mnemonic)
	if err != nil {
		return ports.PublicKeys{}, domain.NewError(domain.CodeInvalidInput, err)
	}
	defer canon.Wipe()
	seed, err := hd.SeedFromMnemonic(canon, sec.Passphrase)
	if err != nil {
		return ports.PublicKeys{}, domain.NewError(domain.CodeInvalidInput, err)
	}
	defer seed.Wipe()
	pd, err := hd.DerivePublicData(seed, s.profile)
	if err != nil {
		return ports.PublicKeys{}, domain.NewError(domain.CodeInternal, err)
	}
	eth, err := s.address(domain.NetworkEthereum, pd.ETHPublicKey[:])
	if err != nil {
		return ports.PublicKeys{}, err
	}
	tron, err := s.address(domain.NetworkTron, pd.TronPublicKey[:])
	if err != nil {
		return ports.PublicKeys{}, err
	}

	file, err := encryptFile(id, canon.Expose(), sec.Password, s.writeParams())
	if err != nil {
		return ports.PublicKeys{}, domain.NewError(domain.CodeInternal, err)
	}
	if err := ctxError(ctx); err != nil {
		return ports.PublicKeys{}, err
	}
	if err := s.writeNew(s.path(id), file); err != nil {
		if errors.Is(err, ErrExists) {
			return ports.PublicKeys{}, domain.NewError(domain.CodeInvalidInput, err)
		}
		return ports.PublicKeys{}, domain.NewError(domain.CodeInternal, err)
	}
	s.log.Info("keystore: wallet saved", "wallet", id)
	return ports.PublicKeys{BTCXpub: pd.BTCXpub, ETHAddress: eth, TronAddress: tron}, nil
}

// Verify проверяет пароль расшифровкой одного из файлов. Если файлов нет,
// возвращает ошибку not_found.
func (s *Store) Verify(ctx context.Context, sec ports.Secrets) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return err
	}
	if err := ctxError(ctx); err != nil {
		return err
	}
	ids, err := s.listIDs()
	if err != nil {
		return domain.NewError(domain.CodeInternal, err)
	}
	if len(ids) == 0 {
		return domain.NewError(domain.CodeNotFound, domain.ErrNotFound)
	}
	m, err := s.open(ids[0], sec.Password)
	if err != nil {
		return err
	}
	m.Wipe()
	return nil
}

// ChangePassword перешифровывает все файлы новым паролем атомарно: либо все
// файлы под новым паролем, либо все под старым.
func (s *Store) ChangePassword(ctx context.Context, oldPwd, newPwd secret.Bytes) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return err
	}

	if newPwd.IsEmpty() {
		return domain.NewError(domain.CodeInvalidInput, errors.New("keystore: new password is empty"))
	}
	if err := ctxError(ctx); err != nil {
		return err
	}
	ids, err := s.listIDs()
	if err != nil {
		return domain.NewError(domain.CodeInternal, err)
	}
	if len(ids) == 0 {
		return domain.NewError(domain.CodeNotFound, domain.ErrNotFound)
	}
	params := s.writeParams()
	oldData := make(map[string][]byte, len(ids))
	newData := make(map[string][]byte, len(ids))
	for _, id := range ids {
		if err := ctxError(ctx); err != nil {
			return err
		}
		file, err := readFile(s.path(id))
		if err != nil {
			return s.fileError(err)
		}
		m, err := decryptFile(id, file, oldPwd)
		if err != nil {
			return s.fileError(err)
		}
		oldData[id] = file
		newData[id], err = encryptFile(id, m.Expose(), newPwd, params)
		m.Wipe()
		if err != nil {
			return domain.NewError(domain.CodeInternal, err)
		}
	}
	if err := ctxError(ctx); err != nil {
		return err
	}
	if err := s.changeFiles(ids, oldData, newData); err != nil {
		// Каталог .pwchange мог остаться: следующая операция его доведёт.
		s.recoverPending = true
		return domain.NewError(domain.CodeInternal, err)
	}
	s.log.Info("keystore: password changed", "wallets", len(ids))
	return nil
}

// RevealSeed возвращает мнемонику кошелька. Вызывающий обязан затереть её.
func (s *Store) RevealSeed(ctx context.Context, id string, sec ports.Secrets) (secret.Bytes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return secret.Bytes{}, err
	}
	if err := ctxError(ctx); err != nil {
		return secret.Bytes{}, err
	}
	if !validID(id) {
		return secret.Bytes{}, domain.NewError(domain.CodeInvalidInput, errors.New("keystore: invalid wallet id"))
	}
	return s.open(id, sec.Password)
}

// Delete удаляет файл кошелька после проверки пароля.
func (s *Store) Delete(ctx context.Context, id string, sec ports.Secrets) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return err
	}
	if err := ctxError(ctx); err != nil {
		return err
	}
	if !validID(id) {
		return domain.NewError(domain.CodeInvalidInput, errors.New("keystore: invalid wallet id"))
	}
	m, err := s.open(id, sec.Password)
	if err != nil {
		return err
	}
	m.Wipe()
	if err := os.Remove(s.path(id)); err != nil {
		return domain.NewError(domain.CodeInternal, err)
	}
	if err := syncDir(s.dir); err != nil {
		return domain.NewError(domain.CodeInternal, err)
	}
	s.log.Info("keystore: wallet deleted", "wallet", id)
	return nil
}

// Sign подписывает 32-байтные дайджесты ключами кошелька id. Сначала для
// каждого запроса проверяется сеть, путь и адрес, и только если все
// совпали, выполняется подпись: частичного результата не бывает. Подпись
// самопроверяется публичным ключом до возврата.
func (s *Store) Sign(ctx context.Context, id string, sec ports.Secrets, reqs []ports.DigestRequest) ([]ports.Signature, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepare(ctx); err != nil {
		return nil, err
	}
	if err := ctxError(ctx); err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, domain.NewError(domain.CodeInvalidInput, errors.New("keystore: invalid wallet id"))
	}
	if len(reqs) == 0 || len(reqs) > maxSignRequests {
		return nil, domain.NewError(domain.CodeInvalidInput, errors.New("keystore: number of digests out of range"))
	}

	// Разбор путей и сети до расшифровки: ошибка в запросе не требует пароля.
	paths := make([]hd.Path, len(reqs))
	for i, r := range reqs {
		p, err := hd.ParsePath(r.Path)
		if err != nil || hd.CheckPathForNetwork(r.Network, s.profile, p) != nil {
			return nil, refused("path does not belong to the network", i)
		}
		if s.addresses[r.Network] == nil || r.ExpectedAddress == "" {
			return nil, refused("no address check for the network", i)
		}
		paths[i] = p
	}

	mnemonic, err := s.open(id, sec.Password)
	if err != nil {
		return nil, err
	}
	defer mnemonic.Wipe()
	seed, err := hd.SeedFromMnemonic(mnemonic, sec.Passphrase)
	if err != nil {
		return nil, domain.NewError(domain.CodeKeystoreCorrupt, err)
	}
	defer seed.Wipe()
	master, err := hd.MasterKey(seed)
	if err != nil {
		return nil, domain.NewError(domain.CodeInternal, err)
	}
	defer master.Wipe()

	type derived struct {
		key *hd.PrivateKey
		pub [65]byte
	}
	cache := make(map[string]*derived)
	defer func() {
		for _, d := range cache {
			d.key.Wipe()
		}
	}()

	// Проход 1: вывод ключей и сверка адресов, подписей ещё нет.
	keys := make([]*derived, len(reqs))
	for i, r := range reqs {
		d := cache[r.Path]
		if d == nil {
			k, err := master.DerivePath(paths[i])
			if err != nil {
				return nil, domain.NewError(domain.CodeInternal, err)
			}
			d = &derived{key: k, pub: k.PublicKey().Uncompressed()}
			cache[r.Path] = d
		}
		addr, err := s.addresses[r.Network](d.pub[:])
		if err != nil || subtle.ConstantTimeCompare([]byte(addr), []byte(r.ExpectedAddress)) != 1 {
			return nil, refused("address from the derivation path differs from the expected one", i)
		}
		keys[i] = d
	}

	// Проход 2: подпись.
	out := make([]ports.Signature, len(reqs))
	for i, r := range reqs {
		if err := ctxError(ctx); err != nil {
			return nil, err
		}
		sig, err := secp256k1.Sign(keys[i].key.Secret().Expose(), r.Digest[:])
		if err != nil {
			return nil, domain.NewError(domain.CodeInternal, err)
		}
		pub, err := secp256k1.ParsePoint(keys[i].pub[:])
		if err != nil || !secp256k1.Verify(pub, r.Digest[:], sig.R[:], sig.S[:]) {
			return nil, domain.NewError(domain.CodeInternal, errors.New("keystore: self-check of the signature failed"))
		}
		copy(out[i].Sig[:32], sig.R[:])
		copy(out[i].Sig[32:64], sig.S[:])
		out[i].Sig[64] = sig.RecID
		c := pub.Compressed()
		out[i].PubKey = c[:]
	}
	return out, nil
}

// prepare выполняется под s.mu в начале каждой операции: проверяет контекст и
// доводит прерванную смену пароля, чтобы операция не увидела смесь файлов
// под старым и новым паролем.
func (s *Store) prepare(ctx context.Context) error {
	if err := ctxError(ctx); err != nil {
		return err
	}
	if s.recoverPending {
		if err := s.recoverStage(); err != nil {
			return domain.NewError(domain.CodeInternal, fmt.Errorf("keystore: finish interrupted password change: %w", err))
		}
		s.recoverPending = false
	}
	return nil
}

// open читает и расшифровывает файл кошелька id.
func (s *Store) open(id string, password secret.Bytes) (secret.Bytes, error) {
	file, err := readFile(s.path(id))
	if err != nil {
		return secret.Bytes{}, s.fileError(err)
	}
	m, err := decryptFile(id, file, password)
	if err != nil {
		return secret.Bytes{}, s.fileError(err)
	}
	return m, nil
}

// fileError переводит ошибку файла в код интерфейса. Текст причины не
// содержит секретов: это ошибки ввода-вывода и разбора заголовка.
func (s *Store) fileError(err error) error {
	switch {
	case errors.Is(err, errBadPassword):
		return domain.NewError(domain.CodeWrongPassword, err)
	case errors.Is(err, errCorrupt):
		return domain.NewError(domain.CodeKeystoreCorrupt, err)
	case errors.Is(err, os.ErrNotExist):
		return domain.NewError(domain.CodeNotFound, domain.ErrNotFound)
	}
	return domain.NewError(domain.CodeInternal, err)
}

func (s *Store) address(n domain.Network, pub []byte) (string, error) {
	f := s.addresses[n]
	if f == nil {
		return "", domain.NewError(domain.CodeInternal, fmt.Errorf("keystore: no address function for %s", n))
	}
	a, err := f(pub)
	if err != nil {
		return "", domain.NewError(domain.CodeInternal, err)
	}
	return a, nil
}

// refused это отказ в подписи; номер запроса помогает найти ошибку, адреса и
// пути в текст не попадают.
func refused(why string, index int) error {
	return domain.NewError(domain.CodeSigningRefused, fmt.Errorf("keystore: request %d: %s", index, why))
}

func ctxError(ctx context.Context) error {
	err := ctx.Err()
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return domain.NewError(domain.CodeTimeout, err)
	}
	return domain.NewError(domain.CodeCanceled, err)
}
