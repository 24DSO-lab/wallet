package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/testkit"
)

func seedPendingTransaction(t *testing.T, st *testkit.MemoryStore, wallet domain.Wallet, txid domain.TxID) {
	t.Helper()
	ctx := context.Background()
	if err := st.Transactions().Upsert(ctx, []domain.Tx{{
		WalletID: wallet.ID, Asset: domain.BTC, TxID: txid,
		Direction: domain.DirectionOut, Status: domain.TxPending,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.PendingTxs().Put(ctx, ports.PendingTx{
		TxID: txid, WalletID: wallet.ID, Asset: domain.BTC,
		Network: domain.NetworkBitcoin, Raw: []byte("signed payload"),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReconcilePendingFinalStatuses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  domain.TxStatus
		height uint64
	}{
		{name: "confirmed", state: domain.TxConfirmed, height: 123},
		{name: "failed", state: domain.TxFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const txid domain.TxID = "reconcile-tx"
			chain := testkit.NewFakeChain(testkit.FakeChainOptions{
				Network: domain.NetworkBitcoin,
				Status:  ports.TxStatus{State: tc.state, BlockHeight: tc.height},
			})
			svc, st := testService(t, chain)
			wallet := createTestWallet(t, svc)
			seedPendingTransaction(t, st, wallet, txid)

			count, err := svc.ReconcilePending(context.Background())
			if err != nil || count != 1 {
				t.Fatalf("ReconcilePending() = (%d, %v), want (1, nil)", count, err)
			}
			history, err := st.Transactions().Get(context.Background(), wallet.ID, domain.BTC, txid)
			if err != nil {
				t.Fatal(err)
			}
			if history.Status != tc.state || history.BlockHeight != tc.height {
				t.Fatalf("history = %+v, want status %q and height %d", history, tc.state, tc.height)
			}
			if _, err := st.PendingTxs().Get(context.Background(), txid); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("pending payload was not removed: %v", err)
			}
		})
	}
}

func TestReconcilePendingLeavesUnresolvedRecords(t *testing.T) {
	for _, tc := range []struct {
		name    string
		chain   []ports.ChainAdapter
		wantErr bool
	}{
		{
			name: "provider error",
			chain: []ports.ChainAdapter{testkit.NewFakeChain(testkit.FakeChainOptions{
				Network:  domain.NetworkBitcoin,
				Failures: map[testkit.ChainOperation]error{testkit.ChainStatus: errors.New("provider offline")},
			})},
			wantErr: true,
		},
		{
			name: "invalid provider status",
			chain: []ports.ChainAdapter{testkit.NewFakeChain(testkit.FakeChainOptions{
				Network: domain.NetworkBitcoin, Status: ports.TxStatus{State: "unknown"},
			})},
			wantErr: true,
		},
		{name: "missing adapter"},
		{
			name: "still pending",
			chain: []ports.ChainAdapter{testkit.NewFakeChain(testkit.FakeChainOptions{
				Network: domain.NetworkBitcoin, Status: ports.TxStatus{State: domain.TxPending},
			})},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const txid domain.TxID = "unresolved-tx"
			svc, st := testService(t, tc.chain...)
			wallet := createTestWallet(t, svc)
			seedPendingTransaction(t, st, wallet, txid)

			count, err := svc.ReconcilePending(context.Background())
			if count != 0 || (err != nil) != tc.wantErr {
				t.Fatalf("ReconcilePending() = (%d, %v), want count 0 and error=%t", count, err, tc.wantErr)
			}
			if _, err := st.PendingTxs().Get(context.Background(), txid); err != nil {
				t.Fatalf("unresolved pending payload was removed: %v", err)
			}
			history, err := st.Transactions().Get(context.Background(), wallet.ID, domain.BTC, txid)
			if err != nil {
				t.Fatal(err)
			}
			if history.Status != domain.TxPending {
				t.Fatalf("history status = %q, want pending", history.Status)
			}
		})
	}
}
