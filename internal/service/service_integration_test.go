package service_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
	"github.com/24DSO-lab/wallet/internal/service"
	"github.com/24DSO-lab/wallet/internal/store"
	"github.com/24DSO-lab/wallet/internal/testkit"
)

func TestSQLiteServiceRestartReconcilesAmbiguousBroadcast(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "wallet.db")
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const walletID = "integration-wallet"
	const txID domain.TxID = "integration-transaction"
	const password = "correct horse battery staple"
	keystore := testkit.NewFakeKeystore(testkit.FakeKeystoreOptions{
		Password: []byte(password),
		PublicKeys: ports.PublicKeys{
			BTCXpub:     "tpub-integration",
			ETHAddress:  "0x1111111111111111111111111111111111111111",
			TronAddress: "T1111111111111111111111111111111111",
		},
	})
	chain := testkit.NewFakeChain(testkit.FakeChainOptions{
		Network: domain.NetworkBitcoin,
		Prepared: ports.Prepared{
			Network:  domain.NetworkBitcoin,
			Asset:    domain.BTC,
			Draft:    []byte("unsigned draft"),
			Fee:      domain.AmountFromUint64(2),
			FeeAsset: domain.BTC,
		},
		Signed: ports.Signed{
			Network: domain.NetworkBitcoin,
			TxID:    txID,
			Raw:     []byte("signed transaction"),
		},
		TxID:   txID,
		Status: ports.TxStatus{State: domain.TxConfirmed, BlockHeight: 42, Confirmations: 3},
		Failures: map[testkit.ChainOperation]error{
			testkit.ChainBroadcast: errors.New("connection lost after submission"),
		},
	})
	chain.SetSnapshot(ports.Snapshot{
		Asset:   domain.BTC,
		Balance: domain.Balance{Confirmed: domain.AmountFromUint64(1000)},
		Addresses: []domain.Address{{
			WalletID: walletID, Network: domain.NetworkBitcoin, Address: "bc1qintegration",
			Path: "m/84'/1'/0'/0/0", Chain: domain.ChainReceive,
		}},
		UTXOs: []domain.UTXO{{WalletID: walletID, TxID: "funding-tx", Amount: domain.AmountFromUint64(1000)}},
	})

	newService := func(db ports.Store) *service.Service {
		t.Helper()
		svc, err := service.New(db, keystore, []ports.ChainAdapter{chain}, service.WithIDGenerator(func() (string, error) {
			return walletID, nil
		}))
		if err != nil {
			t.Fatalf("construct service: %v", err)
		}
		return svc
	}
	secrets := func() ports.Secrets {
		return ports.Secrets{Password: secret.FromString(password), Passphrase: secret.FromString("test passphrase")}
	}

	svc := newService(db)
	wallet, mnemonic, err := svc.CreateWallet(ctx, "SQLite integration", 12, secrets())
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	mnemonic.Wipe()
	if _, err := svc.RefreshWallet(ctx, wallet.ID, domain.BTC); err != nil {
		t.Fatalf("refresh wallet: %v", err)
	}

	intent := domain.SendIntent{
		Wallet: wallet.PublicWallet,
		Asset:  domain.BTC,
		To:     "bc1qrecipient",
		Amount: domain.AmountFromUint64(100),
	}
	prepared, err := svc.PrepareSend(ctx, intent)
	if err != nil {
		t.Fatalf("prepare send: %v", err)
	}
	if _, err := svc.ConfirmSend(ctx, wallet.ID, intent, prepared, secrets()); err == nil {
		t.Fatal("expected ambiguous broadcast error")
	}
	if pending, err := db.PendingTxs().Get(ctx, txID); err != nil || string(pending.Raw) != "signed transaction" {
		t.Fatalf("pending signed payload = (%+v, %v), want persisted payload", pending, err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close store before restart: %v", err)
	}
	db, err = store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}

	restarted := newService(db)
	count, err := restarted.ReconcilePending(ctx)
	if err != nil || count != 1 {
		t.Fatalf("reconcile after restart = (%d, %v), want (1, nil)", count, err)
	}
	history, err := restarted.GetHistory(ctx, wallet.ID, domain.BTC)
	if err != nil || len(history) != 1 {
		t.Fatalf("history after reconciliation = (%+v, %v), want one row", history, err)
	}
	if history[0].TxID != txID || history[0].Status != domain.TxConfirmed || history[0].BlockHeight != 42 {
		t.Fatalf("reconciled history = %+v, want confirmed transaction at height 42", history[0])
	}
	if _, err := db.PendingTxs().Get(ctx, txID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("pending recovery payload remains after reconciliation: %v", err)
	}
}

func TestServiceWalletReadImportPasswordAndSettingsUseCases(t *testing.T) {
	ctx := context.Background()
	svc, st := testService(t)
	wallet := createTestWallet(t, svc)

	wallets, err := svc.ListWallets(ctx)
	if err != nil || len(wallets) != 1 || wallets[0].ID != wallet.ID {
		t.Fatalf("ListWallets() = (%+v, %v), want the created wallet", wallets, err)
	}
	address, err := svc.GetReceiveAddress(ctx, wallet.ID, domain.ETH)
	if err != nil || address.Address != wallet.ETHAddress || address.Network != domain.NetworkEthereum {
		t.Fatalf("GetReceiveAddress(ETH) = (%+v, %v), want wallet ETH address", address, err)
	}
	balances, err := svc.GetBalances(ctx, wallet.ID)
	if err != nil || len(balances) != 0 {
		t.Fatalf("GetBalances() = (%v, %v), want empty cache", balances, err)
	}
	history, err := svc.GetHistory(ctx, wallet.ID, "")
	if err != nil || len(history) != 0 {
		t.Fatalf("GetHistory() = (%+v, %v), want empty history", history, err)
	}

	if err := svc.RenameWallet(ctx, wallet.ID, "Renamed wallet"); err != nil {
		t.Fatalf("RenameWallet(): %v", err)
	}
	renamed, err := st.Wallets().Get(ctx, wallet.ID)
	if err != nil || renamed.Name != "Renamed wallet" {
		t.Fatalf("renamed wallet = (%+v, %v)", renamed, err)
	}

	seed, err := svc.RevealSeed(ctx, wallet.ID, testSecrets())
	if err != nil || seed.IsEmpty() {
		t.Fatalf("RevealSeed() returned empty seed or error: %v", err)
	}
	seed.Wipe()
	if err := svc.VerifyPassword(ctx, secret.FromString("correct horse battery staple")); err != nil {
		t.Fatalf("VerifyPassword(): %v", err)
	}

	oldPassword := secret.FromString("correct horse battery staple")
	newPassword := secret.FromString("new integration password")
	if err := svc.ChangePassword(ctx, oldPassword, newPassword); err != nil {
		t.Fatalf("ChangePassword(): %v", err)
	}
	if !oldPassword.IsEmpty() || !newPassword.IsEmpty() {
		t.Fatal("ChangePassword did not wipe its input secrets")
	}
	if err := svc.VerifyPassword(ctx, secret.FromString("new integration password")); err != nil {
		t.Fatalf("VerifyPassword() rejected the changed password: %v", err)
	}

	if err := svc.SetSetting(ctx, "ui.language", "en"); err != nil {
		t.Fatalf("SetSetting(): %v", err)
	}
	value, err := svc.GetSetting(ctx, "ui.language")
	if err != nil || value != "en" {
		t.Fatalf("GetSetting() = (%q, %v), want en", value, err)
	}
	settings, err := svc.Settings(ctx)
	if err != nil || settings["ui.language"] != "en" {
		t.Fatalf("Settings() = (%v, %v), want ui.language=en", settings, err)
	}

	mnemonic := secret.FromString("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
	importSecrets := ports.Secrets{Password: secret.FromString("new integration password")}
	imported, err := svc.ImportWallet(ctx, "Imported wallet", mnemonic, importSecrets)
	if err != nil {
		t.Fatalf("ImportWallet(): %v", err)
	}
	if !mnemonic.IsEmpty() || !importSecrets.Password.IsEmpty() {
		t.Fatal("ImportWallet did not wipe its input secrets")
	}
	wallets, err = svc.ListWallets(ctx)
	if err != nil || len(wallets) != 2 || wallets[1].ID != imported.ID {
		t.Fatalf("ListWallets() after import = (%+v, %v), want two wallets", wallets, err)
	}
}

func TestConcurrentWalletCreationNeverExceedsConfiguredLimit(t *testing.T) {
	const callers = 12
	svc, st := testService(t)
	results := make(chan error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			_, mnemonic, err := svc.CreateWallet(context.Background(), fmt.Sprintf("Wallet %d", i), 12, testSecrets())
			mnemonic.Wipe()
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)

	var created, limited int
	for err := range results {
		switch {
		case err == nil:
			created++
		case domain.CodeOf(err) == domain.CodeWalletLimit:
			limited++
		default:
			t.Fatalf("unexpected concurrent CreateWallet error: %v", err)
		}
	}
	if created != 3 || limited != callers-3 {
		t.Fatalf("concurrent CreateWallet results: created=%d limited=%d, want 3 and %d", created, limited, callers-3)
	}
	wallets, err := st.Wallets().List(context.Background())
	if err != nil || len(wallets) != 3 {
		t.Fatalf("stored wallets = (%d, %v), want exactly 3", len(wallets), err)
	}
}
