package eth

import (
	"errors"
	"math/big"
)

// ABI-кодирование двух вызовов ERC-20 без внешних зависимостей. Формат
// фиксирован: 4 байта селектора и 32-байтные слова.
var (
	selectorTransfer = [4]byte{0xa9, 0x05, 0x9c, 0xbb} // transfer(address,uint256)
	selectorBalance  = [4]byte{0x70, 0xa0, 0x82, 0x31} // balanceOf(address)
)

const (
	wordLen         = 32
	balanceOfLen    = 4 + wordLen
	transferCallLen = 4 + 2*wordLen
)

// ErrCalldata означает, что calldata не является вызовом transfer в точной форме.
var ErrCalldata = errors.New("eth: calldata is not a canonical ERC-20 transfer call")

var maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// encodeBalanceOf кодирует вызов balanceOf(owner).
func encodeBalanceOf(owner Address) []byte {
	out := make([]byte, balanceOfLen)
	copy(out, selectorBalance[:])
	copy(out[4+12:], owner[:])
	return out
}

// encodeTransfer кодирует вызов transfer(to, amount). Сумма обязана помещаться
// в uint256 и быть неотрицательной.
func encodeTransfer(to Address, amount *big.Int) ([]byte, error) {
	if amount == nil || amount.Sign() < 0 || amount.Cmp(maxUint256) > 0 {
		return nil, ErrCalldata
	}
	out := make([]byte, transferCallLen)
	copy(out, selectorTransfer[:])
	copy(out[4+12:], to[:])
	amount.FillBytes(out[4+wordLen:])
	return out, nil
}

// decodeTransfer разбирает calldata transfer строго: длина ровно 68 байт,
// селектор transfer, в слове адреса 12 нулевых старших байт. Любая другая
// форма (лишние байты, грязное заполнение) отклоняется: Verify не должен
// принимать то, что другой разбор мог бы прочитать иначе.
func decodeTransfer(data []byte) (Address, *big.Int, error) {
	var to Address
	if len(data) != transferCallLen || [4]byte(data[:4]) != selectorTransfer {
		return to, nil, ErrCalldata
	}
	for _, b := range data[4 : 4+12] {
		if b != 0 {
			return to, nil, ErrCalldata
		}
	}
	copy(to[:], data[4+12:4+wordLen])
	return to, new(big.Int).SetBytes(data[4+wordLen:]), nil
}

// decodeWord разбирает ответ eth_call, равный одному слову uint256.
func decodeWord(b []byte) (*big.Int, error) {
	if len(b) != wordLen {
		return nil, errors.New("eth: call result is not a single 32-byte word")
	}
	return new(big.Int).SetBytes(b), nil
}
