package app

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/secret"
	"github.com/24DSO-lab/wallet/internal/service"
)

func TestAssetCatalogUsesProfileAndCoversAllAssets(t *testing.T) {
	tests := []struct {
		profile                    domain.Profile
		btc, eth, tron, usdc, usdt string
	}{
		{domain.ProfileTest, "Bitcoin testnet4", "Ethereum Sepolia", "TRON Nile", "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238", "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf"},
		{domain.ProfileMain, "Bitcoin", "Ethereum", "TRON", "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"},
	}
	for _, tt := range tests {
		t.Run(string(tt.profile), func(t *testing.T) {
			got, err := NewWithDependencies(Dependencies{Profile: tt.profile}).GetAssetCatalog()
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 5 {
				t.Fatalf("got %d assets, want 5", len(got))
			}
			want := []string{"BTC", "ETH", "USDC", "USDT", "TRX"}
			for i := range want {
				if got[i].Asset != want[i] {
					t.Errorf("asset[%d] = %s, want %s", i, got[i].Asset, want[i])
				}
			}
			if got[0].NetworkName != tt.btc || got[1].NetworkName != tt.eth || got[2].ContractAddress != tt.usdc || got[3].NetworkName != tt.tron || got[3].ContractAddress != tt.usdt {
				t.Errorf("catalog mismatch: %+v", got)
			}
			if got[0].Kind != "native" || got[1].Kind != "native" || got[2].Kind != "token" || got[2].Standard != "ERC-20" || got[3].Kind != "token" || got[3].Standard != "TRC-20" || got[4].Kind != "native" {
				t.Errorf("asset kind mismatch: %+v", got)
			}
		})
	}
}

func TestSetProfileRejectsMainnetUntilAcceptanceGatesPass(t *testing.T) {
	called := false
	a := NewWithDependencies(Dependencies{
		Profile: domain.ProfileTest,
		SwitchProfile: func(context.Context, domain.Profile) (*service.Service, error) {
			called = true
			return nil, nil
		},
	})

	if err := a.SetProfile(string(domain.ProfileMain)); err == nil || err.Error() != string(domain.CodeMainnetDisabled) {
		t.Fatalf("SetProfile(main) error = %v, want %s", err, domain.CodeMainnetDisabled)
	}
	if called {
		t.Fatal("mainnet runtime was constructed before acceptance gates passed")
	}
}

func TestRandomChallengePositions(t *testing.T) {
	for _, wordCount := range []int{12, 24} {
		t.Run(fmt.Sprintf("words-%d", wordCount), func(t *testing.T) {
			for iteration := 0; iteration < 100; iteration++ {
				positions, err := randomChallengePositions(wordCount)
				if err != nil {
					t.Fatalf("randomChallengePositions(%d): %v", wordCount, err)
				}
				if len(positions) != 3 {
					t.Fatalf("got %d challenge positions, want 3", len(positions))
				}
				seen := make(map[int]bool, len(positions))
				for i, position := range positions {
					if position < 0 || position >= wordCount {
						t.Fatalf("position %d is outside 0..%d", position, wordCount-1)
					}
					if seen[position] {
						t.Fatalf("duplicate challenge position %d", position)
					}
					seen[position] = true
					if i > 0 && positions[i-1] >= position {
						t.Fatalf("positions are not strictly sorted: %v", positions)
					}
				}
			}
		})
	}
}

func TestRandomChallengePositionsRejectsTooShortInput(t *testing.T) {
	if _, err := randomChallengePositions(2); err == nil {
		t.Fatal("expected invalid challenge length error")
	}
}

func TestRequireUnlockedDoesNotExtendIdleTimeout(t *testing.T) {
	a := New()
	a.locked = false

	if err := a.requireUnlocked(); err != nil {
		t.Fatalf("requireUnlocked: %v", err)
	}
	if a.lockTimer != nil {
		t.Fatal("background authorization check must not create or reset the idle timer")
	}
}

func TestKeepAliveStartsIdleTimer(t *testing.T) {
	a := New()
	a.svc = &service.Service{}
	a.locked = false

	if err := a.KeepAlive(); err != nil {
		t.Fatalf("KeepAlive: %v", err)
	}
	if a.lockTimer == nil {
		t.Fatal("foreground activity must start the idle timer")
	}
	a.Lock()
}

func TestTransactionExplorerURLUsesActiveProfile(t *testing.T) {
	tests := []struct {
		name    string
		profile domain.Profile
		asset   domain.Asset
		want    string
	}{
		{"Bitcoin testnet", domain.ProfileTest, domain.BTC, "https://mempool.space/testnet/tx/abc123"},
		{"Bitcoin mainnet", domain.ProfileMain, domain.BTC, "https://mempool.space/tx/abc123"},
		{"Ethereum Sepolia", domain.ProfileTest, domain.USDC, "https://sepolia.etherscan.io/tx/abc123"},
		{"Ethereum mainnet", domain.ProfileMain, domain.ETH, "https://etherscan.io/tx/abc123"},
		{"TRON Nile", domain.ProfileTest, domain.USDT, "https://nile.tronscan.org/#/transaction/abc123"},
		{"TRON mainnet", domain.ProfileMain, domain.USDT, "https://tronscan.org/#/transaction/abc123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := transactionExplorerURL(tt.profile, domain.Tx{Asset: tt.asset, TxID: "abc123"})
			if got != tt.want {
				t.Fatalf("transactionExplorerURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTransactionExplorerURLRejectsMissingTransactionID(t *testing.T) {
	if got := transactionExplorerURL(domain.ProfileTest, domain.Tx{Asset: domain.BTC}); got != "" {
		t.Fatalf("expected empty URL for missing txid, got %q", got)
	}
}

func TestServiceAndProfileSnapshotsAreSynchronized(t *testing.T) {
	a := NewWithDependencies(Dependencies{Service: &service.Service{}, Profile: domain.ProfileTest})
	services := []*service.Service{{}, {}}
	profiles := []domain.Profile{domain.ProfileTest, domain.ProfileMain}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			a.mu.Lock()
			a.svc = services[i%len(services)]
			a.profile = profiles[i%len(profiles)]
			a.mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			if a.currentService() == nil {
				t.Error("service snapshot unexpectedly nil")
				return
			}
			_ = a.activeProfile()
		}
	}()
	wg.Wait()
}

func TestValidatePasswordMinimumLength(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "empty", password: "", wantErr: true},
		{name: "too short", password: "123456789", wantErr: true},
		{name: "minimum", password: "1234567890", wantErr: false},
		{name: "longer", password: "a-stronger-password", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePassword(tt.password)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validatePassword() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPendingWalletConfirmationIsSingleFlight(t *testing.T) {
	a := &App{
		pendingMnemonic:           secret.FromString("test mnemonic"),
		pendingWalletName:         "wallet",
		pendingChallengePositions: []int{1, 4, 8},
		pendingWalletExpires:      time.Now().Add(time.Minute),
	}
	pending, positions, err := a.beginPendingWalletConfirmation("wallet")
	if err != nil {
		t.Fatalf("first confirmation claim: %v", err)
	}
	defer pending.Wipe()
	if len(positions) != 3 || positions[0] != 1 || positions[1] != 4 || positions[2] != 8 {
		t.Fatalf("challenge positions = %v", positions)
	}
	if _, _, err := a.beginPendingWalletConfirmation("wallet"); err == nil {
		t.Fatal("concurrent confirmation unexpectedly claimed the same mnemonic")
	}
	a.finishPendingWalletConfirmation(false)
	if _, _, err := a.beginPendingWalletConfirmation("wallet"); err != nil {
		t.Fatalf("confirmation could not retry after a failed attempt: %v", err)
	}
	a.finishPendingWalletConfirmation(true)
	if !a.pendingMnemonic.IsEmpty() || a.pendingWalletInFlight {
		t.Fatal("successful confirmation did not wipe and release the pending mnemonic")
	}
	if _, _, err := a.beginPendingWalletConfirmation("wallet"); err == nil {
		t.Fatal("successful confirmation left the mnemonic reusable")
	}
}

func TestCancelCreateWalletDoesNotRaceAnInFlightConfirmation(t *testing.T) {
	a := &App{
		pendingMnemonic:           secret.FromString("test mnemonic"),
		pendingWalletName:         "wallet",
		pendingChallengePositions: []int{0, 2, 5},
		pendingWalletExpires:      time.Now().Add(time.Minute),
	}
	pending, _, err := a.beginPendingWalletConfirmation("wallet")
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Wipe()
	a.CancelCreateWallet()
	if a.pendingMnemonic.IsEmpty() {
		t.Fatal("cancel wiped the mnemonic while wallet persistence was in flight")
	}
	a.finishPendingWalletConfirmation(false)
	if !a.pendingMnemonic.IsEmpty() {
		t.Fatal("cancelled in-flight confirmation retained its mnemonic after completion")
	}
	if a.pendingWalletInFlight {
		t.Fatal("cancelled in-flight confirmation did not release its claim")
	}
}

func TestLockKeepsWalletConfirmationClaimUntilOperationFinishes(t *testing.T) {
	a := &App{
		pendingMnemonic:           secret.FromString("test mnemonic"),
		pendingWalletName:         "wallet",
		pendingChallengePositions: []int{1, 3, 7},
		pendingWalletExpires:      time.Now().Add(time.Minute),
	}
	pending, _, err := a.beginPendingWalletConfirmation("wallet")
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Wipe()
	a.Lock()
	if !a.pendingMnemonic.IsEmpty() {
		t.Fatal("Lock did not wipe the pending mnemonic")
	}
	if !a.pendingWalletInFlight {
		t.Fatal("Lock released the in-flight claim before persistence finished")
	}
	if _, _, err := a.beginPendingWalletConfirmation("wallet"); err == nil {
		t.Fatal("an expired confirmation unexpectedly claimed while prior persistence was in flight")
	}
	if !a.pendingWalletInFlight {
		t.Fatal("expired confirmation released another operation's claim")
	}
	a.finishPendingWalletConfirmation(false)
	if a.pendingWalletInFlight {
		t.Fatal("completed operation did not release its claim")
	}
	if !a.pendingMnemonic.IsEmpty() {
		t.Fatal("lock did not preserve the wipe after the in-flight operation completed")
	}
}

func TestLockWaitsForInFlightTransactionOperation(t *testing.T) {
	a := New()
	a.locked = false

	// Sensitive operations hold this read lock for their complete operation.
	a.transactionMu.RLock()
	locked := make(chan struct{})
	go func() {
		a.Lock()
		close(locked)
	}()

	select {
	case <-locked:
		a.transactionMu.RUnlock()
		t.Fatal("Lock returned while a sensitive operation was still in flight")
	case <-time.After(25 * time.Millisecond):
	}

	a.transactionMu.RUnlock()
	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("Lock did not complete after the sensitive operation finished")
	}
	if !a.locked {
		t.Fatal("Lock returned without locking the session")
	}
}

func TestLockDoesNotWaitForProfileReadLock(t *testing.T) {
	a := New()
	a.locked = false

	// A native directory picker holds profileMu read-locked. It must not
	// prevent the inactivity timer from locking the wallet session.
	a.profileMu.RLock()
	locked := make(chan struct{})
	go func() {
		a.Lock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(time.Second):
		a.profileMu.RUnlock()
		t.Fatal("Lock waited for an unrelated profile read operation")
	}
	a.profileMu.RUnlock()
	if !a.locked {
		t.Fatal("Lock returned without locking the session")
	}
}

func TestLockSessionStateCanBeUsedWhileProfileWriteLockIsHeld(t *testing.T) {
	a := New()
	a.locked = false

	a.profileMu.Lock()
	a.lockSessionState()
	a.profileMu.Unlock()

	if !a.locked {
		t.Fatal("profile transition did not lock the session")
	}
}

func TestLockForRestoreRechecksSessionAfterAcquiringTransactionLock(t *testing.T) {
	a := NewWithDependencies(Dependencies{Service: &service.Service{}, Profile: domain.ProfileTest})
	a.locked = false
	a.profileMu.Lock()
	defer a.profileMu.Unlock()

	// Model Lock completing while RestoreBackup is still in the native picker.
	a.Lock()
	unlock, err := a.lockForRestore()
	if unlock != nil {
		unlock()
		t.Fatal("lockForRestore returned an unlock function for a locked session")
	}
	if err == nil || err.Error() != "session_locked" {
		t.Fatalf("lockForRestore error = %v, want session_locked", err)
	}
}

func TestLockForBackupRechecksSessionAfterDirectoryPicker(t *testing.T) {
	a := NewWithDependencies(Dependencies{Service: &service.Service{}, Profile: domain.ProfileTest})
	a.locked = false
	a.profileMu.RLock()
	defer a.profileMu.RUnlock()

	// Model Lock completing after the user chose a backup destination.
	a.Lock()
	unlock, err := a.lockForBackup()
	if unlock != nil {
		unlock()
		t.Fatal("lockForBackup returned an unlock function for a locked session")
	}
	if err == nil || err.Error() != "session_locked" {
		t.Fatalf("lockForBackup error = %v, want session_locked", err)
	}
}

func TestShutdownWaitsForProfileOperationsAndClosesApp(t *testing.T) {
	a := NewWithDependencies(Dependencies{Service: &service.Service{}, Profile: domain.ProfileTest})
	a.profileMu.RLock()
	done := make(chan struct{})
	go func() {
		a.Shutdown(context.Background())
		close(done)
	}()
	select {
	case <-done:
		a.profileMu.RUnlock()
		t.Fatal("Shutdown returned while a profile operation was still in flight")
	case <-time.After(25 * time.Millisecond):
	}
	a.profileMu.RUnlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after the profile operation completed")
	}
	if err := a.ready(); err == nil || err.Error() != "app_shutdown" {
		t.Fatalf("ready() after shutdown = %v, want app_shutdown", err)
	}
	if err := a.SetProfile(string(domain.ProfileTest)); err == nil || err.Error() != "app_shutdown" {
		t.Fatalf("SetProfile after shutdown = %v, want app_shutdown", err)
	}
}

func TestShutdownWaitsForSensitiveOperations(t *testing.T) {
	a := NewWithDependencies(Dependencies{Service: &service.Service{}, Profile: domain.ProfileTest})
	a.transactionMu.RLock()
	done := make(chan struct{})
	go func() {
		a.Shutdown(context.Background())
		close(done)
	}()
	select {
	case <-done:
		a.transactionMu.RUnlock()
		t.Fatal("Shutdown returned while a sensitive operation was still in flight")
	case <-time.After(25 * time.Millisecond):
	}
	a.transactionMu.RUnlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after the sensitive operation completed")
	}
}
