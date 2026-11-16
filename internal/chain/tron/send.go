package tron

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
	"github.com/24DSO-lab/wallet/internal/ports"
)

// ErrSignature означает, что подпись Keystore не подходит к черновику.
var ErrSignature = errors.New("tron: signature does not match the draft")

// Finalize собирает Transaction (raw_data и подпись). Дайджест и отправитель
// берутся из самого Draft, а не из Prepared: подпись проверяется открытым
// ключом из Keystore, и ключ должен давать адрес отправителя из Draft.
func (a *Adapter) Finalize(p ports.Prepared, sigs []ports.Signature) (ports.Signed, error) {
	fail := func(err error) (ports.Signed, error) {
		return ports.Signed{}, domain.NewError(domain.CodeSigningRefused, fmt.Errorf("%w: %w", ErrSignature, err))
	}
	if len(sigs) != 1 {
		return fail(errors.New("expected exactly one signature"))
	}
	raw, err := ParseRawTx(p.Draft)
	if err != nil {
		return ports.Signed{}, domain.NewError(domain.CodeVerificationFailed, err)
	}
	s := sigs[0]
	if s.Sig[64] > 1 {
		return fail(errors.New("recovery id must be 0 or 1"))
	}
	pt, err := secp256k1.ParsePoint(s.PubKey)
	if err != nil || len(s.PubKey) != 33 {
		return fail(errors.New("public key must be compressed 33 bytes"))
	}
	unc := pt.Uncompressed()
	addr, err := addrFromPublicKey(unc[:])
	if err != nil {
		return fail(err)
	}
	if addr != raw.Owner {
		return fail(errors.New("signer is not the sender"))
	}
	digest := TxIDOf(p.Draft)
	if !secp256k1.IsLowS(s.Sig[32:64]) || !secp256k1.Verify(pt, digest[:], s.Sig[:32], s.Sig[32:64]) {
		return fail(errors.New("signature is invalid"))
	}
	tx := pbAppendBytes(nil, 1, p.Draft)
	tx = pbAppendBytes(tx, 2, s.Sig[:])
	return ports.Signed{Network: domain.NetworkTron, TxID: domain.TxID(hex.EncodeToString(digest[:])), Raw: tx}, nil
}

// splitSigned достаёт raw_data и подпись из Transaction.
func splitSigned(tx []byte) (raw, sig []byte, err error) {
	fields, err := pbParse(tx)
	if err != nil {
		return nil, nil, err
	}
	if len(fields) != 2 || fields[0].num != 1 || fields[1].num != 2 ||
		fields[0].wt != wtBytes || fields[1].wt != wtBytes || len(fields[1].b) != 65 {
		return nil, nil, errUnknownField
	}
	return fields[0].b, fields[1].b, nil
}

// Broadcast отправляет подписанную транзакцию. Идентификатор считается
// локально и сверяется с ответом узла.
func (a *Adapter) Broadcast(ctx context.Context, s ports.Signed) (domain.TxID, error) {
	if s.Network != domain.NetworkTron {
		return "", domain.NewError(domain.CodeWrongNetwork, errors.New("tron: signed transaction is for another network"))
	}
	raw, _, err := splitSigned(s.Raw)
	if err != nil {
		return "", domain.NewError(domain.CodeInvalidInput, fmt.Errorf("tron: malformed signed transaction: %w", err))
	}
	if _, err := ParseRawTx(raw); err != nil {
		return "", domain.NewError(domain.CodeVerificationFailed, err)
	}
	id := TxIDOf(raw)
	want := hex.EncodeToString(id[:])
	if string(s.TxID) != want {
		return "", domain.NewError(domain.CodeVerificationFailed, errors.New("tron: txid does not match raw_data"))
	}
	res, err := a.api.Broadcast(ctx, s.Raw)
	if err != nil {
		return "", err
	}
	if !res.OK && !res.Duplicate {
		return "", domain.NewError(domain.CodeBroadcastRejected, fmt.Errorf("tron: node rejected the transaction: %s", res.Code))
	}
	if res.OK && res.TxID != "" && res.TxID != want {
		return "", bad("node returned a different txid")
	}
	return domain.TxID(want), nil
}

// Status возвращает состояние транзакции. Found=false означает, что TronGrid
// пока не подтвердил наличие транзакции: это pending, но Known=false и не
// доказательство принятия в сеть. Найденная транзакция имеет Known=true;
// до SolidityDepth она pending, REVERT/нехватка energy это failed.
func (a *Adapter) Status(ctx context.Context, id domain.TxID) (ports.TxStatus, error) {
	s := strings.ToLower(string(id))
	if b, err := hex.DecodeString(s); err != nil || len(b) != 32 {
		return ports.TxStatus{}, domain.NewError(domain.CodeInvalidInput, errors.New("tron: invalid transaction id"))
	}
	info, err := a.api.TxInfo(ctx, s)
	if err != nil {
		return ports.TxStatus{}, err
	}
	if !info.Found {
		return ports.TxStatus{State: domain.TxPending}, nil
	}
	return statusOf(info), nil
}

func statusOf(info TxInfo) ports.TxStatus {
	var conf uint32
	if info.CurrentTip >= info.BlockNumber {
		conf = uint32(min(info.CurrentTip-info.BlockNumber+1, 1<<31))
	}
	st := ports.TxStatus{Known: true, BlockHeight: info.BlockNumber, Confirmations: conf}
	switch {
	case !info.Success:
		st.State = domain.TxFailed
	case conf >= SolidityDepth:
		st.State = domain.TxConfirmed
	default:
		st.State = domain.TxPending
	}
	return st
}
