//go:build windows

package securestore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const cryptProtectUIForbidden = 0x1

type dataBlob struct {
	size uint32
	data *byte
}

var (
	crypt32 = syscall.NewLazyDLL("crypt32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	protectData = crypt32.NewProc("CryptProtectData")
	unprotectData = crypt32.NewProc("CryptUnprotectData")
	localFree = kernel32.NewProc("LocalFree")
)

func transform(input []byte, proc *syscall.LazyProc) ([]byte, error) {
	if len(input) == 0 {
		return nil, errors.New("securestore: empty input")
	}
	in := dataBlob{size: uint32(len(input)), data: &input[0]}
	var out dataBlob
	ok, _, callErr := proc.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0,
		cryptProtectUIForbidden, uintptr(unsafe.Pointer(&out)),
	)
	if ok == 0 {
		return nil, fmt.Errorf("securestore: DPAPI operation failed: %w", callErr)
	}
	defer localFree.Call(uintptr(unsafe.Pointer(out.data)))
	if out.size == 0 || out.data == nil {
		return nil, errors.New("securestore: DPAPI returned empty data")
	}
	return append([]byte(nil), unsafe.Slice(out.data, out.size)...), nil
}

func LoadProviderKey(path string) (string, error) {
	encrypted, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("securestore: read provider key: %w", err)
	}
	defer wipe(encrypted)
	plain, err := transform(encrypted, unprotectData)
	if err != nil {
		return "", err
	}
	defer wipe(plain)
	return string(plain), nil
}

func SaveProviderKey(path, key string) error {
	if key == "" {
		return DeleteProviderKey(path)
	}
	plain := []byte(key)
	defer wipe(plain)
	encrypted, err := transform(plain, protectData)
	if err != nil {
		return err
	}
	defer wipe(encrypted)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("securestore: create secret directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".provider-key-*")
	if err != nil {
		return fmt.Errorf("securestore: create temporary provider key: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("securestore: protect temporary provider key: %w", err)
	}
	if _, err := io.Copy(tmp, bytes.NewReader(encrypted)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("securestore: write encrypted provider key: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("securestore: sync encrypted provider key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("securestore: close encrypted provider key: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("securestore: replace encrypted provider key: %w", err)
	}
	return nil
}

func DeleteProviderKey(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("securestore: delete provider key: %w", err)
	}
	return nil
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
