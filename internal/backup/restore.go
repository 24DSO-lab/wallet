package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/24DSO-lab/wallet/internal/secret"
)

// RestoreResult описывает итог восстановления.
type RestoreResult struct {
	// PreviousDir это каталог, куда переехали прежние рабочие данные (БД и
	// keystore). Пуст, если до восстановления данных не было. Сервис его не
	// удаляет: решение о чистке принимает вызывающий после проверки пароля.
	PreviousDir string
	Manifest    Manifest
}

// Restore восстанавливает рабочие данные из архива. Рядом с архивом должен
// лежать файл <архив>.sha256. База должна быть закрыта (см. doc.go).
func (s *Service) Restore(ctx context.Context, archivePath string, password secret.Bytes) (res RestoreResult, err error) {
	defer func() { err = mapCtx(err) }()
	if err := ctx.Err(); err != nil {
		return RestoreResult{}, err
	}

	f, err := os.Open(archivePath) //nolint:gosec // путь выбрал пользователь, это и есть вход
	if err != nil {
		return RestoreResult{}, fmt.Errorf("backup: open archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return RestoreResult{}, fmt.Errorf("backup: stat archive: %w", err)
	}
	if !info.Mode().IsRegular() {
		return RestoreResult{}, invalidf("archive is not a regular file")
	}

	// 1. Контрольная сумма всего архива.
	want, err := readChecksumFile(archivePath + checksumExt)
	if err != nil {
		return RestoreResult{}, err
	}
	got, err := readerSHA256(ctx, f)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("backup: checksum: %w", err)
	}
	if got != want {
		return RestoreResult{}, invalidf("archive checksum does not match")
	}

	// 2. Записи архива и манифест.
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return RestoreResult{}, invalidf("archive is not a valid zip: %w", err)
	}
	man, byName, err := s.inspect(zr)
	if err != nil {
		return RestoreResult{}, err
	}

	// 3. Распаковка в каталог рядом с рабочими данными (один том для переименования).
	if err := os.MkdirAll(s.cfg.DataDir, 0o700); err != nil {
		return RestoreResult{}, fmt.Errorf("backup: data dir: %w", err)
	}
	staging, err := os.MkdirTemp(s.cfg.DataDir, ".restore-")
	if err != nil {
		return RestoreResult{}, fmt.Errorf("backup: staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	stagedDB := filepath.Join(staging, "db")
	stagedKS := filepath.Join(staging, "ks")
	for _, d := range []string{stagedDB, stagedKS} {
		if err := os.Mkdir(d, 0o700); err != nil {
			return RestoreResult{}, fmt.Errorf("backup: staging dir: %w", err)
		}
	}
	for _, fe := range man.Files {
		dst := filepath.Join(stagedKS, strings.TrimPrefix(fe.Path, keystorePrefix))
		if fe.Path == dbEntry {
			dst = filepath.Join(stagedDB, s.cfg.DBName)
		}
		if err := s.extract(ctx, byName[fe.Path], fe, dst, stagedDB, stagedKS); err != nil {
			return RestoreResult{}, err
		}
	}

	// 4. Содержимое: заголовки keystore без пароля и заголовок БД.
	// Flush staged file contents and directory entries before they can become active.
	for _, dir := range []string{stagedDB, stagedKS, staging} {
		if err := syncDir(dir); err != nil {
			return RestoreResult{}, fmt.Errorf("backup: sync staged restore data: %w", err)
		}
	}

	for _, fe := range man.Files {
		if !strings.HasPrefix(fe.Path, keystorePrefix) {
			continue
		}
		name := strings.TrimPrefix(fe.Path, keystorePrefix)
		data, err := os.ReadFile(filepath.Join(stagedKS, name)) //nolint:gosec // имя проверено classifyEntry
		if err != nil {
			return RestoreResult{}, fmt.Errorf("backup: read staged keystore file: %w", err)
		}
		if err := s.cfg.CheckKeystoreFile(name, data); err != nil {
			return RestoreResult{}, invalidf("keystore file %q is not valid: %w", name, err)
		}
	}
	schema, err := readSchemaVersion(filepath.Join(stagedDB, s.cfg.DBName))
	if err != nil {
		return RestoreResult{}, invalidf("database in the archive is not valid: %w", err)
	}
	if schema != man.SchemaVersion {
		return RestoreResult{}, invalidf("database schema %d differs from the manifest (%d)", schema, man.SchemaVersion)
	}
	if s.cfg.VerifyKeystorePassword == nil {
		return RestoreResult{}, errors.New("backup: keystore password verification is required")
	}
	if err := s.cfg.VerifyKeystorePassword(ctx, stagedKS, password); err != nil {
		return RestoreResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return RestoreResult{}, err // последняя точка отмены: дальше идёт замена
	}

	// 5. Замена рабочих данных с откатом.
	prev, err := s.swap(stagedDB, stagedKS)
	if err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{PreviousDir: prev, Manifest: man}, nil
}

// readChecksumFile читает сумму из файла формата sha256sum: первое поле строки.
func readChecksumFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // рядом с архивом, выбранным пользователем
	if errors.Is(err, os.ErrNotExist) {
		return "", invalidf("checksum file %s is missing", filepath.Base(path))
	}
	if err != nil {
		return "", fmt.Errorf("backup: open checksum file: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", fmt.Errorf("backup: read checksum file: %w", err)
	}
	if len(data) > 4096 {
		return "", invalidf("checksum file is too large")
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 || !validSHA256Hex(strings.ToLower(fields[0])) {
		return "", invalidf("checksum file has no valid SHA-256")
	}
	return strings.ToLower(fields[0]), nil
}

// inspect проверяет список записей и манифест, не распаковывая данные.
func (s *Service) inspect(zr *zip.Reader) (Manifest, map[string]*zip.File, error) {
	lim := s.cfg.Limits
	if len(zr.File) > lim.MaxFiles {
		return Manifest{}, nil, invalidf("archive has too many entries (%d)", len(zr.File))
	}
	byName := make(map[string]*zip.File, len(zr.File))
	for _, zf := range zr.File {
		if _, dup := byName[zf.Name]; dup {
			return Manifest{}, nil, invalidf("archive has duplicate entries")
		}
		if _, err := classifyEntry(zf.Name); err != nil {
			return Manifest{}, nil, invalidf("%w", err)
		}
		// Только обычные файлы: ссылки, каталоги и устройства не принимаются.
		if zf.Mode()&os.ModeType != 0 || zf.FileInfo().IsDir() {
			return Manifest{}, nil, invalidf("archive entry is not a regular file")
		}
		if zf.Flags&0x1 != 0 {
			return Manifest{}, nil, invalidf("archive entry is encrypted")
		}
		byName[zf.Name] = zf
	}
	mf, ok := byName[manifestEntry]
	if !ok {
		return Manifest{}, nil, invalidf("archive has no manifest")
	}
	if mf.UncompressedSize64 > maxManifestSize {
		return Manifest{}, nil, invalidf("manifest is too large")
	}
	rc, err := mf.Open()
	if err != nil {
		return Manifest{}, nil, invalidf("cannot read manifest: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(rc, maxManifestSize+1))
	_ = rc.Close()
	if err != nil {
		return Manifest{}, nil, invalidf("cannot read manifest: %w", err)
	}
	man, err := parseManifest(data)
	if err != nil {
		return Manifest{}, nil, invalidf("%w", err)
	}
	if man.SchemaVersion > s.cfg.MaxSchemaVersion {
		return Manifest{}, nil, invalidf("backup schema %d is newer than this build supports (%d)", man.SchemaVersion, s.cfg.MaxSchemaVersion)
	}

	// Манифест и записи архива должны совпадать один в один.
	if len(man.Files) != len(byName)-1 {
		return Manifest{}, nil, invalidf("manifest does not match the archive entries")
	}
	var total int64
	for _, fe := range man.Files {
		zf, ok := byName[fe.Path]
		if !ok {
			return Manifest{}, nil, invalidf("manifest lists a file that is not in the archive")
		}
		limit := lim.MaxKeystoreFileSize
		if fe.Path == dbEntry {
			limit = lim.MaxDBSize
		}
		if fe.Size > limit || zf.UncompressedSize64 > uint64(limit) { //nolint:gosec // limit положителен
			return Manifest{}, nil, invalidf("archive file %q exceeds the size limit", fe.Path)
		}
		total += fe.Size
		if total > lim.MaxTotalSize {
			return Manifest{}, nil, invalidf("archive exceeds the total size limit")
		}
	}
	return man, byName, nil
}

// extract распаковывает одну запись в dst, считая размер и SHA-256 по
// фактически прочитанному, а не по заголовку архива.
func (s *Service) extract(ctx context.Context, zf *zip.File, fe FileEntry, dst, stagedDB, stagedKS string) error {
	// Страховка поверх белого списка имён: цель обязана лежать в одном из
	// двух подготовленных каталогов.
	if filepath.Dir(dst) != stagedDB && filepath.Dir(dst) != stagedKS {
		return invalidf("archive entry resolves outside the target directory")
	}
	rc, err := zf.Open()
	if err != nil {
		return invalidf("cannot read %q: %w", fe.Path, err)
	}
	defer func() { _ = rc.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // dst проверен выше
	if err != nil {
		return fmt.Errorf("backup: stage %q: %w", fe.Path, err)
	}
	h := sha256.New()
	// Читаем не больше заявленного в манифесте плюс один байт: бомба в
	// архиве упирается в лимит, а не в диск.
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(ctxReader{ctx, rc}, fe.Size+1))
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalidf("cannot unpack %q: %w", fe.Path, err)
	}
	if n != fe.Size {
		return invalidf("file %q has a different size than the manifest says", fe.Path)
	}
	if hex.EncodeToString(h.Sum(nil)) != fe.SHA256 {
		return invalidf("file %q does not match its checksum in the manifest", fe.Path)
	}
	return nil
}

// swap заменяет рабочие БД и keystore подготовленными. Прежние файлы не
// удаляются, а переносятся в каталог pre-restore-<время>. Любая ошибка
// откатывает уже сделанные переименования в обратном порядке.
func (s *Service) swap(stagedDB, stagedKS string) (prevDir string, err error) {
	data := s.cfg.DataDir
	prev, err := os.MkdirTemp(data, "pre-restore-"+s.cfg.Now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return "", fmt.Errorf("backup: previous data dir: %w", err)
	}

	var undo []func() error
	move := func(from, to string) error {
		if err := s.rename(from, to); err != nil {
			return err
		}
		undo = append(undo, func() error { return s.rename(to, from) })
		return nil
	}
	rollback := func(cause error) error {
		var errs []error
		for i := len(undo) - 1; i >= 0; i-- {
			if uerr := undo[i](); uerr != nil {
				errs = append(errs, uerr)
			}
		}
		if len(errs) == 0 {
			_ = os.Remove(prev) // пуст: всё вернулось на место
			return fmt.Errorf("backup: restore failed, working data left unchanged: %w", cause)
		}
		return fmt.Errorf("backup: restore failed and rollback was incomplete, previous data is in %s: %w",
			prev, errors.Join(append([]error{cause}, errs...)...))
	}
	exists := func(p string) bool { _, err := os.Lstat(p); return err == nil }

	ksDir := filepath.Join(data, s.cfg.KeystoreDirName)
	for _, name := range []string{s.cfg.DBName, s.cfg.DBName + "-wal", s.cfg.DBName + "-shm"} {
		if src := filepath.Join(data, name); exists(src) {
			if err := move(src, filepath.Join(prev, name)); err != nil {
				return "", rollback(err)
			}
		}
	}
	if exists(ksDir) {
		if err := move(ksDir, filepath.Join(prev, s.cfg.KeystoreDirName)); err != nil {
			return "", rollback(err)
		}
	}
	if err := move(filepath.Join(stagedDB, s.cfg.DBName), filepath.Join(data, s.cfg.DBName)); err != nil {
		return "", rollback(err)
	}
	if err := move(stagedKS, ksDir); err != nil {
		return "", rollback(err)
	}
	syncDir(data)

	if err := os.Remove(prev); err == nil { // был пуст: восстановление на чистую установку
		return "", nil
	}
	return prev, nil
}
