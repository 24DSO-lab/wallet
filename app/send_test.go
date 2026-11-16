package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
	"github.com/24DSO-lab/wallet/internal/service"
	"github.com/24DSO-lab/wallet/internal/testkit"
)

func TestConfirmSendConsumesDraftAfterAmbiguousBroadcast(t *testing.T) {
	const password = "correct horse battery staple"
	const txid domain.TxID = "ambiguous-broadcast-txid"

	st := testkit.NewMemoryStore()
	ks := testkit.NewFakeKeystore(testkit.FakeKeystoreOptions{
		Password: []byte(password),
		PublicKeys: ports.PublicKeys{
			BTCXpub:     "tpub-test",
			ETHAddress:  "0x1111111111111111111111111111111111111111",
			TronAddress: "T1111111111111111111111111111111111",
		},
	})
	chain := testkit.NewFakeChain(testkit.FakeChainOptions{
		Network: domain.NetworkBitcoin,
		Signed:  ports.Signed{Network: domain.NetworkBitcoin, TxID: txid, Raw: []byte{1, 2, 3}},
		Failures: map[testkit.ChainOperation]error{
			testkit.ChainBroadcast: errors.New("network unavailable"),
		},
	})
	svc, err := service.New(st, ks, []ports.ChainAdapter{chain},
		service.WithIDGenerator(func() (string, error) { return "wallet-1", nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	wallet, mnemonic, err := svc.CreateWallet(context.Background(), "Test wallet", 12, ports.Secrets{
		Password: secret.FromString(password),
	})
	if err != nil {
		t.Fatal(err)
	}
	mnemonic.Wipe()

	intent := domain.SendIntent{
		Wallet: wallet.PublicWallet,
		Asset:  domain.BTC,
		To:     "bc1qrecipient",
		Amount: domain.AmountFromUint64(100),
	}
	prepared := ports.Prepared{
		Network:  domain.NetworkBitcoin,
		Asset:    domain.BTC,
		Draft:    []byte{9},
		Fee:      domain.AmountFromUint64(1),
		FeeAsset: domain.BTC,
	}
	a := NewWithDependencies(Dependencies{Service: svc, Profile: domain.ProfileTest})
	a.locked = false
	a.drafts["draft-1"] = pendingDraft{
		walletID: wallet.ID,
		intent:   intent,
		prepared: prepared,
		expires:  time.Now().Add(time.Minute),
	}

	if _, err := a.ConfirmSend("draft-1", password, ""); err == nil || err.Error() != string(domain.CodeBroadcastUnknown) {
		t.Fatalf("ConfirmSend error = %v, want broadcast_unknown", err)
	}
	if _, ok := a.drafts["draft-1"]; ok {
		t.Fatal("draft remained available after signed transaction was persisted")
	}
	if _, err := st.PendingTxs().Get(context.Background(), txid); err != nil {
		t.Fatalf("durable pending transaction was lost: %v", err)
	}
	if _, err := a.ConfirmSend("draft-1", password, ""); err == nil || err.Error() != "draft_expired_or_in_use" {
		t.Fatalf("second ConfirmSend error = %v, want draft_expired_or_in_use", err)
	}
}
