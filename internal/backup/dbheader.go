package backup

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const sqliteMagic = "SQLite format 3\x00"

// readSchemaVersion читает версию схемы (user_version) прямо из заголовка файла
// SQLite: байты 60-63, big-endian. Так проверка не требует драйвера БД и не
// открывает чужой файл как базу.
func readSchemaVersion(path string) (int, error) {
	f, err := os.Open(path) //nolint:gosec // путь выбирает код пакета
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	var hdr [100]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return 0, fmt.Errorf("database file is too short: %w", err)
	}
	if !bytes.HasPrefix(hdr[:], []byte(sqliteMagic)) {
		return 0, errors.New("file is not an SQLite database")
	}
	v := binary.BigEndian.Uint32(hdr[60:64])
	if v > 1<<20 {
		return 0, errors.New("database has an implausible schema version")
	}
	return int(v), nil
}
