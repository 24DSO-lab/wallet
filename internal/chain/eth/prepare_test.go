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
	"github.com/24DSO-lab/wallet/internal/ports"
)

// Отправитель во всех тестах: общеизвестный адрес приватного ключа 1. Векторы
// txVectors подписаны этим ключом независимой реализацией, поэтому сборка
// черновика проверяется побайтно: с базовой комиссией 15 gwei и чаевыми 1 gwei
// maxFeePerGas получается 31 gwei, как в векторах.
const ownAddr = "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf"

func amt(t testing.TB, n int64) domain.Amount {
	t.Helper()
	a, err := domain.NewAmount(big.NewInt(n))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func gweiN(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), gwei) }

func steadyFees(next, tip int64) FeeHistory {
	fh := FeeHistory{OldestBlock: 100}
	for range feeHistoryBlocks {
		fh.BaseFee = append(fh.BaseFee, gweiN(next-1))
		fh.Reward = append(fh.Reward, []*big.Int{gweiN(tip)})
	}
	fh.BaseFee = append(fh.BaseFee, gweiN(next))
	return fh
}

// sendRig это адаптер профиля Sepolia на фейках, настроенный как в векторах.
func sendRig(t *testing.T) (*Adapter, *fakeRPC) {
	t.Helper()
	rpc := &fakeRPC{
		chainID: ChainIDSepolia,
		balance: new(big.Int).Mul(big.NewInt(5), big.NewInt(1e18)), // 5 ETH
		callOut: word(10_000_000),                                  // 10 USDC
		nonce:   7,
		gas:     21000,
		fees:    steadyFees(15, 1),
		head:    1000,
	}
	return newAdapter(t, rpc, &fakeIndexer{}), rpc
}

func ownWallet() domain.PublicWallet { return domain.PublicWallet{ID: "w1", ETHAddress: ownAddr} }

func ethIntent(t *testing.T) domain.SendIntent {
	return domain.SendIntent{Wallet: ownWallet(), Asset: domain.ETH, To: addrB, Amount: amt(t, 1e17)}
}

func usdcIntent(t *testing.T) domain.SendIntent {
	return domain.SendIntent{Wallet: ownWallet(), Asset: domain.USDC, To: addrA, Amount: amt(t, 1_500_000)}
}

func prepareETH(t *testing.T) (*Adapter, *fakeRPC, ports.Prepared) {
	t.Helper()
	a, rpc := sendRig(t)
	p, err := a.Prepare(context.Background(), ethIntent(t))
	if err != nil {
		t.Fatal(err)
	}
	return a, rpc, p
}

func prepareUSDC(t *testing.T) (*Adapter, *fakeRPC, ports.Prepared) {
	t.Helper()
	a, rpc := sendRig(t)
	rpc.nonce, rpc.gas = 8, 50_000
	p, err := a.Prepare(context.Background(), usdcIntent(t))
	if err != nil {
		t.Fatal(err)
	}
	return a, rpc, p
}

func TestPrepareETHMatchesIndependentVector(t *testing.T) {
	_, rpc, p := prepareETH(t)
	v := txVectors[0]
	if hex.EncodeToString(p.Draft) != v.unsigned {
		t.Fatalf("draft\n got  %x\n want %s", p.Draft, v.unsigned)
	}
	if len(p.Digests) != 1 || hex.EncodeToString(p.Digests[0].Digest[:]) != v.digest {
		t.Fatalf("digests = %+v", p.Digests)
	}
	d := p.Digests[0]
	if d.Network != domain.NetworkEthereum || d.Path != "m/44'/60'/0'/0/0" || d.ExpectedAddress != ownAddr {
		t.Errorf("digest request = %+v", d)
	}
	wantFee := "651000000000000" // 21000 * 31 gwei
	if p.Fee.String() != wantFee || p.FeeAsset != domain.ETH || p.Summary.Fee.String() != wantFee {
		t.Errorf("fee = %s %s", p.Fee, p.FeeAsset)
	}
	if p.Summary.To != addrB || p.Summary.Amount.String() != "100000000000000000" || p.Summary.Total.String() != "100651000000000000" {
		t.Errorf("summary = %+v", p.Summary)
	}
	if len(p.Warnings) != 0 {
		t.Errorf("unexpected warnings %v", p.Warnings)
	}
	if m := rpc.gasMsg; m == nil || m.From != mustAddr(t, ownAddr) || m.To != mustAddr(t, addrB) || m.Value.Int64() != 1e17 || len(m.Data) != 0 {
		t.Errorf("estimateGas message = %+v", m)
	}
}

func TestPrepareUSDCMatchesIndependentVector(t *testing.T) {
	_, rpc, p := prepareUSDC(t)
	v := txVectors[1]
	if hex.EncodeToString(p.Draft) != v.unsigned {
		t.Fatalf("draft\n got  %x\n want %s", p.Draft, v.unsigned)
	}
	if hex.EncodeToString(p.Digests[0].Digest[:]) != v.digest {
		t.Errorf("digest = %x", p.Digests[0].Digest)
	}
	if p.Fee.String() != "1860000000000000" { // 60000 * 31 gwei: оценка 50000 плюс запас 20%
		t.Errorf("fee = %s", p.Fee)
	}
	if !p.Summary.Total.IsZero() || p.Summary.To != addrA || p.Summary.Amount.String() != "1500000" {
		t.Errorf("summary = %+v", p.Summary)
	}
	if len(p.Warnings) != 0 {
		t.Errorf("unexpected warnings %v", p.Warnings)
	}
	if m := rpc.gasMsg; m == nil || m.To != mustAddr(t, USDCSepoliaContract) || m.Value.Sign() != 0 || len(m.Data) != transferCallLen {
		t.Errorf("estimateGas message = %+v", m)
	}
}

func TestPrepareWarnings(t *testing.T) {
	codes := func(p ports.Prepared) string {
		var s []string
		for _, w := range p.Warnings {
			s = append(s, string(w.Code))
		}
		return strings.Join(s, ",")
	}
	t.Run("address without checksum", func(t *testing.T) {
		a, _ := sendRig(t)
		in := ethIntent(t)
		in.To = strings.ToLower(addrB)
		p, err := a.Prepare(context.Background(), in)
		if err != nil || codes(p) != "no_checksum_address" {
			t.Fatalf("warnings %q, err %v", codes(p), err)
		}
		if p.Summary.To != addrB {
			t.Errorf("summary must show the canonical address, got %s", p.Summary.To)
		}
	})
	t.Run("own address", func(t *testing.T) {
		a, _ := sendRig(t)
		in := ethIntent(t)
		in.To = ownAddr
		p, err := a.Prepare(context.Background(), in)
		if err != nil || codes(p) != "self_address" {
			t.Fatalf("warnings %q, err %v", codes(p), err)
		}
	})
	t.Run("not enough ETH for USDC gas", func(t *testing.T) {
		a, rpc := sendRig(t)
		rpc.balance = big.NewInt(1000)
		p, err := a.Prepare(context.Background(), usdcIntent(t))
		if err != nil || codes(p) != "insufficient_fee_coin" {
			t.Fatalf("warnings %q, err %v", codes(p), err)
		}
	})
	t.Run("high base fee", func(t *testing.T) {
		a, rpc := sendRig(t)
		rpc.fees = steadyFees(120, 1)
		p, err := a.Prepare(context.Background(), ethIntent(t))
		if err != nil || codes(p) != "high_fee" {
			t.Fatalf("warnings %q, err %v", codes(p), err)
		}
	})
}

func TestPrepareFeePolicy(t *testing.T) {
	tests := []struct {
		name            string
		fees            FeeHistory
		gas             uint64
		asset           domain.Asset
		wantGas         uint64
		wantTip, wantMx int64 // gwei
	}{
		{"tip floor", steadyFees(10, 0), 21000, domain.ETH, 21000, 1, 21},
		{"tip from median", steadyFees(10, 3), 21000, domain.ETH, 21000, 3, 23},
		{"ETH to a contract gets padding", steadyFees(10, 1), 30000, domain.ETH, 36000, 1, 21},
		{"USDC padding", steadyFees(10, 1), 100000, domain.USDC, 120000, 1, 21},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, rpc := sendRig(t)
			rpc.fees, rpc.gas = tt.fees, tt.gas
			in := ethIntent(t)
			if tt.asset == domain.USDC {
				in = usdcIntent(t)
			}
			p, err := a.Prepare(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := parseUnsigned(p.Draft)
			if err != nil {
				t.Fatal(err)
			}
			if tx.Gas != tt.wantGas || tx.MaxPriorityFee.Cmp(gweiN(tt.wantTip)) != 0 || tx.MaxFee.Cmp(gweiN(tt.wantMx)) != 0 {
				t.Errorf("gas %d tip %s maxFee %s", tx.Gas, tx.MaxPriorityFee, tx.MaxFee)
			}
		})
	}
}

func TestPrepareRejects(t *testing.T) {
	boom := domain.NewError(domain.CodeProviderUnavailable, errors.New("down"))
	tests := []struct {
		name   string
		mutate func(rpc *fakeRPC, in *domain.SendIntent)
		code   domain.ErrorCode
	}{
		{"bad recipient", func(_ *fakeRPC, in *domain.SendIntent) { in.To = "0x123" }, domain.CodeInvalidAddress},
		{"bad checksum recipient", func(_ *fakeRPC, in *domain.SendIntent) { in.To = strings.Replace(addrB, "fB69", "FB69", 1) }, domain.CodeInvalidAddress},
		{"tron recipient", func(_ *fakeRPC, in *domain.SendIntent) { in.To = "TQrY8tryqsYVCYS3MFbtffiPp2ccyn4STm" }, domain.CodeInvalidAddress},
		{"zero amount", func(_ *fakeRPC, in *domain.SendIntent) { in.Amount = domain.Amount{} }, domain.CodeInvalidAmount},
		{"bitcoin asset", func(_ *fakeRPC, in *domain.SendIntent) { in.Asset = domain.BTC }, domain.CodeInvalidInput},
		{"wallet without address", func(_ *fakeRPC, in *domain.SendIntent) { in.Wallet.ETHAddress = "" }, domain.CodeInvalidInput},
		{"ETH amount exceeds balance", func(rpc *fakeRPC, _ *domain.SendIntent) { rpc.balance = big.NewInt(1e17) }, domain.CodeInsufficientFunds},
		{"ETH amount plus fee exceeds balance", func(rpc *fakeRPC, _ *domain.SendIntent) { rpc.balance = big.NewInt(1e17 + 1000) }, domain.CodeInsufficientFunds},
		{"wrong chain", func(rpc *fakeRPC, _ *domain.SendIntent) { rpc.chainID = ChainIDMainnet }, domain.CodeWrongNetwork},
		{"gas above cap", func(rpc *fakeRPC, _ *domain.SendIntent) { rpc.gas = 5_000_000 }, domain.CodeVerificationFailed},
		{"fee above ceiling", func(rpc *fakeRPC, _ *domain.SendIntent) { rpc.fees = steadyFees(400, 1) }, domain.CodeVerificationFailed},
		{"fee history down", func(rpc *fakeRPC, _ *domain.SendIntent) { rpc.feesErr = boom }, domain.CodeProviderUnavailable},
		{"nonce down", func(rpc *fakeRPC, _ *domain.SendIntent) { rpc.nonceEr = boom }, domain.CodeProviderUnavailable},
		{"estimate down", func(rpc *fakeRPC, _ *domain.SendIntent) { rpc.gasErr = boom }, domain.CodeProviderUnavailable},
		{"balance down", func(rpc *fakeRPC, _ *domain.SendIntent) { rpc.balErr = boom }, domain.CodeProviderUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, rpc := sendRig(t)
			in := ethIntent(t)
			tt.mutate(rpc, &in)
			_, err := a.Prepare(context.Background(), in)
			if domain.CodeOf(err) != tt.code {
				t.Fatalf("code = %q, want %q (%v)", domain.CodeOf(err), tt.code, err)
			}
		})
	}
}

func TestPrepareUSDCRejects(t *testing.T) {
	t.Run("token balance below amount", func(t *testing.T) {
		a, rpc := sendRig(t)
		rpc.callOut = word(1_499_999)
		_, err := a.Prepare(context.Background(), usdcIntent(t))
		if domain.CodeOf(err) != domain.CodeInsufficientFunds {
			t.Fatalf("err = %v", err)
		}
		if rpc.gasMsg != nil {
			t.Error("gas was estimated for a transfer that would revert")
		}
	})
	t.Run("balanceOf garbage", func(t *testing.T) {
		a, rpc := sendRig(t)
		rpc.callOut = []byte{1, 2}
		if _, err := a.Prepare(context.Background(), usdcIntent(t)); domain.CodeOf(err) != domain.CodeProviderBadResponse {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestVerifyAcceptsPrepared(t *testing.T) {
	a, _, p := prepareETH(t)
	if err := a.Verify(p, ethIntent(t)); err != nil {
		t.Errorf("ETH: %v", err)
	}
	a, _, p = prepareUSDC(t)
	if err := a.Verify(p, usdcIntent(t)); err != nil {
		t.Errorf("USDC: %v", err)
	}
	// Адрес получателя в другом регистре это тот же получатель.
	in := ethIntent(t)
	in.To = strings.ToLower(addrB)
	a, _, p = prepareETH(t)
	if err := a.Verify(p, in); err != nil {
		t.Errorf("lowercase recipient: %v", err)
	}
}

// retarget возвращает копию p с изменённой транзакцией. Дайджест, комиссия и
// итог пересчитываются, чтобы сработала именно проверка изменённого поля, а не
// проверка согласованности.
func retarget(t *testing.T, p ports.Prepared, mutate func(*dynTx)) ports.Prepared {
	t.Helper()
	tx, err := parseUnsigned(p.Draft)
	if err != nil {
		t.Fatal(err)
	}
	mutate(&tx)
	q := p
	q.Draft = tx.unsigned()
	q.Digests = []ports.DigestRequest{p.Digests[0]}
	q.Digests[0].Digest = keccak256(q.Draft)
	cost, err := domain.NewAmount(new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas), tx.MaxFee))
	if err != nil {
		t.Fatal(err)
	}
	q.Fee, q.Summary.Fee = cost, cost
	if q.Asset == domain.ETH {
		q.Summary.Total, _ = q.Summary.Amount.Add(cost)
	}
	return q
}

func TestVerifyCatchesDraftTampering(t *testing.T) {
	other := mustAddr(t, "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb")
	badTransfer := func(mut func(data []byte)) func(*dynTx) {
		return func(tx *dynTx) { mut(tx.Data) }
	}
	tests := []struct {
		name   string
		usdc   bool
		mutate func(*dynTx)
	}{
		{"ETH: recipient swapped", false, func(tx *dynTx) { tx.To = other }},
		{"ETH: value increased", false, func(tx *dynTx) { tx.Value = new(big.Int).Add(tx.Value, big.NewInt(1)) }},
		{"ETH: value decreased", false, func(tx *dynTx) { tx.Value = new(big.Int).Sub(tx.Value, big.NewInt(1)) }},
		{"ETH: calldata smuggled in", false, func(tx *dynTx) { tx.Data = []byte{0xde, 0xad} }},
		{"ETH: sent to USDC contract", false, func(tx *dynTx) { tx.To = mustAddr(t, USDCSepoliaContract) }},
		{"ETH: other chain id", false, func(tx *dynTx) { tx.ChainID = ChainIDMainnet }},
		{"ETH: gas above cap", false, func(tx *dynTx) { tx.Gas = gasCapETH + 1 }},
		{"ETH: gas below transfer minimum", false, func(tx *dynTx) { tx.Gas = 20999 }},
		{"ETH: maxFee above ceiling", false, func(tx *dynTx) { tx.MaxFee = new(big.Int).Add(maxFeeCeiling, big.NewInt(1)) }},
		{"ETH: zero maxFee", false, func(tx *dynTx) { tx.MaxFee, tx.MaxPriorityFee = new(big.Int), new(big.Int) }},
		{"ETH: tip above maxFee", false, func(tx *dynTx) { tx.MaxPriorityFee = new(big.Int).Add(tx.MaxFee, big.NewInt(1)) }},

		{"USDC: other contract", true, func(tx *dynTx) { tx.To = other }},
		{"USDC: mainnet contract on Sepolia profile", true, func(tx *dynTx) { tx.To = mustAddr(t, USDCMainnetContract) }},
		{"USDC: ETH attached", true, func(tx *dynTx) { tx.Value = big.NewInt(1) }},
		{"USDC: recipient swapped in calldata", true, func(tx *dynTx) {
			tx.Data, _ = encodeTransfer(other, big.NewInt(1_500_000))
		}},
		{"USDC: amount raised in calldata", true, func(tx *dynTx) {
			tx.Data, _ = encodeTransfer(mustAddr(t, addrA), big.NewInt(1_500_001))
		}},
		{"USDC: amount lowered in calldata", true, func(tx *dynTx) {
			tx.Data, _ = encodeTransfer(mustAddr(t, addrA), big.NewInt(1))
		}},
		{"USDC: extra calldata byte", true, func(tx *dynTx) { tx.Data = append(tx.Data, 0) }},
		{"USDC: empty calldata", true, func(tx *dynTx) { tx.Data = nil }},
		{"USDC: approve instead of transfer", true, badTransfer(func(d []byte) { copy(d, []byte{0x09, 0x5e, 0xa7, 0xb3}) })},
		{"USDC: transferFrom instead of transfer", true, badTransfer(func(d []byte) { copy(d, []byte{0x23, 0xb8, 0x72, 0xdd}) })},
		{"USDC: dirty address word", true, badTransfer(func(d []byte) { d[4] = 0xff })},
		{"USDC: gas above cap", true, func(tx *dynTx) { tx.Gas = gasCapUSDC + 1 }},
		{"USDC: other chain id", true, func(tx *dynTx) { tx.ChainID = 5 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				a  *Adapter
				p  ports.Prepared
				in domain.SendIntent
			)
			if tt.usdc {
				a, _, p = prepareUSDC(t)
				in = usdcIntent(t)
			} else {
				a, _, p = prepareETH(t)
				in = ethIntent(t)
			}
			bad := retarget(t, p, tt.mutate)
			err := a.Verify(bad, in)
			if domain.CodeOf(err) != domain.CodeVerificationFailed {
				t.Fatalf("tampered draft passed or failed with %v", err)
			}
		})
	}
}

func TestVerifyCatchesPreparedFieldTampering(t *testing.T) {
	other := "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb"
	tests := []struct {
		name   string
		usdc   bool
		mutate func(p *ports.Prepared)
	}{
		{"digest of another draft", false, func(p *ports.Prepared) { p.Digests[0].Digest[0] ^= 1 }},
		{"signer address other than wallet", false, func(p *ports.Prepared) { p.Digests[0].ExpectedAddress = other }},
		{"signer address empty", false, func(p *ports.Prepared) { p.Digests[0].ExpectedAddress = "" }},
		{"derivation path other", false, func(p *ports.Prepared) { p.Digests[0].Path = "m/44'/60'/0'/0/1" }},
		{"digest network other", false, func(p *ports.Prepared) { p.Digests[0].Network = domain.NetworkTron }},
		{"no digests", false, func(p *ports.Prepared) { p.Digests = nil }},
		{"two digests", false, func(p *ports.Prepared) { p.Digests = append(p.Digests, p.Digests[0]) }},
		{"prepared network other", false, func(p *ports.Prepared) { p.Network = domain.NetworkBitcoin }},
		{"prepared asset other", false, func(p *ports.Prepared) { p.Asset = domain.USDC }},
		{"shown fee lower", false, func(p *ports.Prepared) { p.Fee = amt(t, 1) }},
		{"shown fee asset other", false, func(p *ports.Prepared) { p.FeeAsset = domain.USDC }},
		{"summary fee lower", false, func(p *ports.Prepared) { p.Summary.Fee = amt(t, 1) }},
		{"summary recipient other", false, func(p *ports.Prepared) { p.Summary.To = other }},
		{"summary recipient garbage", false, func(p *ports.Prepared) { p.Summary.To = "hello" }},
		{"summary amount lower", false, func(p *ports.Prepared) { p.Summary.Amount = amt(t, 1) }},
		{"summary total lower", false, func(p *ports.Prepared) { p.Summary.Total = amt(t, 1e17) }},
		{"summary asset other", false, func(p *ports.Prepared) { p.Summary.Asset = domain.USDC }},
		{"USDC summary total nonzero", true, func(p *ports.Prepared) { p.Summary.Total = amt(t, 1_500_000) }},
		{"draft is garbage", false, func(p *ports.Prepared) { p.Draft = []byte{0x02, 0xc0} }},
		{"draft is empty", false, func(p *ports.Prepared) { p.Draft = nil }},
		{"draft has trailing byte", false, func(p *ports.Prepared) { p.Draft = append(bytes.Clone(p.Draft), 0) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				a  *Adapter
				p  ports.Prepared
				in domain.SendIntent
			)
			if tt.usdc {
				a, _, p = prepareUSDC(t)
				in = usdcIntent(t)
			} else {
				a, _, p = prepareETH(t)
				in = ethIntent(t)
			}
			tt.mutate(&p)
			if err := a.Verify(p, in); domain.CodeOf(err) != domain.CodeVerificationFailed {
				t.Fatalf("tampering passed or failed with %v", err)
			}
		})
	}
}

func TestVerifyCatchesIntentMismatch(t *testing.T) {
	other := "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb"
	otherWallet := domain.PublicWallet{ID: "w1", ETHAddress: other}
	t.Run("ETH recipient", func(t *testing.T) {
		a, _, p := prepareETH(t)
		in := ethIntent(t)
		in.To = other
		if err := a.Verify(p, in); domain.CodeOf(err) != domain.CodeVerificationFailed {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("ETH amount", func(t *testing.T) {
		a, _, p := prepareETH(t)
		in := ethIntent(t)
		in.Amount = amt(t, 1e17+1)
		if err := a.Verify(p, in); domain.CodeOf(err) != domain.CodeVerificationFailed {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("USDC recipient", func(t *testing.T) {
		a, _, p := prepareUSDC(t)
		in := usdcIntent(t)
		in.To = other
		if err := a.Verify(p, in); domain.CodeOf(err) != domain.CodeVerificationFailed {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("USDC amount", func(t *testing.T) {
		a, _, p := prepareUSDC(t)
		in := usdcIntent(t)
		in.Amount = amt(t, 1_500_001)
		if err := a.Verify(p, in); domain.CodeOf(err) != domain.CodeVerificationFailed {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("another wallet signs", func(t *testing.T) {
		a, _, p := prepareETH(t)
		in := ethIntent(t)
		in.Wallet = otherWallet
		if err := a.Verify(p, in); domain.CodeOf(err) != domain.CodeVerificationFailed {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("intent asset swapped", func(t *testing.T) {
		a, _, p := prepareETH(t)
		in := usdcIntent(t)
		if err := a.Verify(p, in); domain.CodeOf(err) != domain.CodeVerificationFailed {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("invalid recipient in intent", func(t *testing.T) {
		a, _, p := prepareETH(t)
		in := ethIntent(t)
		in.To = "0x00"
		if err := a.Verify(p, in); domain.CodeOf(err) != domain.CodeInvalidAddress {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("non-Ethereum asset", func(t *testing.T) {
		a, _, p := prepareETH(t)
		in := ethIntent(t)
		in.Asset = domain.USDT
		if err := a.Verify(p, in); domain.CodeOf(err) != domain.CodeVerificationFailed {
			t.Errorf("err = %v", err)
		}
	})
}

func TestVerifyRejectsMainnetDraftOnTestProfile(t *testing.T) {
	// Профиль один на приложение: черновик для main не проходит в test, даже
	// если всё остальное согласовано.
	a, _, p := prepareETH(t)
	bad := retarget(t, p, func(tx *dynTx) { tx.ChainID = ChainIDMainnet })
	if err := a.Verify(bad, ethIntent(t)); !errors.Is(err, ErrVerify) {
		t.Errorf("err = %v", err)
	}
}

func TestSendIsSafeForConcurrentUse(t *testing.T) {
	a, _ := sendRig(t)
	in := ethIntent(t)
	done := make(chan error, 8)
	for range 8 {
		go func() {
			p, err := a.Prepare(context.Background(), in)
			if err == nil {
				err = a.Verify(p, in)
			}
			done <- err
		}()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}
