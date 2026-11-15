package eth

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
)

// Ошибки адресов. Наружу они приходят обёрнутыми в domain.AppError.
var (
	ErrAddressFormat   = errors.New("eth: address must be 0x followed by 40 hex digits")
	ErrAddressChecksum = errors.New("eth: address checksum (EIP-55) does not match")
	ErrZeroAddress     = errors.New("eth: the zero address is not an allowed recipient")
	ErrPublicKey       = errors.New("eth: public key must be a valid uncompressed secp256k1 key")
)

const addressHexLen = 40

// Address это 20 байт адреса Ethereum.
type Address [20]byte

// parseAddress разбирает адрес строго: префикс 0x в нижнем регистре и ровно 40
// шестнадцатеричных цифр. Второе значение сообщает, что в строке есть
// контрольная сумма EIP-55 и она верна. Адрес, где буквы только одного
// регистра (или букв нет вовсе), контрольной суммы не несёт: проверить его
// нечем, и вызывающий обязан показать предупреждение.
func parseAddress(s string) (Address, bool, error) {
	var a Address
	if len(s) != 2+addressHexLen || s[0] != '0' || s[1] != 'x' {
		return a, false, ErrAddressFormat
	}
	body := s[2:]
	var lower, upper bool
	for i := range len(body) {
		switch c := body[i]; {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
			lower = true
		case c >= 'A' && c <= 'F':
			upper = true
		default:
			return a, false, ErrAddressFormat
		}
	}
	if _, err := hex.Decode(a[:], []byte(body)); err != nil {
		return a, false, ErrAddressFormat
	}
	if lower && upper {
		if body != checksumHex(a) {
			return a, false, ErrAddressChecksum
		}
		return a, true, nil
	}
	return a, false, nil
}

// checksumHex возвращает 40 цифр адреса с регистром по EIP-55.
func checksumHex(a Address) string {
	lower := []byte(hex.EncodeToString(a[:]))
	h := keccak256(lower)
	for i, c := range lower {
		if c < 'a' {
			continue
		}
		nibble := h[i/2]
		if i%2 == 0 {
			nibble >>= 4
		}
		if nibble&0x0f >= 8 {
			lower[i] = c - ('a' - 'A')
		}
	}
	return string(lower)
}

// String возвращает адрес в форме EIP-55 с префиксом 0x.
func (a Address) String() string { return "0x" + checksumHex(a) }

// ValidateAddress проверяет адрес получателя. Ethereum-адрес одинаков в main и
// test, поэтому профиль на проверку не влияет.
//
// Правила (см. README пакета):
//   - формат: 0x и 40 hex-цифр;
//   - смешанный регистр: контрольная сумма EIP-55 обязана совпасть;
//   - один регистр: принимается, но Prepare добавляет предупреждение
//     WarnNoChecksumAddress, потому что опечатку такой адрес не поймает;
//   - нулевой адрес отклоняется: перевод на него это сжигание средств.
func ValidateAddress(addr string) error {
	a, _, err := parseAddress(addr)
	if err != nil {
		return domain.NewError(domain.CodeInvalidAddress, err)
	}
	if a == (Address{}) {
		return domain.NewError(domain.CodeInvalidAddress, ErrZeroAddress)
	}
	return nil
}

// addressWarnings возвращает предупреждения об адресе получателя.
func addressWarnings(addr string) []domain.Warning {
	if _, checksummed, err := parseAddress(addr); err == nil && !checksummed {
		return []domain.Warning{{
			Code:   domain.WarnNoChecksumAddress,
			Detail: "address has no EIP-55 checksum, a typo would not be detected",
		}}
	}
	return nil
}

// addressFromPublicKey превращает несжатый ключ (0x04 || X || Y) в адрес EIP-55:
// последние 20 байт Keccak-256 от X || Y. Точка обязана лежать на кривой.
func addressFromPublicKey(pub []byte) (Address, error) {
	if len(pub) != secp256k1.PubKeyUncompressedLen || pub[0] != 0x04 {
		return Address{}, ErrPublicKey
	}
	if _, err := secp256k1.ParsePoint(pub); err != nil {
		return Address{}, fmt.Errorf("%w: %w", ErrPublicKey, err)
	}
	h := keccak256(pub[1:])
	var a Address
	copy(a[:], h[12:])
	return a, nil
}
