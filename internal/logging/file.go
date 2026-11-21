package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// RotatingFile это файл журнала с ограничением размера. Когда запись не
// помещается в maxSize, файл переименовывается в .1, прежние копии сдвигаются
// (.1 в .2 и так далее), самая старая удаляется. Права 0600, каталог 0700:
// журнал не должен читаться другими пользователями компьютера.
type RotatingFile struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	backups int
	f       *os.File
	size    int64
}

// OpenRotating открывает (и при необходимости создаёт) файл журнала.
func OpenRotating(path string, maxSize int64, backups int) (*RotatingFile, error) {
	if maxSize <= 0 || backups < 1 {
		return nil, fmt.Errorf("logging: invalid rotation settings")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("logging: create directory: %w", err)
	}
	r := &RotatingFile{path: path, maxSize: maxSize, backups: backups}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	//nolint:gosec // path is built by the application from its own data directory.
	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("logging: open log file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("logging: stat log file: %w", err)
	}
	r.f, r.size = f, info.Size()
	return nil
}

func (r *RotatingFile) rotate() error {
	if err := r.f.Close(); err != nil {
		return err
	}
	r.f = nil
	_ = os.Remove(fmt.Sprintf("%s.%d", r.path, r.backups))
	for i := r.backups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	if err := os.Rename(r.path, r.path+".1"); err != nil {
		return err
	}
	return r.open()
}

// Write реализует io.Writer.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.maxSize {
		if err := r.rotate(); err != nil {
			// Не теряем запись: продолжаем в текущем файле, если он ещё открыт.
			if r.f == nil {
				if openErr := r.open(); openErr != nil {
					return 0, openErr
				}
			}
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// Path возвращает путь к текущему файлу журнала.
func (r *RotatingFile) Path() string { return r.path }

// Close закрывает файл.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// BestEffort оборачивает w так, чтобы его ошибки никогда не мешали записи в
// другие приёмники. У приложения Windows без консоли stderr недоступен, и
// io.MultiWriter остановился бы на нём.
func BestEffort(w io.Writer) io.Writer { return bestEffort{w} }

type bestEffort struct{ w io.Writer }

func (b bestEffort) Write(p []byte) (int, error) {
	_, _ = b.w.Write(p)
	return len(p), nil
}
