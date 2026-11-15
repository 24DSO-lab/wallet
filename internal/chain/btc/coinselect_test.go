package btc

import (
	"errors"
	"math"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
)

func btcUTXO(txid string, value uint64, vout uint32) domain.UTXO {
	return domain.UTXO{TxID: domain.TxID(txid), Vout: vout, Amount: domain.AmountFromUint64(value)}
}

func TestSelectCoinsDeterministicAndExact(t *testing.T) {
	utxos := []domain.UTXO{
		btcUTXO("bbbb", 5000, 0),
		btcUTXO("aaaa", 4000, 0),
		btcUTXO("cccc", 2000, 1),
	}
	amount := domain.AmountFromUint64(6000)
	first, err := SelectCoins(utxos, amount, 1)
	if err != nil { t.Fatal(err) }
	second, err := SelectCoins([]domain.UTXO{utxos[2], utxos[0], utxos[1]}, amount, 1)
	if err != nil { t.Fatal(err) }
	if len(first.UTXOs) != len(second.UTXOs) { t.Fatalf("different selection lengths") }
	for i := range first.UTXOs {
		if first.UTXOs[i].TxID != second.UTXOs[i].TxID || first.UTXOs[i].Vout != second.UTXOs[i].Vout {
			t.Fatalf("selection differs: %+v vs %+v", first.UTXOs, second.UTXOs)
		}
	}
	if !first.HasChange { t.Fatalf("expected change: %+v", first) }
	if first.Fee.String() != "208" || first.VBytes != 208 {
		t.Fatalf("unexpected fee/size: fee=%s vbytes=%d", first.Fee.String(), first.VBytes)
	}
}

func TestSelectCoinsDustIsAddedToFee(t *testing.T) {
	// 10 + 68 + 2*31 = 140 vB; at 1 sat/vB, change below 294 is folded into fee.
	got, err := SelectCoins([]domain.UTXO{btcUTXO("a", 10000, 0)}, domain.AmountFromUint64(9800), 1)
	if err != nil { t.Fatal(err) }
	if got.HasChange || !got.DustAddedToFee { t.Fatalf("dust change must be omitted and reported: %+v", got) }
	if got.Fee.String() != "200" { t.Fatalf("fee = %s, want 200", got.Fee.String()) }
	if got.VBytes != 109 { t.Fatalf("no-change vbytes = %d, want 109", got.VBytes) }
}

func TestSelectCoinsIgnoresSpentAndZero(t *testing.T) {
	u := btcUTXO("spent", 10000, 0)
	u.Spent = true
	_, err := SelectCoins([]domain.UTXO{u, btcUTXO("zero", 0, 0)}, domain.AmountFromUint64(1000), 1)
	if !errors.Is(err, ErrNoSpendableUTXO) { t.Fatalf("got %v", err) }
}

func TestSelectCoinsRejectsBadRateAndInsufficientFunds(t *testing.T) {
	for _, rate := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if _, err := SelectCoins([]domain.UTXO{btcUTXO("a", 10000, 0)}, domain.AmountFromUint64(1000), rate); !errors.Is(err, ErrInvalidFeeRate) {
			t.Fatalf("rate %v: %v", rate, err)
		}
	}
	if _, err := SelectCoins([]domain.UTXO{btcUTXO("a", 100, 0)}, domain.AmountFromUint64(10000), 1); domain.CodeOf(err) != domain.CodeInsufficientFunds {
		t.Fatalf("expected insufficient funds, got %v", err)
	}
}
