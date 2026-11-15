package tron

import (
	"errors"
	"fmt"
	"strings"

	"github.com/24DSO-lab/wallet/internal/domain"
)

const (
	// addrPrefix это первый байт адреса TRON (и в main, и в Nile).
	addrPrefix byte = 0x41
	// addrLen это длина адреса в байтах: префикс и 20 байт хеша ключа.
	addrLen = 21
	// maxAddrText ограничивает длину вводимой строки до разбора.
	maxAddrText = 64
)

// Ошибки адреса. Тексты не содержат введённого значения.
var (
	ErrAddressEmpty     = errors.New("tron: address is empty")
	ErrAddressNotTron   = errors.New("tron: not a TRON address (must start with T)")
	ErrAddressEthereum  = errors.New("tron: this is an Ethereum address, not TRON")
	ErrAddressBitcoin   = errors.New("tron: this is a Bitcoin address, not TRON")
	ErrAddressFormat    = errors.New("tron: address has invalid format")
	ErrAddressChecksum  = errors.New("tron: address checksum mismatch")
	ErrAddressPrefix    = errors.New("tron: address has wrong network prefix")
	ErrAddressLength    = errors.New("tron: address has wrong length")
	ErrPublicKeyInvalid = errors.New("tron: public key must be 65 bytes uncompressed (0x04||X||Y)")
)

// Addr это адрес TRON в бинарном виде: 0x41 и 20 байт.
type Addr [addrLen]byte

// String возвращает адрес в base58check (начинается с T).
func (a Addr) String() string { return base58CheckEncode(a[:]) }

// Hex возвращает адрес в hex с префиксом 41 (формат API при visible=false).
func (a Addr) Hex() string { return hexString(a[:]) }

// Bytes20 возвращает 20 байт хеша без префикса (так адрес лежит в calldata).
func (a Addr) Bytes20() [20]byte { return [20]byte(a[1:]) }

// ParseAddress разбирает адрес base58check и проверяет префикс и контрольную
// сумму. Адреса других сетей отклоняются понятной ошибкой с кодом
// domain.CodeInvalidAddress.
func ParseAddress(s string) (Addr, error) {
	var zero Addr
	bad := func(err error) (Addr, error) {
		return zero, domain.NewError(domain.CodeInvalidAddress, err)
	}
	if s == "" {
		return bad(ErrAddressEmpty)
	}
	if len(s) > maxAddrText {
		return bad(ErrAddressLength)
	}
	// Подсказки про чужие сети: чтобы пользователь понял, что перепутал сеть.
	if len(s) == 42 && (strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
		return bad(ErrAddressEthereum)
	}
	if low := strings.ToLower(s); strings.HasPrefix(low, "bc1") || strings.HasPrefix(low, "tb1") {
		return bad(ErrAddressBitcoin)
	}
	if s[0] != 'T' {
		return bad(ErrAddressNotTron)
	}
	payload, err := base58CheckDecode(s)
	if err != nil {
		if errors.Is(err, errBase58Checksum) {
			return bad(ErrAddressChecksum)
		}
		return bad(fmt.Errorf("%w: %w", ErrAddressFormat, err))
	}
	if len(payload) != addrLen {
		return bad(ErrAddressLength)
	}
	if payload[0] != addrPrefix {
		return bad(ErrAddressPrefix)
	}
	return Addr(payload), nil
}

// ValidateAddress проверяет адрес правилами TRON: base58check, префикс 0x41,
// 21 байт, адрес начинается с T. Профиль на формат адреса не влияет.
func (a *Adapter) ValidateAddress(addr string) error {
	_, err := ParseAddress(addr)
	return err
}

// AddressFromPublicKey строит адрес из несжатого ключа в 65 байт:
// последние 20 байт Keccak-256 от X||Y, префикс 0x41, base58check.
func (a *Adapter) AddressFromPublicKey(pub []byte) (string, error) {
	addr, err := addrFromPublicKey(pub)
	if err != nil {
		return "", err
	}
	return addr.String(), nil
}

func addrFromPublicKey(pub []byte) (Addr, error) {
	var out Addr
	if len(pub) != 65 || pub[0] != 0x04 {
		return out, domain.NewError(domain.CodeInvalidInput, ErrPublicKeyInvalid)
	}
	h := keccak256(pub[1:])
	out[0] = addrPrefix
	copy(out[1:], h[12:])
	return out, nil
}
