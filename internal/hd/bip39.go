package hd

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	_ "embed" // вордлист вшит в бинарник, внешних файлов нет
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/24DSO-lab/wallet/internal/secret"
)

// Допустимые длины мнемоники при импорте (BIP39). Новая мнемоника создаётся
// только в 12 или 24 слова (ADR).
const (
	Words12 = 12
	Words24 = 24
)

const (
	maxWordLen   = 8 // самое длинное слово списка
	pbkdf2Rounds = 2048
	seedLen      = 64
)

// Ошибки BIP39. Тексты не содержат слов мнемоники и парольной фразы.
var (
	ErrWordCount         = errors.New("hd: mnemonic must have 12, 15, 18, 21 or 24 words")
	ErrUnknownWord       = errors.New("hd: word is not in the BIP39 English list")
	ErrChecksum          = errors.New("hd: mnemonic checksum mismatch")
	ErrInvalidEntropy    = errors.New("hd: entropy must be 16, 20, 24, 28 or 32 bytes")
	ErrInvalidText       = errors.New("hd: text is not valid UTF-8")
	ErrUnsupportedLength = errors.New("hd: new mnemonic must have 12 or 24 words")
)

//go:embed wordlist_english.txt
var wordlistText string

// wordlist возвращает 2048 слов в порядке BIP39. Список неизменяемый.
var wordlist = sync.OnceValue(func() []string {
	return strings.Split(strings.TrimSuffix(wordlistText, "\n"), "\n")
})

var wordIndex = sync.OnceValue(func() map[string]uint16 {
	m := make(map[string]uint16, 2048)
	for i, w := range wordlist() {
		m[w] = uint16(i)
	}
	return m
})

// NewMnemonic создаёт мнемонику из 12 или 24 слов по энтропии из crypto/rand.
// Вызывающий затирает результат.
func NewMnemonic(words int) (secret.Bytes, error) {
	var entropyLen int
	switch words {
	case Words12:
		entropyLen = 16
	case Words24:
		entropyLen = 32
	default:
		return secret.Bytes{}, ErrUnsupportedLength
	}
	entropy := make([]byte, entropyLen)
	defer clear(entropy)
	if _, err := rand.Read(entropy); err != nil {
		return secret.Bytes{}, fmt.Errorf("hd: read entropy: %w", err)
	}
	return MnemonicFromEntropy(entropy)
}

// MnemonicFromEntropy кодирует энтропию (16, 20, 24, 28 или 32 байта) в
// мнемонику. Слова разделены одним пробелом. Энтропию затирает вызывающий.
func MnemonicFromEntropy(entropy []byte) (secret.Bytes, error) {
	switch len(entropy) {
	case 16, 20, 24, 28, 32:
	default:
		return secret.Bytes{}, ErrInvalidEntropy
	}
	sum := sha256.Sum256(entropy)
	defer clear(sum[:])
	checkBits := len(entropy) / 4 // ENT/32
	total := len(entropy)*8 + checkBits

	// Битовый буфер: энтропия, затем контрольные биты.
	bits := make([]byte, len(entropy)+1)
	defer clear(bits)
	copy(bits, entropy)
	bits[len(entropy)] = sum[0]

	words := wordlist()
	out := make([]byte, 0, total/11*9)
	for i := 0; i < total/11; i++ {
		var idx int
		for b := 0; b < 11; b++ {
			pos := i*11 + b
			idx = idx<<1 | int(bits[pos/8]>>(7-pos%8)&1)
		}
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, words[idx]...)
	}
	return secret.FromBytes(out), nil
}

// ValidateMnemonic проверяет слова и контрольную сумму. Регистр и пробелы
// между словами не важны.
func ValidateMnemonic(mnemonic secret.Bytes) error {
	idx, err := parseWords(mnemonic)
	if err != nil {
		return err
	}
	defer clear(idx)
	entropy, err := entropyFromIndices(idx)
	if err != nil {
		return err
	}
	clear(entropy)
	return nil
}

// NormalizeMnemonic возвращает мнемонику в каноническом виде: слова в нижнем
// регистре через один пробел. Для неверной мнемоники возвращает ошибку.
// Вызывающий затирает результат.
func NormalizeMnemonic(mnemonic secret.Bytes) (secret.Bytes, error) {
	idx, err := parseWords(mnemonic)
	if err != nil {
		return secret.Bytes{}, err
	}
	defer clear(idx)
	entropy, err := entropyFromIndices(idx)
	if err != nil {
		return secret.Bytes{}, err
	}
	clear(entropy)
	words := wordlist()
	out := make([]byte, 0, len(idx)*9)
	for i, w := range idx {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, words[w]...)
	}
	return secret.FromBytes(out), nil
}

// SuggestWords возвращает до limit слов, начинающихся с prefix (для мастера
// импорта). Регистр не важен. Пустой префикс даёт пустой результат.
func SuggestWords(prefix string, limit int) []string {
	prefix = strings.ToLower(prefix)
	if prefix == "" || limit <= 0 {
		return nil
	}
	words := wordlist()
	i, _ := slices.BinarySearch(words, prefix)
	var out []string
	for ; i < len(words) && len(out) < limit && strings.HasPrefix(words[i], prefix); i++ {
		out = append(out, words[i])
	}
	return out
}

// SeedFromMnemonic выводит 64-байтный seed по BIP39: PBKDF2-HMAC-SHA512,
// 2048 раундов, соль "mnemonic" и парольная фраза. Мнемоника и парольная
// фраза приводятся к NFKD; мнемоника сначала проверяется. Парольная фраза
// может быть пустой. Вызывающий затирает результат.
func SeedFromMnemonic(mnemonic, passphrase secret.Bytes) (secret.Bytes, error) {
	canon, err := NormalizeMnemonic(mnemonic)
	if err != nil {
		return secret.Bytes{}, err
	}
	defer canon.Wipe()

	pass, wipePass, err := nfkd(passphrase.Expose())
	if err != nil {
		return secret.Bytes{}, err
	}
	defer wipePass()

	salt := make([]byte, 0, len("mnemonic")+len(pass))
	defer func() { clear(salt) }()
	salt = append(salt, "mnemonic"...)
	salt = append(salt, pass...)

	return secret.FromBytes(pbkdf2SHA512(canon.Expose(), salt, pbkdf2Rounds, seedLen)), nil
}

// parseWords разбирает мнемонику в индексы слов. Слова приводятся к
// нижнему регистру и NFKD, разделителем служит любой пробельный символ.
// Вызывающий затирает результат.
func parseWords(mnemonic secret.Bytes) ([]uint16, error) {
	text, wipe, err := nfkd(mnemonic.Expose())
	if err != nil {
		return nil, err
	}
	defer wipe()

	var idx []uint16
	index := wordIndex()
	buf := make([]byte, 0, 8)
	defer func() { clear(buf) }()
	for _, tok := range bytes.Fields(text) {
		if len(tok) > maxWordLen {
			clear(idx)
			return nil, fmt.Errorf("%w at position %d", ErrUnknownWord, len(idx)+1)
		}
		buf = append(buf[:0], tok...)
		for i, c := range buf {
			if 'A' <= c && c <= 'Z' {
				buf[i] = c + 'a' - 'A'
			}
		}
		w, ok := index[string(buf)]
		if !ok {
			clear(idx)
			return nil, fmt.Errorf("%w at position %d", ErrUnknownWord, len(idx)+1)
		}
		idx = append(idx, w)
	}
	switch len(idx) {
	case 12, 15, 18, 21, 24:
		return idx, nil
	}
	n := len(idx)
	clear(idx)
	return nil, fmt.Errorf("%w (got %d)", ErrWordCount, n)
}

// entropyFromIndices собирает энтропию из индексов и проверяет контрольные
// биты. Вызывающий затирает результат.
func entropyFromIndices(idx []uint16) ([]byte, error) {
	total := len(idx) * 11
	checkBits := total / 33
	entLen := (total - checkBits) / 8

	bits := make([]byte, entLen+1)
	defer clear(bits)
	for i, w := range idx {
		for b := 0; b < 11; b++ {
			pos := i*11 + b
			if (w>>(10-b))&1 == 1 {
				bits[pos/8] |= 1 << (7 - pos%8)
			}
		}
	}
	entropy := make([]byte, entLen)
	copy(entropy, bits[:entLen])

	sum := sha256.Sum256(entropy)
	defer clear(sum[:])
	mask := byte(0xFF << (8 - checkBits))
	if sum[0]&mask != bits[entLen]&mask {
		clear(entropy)
		return nil, ErrChecksum
	}
	return entropy, nil
}

// nfkd приводит текст к NFKD. Для ASCII копия не нужна. wipe затирает только
// копию, которую сделала эта функция, и никогда буфер вызывающего.
func nfkd(b []byte) (out []byte, wipe func(), err error) {
	if !utf8.Valid(b) {
		return nil, func() {}, ErrInvalidText
	}
	ascii := true
	for _, c := range b {
		if c >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return b, func() {}, nil
	}
	res := norm.NFKD.Bytes(b)
	if len(res) > 0 && len(b) > 0 && &res[0] == &b[0] {
		// Bytes возвращает вход, если он уже в форме NFKD: затирать надо копию,
		// а не буфер вызывающего.
		res = append([]byte(nil), res...)
	}
	return res, func() { clear(res) }, nil
}

// pbkdf2SHA512 это PBKDF2-HMAC-SHA512 (RFC 8018). Своя реализация нужна
// потому, что crypto/pbkdf2 принимает пароль строкой, а строку затереть нельзя.
func pbkdf2SHA512(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha512.New, password)
	hLen := prf.Size()
	blocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, blocks*hLen)

	var counter [4]byte
	u := make([]byte, hLen)
	t := make([]byte, hLen)
	defer clear(u)
	defer clear(t)
	for block := 1; block <= blocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(counter[:], uint32(block))
		prf.Write(counter[:])
		u = prf.Sum(u[:0])
		copy(t, u)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
