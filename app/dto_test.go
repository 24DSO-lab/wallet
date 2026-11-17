package app

import (
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

func TestWalletDTOIncludesReceiveAssetsBeforeFirstRefresh(t *testing.T) {
	wallet := domain.Wallet{ID: "wallet-1", Name: "Wallet"}
	got := walletDTO(wallet, nil)
	want := []string{"BTC", "ETH", "USDC", "USDT", "TRX"}
	if len(got.Balances) != len(want) {
		t.Fatalf("balance count = %d, want %d: %+v", len(got.Balances), len(want), got.Balances)
	}
	for i, asset := range want {
		balance := got.Balances[i]
		if balance.Asset != asset || balance.Amount != "0" || balance.UpdatedAt != "" {
			t.Errorf("balance[%d] = %+v, want unrefreshed %s placeholder", i, balance, asset)
		}
	}
	usdt := got.Balances[3]
	if usdt.FeeAsset != "TRX" || usdt.FeeBalance != "0" {
		t.Errorf("USDT fee balance = %s %s, want zero TRX placeholder", usdt.FeeBalance, usdt.FeeAsset)
	}
}

func TestSnapshotDTOReportsPartialProviderFailuresWithoutFeeBalance(t *testing.T) {
	got := snapshotDTO(ports.Snapshot{
		Asset: domain.USDC,
		Balance: domain.Balance{Confirmed: domain.AmountFromUint64(42)},
		FeeAsset: domain.ETH,
		FeeBalance: domain.AmountFromUint64(99),
		HistoryError: "provider_unavailable",
		FeeBalanceError: "provider_rate_limited",
		FetchedAt: time.Date(2026, 11, 17, 4, 0, 0, 0, time.UTC),
	})
	if got.Amount != "42" {
		t.Fatalf("fresh token balance = %q, want 42", got.Amount)
	}
	if got.FeeAsset != string(domain.ETH) || got.FeeBalance != "" {
		t.Fatalf("unavailable fee balance must be omitted, got %+v", got)
	}
	if len(got.Warnings) != 2 || got.Warnings[0] != "history_unavailable:provider_unavailable" || got.Warnings[1] != "fee_balance_unavailable:provider_rate_limited" {
		t.Fatalf("partial provider warnings = %#v", got.Warnings)
	}
}

func TestTxDTOLeavesUnknownTimestampAndFeeUnspecified(t *testing.T) {
	got := txDTO(domain.Tx{
		TxID: "pending-transaction", Asset: domain.USDT, Direction: domain.DirectionOut,
		Amount: domain.AmountFromUint64(1_000_000), Status: domain.TxPending,
	})
	if got.Timestamp != "" {
		t.Fatalf("zero transaction time = %q, want empty timestamp", got.Timestamp)
	}
	if got.FeeAsset != "" {
		t.Fatalf("unknown transaction fee asset = %q, want empty", got.FeeAsset)
	}
}

func TestWalletDTOUsesCachedTRXBalanceForUSDTFees(t *testing.T) {
	wallet := domain.Wallet{ID: "wallet-1", Name: "Wallet"}
	records := map[domain.Asset]ports.BalanceRecord{
		domain.TRX: {
			Balance:   domain.Balance{Confirmed: domain.AmountFromUint64(25_000_000)},
			UpdatedAt: time.Date(2026, 11, 16, 5, 0, 0, 0, time.UTC),
		},
	}
	got := walletDTO(wallet, records)
	if got.Balances[3].FeeAsset != "TRX" || got.Balances[3].FeeBalance != "25000000" {
		t.Fatalf("USDT fee balance = %+v, want cached TRX amount", got.Balances[3])
	}
	if got.Balances[4].Asset != "TRX" || got.Balances[4].Amount != "25000000" || got.Balances[4].UpdatedAt == "" {
		t.Fatalf("TRX balance = %+v, want visible cached TRX asset", got.Balances[4])
	}
}
