package eth

import (
	"errors"
	"math/big"
)

// Минимальный RLP: кодер строк, чисел и списков и строгий декодер.
//
// Декодер отвергает всё неканоническое (лишние нули в длине и числах, длинную
// форму там, где хватает короткой, однобайтовую строку с префиксом, хвост
// после значения). Verify разбирает чужой черновик, и два разбора одних байтов
// не должны расходиться: то, что принял Verify, узел прочтёт так же.

// Ошибки RLP.
var (
	ErrRLP         = errors.New("eth: malformed RLP")
	errRLPDepth    = errors.New("eth: RLP nesting is too deep")
	errRLPNonCanon = errors.New("eth: non-canonical RLP")
)

const rlpMaxDepth = 4

func rlpBytes(b []byte) []byte {
	if len(b) == 1 && b[0] < 0x80 {
		return []byte{b[0]}
	}
	return append(rlpHeader(0x80, len(b)), b...)
}

// rlpUint кодирует неотрицательное число минимальным числом байт; ноль это
// пустая строка.
func rlpUint(n *big.Int) []byte { return rlpBytes(n.Bytes()) }

func rlpUint64(n uint64) []byte { return rlpUint(new(big.Int).SetUint64(n)) }

// rlpList собирает список из уже закодированных элементов.
func rlpList(items ...[]byte) []byte {
	var n int
	for _, it := range items {
		n += len(it)
	}
	out := rlpHeader(0xc0, n)
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

// rlpHeader возвращает префикс длины: base это 0x80 для строк и 0xc0 для списков.
func rlpHeader(base byte, n int) []byte {
	if n <= 55 {
		return []byte{base + byte(n)}
	}
	var lb []byte
	for v := n; v > 0; v >>= 8 {
		lb = append([]byte{byte(v)}, lb...)
	}
	return append([]byte{base + 55 + byte(len(lb))}, lb...)
}

// rlpItem это разобранное значение: строка (Data) или список (Items).
type rlpItem struct {
	List  bool
	Data  []byte
	Items []rlpItem
}

// rlpDecode разбирает ровно одно значение, занимающее весь вход.
func rlpDecode(b []byte) (rlpItem, error) {
	it, rest, err := rlpDecodeItem(b, 0)
	if err != nil {
		return rlpItem{}, err
	}
	if len(rest) != 0 {
		return rlpItem{}, ErrRLP
	}
	return it, nil
}

func rlpDecodeItem(b []byte, depth int) (rlpItem, []byte, error) {
	if len(b) == 0 {
		return rlpItem{}, nil, ErrRLP
	}
	p := b[0]
	switch {
	case p < 0x80:
		return rlpItem{Data: b[:1]}, b[1:], nil
	case p < 0xb8: // строка до 55 байт
		n := int(p - 0x80)
		if len(b)-1 < n {
			return rlpItem{}, nil, ErrRLP
		}
		if n == 1 && b[1] < 0x80 {
			return rlpItem{}, nil, errRLPNonCanon
		}
		return rlpItem{Data: b[1 : 1+n]}, b[1+n:], nil
	case p < 0xc0: // длинная строка
		n, rest, err := rlpLongLen(b, int(p-0xb7))
		if err != nil {
			return rlpItem{}, nil, err
		}
		if len(rest) < n {
			return rlpItem{}, nil, ErrRLP
		}
		return rlpItem{Data: rest[:n]}, rest[n:], nil
	}
	if depth >= rlpMaxDepth {
		return rlpItem{}, nil, errRLPDepth
	}
	var (
		n    int
		rest []byte
	)
	if p < 0xf8 {
		n, rest = int(p-0xc0), b[1:]
	} else {
		var err error
		if n, rest, err = rlpLongLen(b, int(p-0xf7)); err != nil {
			return rlpItem{}, nil, err
		}
	}
	if len(rest) < n {
		return rlpItem{}, nil, ErrRLP
	}
	body, tail := rest[:n], rest[n:]
	list := rlpItem{List: true}
	for len(body) > 0 {
		var (
			it  rlpItem
			err error
		)
		if it, body, err = rlpDecodeItem(body, depth+1); err != nil {
			return rlpItem{}, nil, err
		}
		list.Items = append(list.Items, it)
	}
	return list, tail, nil
}

// rlpLongLen читает длину в lenOfLen байтах после префикса b[0] и возвращает
// её вместе с остатком после поля длины. Длина обязана быть больше 55 и без
// ведущих нулей.
func rlpLongLen(b []byte, lenOfLen int) (int, []byte, error) {
	if lenOfLen > 4 || len(b)-1 < lenOfLen { // 4 байта: вход всё равно меньше 4 ГиБ
		return 0, nil, ErrRLP
	}
	lb := b[1 : 1+lenOfLen]
	if lb[0] == 0 {
		return 0, nil, errRLPNonCanon
	}
	var n int
	for _, c := range lb {
		n = n<<8 | int(c)
	}
	if n <= 55 {
		return 0, nil, errRLPNonCanon
	}
	if n < 0 || n > len(b) {
		return 0, nil, ErrRLP
	}
	return n, b[1+lenOfLen:], nil
}

// uint64 читает каноническое неотрицательное число до 8 байт.
func (it rlpItem) uint64() (uint64, error) {
	n, err := it.bigInt(8)
	if err != nil {
		return 0, err
	}
	return n.Uint64(), nil
}

// bigInt читает каноническое неотрицательное число до maxBytes байт: без
// ведущих нулей, ноль это пустая строка.
func (it rlpItem) bigInt(maxBytes int) (*big.Int, error) {
	if it.List || len(it.Data) > maxBytes || (len(it.Data) > 0 && it.Data[0] == 0) {
		return nil, errRLPNonCanon
	}
	return new(big.Int).SetBytes(it.Data), nil
}
