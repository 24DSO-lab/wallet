package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/service"
)

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
