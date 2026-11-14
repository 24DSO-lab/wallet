package domain_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
)

func TestAssetRegistry(t *testing.T) {
	// Таблица ADR, раздел «Модель данных». Менять только вместе с ADR.
	want := map[domain.Asset]domain.AssetInfo{
		domain.BTC:  {Asset: domain.BTC, Network: domain.NetworkBitcoin, Kind: domain.KindNative, Decimals: 8, FeeAsset: domain.BTC},
		domain.ETH:  {Asset: domain.ETH, Network: domain.NetworkEthereum, Kind: domain.KindNative, Decimals: 18, FeeAsset: domain.ETH},
		domain.USDC: {Asset: domain.USDC, Network: domain.NetworkEthereum, Kind: domain.KindToken, Decimals: 6, FeeAsset: domain.ETH},
		domain.USDT: {Asset: domain.USDT, Network: domain.NetworkTron, Kind: domain.KindToken, Decimals: 6, FeeAsset: domain.TRX},
		domain.TRX:  {Asset: domain.TRX, Network: domain.NetworkTron, Kind: domain.KindNative, Decimals: 6, FeeAsset: domain.TRX},
	}
	for asset, info := range want {
		got, ok := asset.Info()
		if !ok || got != info {
			t.Errorf("%s.Info() = %+v, %v; want %+v", asset, got, ok, info)
		}
		if !got.Network.Valid() {
			t.Errorf("%s: network %q is not valid", asset, got.Network)
		}
	}
	for _, bad := range []domain.Asset{"", "btc", "DOGE", "USDC "} {
		if _, ok := bad.Info(); ok {
			t.Errorf("%q must not be in the registry", bad)
		}
	}
}

func TestWalletAssets(t *testing.T) {
	got := domain.WalletAssets()
	want := []domain.Asset{domain.BTC, domain.ETH, domain.USDC, domain.USDT}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("WalletAssets() = %v, want %v", got, want)
	}
	for _, a := range got {
		if !a.IsWalletAsset() {
			t.Errorf("%s must be a wallet asset", a)
		}
		// Комиссионная монета каждого актива кошелька известна справочнику.
		info, _ := a.Info()
		if _, ok := info.FeeAsset.Info(); !ok {
			t.Errorf("%s: fee asset %q is not in the registry", a, info.FeeAsset)
		}
	}
	if domain.TRX.IsWalletAsset() {
		t.Error("TRX must not be a wallet asset")
	}

	got[0] = "MUTATED" // результат не делит память со справочником
	if domain.WalletAssets()[0] != domain.BTC {
		t.Error("WalletAssets must return a fresh slice")
	}
}

func TestParseAsset(t *testing.T) {
	for _, s := range []string{"BTC", "ETH", "USDC", "USDT"} {
		if a, err := domain.ParseAsset(s); err != nil || string(a) != s {
			t.Errorf("ParseAsset(%q) = %q, %v", s, a, err)
		}
	}
	for _, s := range []string{"", "btc", "TRX", "DOGE", " BTC", "BTC "} {
		if _, err := domain.ParseAsset(s); !errors.Is(err, domain.ErrUnknownAsset) {
			t.Errorf("ParseAsset(%q) error = %v, want ErrUnknownAsset", s, err)
		}
	}
}

func TestProfileAndNetworkValid(t *testing.T) {
	if !domain.ProfileMain.Valid() || !domain.ProfileTest.Valid() {
		t.Error("main and test profiles must be valid")
	}
	for _, p := range []domain.Profile{"", "Main", "dev"} {
		if p.Valid() {
			t.Errorf("profile %q must be invalid", p)
		}
	}
	for _, n := range []domain.Network{"", "Bitcoin", "solana"} {
		if n.Valid() {
			t.Errorf("network %q must be invalid", n)
		}
	}
}

func TestTxStatusValid(t *testing.T) {
	for _, s := range []domain.TxStatus{domain.TxPending, domain.TxConfirmed, domain.TxFailed} {
		if !s.Valid() {
			t.Errorf("%q must be valid", s)
		}
	}
	if domain.TxStatus("").Valid() || domain.TxStatus("done").Valid() {
		t.Error("unknown status must be invalid")
	}
}

func TestSendIntentValidate(t *testing.T) {
	w := domain.PublicWallet{ID: "w1", Name: "main"}
	one := domain.AmountFromUint64(1)

	tests := []struct {
		name string
		in   domain.SendIntent
		want domain.ErrorCode
	}{
		{"ok", domain.SendIntent{Wallet: w, Asset: domain.BTC, To: "addr", Amount: one}, ""},
		{"unknown asset", domain.SendIntent{Wallet: w, Asset: "DOGE", To: "addr", Amount: one}, domain.CodeInvalidInput},
		{"fee-only asset", domain.SendIntent{Wallet: w, Asset: domain.TRX, To: "addr", Amount: one}, domain.CodeInvalidInput},
		{"no wallet", domain.SendIntent{Asset: domain.BTC, To: "addr", Amount: one}, domain.CodeInvalidInput},
		{"no recipient", domain.SendIntent{Wallet: w, Asset: domain.BTC, Amount: one}, domain.CodeInvalidAddress},
		{"zero amount", domain.SendIntent{Wallet: w, Asset: domain.BTC, To: "addr"}, domain.CodeInvalidAmount},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if got := domain.CodeOf(err); got != tc.want {
				t.Fatalf("code = %q (%v), want %q", got, err, tc.want)
			}
		})
	}
}

func TestAppError(t *testing.T) {
	cause := errors.New("boom")
	err := domain.NewError(domain.CodeProviderUnavailable, cause)

	if !errors.Is(err, cause) {
		t.Error("errors.Is must reach the cause")
	}
	if !errors.Is(err, &domain.AppError{Code: domain.CodeProviderUnavailable}) {
		t.Error("errors.Is must match by code")
	}
	if errors.Is(err, &domain.AppError{Code: domain.CodeLocked}) {
		t.Error("errors.Is must not match a different code")
	}
	var ae *domain.AppError
	if !errors.As(err, &ae) || ae.Code != domain.CodeProviderUnavailable || !errors.Is(ae.Cause, cause) {
		t.Errorf("errors.As gave %+v", ae)
	}
	if got := err.Error(); got != "provider_unavailable: boom" {
		t.Errorf("Error() = %q", got)
	}
	if got := domain.NewError(domain.CodeLocked, nil).Error(); got != "locked" {
		t.Errorf("Error() without cause = %q", got)
	}
}

func TestCodeOf(t *testing.T) {
	inner := domain.NewError(domain.CodeInvalidAddress, nil)
	wrapped := fmt.Errorf("prepare send: %w", inner)

	tests := []struct {
		name string
		err  error
		want domain.ErrorCode
	}{
		{"nil", nil, ""},
		{"plain error", errors.New("x"), domain.CodeInternal},
		{"direct", inner, domain.CodeInvalidAddress},
		{"wrapped", wrapped, domain.CodeInvalidAddress},
		{"double wrapped", fmt.Errorf("outer: %w", wrapped), domain.CodeInvalidAddress},
		{"nearest wins", domain.NewError(domain.CodeVerificationFailed, inner), domain.CodeVerificationFailed},
	}
	for _, tc := range tests {
		if got := domain.CodeOf(tc.err); got != tc.want {
			t.Errorf("%s: CodeOf = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestErrNotFoundWrapping(t *testing.T) {
	err := fmt.Errorf("wallet %q: %w", "w1", domain.ErrNotFound)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Error("wrapped ErrNotFound must be detectable")
	}
}
