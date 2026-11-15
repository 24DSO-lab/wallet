package tron

import (
	"errors"
	"fmt"
)

// Минимальный протобуф wire-формат: только varint (тип 0) и байтовые поля
// (тип 2). Этого хватает для raw_data TRON. Разбор строгий: любое поле или
// кодирование, которого мы не ждём, это ошибка, а не пропуск. Так в подписанную
// транзакцию не может попасть то, чего Verify не увидел.

var (
	errPBTruncated  = errors.New("tron: protobuf truncated")
	errPBVarint     = errors.New("tron: protobuf varint is malformed or not minimal")
	errPBWireType   = errors.New("tron: protobuf wire type is not supported")
	errPBFieldNum   = errors.New("tron: protobuf field number is invalid")
	errPBTooDeep    = errors.New("tron: protobuf message is too large")
	errPBNotCanon   = errors.New("tron: protobuf is not in canonical form")
	errUnknownField = errors.New("tron: unexpected protobuf field")
)

const (
	wtVarint = 0
	wtBytes  = 2

	// maxPBFields защищает разбор от вывернутого ввода.
	maxPBFields = 64
)

type pbField struct {
	num uint64
	wt  uint8
	v   uint64 // для wtVarint
	b   []byte // для wtBytes, ссылка на исходный срез
}

func pbAppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func pbAppendTag(dst []byte, num uint64, wt uint8) []byte {
	return pbAppendVarint(dst, num<<3|uint64(wt))
}

func pbAppendUint(dst []byte, num, v uint64) []byte {
	return pbAppendVarint(pbAppendTag(dst, num, wtVarint), v)
}

func pbAppendBytes(dst []byte, num uint64, b []byte) []byte {
	dst = pbAppendTag(dst, num, wtBytes)
	dst = pbAppendVarint(dst, uint64(len(b)))
	return append(dst, b...)
}

// pbReadVarint читает varint и требует минимальной длины кодирования.
func pbReadVarint(b []byte) (v uint64, n int, err error) {
	var shift uint
	for i := 0; i < len(b); i++ {
		if i == 10 {
			return 0, 0, errPBVarint
		}
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, errPBVarint // переполнение uint64
		}
		v |= uint64(c&0x7F) << shift
		if c < 0x80 {
			if c == 0 && i > 0 {
				return 0, 0, errPBVarint // лишний нулевой байт
			}
			return v, i + 1, nil
		}
		shift += 7
	}
	return 0, 0, errPBTruncated
}

// pbParse разбирает сообщение на поля в порядке следования.
func pbParse(b []byte) ([]pbField, error) {
	var out []pbField
	for len(b) > 0 {
		if len(out) >= maxPBFields {
			return nil, errPBTooDeep
		}
		tag, n, err := pbReadVarint(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		f := pbField{num: tag >> 3, wt: uint8(tag & 7)}
		if f.num == 0 {
			return nil, errPBFieldNum
		}
		switch f.wt {
		case wtVarint:
			if f.v, n, err = pbReadVarint(b); err != nil {
				return nil, err
			}
			b = b[n:]
		case wtBytes:
			l, n, err := pbReadVarint(b)
			if err != nil {
				return nil, err
			}
			b = b[n:]
			if l > uint64(len(b)) {
				return nil, errPBTruncated
			}
			f.b, b = b[:l:l], b[l:]
		default:
			return nil, fmt.Errorf("%w: %d", errPBWireType, f.wt)
		}
		out = append(out, f)
	}
	return out, nil
}
