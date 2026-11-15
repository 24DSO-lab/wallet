package keystore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	fileExt      = ".keystore"
	maxFileSize  = 1024
	stageDirName = ".pwchange"
	tmpPrefix    = ".tmp-"
)

// errSimulatedCrash используют только тесты: операция прерывается без
// очистки, как при обрыве процесса.
var errSimulatedCrash = errors.New("keystore: simulated crash")

func validID(id string) bool {
	if id == "" || len(id) > maxIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+fileExt) }

// listIDs возвращает идентификаторы кошельков по возрастанию. Каталоги,
// ссылки и файлы с чужими именами пропускаются.
func (s *Store) listIDs() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasSuffix(name, fileExt) {
			continue
		}
		if id := strings.TrimSuffix(name, fileExt); validID(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// readFile читает файл кошелька. Слишком большой файл это повреждение: до
// разбора заголовка память под него не выделяется.
func readFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileSize {
		return nil, fmt.Errorf("%w: not a regular small file", errCorrupt)
	}
	return os.ReadFile(path)
}

// writeAtomic записывает файл через временный в том же каталоге: запись,
// fsync, переименование, fsync каталога. Права 0600 (на Windows действует
// наследуемый список доступа каталога данных пользователя).
func (s *Store) writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, tmpPrefix+"*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if s.faults.afterTemp != nil {
		if err := s.faults.afterTemp(); err != nil {
			if errors.Is(err, errSimulatedCrash) {
				return err // временный файл остаётся, как после обрыва
			}
			_ = os.Remove(name)
			return err
		}
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return syncDir(dir)
}

// syncDir сбрасывает запись каталога на диск. Windows не позволяет
// синхронизировать каталог, там это пропускается.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// removeStaleTemps удаляет недописанные временные файлы прерванных записей.
func (s *Store) removeStaleTemps() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tmpPrefix) && e.Type().IsRegular() {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}

// --- смена пароля с журналом ---
//
// Каталог .pwchange содержит new/ (новые файлы), old/ (копии прежних) и
// маркер COMMIT. Пока маркера нет, исходные файлы не тронуты, а каталог
// отбрасывается. После COMMIT новые файлы переименовываются поверх
// исходных; при ошибке маркер переименовывается в ROLLBACK и исходные
// файлы восстанавливаются из old/. Если процесс оборвался, New доводит
// начатое: COMMIT это откат вперёд, ROLLBACK это откат назад, иначе очистка.

func (s *Store) stagePath(parts ...string) string {
	return filepath.Join(append([]string{s.dir, stageDirName}, parts...)...)
}

// changeFiles выполняет замену набора файлов целиком. files это id и новое
// содержимое, old это прежнее содержимое.
func (s *Store) changeFiles(ids []string, oldData, newData map[string][]byte) error {
	stage := s.stagePath()
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	for _, d := range []string{stage, s.stagePath("new"), s.stagePath("old")} {
		if err := os.Mkdir(d, 0o700); err != nil {
			_ = os.RemoveAll(stage)
			return err
		}
	}
	for _, id := range ids {
		for dir, data := range map[string][]byte{"new": newData[id], "old": oldData[id]} {
			if err := s.writeAtomic(s.stagePath(dir, id+fileExt), data); err != nil {
				_ = os.RemoveAll(stage)
				return err
			}
		}
	}
	if err := s.writeAtomic(s.stagePath("COMMIT"), []byte("commit\n")); err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	if s.faults.afterCommit != nil {
		if err := s.faults.afterCommit(); err != nil {
			return err // обрыв после COMMIT: довершит New
		}
	}
	for i, id := range ids {
		if s.faults.beforeRename != nil {
			if err := s.faults.beforeRename(i); err != nil {
				if errors.Is(err, errSimulatedCrash) {
					return err
				}
				return s.rollback(err)
			}
		}
		if err := os.Rename(s.stagePath("new", id+fileExt), s.path(id)); err != nil {
			return s.rollback(err)
		}
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	return os.RemoveAll(stage)
}

// rollback возвращает исходные файлы. cause это причина отката.
func (s *Store) rollback(cause error) error {
	if err := os.Rename(s.stagePath("COMMIT"), s.stagePath("ROLLBACK")); err != nil {
		return fmt.Errorf("keystore: rollback after %v failed: %w", cause, err)
	}
	if err := s.restoreOld(); err != nil {
		return fmt.Errorf("keystore: rollback after %v failed: %w", cause, err)
	}
	if err := os.RemoveAll(s.stagePath()); err != nil {
		return fmt.Errorf("keystore: rollback after %v failed: %w", cause, err)
	}
	return cause
}

func (s *Store) restoreOld() error {
	entries, err := os.ReadDir(s.stagePath("old"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		data, err := os.ReadFile(s.stagePath("old", e.Name()))
		if err != nil {
			return err
		}
		if err := s.writeAtomic(filepath.Join(s.dir, e.Name()), data); err != nil {
			return err
		}
	}
	return nil
}

// recoverStage доводит прерванную смену пароля до конца или отменяет её.
func (s *Store) recoverStage() error {
	stage := s.stagePath()
	if _, err := os.Stat(stage); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	switch {
	case exists(s.stagePath("COMMIT")):
		entries, err := os.ReadDir(s.stagePath("new"))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := os.Rename(s.stagePath("new", e.Name()), filepath.Join(s.dir, e.Name())); err != nil {
				return err
			}
		}
	case exists(s.stagePath("ROLLBACK")):
		if err := s.restoreOld(); err != nil {
			return err
		}
	}
	return os.RemoveAll(stage)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
