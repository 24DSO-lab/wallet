package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
	"github.com/24DSO-lab/wallet/internal/service"
	"github.com/24DSO-lab/wallet/internal/testkit"
)

func TestVerifyWalletAddressesMatchesUntamperedData(t *testing.T) {
	svc, _ := testService(t)
	wallet := createTestWallet(t, svc)
	sec := testSecrets()

	got, err := svc.VerifyWalletAddresses(context.Background(), wallet.ID, sec)
	if err != nil {
		t.Fatal(err)
	}
	if want := (service.AddressVerification{Bitcoin: true, Ethereum: true, Tron: true}); got != want {
		t.Fatalf("verification = %+v, want %+v", got, want)
	}
	if !sec.Password.IsEmpty() || !sec.Passphrase.IsEmpty() {
		t.Fatal("input secrets were not wiped")
	}
}

func TestVerifyWalletAddressesDetectsForeignAddressRow(t *testing.T) {
	svc, st := testService(t)
	wallet := createTestWallet(t, svc)
	// A tampered database adds an attacker's unused ETH address that the
	// receive screen would show.
	err := st.Addresses().Upsert(context.Background(), []domain.Address{{
		WalletID: wallet.ID, Network: domain.NetworkEthereum, Chain: domain.ChainReceive, Index: 1,
		Address: "0x2222222222222222222222222222222222222222", Path: "m/44'/60'/0'/0/1",
	}})
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.VerifyWalletAddresses(context.Background(), wallet.ID, testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	if got.Ethereum || !got.Tron || !got.Bitcoin {
		t.Fatalf("verification = %+v, want only Ethereum to fail", got)
	}
}

func TestVerifyWalletAddressesDetectsChangedPublicData(t *testing.T) {
	svc, st := testService(t)
	wallet := createTestWallet(t, svc)

	// Same database, but the seed now derives different public data: what a
	// swapped xpub/address in a tampered backup looks like to the keystore.
	other := testkit.NewFakeKeystore(testkit.FakeKeystoreOptions{
		Password: []byte("correct horse battery staple"),
		PublicKeys: ports.PublicKeys{
			BTCXpub: "tpub-other", ETHAddress: "0x3333333333333333333333333333333333333333",
			TronAddress: "T3333333333333333333333333333333333",
		},
	})
	if _, err := other.Save(context.Background(), wallet.KeystoreID, secret.FromString("seed"), testSecrets()); err != nil {
		t.Fatal(err)
	}
	checker, err := service.New(st, other, nil, service.WithClock(func() time.Time { return time.Now() }))
	if err != nil {
		t.Fatal(err)
	}

	got, err := checker.VerifyWalletAddresses(context.Background(), wallet.ID, testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	if got.Bitcoin || got.Ethereum || got.Tron {
		t.Fatalf("verification = %+v, want every network to fail", got)
	}
}

func TestVerifyWalletAddressesRejectsWrongPassword(t *testing.T) {
	svc, _ := testService(t)
	wallet := createTestWallet(t, svc)

	_, err := svc.VerifyWalletAddresses(context.Background(), wallet.ID, ports.Secrets{Password: secret.FromString("incorrect password")})
	if domain.CodeOf(err) != domain.CodeWrongPassword {
		t.Fatalf("error code = %s (%v), want %s", domain.CodeOf(err), err, domain.CodeWrongPassword)
	}
}

func TestVerifyWalletAddressesUnknownWallet(t *testing.T) {
	svc, _ := testService(t)
	if _, err := svc.VerifyWalletAddresses(context.Background(), "missing", testSecrets()); err == nil {
		t.Fatal("expected an error for an unknown wallet")
	}
}
