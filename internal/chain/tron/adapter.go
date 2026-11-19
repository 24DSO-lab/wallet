package tron

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

// Параметры безопасности отправки. Значения это предложение Dev 6, они
// требуют согласования с владельцем проекта (см. README пакета).
const (
	// TronPath это путь деривации адреса TRON (ADR).
	TronPath = "m/44'/195'/0'/0/0"

	// MaxFeeLimitSun это потолок fee_limit: 100 TRX. Verify отклоняет черновик
	// с большим значением, Prepare отказывается собирать транзакцию дороже.
	MaxFeeLimitSun int64 = 100_000_000
	// feeLimitNum/feeLimitDen это запас над оценкой energy: 120%.
	feeLimitNum, feeLimitDen = 12, 10
	// HighFeeSun это порог предупреждения о высокой комиссии: 30 TRX.
	HighFeeSun uint64 = 30_000_000

	// DraftWindow это срок жизни черновика (expiration - timestamp).
	DraftWindow = 5 * time.Minute
	// maxDraftWindow это наибольший срок, который принимает Verify.
	maxDraftWindow = 10 * time.Minute
	// maxClockSkew это допустимое расхождение времени черновика и часов.
	maxClockSkew = 2 * time.Minute

	// SolidityDepth это число блоков, после которого перевод считается
	// подтверждённым (блок необратим, около минуты).
	SolidityDepth = 19

	// bandwidthOverhead это добавка к размеру raw_data для оценки bandwidth:
	// подпись (67 байт в Transaction) и служебное поле ret (до 64 байт).
	bandwidthOverhead = 131

	maxSaneEnergyFee    = 10_000
	maxSaneBandwidthFee = 10_000
	maxSaneEnergy       = 10_000_000
	historyLimit        = 200
	// maxInfoLookups ограничивает число запросов за блоком и комиссией при Refresh.
	maxInfoLookups = 20
)

// Ошибки Prepare и Verify.
var (
	ErrUnsupportedAsset = errors.New("tron: asset is not supported for sending")
	ErrFeeAboveCeiling  = errors.New("tron: estimated fee exceeds the fee_limit ceiling")
	ErrVerify           = errors.New("tron: draft does not match the send intent")
)

// Adapter это адаптер сети TRON. После создания не меняется: безопасен для
// нескольких горутин.
type Adapter struct {
	api     API
	profile domain.Profile
	now     func() time.Time
}

var _ ports.ChainAdapter = (*Adapter)(nil)

// Option настраивает Adapter.
type Option func(*Adapter)

// WithClock подменяет часы (для тестов).
func WithClock(now func() time.Time) Option { return func(a *Adapter) { a.now = now } }

// New создаёт адаптер для профиля сети.
func New(api API, profile domain.Profile, opts ...Option) (*Adapter, error) {
	if api == nil {
		return nil, errors.New("tron: api is nil")
	}
	if !profile.Valid() {
		return nil, fmt.Errorf("tron: invalid profile %q", string(profile))
	}
	a := &Adapter{api: api, profile: profile, now: time.Now}
	for _, o := range opts {
		o(a)
	}
	return a, nil
}

// Network возвращает сеть адаптера.
func (a *Adapter) Network() domain.Network { return domain.NetworkTron }

func verr(detail string) error {
	return domain.NewError(domain.CodeVerificationFailed, fmt.Errorf("%w: %s", ErrVerify, detail))
}

// feeLimitFor считает fee_limit по оценке energy: запас 120%, не выше потолка.
func feeLimitFor(energy, energyFee uint64) (int64, error) {
	if energy == 0 || energy > maxSaneEnergy || energyFee == 0 || energyFee > maxSaneEnergyFee {
		return 0, domain.NewError(domain.CodeProviderBadResponse, fmt.Errorf("%w: energy or price out of range", ErrBadResponse))
	}
	limit := energy * energyFee * feeLimitNum / feeLimitDen
	if limit > uint64(MaxFeeLimitSun) {
		return 0, domain.NewError(domain.CodeInvalidInput, ErrFeeAboveCeiling)
	}
	return int64(limit), nil
}

// ownTron разбирает адрес TRON кошелька.
func ownTron(w domain.PublicWallet) (Addr, error) {
	ad, err := ParseAddress(w.TronAddress)
	if err != nil {
		return ad, domain.NewError(domain.CodeInvalidInput, fmt.Errorf("wallet TRON address: %w", err))
	}
	return ad, nil
}

// Prepare собирает неподписанный черновик transfer(to, amount) без ключей.
// От сервера берутся блок, оценка energy и цены; поля транзакции собираются
// здесь, а сразу после сборки черновик проходит Verify.
func (a *Adapter) Prepare(ctx context.Context, in domain.SendIntent) (ports.Prepared, error) {
	if err := in.Validate(); err != nil {
		return ports.Prepared{}, err
	}
	if in.Asset != domain.USDT {
		code := domain.CodeInvalidInput
		if info, ok := in.Asset.Info(); ok && info.Network != domain.NetworkTron {
			code = domain.CodeWrongNetwork
		}
		return ports.Prepared{}, domain.NewError(code, ErrUnsupportedAsset)
	}
	to, err := ParseAddress(in.To)
	if err != nil {
		return ports.Prepared{}, err
	}
	owner, err := ownTron(in.Wallet)
	if err != nil {
		return ports.Prepared{}, err
	}
	contract, err := usdtContract(a.profile)
	if err != nil {
		return ports.Prepared{}, domain.NewError(domain.CodeInternal, err)
	}

	bal, err := a.api.TRC20Balance(ctx, contract, owner)
	if err != nil {
		return ports.Prepared{}, err
	}
	if bal.Cmp(in.Amount.BigInt()) < 0 {
		return ports.Prepared{}, domain.NewError(domain.CodeInsufficientFunds, errors.New("USDT balance is below the amount"))
	}
	data, err := transferCalldata(to, in.Amount.BigInt())
	if err != nil {
		return ports.Prepared{}, domain.NewError(domain.CodeInvalidAmount, err)
	}
	energy, err := a.api.EstimateEnergy(ctx, owner, contract, data)
	if err != nil {
		return ports.Prepared{}, err
	}
	prices, err := a.api.Prices(ctx)
	if err != nil {
		return ports.Prepared{}, err
	}
	feeLimit, err := feeLimitFor(energy, prices.EnergyFee)
	if err != nil {
		return ports.Prepared{}, err
	}
	blk, err := a.api.NowBlock(ctx)
	if err != nil {
		return ports.Prepared{}, err
	}
	trx, _, err := a.api.Account(ctx, owner)
	if err != nil {
		return ports.Prepared{}, err
	}
	res, err := a.api.Resources(ctx, owner)
	if err != nil {
		return ports.Prepared{}, err
	}

	ts := blk.Timestamp.UnixMilli()
	raw := RawTx{
		Expiration: ts + DraftWindow.Milliseconds(), Timestamp: ts, FeeLimit: feeLimit,
		Owner: owner, Contract: contract, Data: data,
	}
	//nolint:gosec // TRON ref_block_bytes intentionally take the low two bytes.
	raw.RefBlockBytes = [2]byte{byte((blk.Number >> 8) & 0xff), byte(blk.Number & 0xff)}
	copy(raw.RefBlockHash[:], blk.ID[8:16])
	draft := raw.Marshal()

	// Ожидаемое сжигание TRX: energy сверх свободной и bandwidth сверх свободного.
	var burn uint64
	var warns []domain.Warning
	if energy > res.Energy {
		burn += (energy - res.Energy) * prices.EnergyFee
		warns = append(warns, domain.Warning{Code: domain.WarnInsufficientEnergy,
			Detail: "not enough energy: the missing part is paid by burning TRX"})
	}
	if bw := uint64(len(draft)) + bandwidthOverhead; bw > res.FreeBandwidth && bw > res.StakedBandwidth {
		// TRON не суммирует частичные квоты: один источник должен покрыть
		// полный размер транзакции, иначе сжигается полный bandwidth fee.
		burn += bw * prices.BandwidthFee
	}
	if trx < burn {
		warns = append(warns, domain.Warning{Code: domain.WarnInsufficientFeeCoin,
			Detail: "TRX balance is below the expected fee"})
	}
	if burn > HighFeeSun {
		warns = append(warns, domain.Warning{Code: domain.WarnHighFee, Detail: "fee is higher than usual"})
	}
	if to == owner {
		warns = append(warns, domain.Warning{Code: domain.WarnSelfAddress, Detail: "recipient is your own address"})
	}

	fee := domain.AmountFromUint64(burn)
	p := ports.Prepared{
		Network: domain.NetworkTron, Asset: domain.USDT, Draft: draft,
		Digests: []ports.DigestRequest{{
			Network: domain.NetworkTron, Path: TronPath, ExpectedAddress: owner.String(), Digest: TxIDOf(draft),
		}},
		Fee: fee, FeeAsset: domain.TRX, Warnings: warns,
		Summary: ports.Summary{Network: domain.NetworkTron, Asset: domain.USDT, To: to.String(),
			Amount: in.Amount, Fee: fee, FeeAsset: domain.TRX},
	}
	// Собственный результат проходит ту же проверку, что и перед подписью.
	if err := a.Verify(p, in); err != nil {
		return ports.Prepared{}, err
	}
	return p, nil
}

// Verify разбирает Draft строгим разборщиком и сверяет его с намерением.
// Поля Prepared не используются как источник правды: дайджест пересчитывается
// из Draft, а сводка сравнивается с намерением.
func (a *Adapter) Verify(p ports.Prepared, in domain.SendIntent) error {
	if err := in.Validate(); err != nil {
		return err
	}
	if in.Asset != domain.USDT || p.Network != domain.NetworkTron || p.Asset != domain.USDT {
		return verr("network or asset")
	}
	to, err := ParseAddress(in.To)
	if err != nil {
		return err
	}
	owner, err := ownTron(in.Wallet)
	if err != nil {
		return err
	}
	contract, err := usdtContract(a.profile)
	if err != nil {
		return domain.NewError(domain.CodeInternal, err)
	}

	raw, err := ParseRawTx(p.Draft)
	if err != nil {
		return domain.NewError(domain.CodeVerificationFailed, err)
	}
	if raw.Owner != owner {
		return verr("sender is not the wallet address")
	}
	if raw.Contract != contract {
		return verr("contract is not the USDT contract")
	}
	dTo, dAmount, err := parseTransferCalldata(raw.Data)
	if err != nil {
		return domain.NewError(domain.CodeVerificationFailed, err)
	}
	if dTo != to {
		return verr("recipient differs from the intent")
	}
	if dAmount.Cmp(in.Amount.BigInt()) != 0 {
		return verr("amount differs from the intent")
	}
	if raw.FeeLimit <= 0 || raw.FeeLimit > MaxFeeLimitSun {
		return verr("fee_limit is missing or above the ceiling")
	}

	now := a.now()
	ts, exp := time.UnixMilli(raw.Timestamp), time.UnixMilli(raw.Expiration)
	if w := exp.Sub(ts); w <= 0 || w > maxDraftWindow {
		return verr("expiration window is out of range")
	}
	if d := ts.Sub(now); d > maxClockSkew || d < -maxDraftWindow-maxClockSkew {
		return verr("draft timestamp is far from the local clock")
	}
	if !exp.After(now) {
		return domain.NewError(domain.CodeDraftExpired, ErrVerify)
	}

	// Подписывается ровно то, что разобрано выше.
	if len(p.Digests) != 1 {
		return verr("expected exactly one digest")
	}
	d := p.Digests[0]
	if d.Network != domain.NetworkTron || d.Path != TronPath || d.ExpectedAddress != owner.String() || d.Digest != TxIDOf(p.Draft) {
		return verr("digest request does not match the draft")
	}
	// Сводка для экрана не должна расходиться с намерением.
	s := p.Summary
	if s.To != in.To || !s.Amount.Equal(in.Amount) || s.Asset != domain.USDT || s.Network != domain.NetworkTron ||
		s.FeeAsset != domain.TRX || p.FeeAsset != domain.TRX || !s.Fee.Equal(p.Fee) {
		return verr("summary differs from the intent")
	}
	if p.Fee.Cmp(domain.AmountFromUint64(uint64(MaxFeeLimitSun)+maxBandwidthBurn)) > 0 {
		return verr("displayed fee is above the ceiling")
	}
	return nil
}

// VerifyLive повторно проверяет баланс, комиссионные ресурсы и цену Energy
// после пользовательского подтверждения, непосредственно перед подписью.
// Любое изменение, влияющее на расчёт комиссии, требует пересоздать черновик.
func (a *Adapter) VerifyLive(ctx context.Context, p ports.Prepared, in domain.SendIntent) error {
	if err := a.Verify(p, in); err != nil {
		return err
	}
	raw, err := ParseRawTx(p.Draft)
	if err != nil {
		return domain.NewError(domain.CodeVerificationFailed, err)
	}
	_, amount, err := parseTransferCalldata(raw.Data)
	if err != nil {
		return domain.NewError(domain.CodeVerificationFailed, err)
	}
	owner, err := ownTron(in.Wallet)
	if err != nil {
		return err
	}
	balance, err := a.api.TRC20Balance(ctx, raw.Contract, owner)
	if err != nil {
		return err
	}
	if balance.Cmp(amount) < 0 {
		return domain.NewError(domain.CodeInsufficientFunds, errors.New("USDT balance changed after transaction preparation"))
	}
	energy, err := a.api.EstimateEnergy(ctx, owner, raw.Contract, raw.Data)
	if err != nil {
		return err
	}
	prices, err := a.api.Prices(ctx)
	if err != nil {
		return err
	}
	feeLimit, err := feeLimitFor(energy, prices.EnergyFee)
	if err != nil {
		return err
	}
	if raw.FeeLimit < feeLimit {
		return domain.NewError(domain.CodeDraftExpired, errors.New("energy price or estimate changed after transaction preparation"))
	}
	trx, _, err := a.api.Account(ctx, owner)
	if err != nil {
		return err
	}
	resources, err := a.api.Resources(ctx, owner)
	if err != nil {
		return err
	}
	var expectedBurn uint64
	if energy > resources.Energy {
		expectedBurn = (energy - resources.Energy) * prices.EnergyFee
	}
	bandwidth := uint64(len(p.Draft)) + bandwidthOverhead
	if bandwidth > resources.FreeBandwidth && bandwidth > resources.StakedBandwidth {
		expectedBurn += bandwidth * prices.BandwidthFee
	}
	liveFee := domain.AmountFromUint64(expectedBurn)
	if !liveFee.Equal(p.Fee) {
		return domain.NewError(domain.CodeDraftExpired, errors.New("fee estimate changed after transaction preparation"))
	}
	if trx < expectedBurn {
		return domain.NewError(domain.CodeInsufficientFunds, errors.New("TRX balance is below the current estimated transaction fee"))
	}
	return nil
}

// maxBandwidthBurn это верхняя оценка сжигания TRX на bandwidth (1000 sun
// за байт при размере до 1 КБ), добавляемая к потолку при проверке показа.
const maxBandwidthBurn = 1_000_000
