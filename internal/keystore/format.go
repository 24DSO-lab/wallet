package keystore

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/24DSO-lab/wallet/internal/keystore/argon2id"
	"github.com/24DSO-lab/wallet/internal/secret"
)

// Формат файла кошелька, версия 1 (все числа big-endian):
//
//	"WLTK"            4  сигнатура
//	version           1  = 1
//	kdf               1  = 1 (Argon2id v0x13)
//	cipher            1  = 1 (AES-256-GCM)
//	time              4  проходы Argon2id
//	memory KiB        4
//	threads           1
//	idLen             1  длина идентификатора кошелька
//	id                n  идентификатор
//	salt             16
//	nonce            12
//	ciphertext      272  AES-GCM от payload (256) и тег (16)
//
// Весь заголовок до шифртекста входит в AAD: подмена параметров KDF,
// соли, nonce и идентификатора ломает расшифровку. Идентификатор в
// заголовке привязывает файл к кошельку: переименованный файл не
// расшифруется как чужой. Payload это длина мнемоники (2 байта), сама
// мнемоника и нули до 256 байт, поэтому размер файла не зависит от длины
// фразы. Парольная фраза BIP39 в файл не попадает.
const (
	magic          = "WLTK"
	formatVersion  = 1
	kdfArgon2id    = 1
	cipherAESGCM   = 1
	saltLen        = 16
	nonceLen       = 12
	payloadLen     = 256
	tagLen         = 16
	maxIDLen       = 64
	fixedHeaderLen = 4 + 1 + 1 + 1 + 4 + 4 + 1 + 1
)

var (
	errCorrupt     = errors.New("keystore: file is corrupt")
	errBadPassword = errors.New("keystore: wrong password or modified file")
)

type header struct {
	params Params
	id     string
	salt   [saltLen]byte
	nonce  [nonceLen]byte
}

func (h header) marshal() []byte {
	b := make([]byte, 0, fixedHeaderLen+len(h.id)+saltLen+nonceLen)
	b = append(b, magic...)
	b = append(b, formatVersion, kdfArgon2id, cipherAESGCM)
	b = binary.BigEndian.AppendUint32(b, h.params.Time)
	b = binary.BigEndian.AppendUint32(b, h.params.MemoryKiB)
	b = append(b, h.params.Threads, byte(len(h.id))) //nolint:gosec // sealFile validates the wallet ID length before marshal.
	b = append(b, h.id...)
	b = append(b, h.salt[:]...)
	b = append(b, h.nonce[:]...)
	return b
}

// parseFile разбирает структуру файла. Пароль не нужен; ошибка структуры
// это errCorrupt.
func parseFile(file []byte) (h header, headerBytes, ct []byte, err error) {
	if len(file) < fixedHeaderLen || string(file[:4]) != magic {
		return h, nil, nil, errCorrupt
	}
	if file[4] != formatVersion || file[5] != kdfArgon2id || file[6] != cipherAESGCM {
		return h, nil, nil, fmt.Errorf("%w: unsupported version or algorithm", errCorrupt)
	}
	h.params = Params{
		Time:      binary.BigEndian.Uint32(file[7:11]),
		MemoryKiB: binary.BigEndian.Uint32(file[11:15]),
		Threads:   file[15],
	}
	idLen := int(file[16])
	if idLen == 0 || idLen > maxIDLen || !h.params.withinReadBounds() {
		return h, nil, nil, fmt.Errorf("%w: header values out of range", errCorrupt)
	}
	headerLen := fixedHeaderLen + idLen + saltLen + nonceLen
	if len(file) != headerLen+payloadLen+tagLen {
		return h, nil, nil, fmt.Errorf("%w: unexpected size", errCorrupt)
	}
	h.id = string(file[fixedHeaderLen : fixedHeaderLen+idLen])
	copy(h.salt[:], file[fixedHeaderLen+idLen:])
	copy(h.nonce[:], file[fixedHeaderLen+idLen+saltLen:])
	return h, file[:headerLen], file[headerLen:], nil
}

func deriveKey(password secret.Bytes, h header) []byte {
	return argon2id.Key(password.Expose(), h.salt[:], nil, nil, h.params.Time, h.params.MemoryKiB, h.params.Threads, derivedKeyLen)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// encryptFile шифрует мнемонику паролем и возвращает содержимое файла. Соль
// и nonce случайные и новые при каждой записи.
func encryptFile(id string, mnemonic []byte, password secret.Bytes, params Params) ([]byte, error) {
	h := header{params: params, id: id}
	if _, err := rand.Read(h.salt[:]); err != nil {
		return nil, fmt.Errorf("keystore: random salt: %w", err)
	}
	if _, err := rand.Read(h.nonce[:]); err != nil {
		return nil, fmt.Errorf("keystore: random nonce: %w", err)
	}
	return sealFile(h, mnemonic, password)
}

// sealFile шифрует с готовым заголовком. Соль и nonce на повтор не
// проверяются: вызывающий (encryptFile) всегда берёт новые.
func sealFile(h header, mnemonic []byte, password secret.Bytes) ([]byte, error) {
	if len(mnemonic) == 0 || len(mnemonic) > payloadLen-2 || len(h.id) == 0 || len(h.id) > maxIDLen {
		return nil, errors.New("keystore: mnemonic length out of range")
	}
	key := deriveKey(password, h)
	defer clear(key)
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	payload := make([]byte, payloadLen)
	defer clear(payload)
	binary.BigEndian.PutUint16(payload, uint16(len(mnemonic))) //nolint:gosec // The length is bounded by payloadLen-2 (254) above.
	copy(payload[2:], mnemonic)

	hb := h.marshal()
	return aead.Seal(hb[:len(hb):len(hb)], h.nonce[:], payload, hb), nil
}

// decryptFile расшифровывает файл кошелька id. Вызывающий затирает результат.
func decryptFile(id string, file []byte, password secret.Bytes) (secret.Bytes, error) {
	h, hb, ct, err := parseFile(file)
	if err != nil {
		return secret.Bytes{}, err
	}
	if h.id != id {
		return secret.Bytes{}, fmt.Errorf("%w: file belongs to another wallet", errCorrupt)
	}
	key := deriveKey(password, h)
	defer clear(key)
	aead, err := newGCM(key)
	if err != nil {
		return secret.Bytes{}, err
	}
	payload, err := aead.Open(nil, h.nonce[:], ct, hb)
	if err != nil {
		return secret.Bytes{}, errBadPassword
	}
	defer clear(payload)
	n := int(binary.BigEndian.Uint16(payload))
	if n == 0 || n > payloadLen-2 || !allZero(payload[2+n:]) {
		return secret.Bytes{}, fmt.Errorf("%w: bad payload", errCorrupt)
	}
	return secret.Copy(payload[2 : 2+n]), nil
}

func allZero(b []byte) bool {
	return bytes.Equal(b, make([]byte, len(b)))
}
