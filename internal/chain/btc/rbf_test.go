package btc

import "testing"

func TestReplacementVSizeAddsConservativeWitnessAllowance(t *testing.T) {
	got, err := replacementVSize(make([]byte,100),make([]byte,80),2)
	if err != nil { t.Fatal(err) }
	if got != 89 { t.Fatalf("replacement vsize = %d, want 89",got) }
}

func TestIncrementalRelayFeeRateMustBePositive(t *testing.T) {
	engine := &SpendEngine{}
	if err := engine.SetIncrementalRelayFeeRate(0); err == nil { t.Fatal("zero incremental relay fee rate must be rejected") }
	if err := engine.SetIncrementalRelayFeeRate(2); err != nil { t.Fatalf("valid rate rejected: %v",err) }
}
