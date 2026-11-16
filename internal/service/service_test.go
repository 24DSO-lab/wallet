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
	_, err := svc.ConfirmSend(context.Background(), wallet.ID, intent, prepared, testSecrets())
	if err == nil {
		t.Fatal("expected broadcast error")
	}
	pending, err := st.PendingTxs().Get(context.Background(), txid)
	if err != nil {
		t.Fatalf("pending transaction was not retained: %v", err)
	}
	if pending.WalletID != wallet.ID || pending.Asset != domain.BTC || string(pending.Raw) != string([]byte{1, 2, 3}) {
		t.Fatalf("unexpected pending transaction: %+v", pending)
	}
}

func TestRefreshWalletPersistsSnapshotAtomically(t *testing.T) {
	address := "bc1qexample"
	snapshot := ports.Snapshot{
		Asset:     domain.BTC,
		Balance:   domain.Balance{Confirmed: domain.AmountFromUint64(1234), Pending: domain.AmountFromUint64(56)},
		Addresses: []domain.Address{{WalletID: "wallet-1", Network: domain.NetworkBitcoin, Address: address, Path: "m/84'/1'/0'/0/0", Chain: domain.ChainReceive}},
		UTXOs:     []domain.UTXO{{WalletID: "wallet-1", TxID: "abcd", Amount: domain.AmountFromUint64(1234)}},
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
