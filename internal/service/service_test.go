package service_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
	"github.com/24DSO-lab/wallet/internal/service"
	"github.com/24DSO-lab/wallet/internal/testkit"
)

func testService(t *testing.T, chains ...ports.ChainAdapter) (*service.Service, *testkit.MemoryStore) {
	t.Helper()
	st := testkit.NewMemoryStore()
	ks := testkit.NewFakeKeystore(testkit.FakeKeystoreOptions{
		Password: []byte("correct horse battery staple"),
		PublicKeys: ports.PublicKeys{
			BTCXpub: "tpub-test", ETHAddress: "0x1111111111111111111111111111111111111111",
			TronAddress: "T1111111111111111111111111111111111",
		},
	})
	var sequence atomic.Uint64
	svc, err := service.New(st, ks, chains,
		service.WithClock(func() time.Time { return time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC) }),
		service.WithIDGenerator(func() (string, error) { return fmt.Sprintf("wallet-%d", sequence.Add(1)), nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return svc, st
}

func testSecrets() ports.Secrets {
	return ports.Secrets{Password: secret.FromString("correct horse battery staple"), Passphrase: secret.FromString("optional phrase")}
}

func createTestWallet(t *testing.T, svc *service.Service) domain.Wallet {
	t.Helper()
	wallet, mnemonic, err := svc.CreateWallet(context.Background(), "  Personal  ", 12, testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	if mnemonic.IsEmpty() {
		t.Fatal("created mnemonic is empty")
	}
	mnemonic.Wipe()
	return wallet
}

func TestCreateWalletReturnsOneTimeMnemonicAndPersistsPublicData(t *testing.T) {
	svc, st := testService(t)
	sec := testSecrets()
	wallet, mnemonic, err := svc.CreateWallet(context.Background(), " Main wallet ", 12, sec)
	if err != nil {
		t.Fatal(err)
	}
	defer mnemonic.Wipe()
	if mnemonic.IsEmpty() {
		t.Fatal("mnemonic was not returned")
	}
	if !sec.Password.IsEmpty() || !sec.Passphrase.IsEmpty() {
		t.Fatal("input secrets were not wiped")
	}
	if wallet.ID != "wallet-1" || wallet.Name != "Main wallet" || wallet.KeystoreID != wallet.ID {
		t.Fatalf("unexpected wallet: %+v", wallet)
	}
	if wallet.BTCXpub != "tpub-test" || wallet.ETHAddress == "" || wallet.TronAddress == "" {
		t.Fatalf("public keys missing: %+v", wallet.PublicWallet)
	}
	if !wallet.CreatedAt.Equal(time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected creation time: %s", wallet.CreatedAt)
	}
	got, err := st.Wallets().Get(context.Background(), wallet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicWallet != wallet.PublicWallet {
		t.Fatalf("stored wallet differs: %+v", got)
	}
	addresses, err := st.Addresses().List(context.Background(), wallet.ID, domain.NetworkEthereum)
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 1 || addresses[0].Address != wallet.ETHAddress {
		t.Fatalf("ETH address not persisted: %+v", addresses)
	}
}

func TestCreateWalletEnforcesThreeWalletLimit(t *testing.T) {
	svc, _ := testService(t)
	for i := 0; i < 3; i++ {
		createTestWallet(t, svc)
	}
	_, mnemonic, err := svc.CreateWallet(context.Background(), "Fourth", 12, testSecrets())
	if mnemonic.IsEmpty() == false {
		mnemonic.Wipe()
		t.Fatal("mnemonic should not be returned after limit failure")
	}
	if domain.CodeOf(err) != domain.CodeWalletLimit {
		t.Fatalf("error = %v, want wallet limit", err)
	}
}

func TestConfirmSendPersistsBeforeBroadcastAndRetainsAmbiguousPending(t *testing.T) {
	const txid domain.TxID = "deadbeef"
	chain := testkit.NewFakeChain(testkit.FakeChainOptions{
		Network:  domain.NetworkBitcoin,
		Signed:   ports.Signed{Network: domain.NetworkBitcoin, TxID: txid, Raw: []byte{1, 2, 3}},
		TxID:     txid,
		Failures: map[testkit.ChainOperation]error{testkit.ChainBroadcast: errors.New("network unavailable")},
	})
	svc, st := testService(t, chain)
	wallet := createTestWallet(t, svc)
	intent := domain.SendIntent{Wallet: wallet.PublicWallet, Asset: domain.BTC, To: "bc1qrecipient", Amount: domain.AmountFromUint64(100)}
	prepared := ports.Prepared{Network: domain.NetworkBitcoin, Asset: domain.BTC, Draft: []byte{9}}
	gotTxID, err := svc.ConfirmSend(context.Background(), wallet.ID, intent, prepared, testSecrets())
	if err == nil {
		t.Fatal("expected broadcast error")
	}
	if gotTxID != txid || domain.CodeOf(err) != domain.CodeBroadcastUnknown {
		t.Fatalf("ambiguous broadcast = (%q, %v), want persisted txid and broadcast_unknown", gotTxID, err)
	}
	if _, err := svc.PrepareSend(context.Background(), intent); domain.CodeOf(err) != domain.CodeDuplicateSend {
		t.Fatalf("PrepareSend after ambiguous broadcast = %v, want duplicate_send", err)
	}
	if _, err := svc.ConfirmSend(context.Background(), wallet.ID, intent, prepared, testSecrets()); domain.CodeOf(err) != domain.CodeDuplicateSend {
		t.Fatalf("second ConfirmSend after ambiguous broadcast = %v, want duplicate_send", err)
	}
	pending, err := st.PendingTxs().Get(context.Background(), txid)
	if err != nil {
		t.Fatalf("pending transaction was not retained: %v", err)
	}
	if pending.WalletID != wallet.ID || pending.Asset != domain.BTC || string(pending.Raw) != string([]byte{1, 2, 3}) || string(pending.Draft) != string(prepared.Draft) || pending.BroadcastAccepted {
		t.Fatalf("unexpected pending transaction: %+v", pending)
	}
	history, err := st.Transactions().List(context.Background(), wallet.ID, domain.BTC)
	if err != nil || len(history) != 1 {
		t.Fatalf("pending send was not added to history: rows=%+v err=%v", history, err)
	}
	if history[0].TxID != txid || history[0].Status != domain.TxPending || history[0].Direction != domain.DirectionOut || history[0].Counterparty != intent.To {
		t.Fatalf("unexpected pending history record: %+v", history[0])
	}
}

func TestPrepareSendReconcilesConfirmedAmbiguousBroadcast(t *testing.T) {
	const txid domain.TxID = "confirmed-after-ambiguous-broadcast"
	chain := testkit.NewFakeChain(testkit.FakeChainOptions{
		Network:  domain.NetworkBitcoin,
		Prepared: ports.Prepared{Network: domain.NetworkBitcoin, Asset: domain.BTC},
		Signed:   ports.Signed{Network: domain.NetworkBitcoin, TxID: txid, Raw: []byte{7, 8, 9}},
		Status:   ports.TxStatus{State: domain.TxConfirmed, BlockHeight: 100, Confirmations: 1},
		Failures: map[testkit.ChainOperation]error{
			testkit.ChainBroadcast: errors.New("connection closed after submit"),
		},
	})
	svc, st := testService(t, chain)
	wallet := createTestWallet(t, svc)
	intent := domain.SendIntent{
		Wallet: wallet.PublicWallet, Asset: domain.BTC,
		To: "bc1qrecipient", Amount: domain.AmountFromUint64(100),
	}
	initial := ports.Prepared{Network: domain.NetworkBitcoin, Asset: domain.BTC, Draft: []byte{1}}
	if got, err := svc.ConfirmSend(context.Background(), wallet.ID, intent, initial, testSecrets()); got != txid || domain.CodeOf(err) != domain.CodeBroadcastUnknown {
		t.Fatalf("ConfirmSend = (%q, %v), want persisted txid and broadcast_unknown", got, err)
	}

	prepared, err := svc.PrepareSend(context.Background(), intent)
	if err != nil {
		t.Fatalf("PrepareSend after confirmed reconciliation: %v", err)
	}
	if prepared.Network != domain.NetworkBitcoin || prepared.Asset != domain.BTC {
		t.Fatalf("unexpected replacement quote: %+v", prepared)
	}
	if _, err := st.PendingTxs().Get(context.Background(), txid); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("confirmed pending payload was not removed: %v", err)
	}
	history, err := st.Transactions().Get(context.Background(), wallet.ID, domain.BTC, txid)
	if err != nil || history.Status != domain.TxConfirmed || history.BlockHeight != 100 {
		t.Fatalf("history was not reconciled: %+v, %v", history, err)
	}
}

func TestReconcilePendingMarksMempoolTransactionAccepted(t *testing.T) {
	const txid domain.TxID = "seen-in-mempool"
	chain := testkit.NewFakeChain(testkit.FakeChainOptions{
		Network:  domain.NetworkBitcoin,
		Prepared: ports.Prepared{Network: domain.NetworkBitcoin, Asset: domain.BTC},
		Signed:   ports.Signed{Network: domain.NetworkBitcoin, TxID: txid, Raw: []byte{1, 3, 5}},
		Status:   ports.TxStatus{State: domain.TxPending},
		Failures: map[testkit.ChainOperation]error{
			testkit.ChainBroadcast: errors.New("connection closed after submit"),
		},
	})
	svc, st := testService(t, chain)
	wallet := createTestWallet(t, svc)
	intent := domain.SendIntent{Wallet: wallet.PublicWallet, Asset: domain.BTC, To: "bc1qrecipient", Amount: domain.AmountFromUint64(100)}
	prepared := ports.Prepared{Network: domain.NetworkBitcoin, Asset: domain.BTC, Draft: []byte{4}}
	if _, err := svc.ConfirmSend(context.Background(), wallet.ID, intent, prepared, testSecrets()); domain.CodeOf(err) != domain.CodeBroadcastUnknown {
		t.Fatalf("ConfirmSend error = %v, want broadcast_unknown", err)
	}
	if n, err := svc.ReconcilePending(context.Background()); err != nil || n != 0 {
		t.Fatalf("ReconcilePending = (%d, %v), want no terminal transitions", n, err)
	}
	pending, err := st.PendingTxs().Get(context.Background(), txid)
	if err != nil || !pending.BroadcastAccepted {
		t.Fatalf("mempool-seen transaction was not marked accepted: %+v, %v", pending, err)
	}
	if _, err := svc.PrepareSend(context.Background(), intent); err != nil {
		t.Fatalf("new send should be allowed after provider confirms mempool acceptance: %v", err)
	}
}

func TestConfirmSendSuccessKeepsPendingHistoryAndRecoveryPayloadForRBF(t *testing.T) {
	const txid domain.TxID = "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe"
	chain := testkit.NewFakeChain(testkit.FakeChainOptions{
		Network: domain.NetworkBitcoin,
		Signed:  ports.Signed{Network: domain.NetworkBitcoin, TxID: txid, Raw: []byte{4, 5, 6}},
		TxID:    txid,
	})
	svc, st := testService(t, chain)
	wallet := createTestWallet(t, svc)
	intent := domain.SendIntent{Wallet: wallet.PublicWallet, Asset: domain.BTC, To: "bc1qrecipient", Amount: domain.AmountFromUint64(250)}
	prepared := ports.Prepared{Network: domain.NetworkBitcoin, Asset: domain.BTC, Draft: []byte{9}, Fee: domain.AmountFromUint64(3), FeeAsset: domain.BTC}

	got, err := svc.ConfirmSend(context.Background(), wallet.ID, intent, prepared, testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	if got != txid {
		t.Fatalf("txid = %q, want %q", got, txid)
	}
	pending, err := st.PendingTxs().Get(context.Background(), txid)
	if err != nil {
		t.Fatalf("recovery payload was not retained for RBF: %v", err)
	}
	if !pending.BroadcastAccepted || string(pending.Draft) != string(prepared.Draft) || string(pending.Raw) != string([]byte{4, 5, 6}) {
		t.Fatalf("pending transaction lacks RBF metadata: %+v", pending)
	}
	history, err := st.Transactions().List(context.Background(), wallet.ID, domain.BTC)
	if err != nil || len(history) != 1 {
		t.Fatalf("history rows = %+v, err = %v", history, err)
	}
	if history[0].Status != domain.TxPending || !history[0].Amount.Equal(intent.Amount) || !history[0].Fee.Equal(prepared.Fee) {
		t.Fatalf("unexpected outgoing history record: %+v", history[0])
	}
}

func TestRefreshWalletPersistsTRXFeeBalance(t *testing.T) {
	chain := testkit.NewFakeChain(testkit.FakeChainOptions{
		Network: domain.NetworkTron,
		Snapshot: ports.Snapshot{
			Asset: domain.TRX,
			Balance: domain.Balance{Confirmed: domain.AmountFromUint64(25_000_000)},
		},
	})
	svc, st := testService(t, chain)
	wallet := createTestWallet(t, svc)

	snapshot, err := svc.RefreshWallet(context.Background(), wallet.ID, domain.TRX)
	if err != nil {
		t.Fatalf("RefreshWallet(TRX): %v", err)
	}
	if snapshot.Asset != domain.TRX || !snapshot.Balance.Confirmed.Equal(domain.AmountFromUint64(25_000_000)) {
		t.Fatalf("unexpected TRX snapshot: %+v", snapshot)
	}
	record, err := st.Balances().Get(context.Background(), wallet.ID, domain.TRX)
	if err != nil || !record.Balance.Confirmed.Equal(domain.AmountFromUint64(25_000_000)) {
		t.Fatalf("stored TRX balance = (%+v, %v)", record, err)
	}
}

func TestRefreshWalletPersistsSnapshotAtomically(t *testing.T) {
	address := "bc1qexample"
	snapshot := ports.Snapshot{
		Asset:     domain.BTC,
		Balance:   domain.Balance{Confirmed: domain.AmountFromUint64(1234), Pending: domain.AmountFromUint64(56)},
		Addresses: []domain.Address{{WalletID: "wallet-1", Network: domain.NetworkBitcoin, Address: address, Path: "m/84'/1'/0'/0/0", Chain: domain.ChainReceive}},
		UTXOs:     []domain.UTXO{{WalletID: "wallet-1", TxID: "abcd", Address: address, Amount: domain.AmountFromUint64(1234)}},
	}
	chain := testkit.NewFakeChain(testkit.FakeChainOptions{Network: domain.NetworkBitcoin, Snapshot: snapshot})
	svc, st := testService(t, chain)
	wallet := createTestWallet(t, svc)
	got, err := svc.RefreshWallet(context.Background(), wallet.ID, domain.BTC)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Balance.Confirmed.Equal(domain.AmountFromUint64(1234)) {
		t.Fatalf("unexpected snapshot balance: %+v", got.Balance)
	}
	balance, err := st.Balances().Get(context.Background(), wallet.ID, domain.BTC)
	if err != nil {
		t.Fatal(err)
	}
	if !balance.Balance.Pending.Equal(domain.AmountFromUint64(56)) {
		t.Fatalf("balance was not persisted: %+v", balance)
	}
	addresses, err := st.Addresses().List(context.Background(), wallet.ID, domain.NetworkBitcoin)
	if err != nil || len(addresses) != 1 {
		t.Fatalf("addresses = %+v, err = %v", addresses, err)
	}
	utxos, err := st.UTXOs().List(context.Background(), wallet.ID)
	if err != nil || len(utxos) != 1 {
		t.Fatalf("utxos = %+v, err = %v", utxos, err)
	}
}

func TestDeleteWalletRequiresNameAndPasswordConfirmation(t *testing.T) {
	svc, st := testService(t)
	wallet := createTestWallet(t, svc)

	if err := svc.DeleteWallet(context.Background(), wallet.ID, "not the wallet name", testSecrets()); err == nil {
		t.Fatal("expected name confirmation error")
	}
	if _, err := st.Wallets().Get(context.Background(), wallet.ID); err != nil {
		t.Fatalf("wallet was removed after wrong name: %v", err)
	}

	wrongPassword := ports.Secrets{Password: secret.FromString("incorrect password")}
	if err := svc.DeleteWallet(context.Background(), wallet.ID, wallet.Name, wrongPassword); err == nil {
		t.Fatal("expected password error")
	}
	if _, err := st.Wallets().Get(context.Background(), wallet.ID); err != nil {
		t.Fatalf("wallet was removed after wrong password: %v", err)
	}
}

func TestDeleteWalletRemovesWalletAndKeystore(t *testing.T) {
	st := testkit.NewMemoryStore()
	ks := testkit.NewFakeKeystore(testkit.FakeKeystoreOptions{
		Password: []byte("correct horse battery staple"),
		PublicKeys: ports.PublicKeys{
			BTCXpub: "tpub-test", ETHAddress: "0x1111111111111111111111111111111111111111",
			TronAddress: "T1111111111111111111111111111111111",
		},
	})
	svc, err := service.New(st, ks, nil, service.WithIDGenerator(func() (string, error) { return "wallet-delete", nil }))
	if err != nil {
		t.Fatal(err)
	}
	wallet := createTestWallet(t, svc)
	if err := svc.DeleteWallet(context.Background(), wallet.ID, wallet.Name, testSecrets()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Wallets().Get(context.Background(), wallet.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("wallet still exists or unexpected error: %v", err)
	}
	password := secret.FromString("correct horse battery staple")
	_, revealErr := ks.RevealSeed(context.Background(), wallet.KeystoreID, ports.Secrets{Password: password})
	password.Wipe()
	if revealErr == nil {
		t.Fatal("keystore file still exists after wallet deletion")
	}
}

type walletDeleteFailStore struct {
	ports.Store
}

func (s walletDeleteFailStore) Wallets() ports.WalletRepo {
	return walletDeleteFailRepo{WalletRepo: s.Store.Wallets()}
}

type walletDeleteFailRepo struct {
	ports.WalletRepo
}

func (walletDeleteFailRepo) Delete(context.Context, string) error {
	return errors.New("simulated database delete failure")
}

func TestDeleteWalletRestoresKeystoreWhenDatabaseDeleteFails(t *testing.T) {
	base := testkit.NewMemoryStore()
	ks := testkit.NewFakeKeystore(testkit.FakeKeystoreOptions{
		Password: []byte("correct horse battery staple"),
		PublicKeys: ports.PublicKeys{
			BTCXpub: "tpub-test", ETHAddress: "0x1111111111111111111111111111111111111111",
			TronAddress: "T1111111111111111111111111111111111",
		},
	})
	svc, err := service.New(walletDeleteFailStore{Store: base}, ks, nil, service.WithIDGenerator(func() (string, error) { return "wallet-rollback", nil }))
	if err != nil {
		t.Fatal(err)
	}
	wallet := createTestWallet(t, svc)
	if err := svc.DeleteWallet(context.Background(), wallet.ID, wallet.Name, testSecrets()); err == nil {
		t.Fatal("expected database delete failure")
	}
	if _, err := base.Wallets().Get(context.Background(), wallet.ID); err != nil {
		t.Fatalf("wallet record was removed: %v", err)
	}
	password := secret.FromString("correct horse battery staple")
	seed, err := ks.RevealSeed(context.Background(), wallet.KeystoreID, ports.Secrets{Password: password})
	password.Wipe()
	if err != nil {
		t.Fatalf("keystore was not restored: %v", err)
	}
	seed.Wipe()
}
