package eth

import (
	"context"
	"math/big"
	"sort"
)

// Политика комиссии EIP-1559.
//
// Prepare выбирает комиссию с запасом, Verify отвергает черновик выше потолков
// ниже, не доверяя Prepared. Потолки это самая жёсткая защита от провайдера,
// который подсунул бы бешеную цену газа: даже при полной подмене оценок потери
// ограничены gasCap * maxFeeCeiling. Значения подлежат согласованию с Dev 1 и
// владельцем продукта (ADR не задаёт чисел).
var (
	gwei = big.NewInt(1_000_000_000)

	// minPriorityFee это нижняя граница чаевых майнеру: без неё транзакция
	// в тихой сети может «застрять» при нулевой оценке.
	minPriorityFee = new(big.Int).Set(gwei)
	// maxFeeCeiling это потолок maxFeePerGas: 500 gwei.
	maxFeeCeiling = new(big.Int).Mul(big.NewInt(500), gwei)
	// highBaseFee это порог предупреждения «комиссия высокая»: 100 gwei.
	highBaseFee = new(big.Int).Mul(big.NewInt(100), gwei)
)

const (
	gasTransferETH = 21_000  // перевод ETH на адрес без кода
	gasCapETH      = 100_000 // потолок газа для перевода ETH (получатель может быть контрактом)
	gasCapUSDC     = 150_000 // потолок газа для transfer USDC (обычно 45–65 тысяч)

	feeHistoryBlocks = 10
	gasPadNum        = 12 // запас газа 20% поверх оценки узла
	gasPadDen        = 10
)

var feePercentiles = []float64{50}

// gasCap возвращает потолок газа для актива.
func gasCap(isUSDC bool) uint64 {
	if isUSDC {
		return gasCapUSDC
	}
	return gasCapETH
}

// feeQuote это выбранные параметры комиссии.
type feeQuote struct {
	maxPriority *big.Int
	maxFee      *big.Int
	nextBase    *big.Int // базовая комиссия следующего блока, для предупреждения
}

// quoteFees выбирает чаевые (медиана за последние блоки, не ниже
// minPriorityFee) и maxFeePerGas = 2 * baseFee следующего блока + чаевые: так
// транзакция выдерживает рост базовой комиссии на 12,5% в блоке шесть блоков
// подряд. Лишнее по итогу не списывается: платится реальная базовая комиссия.
func quoteFees(ctx context.Context, rpc RPC) (feeQuote, error) {
	fh, err := rpc.FeeHistory(ctx, feeHistoryBlocks, feePercentiles)
	if err != nil {
		return feeQuote{}, err
	}
	next := fh.BaseFee[len(fh.BaseFee)-1]

	var tips []*big.Int
	for _, row := range fh.Reward {
		if len(row) > 0 {
			tips = append(tips, row[0])
		}
	}
	priority := new(big.Int).Set(minPriorityFee)
	if len(tips) > 0 {
		sort.Slice(tips, func(i, j int) bool { return tips[i].Cmp(tips[j]) < 0 })
		if m := tips[len(tips)/2]; m.Cmp(priority) > 0 {
			priority.Set(m)
		}
	}
	maxFee := new(big.Int).Lsh(next, 1)
	maxFee.Add(maxFee, priority)
	return feeQuote{maxPriority: priority, maxFee: maxFee, nextBase: new(big.Int).Set(next)}, nil
}

// paddedGas добавляет запас к оценке узла. Перевод ETH ровно в 21000 остаётся
// как есть: больше газа там не нужно.
func paddedGas(estimate uint64, isUSDC bool) uint64 {
	if !isUSDC && estimate <= gasTransferETH {
		return gasTransferETH
	}

	cap := gasCap(isUSDC)
	// Reject estimates whose 20% padding would exceed the configured cap.
	// The bound is checked before multiplication, preventing uint64 overflow
	// from wrapping a hostile provider estimate into an apparently valid gas limit.
	maxEstimate := cap * gasPadDen / gasPadNum
	if estimate > maxEstimate {
		return cap + 1
	}
	return estimate + estimate*(gasPadNum-gasPadDen)/gasPadDen
}
