package btc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

var _ ports.RBFAdapter = (*SpendEngine)(nil)

// PrepareReplacement builds a BIP125 replacement using the same inputs,
// recipient and change destination. Only the change output is reduced.
func (e *SpendEngine) PrepareReplacement(ctx context.Context, wallet domain.PublicWallet, original ports.PendingTx) (ports.Replacement, error) {
	if original.WalletID != wallet.ID || original.Asset != domain.BTC || original.Network != domain.NetworkBitcoin ||
		!original.BroadcastAccepted || len(original.Raw) == 0 || len(original.Draft) == 0 {
		return ports.Replacement{}, domain.NewError(domain.CodeInvalidInput, errors.New("btc: transaction is not eligible for RBF"))
	}
	status, err := e.Status(ctx, original.TxID)
	if err != nil {
		return ports.Replacement{}, err
	}
	if status.State != domain.TxPending || !status.Known {
		return ports.Replacement{}, domain.NewError(domain.CodeInvalidInput, errors.New("btc: only an unconfirmed transaction can be replaced"))
	}
	hasDescendants, err := e.api.HasSpentOutputs(ctx, string(original.TxID))
	if err != nil { return ports.Replacement{}, err }
	if hasDescendants {
		return ports.Replacement{}, domain.NewError(domain.CodeInvalidInput, errors.New("btc: transaction has spent outputs; descendant package fees cannot be safely calculated"))
	}

	oldDraft, err := decodeSpendDraft(original.Draft)
	if err != nil {
		return ports.Replacement{}, domain.NewError(domain.CodeVerificationFailed, err)
	}
	if oldDraft.Profile != e.profile || oldDraft.WalletID != wallet.ID || oldDraft.Xpub != wallet.BTCXpub ||
		len(oldDraft.Outputs) != 2 || oldDraft.To == "" {
		return ports.Replacement{}, domain.NewError(domain.CodeInvalidInput, errors.New("btc: original transaction has no replaceable change output"))
	}
	if err := validateSpendDraft(oldDraft, e.derive); err != nil {
		return ports.Replacement{}, domain.NewError(domain.CodeVerificationFailed, err)
	}
	stripped, err := stripWitness(original.Raw)
	if err != nil {
		return ports.Replacement{}, domain.NewError(domain.CodeVerificationFailed, err)
	}
	oldUnsigned, err := hex.DecodeString(oldDraft.UnsignedTx)
	if err != nil || !bytes.Equal(stripped, oldUnsigned) || txIDFromRaw(original.Raw) != strings.ToLower(string(original.TxID)) {
		return ports.Replacement{}, domain.NewError(domain.CodeVerificationFailed, errors.New("btc: stored signed transaction does not match its original draft"))
	}

	vsize, err := replacementVSize(original.Raw, stripped, len(oldDraft.Inputs))
	if err != nil || e.incrementalRelayFeeRate == 0 || oldDraft.Fee > ^uint64(0)-vsize {
		return ports.Replacement{}, domain.NewError(domain.CodeInvalidInput, errors.New("btc: invalid original transaction size or fee"))
	}
	estimates, err := e.api.GetFeeEstimates(ctx)
	if err != nil {
		return ports.Replacement{}, err
	}
	rate, err := ConservativeFeeRate(estimates)
	if err != nil {
		return ports.Replacement{}, err
	}
	target := math.Ceil(rate * float64(vsize))
	if target >= float64(^uint64(0)) {
		return ports.Replacement{}, domain.NewError(domain.CodeInvalidAmount, errors.New("btc: replacement fee exceeds supported range"))
	}
	newFee := uint64(target)
	// BIP125 requires original absolute fee plus incremental relay fee
	// multiplied by the replacement's conservative virtual size.
	if vsize > (^uint64(0)-oldDraft.Fee)/e.incrementalRelayFeeRate {
		return ports.Replacement{}, domain.NewError(domain.CodeInvalidAmount, errors.New("btc: minimum replacement fee overflows"))
	}
	minimumFee := oldDraft.Fee + vsize*e.incrementalRelayFeeRate
	if newFee < minimumFee {
		newFee = minimumFee
	}
	if newFee <= oldDraft.Fee {
		return ports.Replacement{}, domain.NewError(domain.CodeInvalidAmount, errors.New("btc: replacement fee must be higher than the original fee"))
	}
	delta := newFee - oldDraft.Fee
	oldChange := oldDraft.Outputs[1].Value
	if oldChange <= delta || oldChange-delta < DustThreshold {
		return ports.Replacement{}, domain.NewError(domain.CodeInsufficientFunds, errors.New("btc: change output is too small to pay the replacement fee"))
	}

	replacementDraft := oldDraft
	replacementDraft.Fee = newFee
	replacementDraft.Outputs = append([]spendOutput(nil), oldDraft.Outputs...)
	replacementDraft.Outputs[1].Value = oldChange - delta
	unsigned, err := serializeUnsigned(replacementDraft)
	if err != nil {
		return ports.Replacement{}, err
	}
	replacementDraft.UnsignedTx = hex.EncodeToString(unsigned)
	encoded, err := json.Marshal(replacementDraft)
	if err != nil {
		return ports.Replacement{}, err
	}
	digests, err := digestRequests(replacementDraft, unsigned, e.derive)
	if err != nil {
		return ports.Replacement{}, err
	}
	amount := domain.AmountFromUint64(replacementDraft.Amount)
	fee := domain.AmountFromUint64(newFee)
	total, err := amount.Add(fee)
	if err != nil {
		return ports.Replacement{}, err
	}
	intent := domain.SendIntent{Wallet: wallet, Asset: domain.BTC, To: replacementDraft.To, Amount: amount}
	prepared := ports.Prepared{
		Network: domain.NetworkBitcoin,
		Asset:   domain.BTC,
		Draft:   encoded,
		Digests: digests,
		Fee:     fee,
		FeeAsset: domain.BTC,
		Summary: ports.Summary{
			Network: domain.NetworkBitcoin, Asset: domain.BTC, To: replacementDraft.To,
			Amount: amount, Fee: fee, FeeAsset: domain.BTC, Total: total,
		},
	}
	if delta > oldDraft.Fee {
		prepared.Warnings = append(prepared.Warnings, domain.Warning{Code: domain.WarnHighFee, Detail: "RBF replacement increases the miner fee"})
	}
	return ports.Replacement{ReplacesTxID: original.TxID, Intent: intent, Prepared: prepared}, nil
}

// VerifyReplacement independently revalidates the original pending transaction
// and the replacement envelope immediately before signing. It deliberately
// does not require the original inputs to appear unspent: they are held by the
// original mempool transaction and are reused under BIP125.
func (e *SpendEngine) VerifyReplacement(ctx context.Context, wallet domain.PublicWallet, original ports.PendingTx, replacement ports.Replacement) error {
	fail := func(err error) error {
		return domain.NewError(domain.CodeVerificationFailed, fmt.Errorf("%w: %w", ErrInvalidDraft, err))
	}
	if replacement.ReplacesTxID != original.TxID || original.WalletID != wallet.ID ||
		original.Asset != domain.BTC || original.Network != domain.NetworkBitcoin ||
		!original.BroadcastAccepted || len(original.Raw) == 0 || len(original.Draft) == 0 {
		return fail(errors.New("original transaction is not eligible for replacement"))
	}
	status, err := e.Status(ctx, original.TxID)
	if err != nil {
		return err
	}
	if status.State != domain.TxPending || !status.Known {
		return fail(errors.New("original transaction is no longer pending"))
	}
	hasDescendants, err := e.api.HasSpentOutputs(ctx, string(original.TxID))
	if err != nil { return err }
	if hasDescendants { return fail(errors.New("original transaction has spent outputs and may have descendants")) }
	oldDraft, err := decodeSpendDraft(original.Draft)
	if err != nil {
		return fail(err)
	}
	newDraft, err := decodeSpendDraft(replacement.Prepared.Draft)
	if err != nil {
		return fail(err)
	}
	if oldDraft.WalletID != wallet.ID || oldDraft.Xpub != wallet.BTCXpub ||
		replacement.Intent.Wallet.ID != wallet.ID || replacement.Intent.Wallet.BTCXpub != wallet.BTCXpub ||
		replacement.Intent.Asset != domain.BTC || replacement.Intent.To != oldDraft.To ||
		!replacement.Intent.Amount.Equal(domain.AmountFromUint64(oldDraft.Amount)) {
		return fail(errors.New("replacement intent differs from original send"))
	}
	if err := validateSpendDraft(oldDraft, e.derive); err != nil {
		return fail(err)
	}
	if err := validateSpendDraft(newDraft, e.derive); err != nil {
		return fail(err)
	}
	stripped, err := stripWitness(original.Raw)
	if err != nil {
		return fail(err)
	}
	oldUnsigned, err := hex.DecodeString(oldDraft.UnsignedTx)
	if err != nil || !bytes.Equal(stripped, oldUnsigned) || txIDFromRaw(original.Raw) != strings.ToLower(string(original.TxID)) {
		return fail(errors.New("original raw transaction does not match its stored draft"))
	}
	if len(oldDraft.Outputs) != 2 || len(newDraft.Outputs) != 2 ||
		oldDraft.Profile != newDraft.Profile || oldDraft.WalletID != newDraft.WalletID ||
		oldDraft.Xpub != newDraft.Xpub || oldDraft.To != newDraft.To || oldDraft.Amount != newDraft.Amount ||
		len(oldDraft.Inputs) != len(newDraft.Inputs) || oldDraft.Outputs[0] != newDraft.Outputs[0] ||
		oldDraft.Outputs[1].Address != newDraft.Outputs[1].Address ||
		oldDraft.Outputs[1].Path != newDraft.Outputs[1].Path ||
		newDraft.Outputs[1].Value >= oldDraft.Outputs[1].Value || newDraft.Fee <= oldDraft.Fee {
		return fail(errors.New("replacement changed the original inputs or recipient"))
	}
	for i := range oldDraft.Inputs {
		if oldDraft.Inputs[i] != newDraft.Inputs[i] {
			return fail(errors.New("replacement changed an original input"))
		}
	}
	vsize, sizeErr := replacementVSize(original.Raw, stripped, len(newDraft.Inputs))
	if sizeErr != nil || e.incrementalRelayFeeRate == 0 || vsize > (^uint64(0)-oldDraft.Fee)/e.incrementalRelayFeeRate || newDraft.Fee < oldDraft.Fee+vsize*e.incrementalRelayFeeRate {
		return fail(errors.New("replacement does not meet the incremental relay fee requirement"))
	}
	if oldDraft.Outputs[1].Value-newDraft.Outputs[1].Value != newDraft.Fee-oldDraft.Fee {
		return fail(errors.New("replacement fee delta does not match change reduction"))
	}
	if replacement.Prepared.Network != domain.NetworkBitcoin || replacement.Prepared.Asset != domain.BTC ||
		!replacement.Prepared.Fee.Equal(domain.AmountFromUint64(newDraft.Fee)) || replacement.Prepared.FeeAsset != domain.BTC {
		return fail(errors.New("prepared replacement metadata differs from draft"))
	}
	unsigned, err := hex.DecodeString(newDraft.UnsignedTx)
	if err != nil {
		return fail(err)
	}
	expectedUnsigned, err := serializeUnsigned(newDraft)
	if err != nil || !bytes.Equal(unsigned, expectedUnsigned) {
		return fail(errors.New("replacement unsigned bytes differ from metadata"))
	}
	digests, err := digestRequests(newDraft, unsigned, e.derive)
	if err != nil {
		return fail(err)
	}
	if len(digests) != len(replacement.Prepared.Digests) {
		return fail(errors.New("replacement digest count mismatch"))
	}
	for i := range digests {
		if digests[i] != replacement.Prepared.Digests[i] {
			return fail(fmt.Errorf("replacement digest %d differs from transaction", i))
		}
	}
	amount := domain.AmountFromUint64(newDraft.Amount)
	fee := domain.AmountFromUint64(newDraft.Fee)
	total, err := amount.Add(fee)
	if err != nil {
		return fail(err)
	}
	summary := replacement.Prepared.Summary
	if summary.Network != domain.NetworkBitcoin || summary.Asset != domain.BTC ||
		summary.To != newDraft.To || !summary.Amount.Equal(amount) ||
		!summary.Fee.Equal(fee) || summary.FeeAsset != domain.BTC || !summary.Total.Equal(total) {
		return fail(errors.New("replacement summary differs from draft"))
	}
	return nil
}

func decodeSpendDraft(encoded []byte) (spendDraft, error) {
	var draft spendDraft
	if len(encoded) == 0 {
		return draft, ErrInvalidDraft
	}
	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&draft); err != nil {
		return draft, err
	}
	// Decode a second value: More is only meaningful inside arrays/objects
	// and does not detect another top-level JSON value.
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return draft, errors.New("trailing draft data")
		}
		return draft, fmt.Errorf("trailing draft data: %w", err)
	}
	canonical, err := json.Marshal(draft)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return draft, errors.New("non-canonical draft encoding")
	}
	return draft, nil
}


func txIDFromRaw(raw []byte) string {
	stripped, err := stripWitness(raw)
	if err != nil {
		return ""
	}
	hash := sha256d(stripped)
	reversed := make([]byte, len(hash))
	for i := range hash {
		reversed[i] = hash[len(hash)-1-i]
	}
	return strings.ToLower(hex.EncodeToString(reversed))
}

func virtualSize(raw, stripped []byte) uint64 {
	if len(raw) == 0 || len(stripped) == 0 {
		return 0
	}
	weight := uint64(len(stripped))*3 + uint64(len(raw))
	return (weight + 3) / 4
}
