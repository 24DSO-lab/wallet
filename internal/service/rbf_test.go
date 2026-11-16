package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/service"
	"github.com/24DSO-lab/wallet/internal/testkit"
)

const (
	rbfOriginalTxID domain.TxID = "original-rbf-txid"
	rbfReplacementTxID domain.TxID = "replacement-rbf-txid"
)

type fakeRBFChain struct {
	*testkit.FakeChain
	replacement ports.Replacement
	signed ports.Signed
	broadcastErr error
	verifyErr error
	verifyCalls int
}

func (c *fakeRBFChain) PrepareReplacement(_ context.Context, _ domain.PublicWallet, original ports.PendingTx) (ports.Replacement, error) {
	if original.TxID != rbfOriginalTxID {
		return ports.Replacement{}, errors.New("unexpected original transaction")
	}
	return c.replacement, nil
}

func (c *fakeRBFChain) VerifyReplacement(_ context.Context, _ domain.PublicWallet, original ports.PendingTx, replacement ports.Replacement) error {
	c.verifyCalls++
	if original.TxID != replacement.ReplacesTxID || replacement.ReplacesTxID != rbfOriginalTxID {
		return errors.New("replacement does not identify original transaction")
	}
	if c.verifyErr != nil {
		return c.verifyErr
	}
	return nil
}

func (c *fakeRBFChain) Finalize(_ ports.Prepared, _ []ports.Signature) (ports.Signed, error) {
	return c.signed, nil
}

func (c *fakeRBFChain) Broadcast(_ context.Context, signed ports.Signed) (domain.TxID, error) {
	if c.broadcastErr != nil {
		return "", c.broadcastErr
	}
	return signed.TxID, nil
}

func newRBFTestService(t *testing.T, accepted bool, broadcastErr error) (*service.Service, *testkit.MemoryStore, *fakeRBFChain, domain.Wallet) {
	t.Helper()
	chain := &fakeRBFChain{
		FakeChain: testkit.NewFakeChain(testkit.FakeChainOptions{
			Network: domain.NetworkBitcoin,
			Status: ports.TxStatus{State: domain.TxPending, Known: true},
		}),
		signed: ports.Signed{Network: domain.NetworkBitcoin, TxID: rbfReplacementTxID, Raw: []byte("replacement-raw")},
		broadcastErr: broadcastErr,
	}
	svc, st := testService(t, chain)
	wallet := createTestWallet(t, svc)
	original := ports.PendingTx{
		TxID: rbfOriginalTxID, WalletID: wallet.ID, Asset: domain.BTC,
		Network: domain.NetworkBitcoin, Raw: []byte("original-raw"),
		Draft: []byte("original-draft"), BroadcastAccepted: accepted,
	}
	if err := st.PendingTxs().Put(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	if err := st.Transactions().Upsert(context.Background(), []domain.Tx{{
		WalletID: wallet.ID, Asset: domain.BTC, TxID: rbfOriginalTxID,
		Direction: domain.DirectionOut, Amount: domain.AmountFromUint64(250),
		Fee: domain.AmountFromUint64(2), FeeAsset: domain.BTC,
		Status: domain.TxPending, Counterparty: "bc1qrecipient",
	}}); err != nil {
		t.Fatal(err)
	}
	chain.replacement = ports.Replacement{
		ReplacesTxID: rbfOriginalTxID,
		Intent: domain.SendIntent{
			Wallet: wallet.PublicWallet, Asset: domain.BTC,
			To: "bc1qrecipient", Amount: domain.AmountFromUint64(250),
		},
		Prepared: ports.Prepared{
			Network: domain.NetworkBitcoin, Asset: domain.BTC,
			Draft: []byte("replacement-draft"), Digests: []ports.DigestRequest{{}},
			Fee: domain.AmountFromUint64(5), FeeAsset: domain.BTC,
		},
	}
	return svc, st, chain, wallet
}

func TestRBFLifecyclePersistsReplacementAndRetiresOriginal(t *testing.T) {
	svc, st, chain, wallet := newRBFTestService(t, true, nil)

	replacement, err := svc.PrepareRBF(context.Background(), wallet.ID, rbfOriginalTxID)
	if err != nil {
		t.Fatalf("PrepareRBF: %v", err)
	}
	if chain.verifyCalls != 1 {
		t.Fatalf("VerifyReplacement calls after prepare = %d, want 1", chain.verifyCalls)
	}

	got, err := svc.ConfirmRBF(context.Background(), wallet.ID, replacement, testSecrets())
	if err != nil {
		t.Fatalf("ConfirmRBF: %v", err)
	}
	if got != rbfReplacementTxID {
		t.Fatalf("replacement txid = %q, want %q", got, rbfReplacementTxID)
	}
	if chain.verifyCalls != 2 {
		t.Fatalf("VerifyReplacement calls after confirm = %d, want 2", chain.verifyCalls)
	}

	if _, err := st.PendingTxs().Get(context.Background(), rbfOriginalTxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("original pending record should be retired, got %v", err)
	}
	pending, err := st.PendingTxs().Get(context.Background(), rbfReplacementTxID)
	if err != nil {
		t.Fatalf("replacement pending record missing: %v", err)
	}
	if !pending.BroadcastAccepted || pending.ReplacesTxID != rbfOriginalTxID {
		t.Fatalf("replacement pending metadata is wrong: %+v", pending)
	}
	oldHistory, err := st.Transactions().Get(context.Background(), wallet.ID, domain.BTC, rbfOriginalTxID)
	if err != nil || oldHistory.Status != domain.TxReplaced {
		t.Fatalf("original history status = %q, err = %v; want replaced", oldHistory.Status, err)
	}
	newHistory, err := st.Transactions().Get(context.Background(), wallet.ID, domain.BTC, rbfReplacementTxID)
	if err != nil || newHistory.Status != domain.TxPending {
		t.Fatalf("replacement history status = %q, err = %v; want pending", newHistory.Status, err)
	}
}

func TestRBFUnknownBroadcastIsReconciledWithoutLosingOriginal(t *testing.T) {
	svc, st, _, wallet := newRBFTestService(t, true, errors.New("connection lost after submit"))

	replacement, err := svc.PrepareRBF(context.Background(), wallet.ID, rbfOriginalTxID)
	if err != nil {
		t.Fatalf("PrepareRBF: %v", err)
	}
	got, err := svc.ConfirmRBF(context.Background(), wallet.ID, replacement, testSecrets())
	if got != rbfReplacementTxID || domain.CodeOf(err) != domain.CodeBroadcastUnknown {
		t.Fatalf("ConfirmRBF = (%q, %v), want replacement txid and broadcast_unknown", got, err)
	}

	if _, err := st.PendingTxs().Get(context.Background(), rbfOriginalTxID); err != nil {
		t.Fatalf("original pending record was lost after ambiguous broadcast: %v", err)
	}
	child, err := st.PendingTxs().Get(context.Background(), rbfReplacementTxID)
	if err != nil || child.BroadcastAccepted {
		t.Fatalf("ambiguous replacement state = %+v, %v; want durable but unaccepted", child, err)
	}
	if _, err := svc.PrepareRBF(context.Background(), wallet.ID, rbfOriginalTxID); domain.CodeOf(err) != domain.CodeDuplicateSend {
		t.Fatalf("second replacement attempt error = %v, want duplicate_send", err)
	}

	if _, err := svc.ReconcilePending(context.Background()); err != nil {
		t.Fatalf("first ReconcilePending: %v", err)
	}
	if _, err := svc.ReconcilePending(context.Background()); err != nil {
		t.Fatalf("second ReconcilePending: %v", err)
	}
	if _, err := st.PendingTxs().Get(context.Background(), rbfOriginalTxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("accepted replacement should retire original after reconciliation, got %v", err)
	}
	child, err = st.PendingTxs().Get(context.Background(), rbfReplacementTxID)
	if err != nil || !child.BroadcastAccepted {
		t.Fatalf("replacement should remain accepted and pending: %+v, %v", child, err)
	}
	oldHistory, err := st.Transactions().Get(context.Background(), wallet.ID, domain.BTC, rbfOriginalTxID)
	if err != nil || oldHistory.Status != domain.TxReplaced {
		t.Fatalf("original history status = %q, err = %v; want replaced", oldHistory.Status, err)
	}
}

func TestPrepareRBFRejectsUnacceptedOriginal(t *testing.T) {
	svc, _, _, wallet := newRBFTestService(t, false, nil)
	if _, err := svc.PrepareRBF(context.Background(), wallet.ID, rbfOriginalTxID); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Fatalf("PrepareRBF error = %v, want invalid_input", err)
	}
}

func TestConfirmRBFRevalidatesReplacementBeforeSigning(t *testing.T) {
	svc, st, chain, wallet := newRBFTestService(t, true, nil)
	replacement, err := svc.PrepareRBF(context.Background(), wallet.ID, rbfOriginalTxID)
	if err != nil {
		t.Fatalf("PrepareRBF: %v", err)
	}
	chain.verifyErr = domain.NewError(domain.CodeVerificationFailed, errors.New("replacement changed"))
	if _, err := svc.ConfirmRBF(context.Background(), wallet.ID, replacement, testSecrets()); domain.CodeOf(err) != domain.CodeVerificationFailed {
		t.Fatalf("ConfirmRBF error = %v, want verification_failed", err)
	}
	if _, err := st.PendingTxs().Get(context.Background(), rbfReplacementTxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unverified replacement must not be persisted, got %v", err)
	}
	if chain.verifyCalls != 2 {
		t.Fatalf("VerifyReplacement calls = %d, want 2", chain.verifyCalls)
	}
}
