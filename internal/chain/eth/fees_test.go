package eth

import "testing"

func TestPaddedGasRejectsOverflowingProviderEstimates(t *testing.T) {
	// This value makes the previous multiply-then-add implementation wrap to
	// 21,000, which passed the ETH gas cap despite an absurd provider estimate.
	const crafted uint64 = 16909515400900439815
	if got := paddedGas(crafted, false); got <= gasCapETH {
		t.Fatalf("overflowing ETH estimate produced gas limit %d within cap %d", got, gasCapETH)
	}
	if got := paddedGas(^uint64(0), true); got <= gasCapUSDC {
		t.Fatalf("overflowing USDC estimate produced gas limit %d within cap %d", got, gasCapUSDC)
	}
}

func TestPaddedGasPreservesValidBoundaries(t *testing.T) {
	if got := paddedGas(gasTransferETH, false); got != gasTransferETH {
		t.Fatalf("plain ETH transfer gas = %d, want %d", got, gasTransferETH)
	}
	if got := paddedGas(83_333, false); got != 99_999 {
		t.Fatalf("padded ETH gas = %d, want 99999", got)
	}
	if got := paddedGas(125_000, true); got != gasCapUSDC {
		t.Fatalf("padded USDC gas = %d, want %d", got, gasCapUSDC)
	}
}
