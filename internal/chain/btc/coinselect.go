package btc

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/24DSO-lab/wallet/internal/domain"
)

const (
	P2WPKHInputVBytes  = uint64(68)
	P2WPKHOutputVBytes = uint64(31)
	TxOverheadVBytes   = uint64(10)
	DustThreshold      = uint64(294)
	selectionNodeLimit = 100000
)

var (
	ErrNoSpendableUTXO = errors.New("btc: no spendable UTXO")
	ErrInvalidFeeRate  = errors.New("btc: invalid fee rate")
	ErrSelectionRange  = errors.New("btc: amount exceeds supported selection range")
)

// Selection is a deterministic P2WPKH coin selection result. Fee and Change
// are expressed in satoshis. When HasChange is false, the remainder below
// the dust threshold is deliberately added to the miner fee.
type Selection struct {
	UTXOs    []domain.UTXO
	Fee      domain.Amount
	Change   domain.Amount
	HasChange bool
	DustAddedToFee bool
	VBytes   uint64
}

type selectionCandidate struct {
	utxo  domain.UTXO
	value uint64
}

// SelectCoins chooses a deterministic subset minimizing waste. It evaluates
// both one-output (no change) and two-output (change) transaction fees. Search
// is bounded; a deterministic descending-value fallback is evaluated too.
func SelectCoins(utxos []domain.UTXO, amount domain.Amount, feeRateSatPerVByte float64) (Selection, error) {
	if feeRateSatPerVByte <= 0 || math.IsNaN(feeRateSatPerVByte) || math.IsInf(feeRateSatPerVByte, 0) {
		return Selection{}, ErrInvalidFeeRate
	}
	target, ok := amount.Uint64()
	if !ok || target == 0 {
		return Selection{}, ErrSelectionRange
	}
	candidates := make([]selectionCandidate, 0, len(utxos))
	for _, u := range utxos {
		if u.Spent || u.Amount.IsZero() {
			continue
		}
		value, ok := u.Amount.Uint64()
		if !ok {
			return Selection{}, ErrSelectionRange
		}
		candidates = append(candidates, selectionCandidate{utxo: u, value: value})
	}
	if len(candidates) == 0 {
		return Selection{}, ErrNoSpendableUTXO
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].value != candidates[j].value {
			return candidates[i].value > candidates[j].value
		}
		if candidates[i].utxo.TxID != candidates[j].utxo.TxID {
			return candidates[i].utxo.TxID < candidates[j].utxo.TxID
		}
		return candidates[i].utxo.Vout < candidates[j].utxo.Vout
	})

	var best Selection
	bestWaste := uint64(math.MaxUint64)
	var nodes int
	searchCandidates := candidates
	if len(searchCandidates) > 24 {
		searchCandidates = searchCandidates[:24]
	}
	chosen := make([]int, 0, len(searchCandidates))
	var visit func(start int, total uint64)
	visit = func(start int, total uint64) {
		if nodes >= selectionNodeLimit {
			return
		}
		nodes++
		if len(chosen) > 0 {
			if result, waste, ok := evaluateSelection(searchCandidates, chosen, total, target, feeRateSatPerVByte); ok && waste < bestWaste {
				best, bestWaste = result, waste
			}
		}
		for i := start; i < len(searchCandidates) && nodes < selectionNodeLimit; i++ {
			if math.MaxUint64-total < searchCandidates[i].value {
				continue
			}
			chosen = append(chosen, i)
			visit(i+1, total+searchCandidates[i].value)
			chosen = chosen[:len(chosen)-1]
		}
	}
	visit(0, 0)

	// Ensure bounded search still returns a valid deterministic result for
	// large wallets. The fallback considers descending-value prefixes.
	prefix := make([]int, 0, len(candidates))
	var total uint64
	for i, c := range candidates {
		if math.MaxUint64-total < c.value {
			break
		}
		total += c.value
		prefix = append(prefix, i)
		if result, waste, ok := evaluateSelection(candidates, prefix, total, target, feeRateSatPerVByte); ok && waste < bestWaste {
			best, bestWaste = result, waste
		}
	}
	if bestWaste == math.MaxUint64 {
		return Selection{}, domain.NewError(domain.CodeInsufficientFunds, fmt.Errorf("%w: insufficient funds after fee", ErrNoSpendableUTXO))
	}
	return best, nil
}

func evaluateSelection(candidates []selectionCandidate, indexes []int, total, target uint64, rate float64) (Selection, uint64, bool) {
	inputCount := uint64(len(indexes))
	feeWithChange, ok := estimateFee(inputCount, 2, rate)
	if !ok || total < target || total-target < feeWithChange {
		feeWithoutChange, ok := estimateFee(inputCount, 1, rate)
		if !ok || total < target || total-target < feeWithoutChange {
			return Selection{}, 0, false
		}
		remainder := total - target
		fee := domain.AmountFromUint64(remainder)
		selected := selectedUTXOs(candidates, indexes)
		vbytes := estimateVBytes(inputCount, 1)
		return Selection{UTXOs: selected, Fee: fee, HasChange: false, DustAddedToFee: remainder-feeWithoutChange < DustThreshold, VBytes: vbytes}, remainder-feeWithoutChange, true
	}

	change := total - target - feeWithChange
	if change < DustThreshold {
		remainder := total-target
		fee := domain.AmountFromUint64(remainder)
		vbytes := estimateVBytes(inputCount, 1)
		feeWithoutChange, ok := estimateFee(inputCount, 1, rate)
		if !ok { return Selection{}, 0, false }
		return Selection{UTXOs: selectedUTXOs(candidates, indexes), Fee: fee, HasChange: false, DustAddedToFee: true, VBytes: vbytes}, remainder-feeWithoutChange, true
	}
	changeAmount := domain.AmountFromUint64(change)
	feeAmount := domain.AmountFromUint64(feeWithChange)
	return Selection{
		UTXOs: selectedUTXOs(candidates, indexes), Fee: feeAmount,
		Change: changeAmount, HasChange: true, VBytes: estimateVBytes(inputCount, 2),
	}, change, true
}

func selectedUTXOs(candidates []selectionCandidate, indexes []int) []domain.UTXO {
	out := make([]domain.UTXO, 0, len(indexes))
	for _, i := range indexes {
		out = append(out, candidates[i].utxo)
	}
	return out
}

func estimateVBytes(inputs, outputs uint64) uint64 {
	return TxOverheadVBytes + inputs*P2WPKHInputVBytes + outputs*P2WPKHOutputVBytes
}

func estimateFee(inputs, outputs uint64, rate float64) (uint64, bool) {
	vbytes := estimateVBytes(inputs, outputs)
	fee := math.Ceil(float64(vbytes) * rate)
	if fee < 1 || fee >= float64(math.MaxUint64) {
		return 0, false
	}
	return uint64(fee), true
}
