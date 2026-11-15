package testkit

import (
	"context"
	"errors"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
)

func TestFakeKeystoreSaveRevealAndPassword(t *testing.T) {
	ctx := context.Background()
	ks := NewFakeKeystore(FakeKeystoreOptions{Password: []byte("correct")})
	mnemonic := secret.Copy([]byte("test seed"))
	secrets := ports.Secrets{Password: secret.Copy([]byte("correct"))}
	defer mnemonic.Wipe()
	defer secrets.Password.Wipe()

	if _, err := ks.Save(ctx, "wallet-1", mnemonic, secrets); err != nil {
		t.Fatal(err)
	}
	revealed, err := ks.RevealSeed(ctx, "wallet-1", secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer revealed.Wipe()
	if !revealed.Equal(mnemonic) {
		t.Fatal("revealed seed differs from saved seed")
	}

	wrong := ports.Secrets{Password: secret.Copy([]byte("wrong"))}
	defer wrong.Password.Wipe()
	if err := ks.Verify(ctx, wrong); domain.CodeOf(err) != domain.CodeWrongPassword {
		t.Fatalf("Verify error code = %q, want %q", domain.CodeOf(err), domain.CodeWrongPassword)
	}
}

func TestFakeKeystoreRefusesAddressMismatch(t *testing.T) {
	ctx := context.Background()
	ks := NewFakeKeystore(FakeKeystoreOptions{
		Password: []byte("pw"),
		AddressFor: func(domain.Network, []byte) (string, error) { return "derived-address", nil },
	})
	password := secret.Copy([]byte("pw"))
	defer password.Wipe()
	mnemonic := secret.Copy([]byte("seed"))
	defer mnemonic.Wipe()
	if _, err := ks.Save(ctx, "w", mnemonic, ports.Secrets{Password: password}); err != nil {
		t.Fatal(err)
	}
	_, err := ks.Sign(ctx, "w", ports.Secrets{Password: password}, []ports.DigestRequest{{
		Network: domain.NetworkEthereum, Path: "m/44'/60'/0'/0/0", ExpectedAddress: "other-address",
	}})
	if domain.CodeOf(err) != domain.CodeSigningRefused {
		t.Fatalf("Sign error code = %q, want %q", domain.CodeOf(err), domain.CodeSigningRefused)
	}
}

func TestFakeChainCannedResponsesAndFailure(t *testing.T) {
	failure := errors.New("provider unavailable")
	chain := NewFakeChain(FakeChainOptions{
		Network: domain.NetworkEthereum,
		Prepared: ports.Prepared{Network: domain.NetworkEthereum, Asset: domain.ETH, Draft: []byte{1, 2}},
		Failures: map[ChainOperation]error{ChainRefresh: failure},
	})
	if _, err := chain.Refresh(context.Background(), domain.PublicWallet{}, domain.ETH); !errors.Is(err, failure) {
		t.Fatalf("Refresh error = %v, want injected failure", err)
	}
	prepared, err := chain.Prepare(context.Background(), domain.SendIntent{Asset: domain.ETH})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Network != domain.NetworkEthereum || prepared.Asset != domain.ETH {
		t.Fatalf("unexpected prepared result: %+v", prepared)
	}
	prepared.Draft[0] = 9
	again, err := chain.Prepare(context.Background(), domain.SendIntent{Asset: domain.ETH})
	if err != nil {
		t.Fatal(err)
	}
	if again.Draft[0] != 1 {
		t.Fatal("Prepare returned aliased draft bytes")
	}
}
