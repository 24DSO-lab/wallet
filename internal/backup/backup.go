package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/secret"
)

// Snapshotter делает согласованный снимок БД в новый файл. Его реализует
// store.DB.Snapshot (VACUUM INTO); интерфейс отделяет резервную копию от драйвера.
type Snapshotter interface {
	Snapshot(ctx context.Context, dst string) error
}

// Limits ограничивают размеры при распаковке: архив приходит снаружи и не
// доверяется. Нулевые поля заменяются значениями по умолчанию.
type Limits struct {
	MaxFiles            int   // записей в архиве, включая манифест
	MaxDBSize           int64 // байт в БД после распаковки
	MaxKeystoreFileSize int64 // байт в одном keystore-файле
	MaxTotalSize        int64 // байт во всех файлах вместе
}

func (l Limits) withDefaults() Limits {
	if l.MaxFiles <= 0 {
		l.MaxFiles = 256
	}
	if l.MaxDBSize <= 0 {
		l.MaxDBSize = 512 << 20
	}
	if l.MaxKeystoreFileSize <= 0 {
		l.MaxKeystoreFileSize = 4 << 10
	}
	if l.MaxTotalSize <= 0 {
		l.MaxTotalSize = 1 << 30
	}
	return l
}

// Config описывает рабочие данные и зависимости.
type Config struct {
	// DataDir это каталог данных профиля (config.DataDir): в нём лежат БД и
	// каталог keystore. Временные каталоги восстановления создаются здесь же,
	// чтобы замена шла переименованием внутри одного тома.
	DataDir string

	// DBName это имя файла БД в DataDir. По умолчанию "wallet.db".
	DBName string

	// KeystoreDirName это имя каталога keystore в DataDir. По умолчанию "keystore".
	KeystoreDirName string

	// DB делает снимок базы. Обязателен.
	DB Snapshotter

	// CheckKeystoreFile разбирает заголовок keystore-файла без пароля и
	// возвращает ошибку, если файл не keystore. Обязателен: его реализует
	// Dev 2, подключает Dev 7. Вызывается при создании и при восстановлении.
	CheckKeystoreFile func(name string, data []byte) error

	// VerifyKeystorePassword verifies the staged archive's keystore before any
	// working files are replaced.
	VerifyKeystorePassword func(context.Context, string, secret.Bytes) error

	// MaxSchemaVersion это новейшая версия схемы БД, которую понимает сборка
	// (store.SchemaVersion()). Архив с более новой схемой не восстанавливается.
	MaxSchemaVersion int

	Limits Limits
	Now    func() time.Time // по умолчанию time.Now
}

// Service создаёт и восстанавливает резервные копии.
type Service struct {
	cfg    Config
	rename func(oldpath, newpath string) error // подменяется в тестах для проверки отката
}

// New проверяет настройки и возвращает сервис.
func New(cfg Config) (*Service, error) {
	switch {
	case cfg.DataDir == "":
		return nil, errors.New("backup: data dir is empty")
	case cfg.DB == nil:
		return nil, errors.New("backup: snapshotter is required")
	case cfg.CheckKeystoreFile == nil:
		return nil, errors.New("backup: keystore file checker is required")
	case cfg.MaxSchemaVersion < 1:
		return nil, errors.New("backup: max schema version must be set")
	}
	if cfg.DBName == "" {
		cfg.DBName = "wallet.db"
	}
	if cfg.KeystoreDirName == "" {
		cfg.KeystoreDirName = "keystore"
	}
	if !validKeystoreName(cfg.KeystoreDirName) || strings.ContainsAny(cfg.DBName, `/\`) || cfg.DBName == "" {
		return nil, errors.New("backup: db and keystore names must be plain file names")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	cfg.Limits = cfg.Limits.withDefaults()
	return &Service{cfg: cfg, rename: os.Rename}, nil
}

// Result описывает созданную копию.
type Result struct {
	Path         string // архив
	ChecksumPath string // файл контрольной суммы рядом
	SHA256       string // сумма архива, hex
	Files        int    // сколько файлов внутри, без манифеста
}

// invalidf это ошибка непригодного архива (CodeBackupInvalid).
func invalidf(format string, a ...any) error {
	return domain.NewError(domain.CodeBackupInvalid, fmt.Errorf("backup: "+format, a...))
}

// mapCtx превращает ошибки контекста в AppError с кодом, сохраняя цепочку для errors.Is.
func mapCtx(err error) error {
	var ae *domain.AppError
	switch {
	case err == nil, errors.As(err, &ae):
		return err
	case errors.Is(err, context.Canceled):
		return domain.NewError(domain.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return domain.NewError(domain.CodeTimeout, err)
	}
	return err
}

// ctxReader прерывает чтение при отмене контекста, чтобы долгое копирование
// и хеширование можно было остановить.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// archiveName возвращает имя архива по времени UTC.
func archiveName(t time.Time) string {
	return "wallet-backup-" + t.UTC().Format("20060102T150405Z")
}

type keystoreFile struct {
	name string
	data []byte
}

// Create собирает резервную копию в каталоге destDir и возвращает пути. Файлы
// keystore копируются как есть, без расшифровки.
func (s *Service) Create(ctx context.Context, destDir string) (res Result, err error) {
	defer func() { err = mapCtx(err) }()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if destDir == "" {
		return Result{}, errors.New("backup: destination is empty")
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("backup: create destination: %w", err)
	}
	work, err := os.MkdirTemp(destDir, ".wallet-backup-")
	if err != nil {
		return Result{}, fmt.Errorf("backup: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	snap := filepath.Join(work, "snapshot.db")
	if err := s.cfg.DB.Snapshot(ctx, snap); err != nil {
		return Result{}, fmt.Errorf("backup: database snapshot: %w", err)
	}
	schema, err := readSchemaVersion(snap)
	if err != nil {
		return Result{}, fmt.Errorf("backup: database snapshot: %w", err)
	}
	if schema > s.cfg.MaxSchemaVersion {
		return Result{}, fmt.Errorf("backup: database schema %d is newer than supported %d", schema, s.cfg.MaxSchemaVersion)
	}
	ks, err := s.readKeystore(ctx)
	if err != nil {
		return Result{}, err
	}

	now := s.cfg.Now().UTC()
	zipPath := filepath.Join(work, "archive.zip")
	if err := s.writeArchive(ctx, zipPath, snap, ks, schema, now); err != nil {
		return Result{}, err
	}
	sum, err := fileSHA256(ctx, zipPath)
	if err != nil {
		return Result{}, fmt.Errorf("backup: checksum: %w", err)
	}

	// Контрольная сумма появляется раньше архива: если есть архив, есть и сумма.
	base := archiveName(now)
	for i := 0; i < 100; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i+1)
		}
		final := filepath.Join(destDir, name+".zip")
		if _, statErr := os.Lstat(final); statErr == nil {
			continue
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return Result{}, fmt.Errorf("backup: destination: %w", statErr)
		}
		sumTmp := filepath.Join(work, "archive.zip.sha256")
		if err := os.WriteFile(sumTmp, []byte(sum+"  "+name+".zip\n"), 0o600); err != nil {
			return Result{}, fmt.Errorf("backup: write checksum: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if err := s.rename(sumTmp, final+checksumExt); err != nil {
			return Result{}, fmt.Errorf("backup: publish checksum: %w", err)
		}
		if err := s.rename(zipPath, final); err != nil {
			_ = os.Remove(final + checksumExt)
			return Result{}, fmt.Errorf("backup: publish archive: %w", err)
		}
        result := Result{Path: final, ChecksumPath: final + checksumExt, SHA256: sum, Files: 1 + len(ks)}
        if err := syncDir(destDir); err != nil {
            return result, fmt.Errorf("backup: archive published but destination directory sync failed: %w", err)
        }
        return result, nil
	}
	return Result{}, errors.New("backup: could not choose a free archive name")
}

const (
	checksumExt = ".sha256"

	// keystoreStageDir это каталог журнала смены пароля в keystore (Dev 2,
	// internal/keystore/files.go). Пока он есть, копию делать нельзя.
	keystoreStageDir = ".pwchange"
)

// errKeystoreBusy означает, что keystore сейчас меняется; попытку надо повторить позже.
var errKeystoreBusy = errors.New("backup: keystore is being modified (password change in progress), try again later")

// readKeystore читает файлы keystore целиком (они маленькие), проверяет каждый
// и возвращает копии в памяти: проверенное и записанное в архив это одни и те же байты.
func (s *Service) readKeystore(ctx context.Context) ([]keystoreFile, error) {
	dir := filepath.Join(s.cfg.DataDir, s.cfg.KeystoreDirName)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // кошельков ещё нет
	}
	if err != nil {
		return nil, fmt.Errorf("backup: read keystore dir: %w", err)
	}
	var out []keystoreFile
	for _, e := range entries {
		name := e.Name()
		if name == keystoreStageDir {
			// Идёт (или оборвалась) смена пароля: часть файлов уже с новым
			// паролем, часть ещё со старым. Такая копия была бы негодной.
			return nil, errKeystoreBusy
		}
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".tmp") {
			continue // временные файлы записи keystore
		}
		if !e.Type().IsRegular() {
			return nil, fmt.Errorf("backup: keystore entry %q is not a regular file", name)
		}
		if !validKeystoreName(name) {
			return nil, fmt.Errorf("backup: keystore file name %q cannot be stored in a backup", name)
		}
		f, err := os.Open(filepath.Join(dir, name)) //nolint:gosec // имя проверено validKeystoreName
		if err != nil {
			return nil, fmt.Errorf("backup: open keystore file: %w", err)
		}
		data, err := io.ReadAll(io.LimitReader(ctxReader{ctx, f}, s.cfg.Limits.MaxKeystoreFileSize+1))
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("backup: read keystore file: %w", err)
		}
		if int64(len(data)) > s.cfg.Limits.MaxKeystoreFileSize {
			return nil, fmt.Errorf("backup: keystore file %q is too large", name)
		}
		if err := s.cfg.CheckKeystoreFile(name, data); err != nil {
			return nil, invalidf("keystore file %q is not valid: %w", name, err)
		}
		out = append(out, keystoreFile{name, data})
	}
	return out, nil
}

// hashingCounter считает размер и SHA-256 записанного.
type hashingCounter struct {
	h hash.Hash
	n int64
}

func (c *hashingCounter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return c.h.Write(p)
}

func (s *Service) writeArchive(ctx context.Context, path, snap string, ks []keystoreFile, schema int, now time.Time) (err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // путь внутри нашего временного каталога
	if err != nil {
		return fmt.Errorf("backup: create archive: %w", err)
	}
	defer func() {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("backup: close archive: %w", cerr)
		}
	}()
	zw := zip.NewWriter(f)
	var files []FileEntry

	add := func(name string, r io.Reader) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return fmt.Errorf("backup: write archive: %w", err)
		}
		hc := &hashingCounter{h: sha256.New()}
		if _, err := io.Copy(io.MultiWriter(w, hc), ctxReader{ctx, r}); err != nil {
			return fmt.Errorf("backup: write archive: %w", err)
		}
		files = append(files, FileEntry{Path: name, Size: hc.n, SHA256: hex.EncodeToString(hc.h.Sum(nil))})
		return nil
	}

	db, err := os.Open(snap) //nolint:gosec // наш временный файл
	if err != nil {
		return fmt.Errorf("backup: open snapshot: %w", err)
	}
	err = add(dbEntry, db)
	_ = db.Close()
	if err != nil {
		return err
	}
	for _, k := range ks {
		if err := add(keystorePrefix+k.name, strings.NewReader(string(k.data))); err != nil {
			return err
		}
	}

	mdata, err := json.MarshalIndent(Manifest{Format: FormatVersion, CreatedAt: now, SchemaVersion: schema, Files: files}, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: manifest: %w", err)
	}
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: manifestEntry, Method: zip.Deflate, Modified: now})
	if err != nil {
		return fmt.Errorf("backup: write archive: %w", err)
	}
	if _, err := mw.Write(mdata); err != nil {
		return fmt.Errorf("backup: write archive: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("backup: write archive: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("backup: sync archive: %w", err)
	}
	return nil
}

func fileSHA256(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // путь выбирает вызывающий код пакета
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return readerSHA256(ctx, f)
}

func readerSHA256(ctx context.Context, r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, ctxReader{ctx, r}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
