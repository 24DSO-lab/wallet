package eth

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

var _ ports.ChainAdapter = (*Adapter)(nil)

// Finalize собирает подписанную транзакцию типа 2 из подписи Keystore.
//
// Получателя и сумму здесь не сверить (намерения нет): это сделал Verify до
// подписи, и сервис обязан вызывать их в порядке Prepare, Verify, Sign,
// Finalize. Зато Finalize сам проверяет всё, что видно из подписи: дайджест
// совпадает с байтами черновика, подпись восстанавливается в ключ, ключ
// заявлен Keystore, а его адрес равен адресу в запросе подписи. Неверная
// чётность v дала бы транзакцию от чужого адреса, поэтому проверяется и она.
func (a *Adapter) Finalize(p ports.Prepared, sigs []ports.Signature) (ports.Signed, error) {
	tx, err := parseUnsigned(p.Draft)
	if err != nil {
		return ports.Signed{}, verifyFail("%v", err)
	}
	if p.Network != domain.NetworkEthereum || tx.ChainID != a.params.chainID {
		return ports.Signed{}, verifyFail("draft is not for chain %d", a.params.chainID)
	}
	if len(p.Digests) != 1 || len(sigs) != 1 {
		return ports.Signed{}, verifyFail("expected one digest and one signature, got %d and %d", len(p.Digests), len(sigs))
	}
	d := p.Digests[0]
	if d.Digest != keccak256(p.Draft) {
		return ports.Signed{}, verifyFail("digest does not match the draft")
	}
	want, _, err := parseAddress(d.ExpectedAddress)
	if err != nil {
		return ports.Signed{}, verifyFail("signing request has an invalid address")
	}
	sig := sigs[0]
	signer, err := checkSignature(d.Digest, sig.Sig, sig.PubKey)
	if err != nil {
		return ports.Signed{}, domain.NewError(domain.CodeVerificationFailed, err)
	}
	if signer != want {
		return ports.Signed{}, verifyFail("signature is from %s, not from the wallet address %s", signer, want)
	}

	r := new(big.Int).SetBytes(sig.Sig[:32])
	s := new(big.Int).SetBytes(sig.Sig[32:64])
	raw := tx.signed(sig.Sig[64], r, s)
	return ports.Signed{Network: domain.NetworkEthereum, TxID: txID(raw), Raw: raw}, nil
}

// txID это Keccak-256 от подписанной транзакции целиком (с байтом типа).
func txID(raw []byte) domain.TxID {
	h := keccak256(raw)
	return domain.TxID("0x" + hex.EncodeToString(h[:]))
}

// Broadcast отправляет транзакцию. Идентификатор считается локально и от
// провайдера не берётся: ответ с другим хэшем не доверяется. Повторов нет, а
// «уже известна» считается успехом, потому что та же подписанная транзакция
// от нас же. После обрыва связи неизвестно, дошла ли транзакция; сервис
// выясняет это через Status по TxID, а не повторной подписью.
func (a *Adapter) Broadcast(ctx context.Context, s ports.Signed) (domain.TxID, error) {
	if s.Network != domain.NetworkEthereum {
		return "", domain.NewError(domain.CodeWrongNetwork, errors.New("eth: signed transaction is for another network"))
	}
	if len(s.Raw) == 0 || s.Raw[0] != txTypeDynamicFee || txID(s.Raw) != s.TxID {
		return "", domain.NewError(domain.CodeInvalidInput, errors.New("eth: signed transaction is malformed or its id does not match"))
	}
	if err := a.ensureChain(ctx); err != nil {
		return "", err
	}
	if _, err := a.rpc.SendRawTransaction(ctx, s.Raw); err != nil && !errors.Is(err, ErrAlreadyKnown) {
		return "", err
	}
	return s.TxID, nil
}

// Status возвращает состояние транзакции: в ожидании (узел знает, в блок не
// попала), подтверждено, ошибка (откат в блоке). Неизвестная узлу транзакция
// это CodeNotFound, а не «в ожидании»: сразу после отправки балансировщик
// провайдера может её ещё не показывать, и решает сервис, а не адаптер.
func (a *Adapter) Status(ctx context.Context, id domain.TxID) (ports.TxStatus, error) {
	hash, err := parseTxHash(string(id))
	if err != nil {
		return ports.TxStatus{}, domain.NewError(domain.CodeInvalidInput, fmt.Errorf("eth: invalid transaction id"))
	}
	if err := a.ensureChain(ctx); err != nil {
		return ports.TxStatus{}, err
	}
	rec, err := a.rpc.TransactionReceipt(ctx, hash)
	if err != nil {
		return ports.TxStatus{}, err
	}
	if rec == nil {
		known, err := a.rpc.TransactionKnown(ctx, hash)
		if err != nil {
			return ports.TxStatus{}, err
		}
		if !known {
			return ports.TxStatus{}, domain.NewError(domain.CodeNotFound, errors.New("eth: the node does not know this transaction"))
		}
		return ports.TxStatus{State: domain.TxPending, Known: true}, nil
	}
	st := ports.TxStatus{State: domain.TxConfirmed, Known: true, BlockHeight: rec.BlockNumber, Confirmations: 1}
	if rec.Status == 0 {
		st.State = domain.TxFailed
	}
	// Подтверждения считаются от головы цепи; отстающий узел даёт минимум 1:
	// транзакция в блоке уже есть.
	if head, err := a.rpc.BlockNumber(ctx); err == nil && head >= rec.BlockNumber {
		if n := head - rec.BlockNumber + 1; n < 1<<32 {
			st.Confirmations = uint32(n)
		} else {
			st.Confirmations = 1<<32 - 1
		}
	}
	return st, nil
}
