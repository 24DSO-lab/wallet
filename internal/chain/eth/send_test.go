package eth

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
	"github.com/24DSO-lab/wallet/internal/ports"
)

func sigOf(t *testing.T, v txVector) ports.Signature {
	t.Helper()
	var s ports.Signature
	copy(s.Sig[:32], decHex(t, v.r))
	copy(s.Sig[32:64], decHex(t, v.s))
	s.Sig[64] = v.v
	pt, _ := secp256k1.ScalarBaseMult(scalarOf(1))
	c := pt.Compressed()
	s.PubKey = c[:]
	return s
}

func TestFinalizeMatchesIndependentVectors(t *testing.T) {
	for i, prep := range []func(*testing.T) (*Adapter, *fakeRPC, ports.Prepared){prepareETH, prepareUSDC} {
		a, _, p := prep(t)
		v := txVectors[i]
		signed, err := a.Finalize(p, []ports.Signature{sigOf(t, v)})
		if err != nil {
			t.Fatalf("%s: %v", v.name, err)
		}
		if hex.EncodeToString(signed.Raw) != v.signed || string(signed.TxID) != v.txid || signed.Network != domain.NetworkEthereum {
			t.Errorf("%s: raw %x id %s", v.name, signed.Raw, signed.TxID)
		}
	}
}

func TestFinalizeRejects(t *testing.T) {
	a, _, p := prepareETH(t)
	good := sigOf(t, txVectors[0])

	mut := func(f func(s *ports.Signature)) []ports.Signature {
		s := good
		s.PubKey = bytes.Clone(good.PubKey)
		f(&s)
		return []ports.Signature{s}
	}
	pt2, _ := secp256k1.ScalarBaseMult(scalarOf(2))
	c2 := pt2.Compressed()

	// Подпись настоящего ключа 2: валидна сама по себе, но не от кошелька.
	sg2, err := secp256k1.Sign(scalarOf(2), p.Digests[0].Digest[:])
	if err != nil {
		t.Fatal(err)
	}
	var foreign ports.Signature
	copy(foreign.Sig[:32], sg2.R[:])
	copy(foreign.Sig[32:64], sg2.S[:])
	foreign.Sig[64] = sg2.RecID
	foreign.PubKey = c2[:]

	s := new(big.Int).SetBytes(good.Sig[32:64])
	high := new(big.Int).Sub(curveOrder, s)

	tests := map[string][]ports.Signature{
		"no signatures":      nil,
		"two signatures":     {good, good},
		"flipped v":          mut(func(s *ports.Signature) { s.Sig[64] ^= 1 }),
		"corrupted r":        mut(func(s *ports.Signature) { s.Sig[3] ^= 1 }),
		"corrupted s":        mut(func(s *ports.Signature) { s.Sig[40] ^= 1 }),
		"v above 1":          mut(func(s *ports.Signature) { s.Sig[64] = 27 }),
		"high s":             mut(func(s *ports.Signature) { high.FillBytes(s.Sig[32:64]); s.Sig[64] ^= 1 }),
		"claimed other key":  mut(func(s *ports.Signature) { s.PubKey = c2[:] }),
		"empty public key":   mut(func(s *ports.Signature) { s.PubKey = nil }),
		"valid foreign sign": {foreign},
	}
	for name, sigs := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := a.Finalize(p, sigs); domain.CodeOf(err) != domain.CodeVerificationFailed {
				t.Fatalf("err = %v, want verification_failed", err)
			}
		})
	}

	t.Run("digest of another draft", func(t *testing.T) {
		bad := p
		bad.Digests = []ports.DigestRequest{p.Digests[0]}
		bad.Digests[0].Digest[0] ^= 1
		if _, err := a.Finalize(bad, []ports.Signature{good}); domain.CodeOf(err) != domain.CodeVerificationFailed {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("draft swapped after signing", func(t *testing.T) {
		// Подпись валидна для исходного дайджеста, но байты черновика уже
		// другие: без сверки дайджеста с байтами получилась бы транзакция с
		// подписью, которая восстанавливается в чужой адрес.
		swapped := retarget(t, p, func(tx *dynTx) { tx.To = mustAddr(t, "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb") })
		swapped.Digests = p.Digests
		if _, err := a.Finalize(swapped, []ports.Signature{good}); domain.CodeOf(err) != domain.CodeVerificationFailed {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("draft for another chain", func(t *testing.T) {
		bad := retarget(t, p, func(tx *dynTx) { tx.ChainID = ChainIDMainnet })
		if _, err := a.Finalize(bad, []ports.Signature{good}); domain.CodeOf(err) != domain.CodeVerificationFailed {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("expected address of another wallet", func(t *testing.T) {
		bad := p
		bad.Digests = []ports.DigestRequest{p.Digests[0]}
		bad.Digests[0].ExpectedAddress = "0x2B5AD5c4795c026514f8317c7a215E218DcCD6cF"
		if _, err := a.Finalize(bad, []ports.Signature{good}); domain.CodeOf(err) != domain.CodeVerificationFailed {
			t.Errorf("err = %v", err)
		}
	})
}

// Весь путь отправки со своей подписью (так подписывает Keystore): Prepare,
// Verify, Sign, Finalize, Broadcast. Отправленные байты разбираются заново, а
// восстановленный из них отправитель обязан быть кошельком.
func TestFullSendFlow(t *testing.T) {
	for _, usdc := range []bool{false, true} {
		var (
			a   *Adapter
			rpc *fakeRPC
			p   ports.Prepared
			in  domain.SendIntent
		)
		if usdc {
			a, rpc, p = prepareUSDC(t)
			in = usdcIntent(t)
		} else {
			a, rpc, p = prepareETH(t)
			in = ethIntent(t)
		}
		if err := a.Verify(p, in); err != nil {
			t.Fatal(err)
		}
		sg, err := secp256k1.Sign(scalarOf(1), p.Digests[0].Digest[:])
		if err != nil {
			t.Fatal(err)
		}
		var sig ports.Signature
		copy(sig.Sig[:32], sg.R[:])
		copy(sig.Sig[32:64], sg.S[:])
		sig.Sig[64] = sg.RecID
		pt, _ := secp256k1.ScalarBaseMult(scalarOf(1))
		c := pt.Compressed()
		sig.PubKey = c[:]

		signed, err := a.Finalize(p, []ports.Signature{sig})
		if err != nil {
			t.Fatal(err)
		}
		rpc.sendHash = string(signed.TxID)
		id, err := a.Broadcast(context.Background(), signed)
		if err != nil || id != signed.TxID {
			t.Fatalf("broadcast: %q %v", id, err)
		}
		if len(rpc.sentRaw) != 1 || !bytes.Equal(rpc.sentRaw[0], signed.Raw) {
			t.Fatal("the node did not receive the signed bytes")
		}
	}
}

func TestBroadcast(t *testing.T) {
	a, rpc, p := prepareETH(t)
	signed, err := a.Finalize(p, []ports.Signature{sigOf(t, txVectors[0])})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	t.Run("provider names another hash", func(t *testing.T) {
		rpc.sentRaw, rpc.sendHash, rpc.sendErr = nil, "0x"+strings.Repeat("ab", 32), nil
		id, err := a.Broadcast(ctx, signed)
		if err != nil || id != signed.TxID {
			t.Errorf("id %q, err %v: the local hash is the truth", id, err)
		}
	})
	t.Run("already known is success", func(t *testing.T) {
		rpc.sendErr = ErrAlreadyKnown
		if id, err := a.Broadcast(ctx, signed); err != nil || id != signed.TxID {
			t.Errorf("id %q, err %v", id, err)
		}
	})
	t.Run("rejection", func(t *testing.T) {
		rpc.sendErr = domain.NewError(domain.CodeBroadcastRejected, errors.New("nonce too low"))
		if _, err := a.Broadcast(ctx, signed); domain.CodeOf(err) != domain.CodeBroadcastRejected {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("malformed input is never sent", func(t *testing.T) {
		rpc.sentRaw, rpc.sendErr = nil, nil
		bad := map[string]ports.Signed{
			"id mismatch":   {Network: signed.Network, Raw: signed.Raw, TxID: "0x" + domain.TxID(strings.Repeat("00", 32))},
			"empty raw":     {Network: signed.Network, TxID: signed.TxID},
			"legacy prefix": {Network: signed.Network, Raw: append([]byte{0x01}, signed.Raw[1:]...), TxID: signed.TxID},
			"other network": {Network: domain.NetworkTron, Raw: signed.Raw, TxID: signed.TxID},
		}
		for name, s := range bad {
			if _, err := a.Broadcast(ctx, s); err == nil {
				t.Errorf("%s: accepted", name)
			}
		}
		if len(rpc.sentRaw) != 0 {
			t.Errorf("%d malformed transactions reached the node", len(rpc.sentRaw))
		}
	})
	t.Run("wrong chain blocks sending", func(t *testing.T) {
		a2, rpc2 := sendRig(t)
		rpc2.chainID = ChainIDMainnet
		if _, err := a2.Broadcast(ctx, signed); domain.CodeOf(err) != domain.CodeWrongNetwork {
			t.Errorf("err = %v", err)
		}
		if len(rpc2.sentRaw) != 0 {
			t.Error("transaction was sent to the wrong chain")
		}
	})
}

func TestStatus(t *testing.T) {
	id := domain.TxID(txVectors[0].txid)
	boom := domain.NewError(domain.CodeProviderUnavailable, errors.New("down"))
	tests := []struct {
		name  string
		setup func(rpc *fakeRPC)
		want  ports.TxStatus
		code  domain.ErrorCode
	}{
		{"pending", func(r *fakeRPC) { r.known = true }, ports.TxStatus{State: domain.TxPending, Known: true}, ""},
		{"unknown to the node", func(r *fakeRPC) {}, ports.TxStatus{}, domain.CodeNotFound},
		{"confirmed", func(r *fakeRPC) { r.receipt = &Receipt{Status: 1, BlockNumber: 990} },
			ports.TxStatus{State: domain.TxConfirmed, Known: true, BlockHeight: 990, Confirmations: 11}, ""},
		{"in the head block", func(r *fakeRPC) { r.receipt = &Receipt{Status: 1, BlockNumber: 1000} },
			ports.TxStatus{State: domain.TxConfirmed, BlockHeight: 1000, Confirmations: 1}, ""},
		{"reverted", func(r *fakeRPC) { r.receipt = &Receipt{Status: 0, BlockNumber: 995} },
			ports.TxStatus{State: domain.TxFailed, Known: true, BlockHeight: 995, Confirmations: 6}, ""},
		{"node lags behind the receipt", func(r *fakeRPC) { r.receipt = &Receipt{Status: 1, BlockNumber: 1005} },
			ports.TxStatus{State: domain.TxConfirmed, BlockHeight: 1005, Confirmations: 1}, ""},
		{"head unavailable", func(r *fakeRPC) { r.receipt, r.headErr = &Receipt{Status: 1, BlockNumber: 990}, boom },
			ports.TxStatus{State: domain.TxConfirmed, BlockHeight: 990, Confirmations: 1}, ""},
		{"receipt down", func(r *fakeRPC) { r.receiptEr = boom }, ports.TxStatus{}, domain.CodeProviderUnavailable},
		{"lookup down", func(r *fakeRPC) { r.knownErr = boom }, ports.TxStatus{}, domain.CodeProviderUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, rpc := sendRig(t)
			tt.setup(rpc)
			got, err := a.Status(context.Background(), id)
			if domain.CodeOf(err) != tt.code {
				t.Fatalf("code = %q, want %q (%v)", domain.CodeOf(err), tt.code, err)
			}
			if tt.code == "" && got != tt.want {
				t.Errorf("status = %+v, want %+v", got, tt.want)
			}
		})
	}
	t.Run("invalid ids", func(t *testing.T) {
		a, _ := sendRig(t)
		for _, bad := range []string{"", "0x", "0x12", string(id)[2:], "0x" + strings.Repeat("zz", 32)} {
			if _, err := a.Status(context.Background(), domain.TxID(bad)); domain.CodeOf(err) != domain.CodeInvalidInput {
				t.Errorf("%q: err = %v", bad, err)
			}
		}
	})
}
