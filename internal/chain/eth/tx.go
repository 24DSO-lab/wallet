package eth

import (
	"bytes"
	"errors"
	"math/big"
)

// Транзакция типа 2 (EIP-1559). Здесь только то, что нужно переводам: список
// доступа всегда пуст, создание контракта не поддерживается.

const txTypeDynamicFee = 0x02

var (
	// ErrTxFormat означает, что черновик не является транзакцией типа 2 в
	// точной форме, которую собирает адаптер.
	ErrTxFormat = errors.New("eth: draft is not a canonical type-2 transaction")
)

// dynTx это поля неподписанной транзакции типа 2.
type dynTx struct {
	ChainID        uint64
	Nonce          uint64
	MaxPriorityFee *big.Int // wei за газ
	MaxFee         *big.Int // wei за газ
	Gas            uint64
	To             Address
	Value          *big.Int // wei
	Data           []byte
}

func (t dynTx) fields() [][]byte {
	return [][]byte{
		rlpUint64(t.ChainID),
		rlpUint64(t.Nonce),
		rlpUint(t.MaxPriorityFee),
		rlpUint(t.MaxFee),
		rlpUint64(t.Gas),
		rlpBytes(t.To[:]),
		rlpUint(t.Value),
		rlpBytes(t.Data),
		rlpList(), // accessList
	}
}

// unsigned возвращает 0x02 || rlp([chainId, nonce, maxPriorityFeePerGas,
// maxFeePerGas, gasLimit, to, value, data, accessList]). Это же байты черновика,
// а Keccak-256 от них это дайджест подписи.
func (t dynTx) unsigned() []byte {
	return append([]byte{txTypeDynamicFee}, rlpList(t.fields()...)...)
}

// signed возвращает 0x02 || rlp([..., yParity, r, s]).
func (t dynTx) signed(yParity byte, r, s *big.Int) []byte {
	f := append(t.fields(), rlpUint64(uint64(yParity)), rlpUint(r), rlpUint(s))
	return append([]byte{txTypeDynamicFee}, rlpList(f...)...)
}

// parseUnsigned разбирает черновик строго. Всё, что адаптер не собирает сам
// (другой тип, лишние или недостающие поля, пустой to, непустой список
// доступа, неканоническое число), отвергается. Повторное кодирование должно
// дать исходные байты: это закрывает любые остаточные неоднозначности.
func parseUnsigned(b []byte) (dynTx, error) {
	var t dynTx
	if len(b) < 2 || b[0] != txTypeDynamicFee {
		return t, ErrTxFormat
	}
	root, err := rlpDecode(b[1:])
	if err != nil || !root.List || len(root.Items) != 9 {
		return t, ErrTxFormat
	}
	f := root.Items
	var e [8]error
	t.ChainID, e[0] = f[0].uint64()
	t.Nonce, e[1] = f[1].uint64()
	t.MaxPriorityFee, e[2] = f[2].bigInt(32)
	t.MaxFee, e[3] = f[3].bigInt(32)
	t.Gas, e[4] = f[4].uint64()
	t.Value, e[6] = f[6].bigInt(32)
	for _, err := range e {
		if err != nil {
			return dynTx{}, ErrTxFormat
		}
	}
	if f[5].List || len(f[5].Data) != len(t.To) {
		return dynTx{}, ErrTxFormat // пустой to это создание контракта
	}
	copy(t.To[:], f[5].Data)
	if f[7].List {
		return dynTx{}, ErrTxFormat
	}
	t.Data = bytes.Clone(f[7].Data)
	if !f[8].List || len(f[8].Items) != 0 {
		return dynTx{}, ErrTxFormat
	}
	if !bytes.Equal(t.unsigned(), b) {
		return dynTx{}, ErrTxFormat
	}
	return t, nil
}
