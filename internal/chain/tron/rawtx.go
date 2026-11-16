package tron

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
)

// Структура raw_data TRON (protocol/Tron.proto), только то, что нужно для
// вызова TRC20 transfer. Номера полей:
//
//	Transaction.raw_data: ref_block_bytes=1, ref_block_hash=4, expiration=8,
//	                      contract=11, timestamp=14, fee_limit=18
//	Contract:             type=1 (31 = TriggerSmartContract), parameter=2 (Any)
//	Any:                  type_url=1, value=2
//	TriggerSmartContract: owner_address=1, contract_address=2, call_value=3, data=4
const (
	contractTypeTrigger = 31
	anyTypeURLTrigger   = "type.googleapis.com/protocol.TriggerSmartContract"

	// Размер calldata transfer(address,uint256): 4 + 32 + 32.
	transferCallLen = 68
)

// transferSelector это первые 4 байта Keccak-256("transfer(address,uint256)").
var transferSelector = [4]byte{0xa9, 0x05, 0x9c, 0xbb}

// Ошибки разбора и проверки черновика.
var (
	ErrDraftMalformed  = errors.New("tron: draft is not a valid raw_data")
	ErrDraftNotTrigger = errors.New("tron: draft is not a single TriggerSmartContract call")
	ErrDraftCalldata   = errors.New("tron: draft calldata is not transfer(address,uint256)")
)

// RawTx это разобранный raw_data вызова TRC20 transfer.
type RawTx struct {
	RefBlockBytes [2]byte
	RefBlockHash  [8]byte
	Expiration    int64 // миллисекунды Unix
	Timestamp     int64 // миллисекунды Unix
	FeeLimit      int64 // sun
	Owner         Addr
	Contract      Addr
	Data          []byte // calldata
}

// transferCalldata собирает calldata transfer(to, amount).
func transferCalldata(to Addr, amount *big.Int) ([]byte, error) {
	if amount.Sign() < 0 || amount.BitLen() > 256 {
		return nil, errors.New("tron: amount does not fit uint256")
	}
	d := make([]byte, transferCallLen)
	copy(d, transferSelector[:])
	copy(d[4+12:36], to[1:])
	amount.FillBytes(d[36:68])
	return d, nil
}

// parseTransferCalldata разбирает calldata. Требует чистого заполнения нулями
// старших байт адреса: «грязный» адрес EVM молча обрезал бы, и получатель
// в Verify и в сети разошёлся бы.
func parseTransferCalldata(d []byte) (to Addr, amount *big.Int, err error) {
	if len(d) != transferCallLen || !bytes.Equal(d[:4], transferSelector[:]) {
		return to, nil, ErrDraftCalldata
	}
	if !bytes.Equal(d[4:16], make([]byte, 12)) {
		return to, nil, fmt.Errorf("%w: dirty address padding", ErrDraftCalldata)
	}
	to[0] = addrPrefix
	copy(to[1:], d[16:36])
	return to, new(big.Int).SetBytes(d[36:68]), nil
}

// Marshal сериализует raw_data в каноническом порядке полей. Нулевые значения
// пропускаются, как в proto3.
func (r RawTx) Marshal() []byte {
	if r.Expiration < 0 || r.Timestamp < 0 || r.FeeLimit < 0 {
		return nil
	}
	var trig []byte
	trig = pbAppendBytes(trig, 1, r.Owner[:])
	trig = pbAppendBytes(trig, 2, r.Contract[:])
	trig = pbAppendBytes(trig, 4, r.Data)

	var anyMsg []byte
	anyMsg = pbAppendBytes(anyMsg, 1, []byte(anyTypeURLTrigger))
	anyMsg = pbAppendBytes(anyMsg, 2, trig)

	var ct []byte
	ct = pbAppendUint(ct, 1, contractTypeTrigger)
	ct = pbAppendBytes(ct, 2, anyMsg)

	var raw []byte
	raw = pbAppendBytes(raw, 1, r.RefBlockBytes[:])
	raw = pbAppendBytes(raw, 4, r.RefBlockHash[:])
	raw = pbAppendUint(raw, 8, uint64(r.Expiration) //nolint:gosec // Non-negative values are checked at the start of Marshal.)
	raw = pbAppendBytes(raw, 11, ct)
	raw = pbAppendUint(raw, 14, uint64(r.Timestamp) //nolint:gosec // Non-negative values are checked at the start of Marshal.)
	if r.FeeLimit != 0 {
		raw = pbAppendUint(raw, 18, uint64(r.FeeLimit) //nolint:gosec // Non-negative values are checked at the start of Marshal.)
	}
	return raw
}

// TxID возвращает идентификатор транзакции: SHA-256 от raw_data.
func TxIDOf(raw []byte) [32]byte { return sha256.Sum256(raw) }

// ParseRawTx строго разбирает raw_data. Принимает только то, что собирает
// Marshal: ровно один контракт TriggerSmartContract без call_value, токенов,
// памятки и прочих полей. После разбора сериализует заново и требует
// побайтового совпадения, поэтому в Draft нечему прятаться между полями.
func ParseRawTx(raw []byte) (RawTx, error) {
	var r RawTx
	bad := func(err error) (RawTx, error) { return RawTx{}, fmt.Errorf("%w: %w", ErrDraftMalformed, err) }

	fields, err := pbParse(raw)
	if err != nil {
		return bad(err)
	}
	var contract []byte
	seen := map[uint64]bool{}
	for _, f := range fields {
		if seen[f.num] {
			return bad(fmt.Errorf("%w: duplicate field %d", errUnknownField, f.num))
		}
		seen[f.num] = true
		switch {
		case f.num == 1 && f.wt == wtBytes && len(f.b) == 2:
			r.RefBlockBytes = [2]byte(f.b)
		case f.num == 4 && f.wt == wtBytes && len(f.b) == 8:
			r.RefBlockHash = [8]byte(f.b)
		case f.num == 8 && f.wt == wtVarint:
			if f.v > uint64(^uint64(0)>>1) { return bad(errors.New("tron: expiration overflows int64")) }
			r.Expiration = int64(f.v) //nolint:gosec // Range checked immediately above.
		case f.num == 11 && f.wt == wtBytes:
			contract = f.b
		case f.num == 14 && f.wt == wtVarint:
			if f.v > uint64(^uint64(0)>>1) { return bad(errors.New("tron: timestamp overflows int64")) }
			r.Timestamp = int64(f.v) //nolint:gosec // Range checked immediately above.
		case f.num == 18 && f.wt == wtVarint:
			if f.v > uint64(^uint64(0)>>1) { return bad(errors.New("tron: fee limit overflows int64")) }
			r.FeeLimit = int64(f.v) //nolint:gosec // Range checked immediately above.
		default:
			// Сюда попадают memo (10), scripts (12), auths (9), второй
			// контракт и любые чужие поля.
			return bad(fmt.Errorf("%w: field %d", errUnknownField, f.num))
		}
	}
	if contract == nil {
		return bad(ErrDraftNotTrigger)
	}
	if r.Expiration <= 0 || r.Timestamp <= 0 || r.FeeLimit < 0 {
		return bad(errors.New("tron: draft has non-positive time or negative fee limit"))
	}

	ctFields, err := pbParse(contract)
	if err != nil {
		return bad(err)
	}
	var param []byte
	var ctype uint64
	var haveType bool
	for _, f := range ctFields {
		switch {
		case f.num == 1 && f.wt == wtVarint && !haveType:
			ctype, haveType = f.v, true
		case f.num == 2 && f.wt == wtBytes && param == nil:
			param = f.b
		default:
			return bad(fmt.Errorf("%w: contract field %d", errUnknownField, f.num))
		}
	}
	if ctype != contractTypeTrigger || param == nil {
		return bad(ErrDraftNotTrigger)
	}

	anyFields, err := pbParse(param)
	if err != nil {
		return bad(err)
	}
	var url, value []byte
	for _, f := range anyFields {
		switch {
		case f.num == 1 && f.wt == wtBytes && url == nil:
			url = f.b
		case f.num == 2 && f.wt == wtBytes && value == nil:
			value = f.b
		default:
			return bad(fmt.Errorf("%w: any field %d", errUnknownField, f.num))
		}
	}
	if string(url) != anyTypeURLTrigger || value == nil {
		return bad(ErrDraftNotTrigger)
	}

	trFields, err := pbParse(value)
	if err != nil {
		return bad(err)
	}
	var haveOwner, haveContract bool
	for _, f := range trFields {
		switch {
		case f.num == 1 && f.wt == wtBytes && !haveOwner && len(f.b) == addrLen && f.b[0] == addrPrefix:
			r.Owner, haveOwner = Addr(f.b), true
		case f.num == 2 && f.wt == wtBytes && !haveContract && len(f.b) == addrLen && f.b[0] == addrPrefix:
			r.Contract, haveContract = Addr(f.b), true
		case f.num == 4 && f.wt == wtBytes && r.Data == nil:
			r.Data = append([]byte(nil), f.b...)
		default:
			// call_value (3), call_token_value (5), token_id (6) и чужое.
			return bad(fmt.Errorf("%w: trigger field %d", errUnknownField, f.num))
		}
	}
	if !haveOwner || !haveContract || r.Data == nil {
		return bad(ErrDraftNotTrigger)
	}

	if !bytes.Equal(r.Marshal(), raw) {
		return bad(errPBNotCanon)
	}
	return r, nil
}
