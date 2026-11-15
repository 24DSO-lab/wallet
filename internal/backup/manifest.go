package backup

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// FormatVersion это версия формата архива. Увеличивается при несовместимом изменении.
const FormatVersion = 1

const (
	manifestEntry  = "manifest.json"
	dbEntry        = "db/wallet.db"
	keystorePrefix = "keystore/"

	maxManifestSize = 1 << 20
)

// FileEntry описывает один файл архива в манифесте.
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"` // 64 символа hex, строчные
}

// Manifest это содержимое manifest.json.
type Manifest struct {
	Format        int         `json:"format"`
	CreatedAt     time.Time   `json:"created_at"`
	SchemaVersion int         `json:"schema_version"`
	Files         []FileEntry `json:"files"`
}

type entryKind int

const (
	kindManifest entryKind = iota + 1
	kindDB
	kindKeystore
)

// keystoreName допускает простые имена: буквы, цифры, точку, дефис и
// подчёркивание; без разделителей путей, без начальной точки и без точки или
// пробела в конце (Windows их молча отбрасывает).
var keystoreName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,126}[A-Za-z0-9_-])?$`)

// reservedDeviceNames это имена устройств Windows: файл с таким именем (с
// любым расширением) открывает устройство, а не файл.
var reservedDeviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// validKeystoreName сообщает, что имя файла keystore безопасно как имя в
// архиве и в каталоге на любой ОС.
func validKeystoreName(name string) bool {
	if !keystoreName.MatchString(name) {
		return false
	}
	stem, _, _ := strings.Cut(name, ".")
	return !reservedDeviceNames[strings.ToUpper(stem)]
}

// classifyEntry разбирает имя записи архива. Разрешён закрытый список:
// manifest.json, db/wallet.db и keystore/<простое имя>. Всё остальное (пути
// вверх, абсолютные пути, обратные слэши, диски, вложенные каталоги) это
// ошибка, поэтому выйти за целевой каталог имя не может.
func classifyEntry(name string) (entryKind, error) {
	switch {
	case name == manifestEntry:
		return kindManifest, nil
	case name == dbEntry:
		return kindDB, nil
	case strings.HasPrefix(name, keystorePrefix) && validKeystoreName(name[len(keystorePrefix):]):
		return kindKeystore, nil
	}
	return 0, errors.New("archive entry has a disallowed name")
}

func validSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && s == strings.ToLower(s)
}

// parseManifest строго разбирает манифест: неизвестные поля, лишние данные
// после объекта, повторные пути, неверные размеры и суммы это ошибка.
func parseManifest(data []byte) (Manifest, error) {
	if len(data) > maxManifestSize {
		return Manifest{}, errors.New("manifest is too large")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("manifest is not valid: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("manifest has data after the JSON object")
	}
	if m.Format != FormatVersion {
		return Manifest{}, fmt.Errorf("unsupported backup format %d, this build reads %d", m.Format, FormatVersion)
	}
	if m.SchemaVersion < 1 {
		return Manifest{}, errors.New("manifest has no schema version")
	}
	seen := make(map[string]bool, len(m.Files))
	dbs := 0
	for _, f := range m.Files {
		kind, err := classifyEntry(f.Path)
		if err != nil || kind == kindManifest {
			return Manifest{}, errors.New("manifest lists a disallowed path")
		}
		if seen[f.Path] {
			return Manifest{}, errors.New("manifest lists a path twice")
		}
		seen[f.Path] = true
		if f.Size < 0 || !validSHA256Hex(f.SHA256) {
			return Manifest{}, errors.New("manifest has an invalid size or checksum")
		}
		if kind == kindDB {
			dbs++
		}
	}
	if dbs != 1 {
		return Manifest{}, errors.New("manifest must list exactly one database")
	}
	return m, nil
}
