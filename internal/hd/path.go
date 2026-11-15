package hd

import (
	"errors"
	"strconv"
	"strings"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// Hardened это смещение усиленных индексов BIP32.
const Hardened uint32 = 0x80000000

// maxPathDepth ограничивает глубину пути: в кошельке пути не длиннее пяти
// уровней, а длинный путь от адаптера это ошибка.
const maxPathDepth = 10

// Типы монет SLIP-44, которые использует кошелёк.
const (
	coinBitcoin  = 0
	coinTestnet  = 1
	coinEthereum = 60
	coinTron     = 195
)

// ErrInvalidPath возвращается для пути, не соответствующего виду m/44'/60'/0'/0/0.
var ErrInvalidPath = errors.New("hd: invalid derivation path")

// Path это путь деривации BIP32 без начального "m".
type Path []uint32

// ParsePath разбирает путь вида m/84'/0'/0'/0/5. Усиленный индекс помечается
// ' или h. Допускаются только десятичные индексы без знака и ведущих нулей.
func ParsePath(s string) (Path, error) {
	parts := strings.Split(s, "/")
	if parts[0] != "m" || len(parts)-1 > maxPathDepth {
		return nil, ErrInvalidPath
	}
	path := make(Path, 0, len(parts)-1)
	for _, p := range parts[1:] {
		hardened := false
		if n := len(p); n > 0 && (p[n-1] == '\'' || p[n-1] == 'h') {
			hardened = true
			p = p[:n-1]
		}
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return nil, ErrInvalidPath
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return nil, ErrInvalidPath
			}
		}
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil || v >= uint64(Hardened) {
			return nil, ErrInvalidPath
		}
		idx := uint32(v)
		if hardened {
			idx += Hardened
		}
		path = append(path, idx)
	}
	return path, nil
}

// String возвращает путь в виде m/84'/0'/0'.
func (p Path) String() string {
	var b strings.Builder
	b.WriteByte('m')
	for _, i := range p {
		b.WriteByte('/')
		b.WriteString(strconv.FormatUint(uint64(i&^Hardened), 10))
		if i >= Hardened {
			b.WriteByte('\'')
		}
	}
	return b.String()
}

// Child возвращает путь, продолженный индексом i. Исходный путь не меняется.
func (p Path) Child(i uint32) Path {
	return append(append(Path(nil), p...), i)
}

// BTCAccountPath возвращает путь счёта BIP84: m/84'/0'/0' для основного
// профиля и m/84'/1'/0' для тестового (A4).
func BTCAccountPath(profile domain.Profile) (Path, error) {
	coin, err := btcCoin(profile)
	if err != nil {
		return nil, err
	}
	return Path{84 + Hardened, coin + Hardened, 0 + Hardened}, nil
}

// ETHPath возвращает путь адреса ETH и USDC: m/44'/60'/0'/0/0.
func ETHPath() Path { return Path{44 + Hardened, coinEthereum + Hardened, Hardened, 0, 0} }

// TronPath возвращает путь адреса TRON: m/44'/195'/0'/0/0.
func TronPath() Path { return Path{44 + Hardened, coinTron + Hardened, Hardened, 0, 0} }

func btcCoin(profile domain.Profile) (uint32, error) {
	switch profile {
	case domain.ProfileMain:
		return coinBitcoin, nil
	case domain.ProfileTest:
		return coinTestnet, nil
	}
	return 0, errors.New("hd: unknown network profile")
}

// CheckPathForNetwork проверяет, что путь принадлежит сети и профилю: BTC
// только m/84'/{0|1}'/0'/{0|1}/i, ETH только m/44'/60'/0'/0/0, TRON только
// m/44'/195'/0'/0/0. Так подпись нельзя запросить по пути чужой монеты.
func CheckPathForNetwork(network domain.Network, profile domain.Profile, p Path) error {
	switch network {
	case domain.NetworkBitcoin:
		coin, err := btcCoin(profile)
		if err != nil {
			return err
		}
		if len(p) == 5 && p[0] == 84+Hardened && p[1] == coin+Hardened && p[2] == Hardened &&
			(p[3] == 0 || p[3] == 1) && p[4] < Hardened {
			return nil
		}
	case domain.NetworkEthereum:
		if slicesEqual(p, ETHPath()) {
			return nil
		}
	case domain.NetworkTron:
		if slicesEqual(p, TronPath()) {
			return nil
		}
	}
	return ErrInvalidPath
}

func slicesEqual(a, b Path) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
