package tron

import (
	"context"
	"math/big"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// API это то, что адаптеру нужно от провайдера. Реализация для TronGrid
// лежит в trongrid.go; тесты подставляют записанные ответы или фейк.
// Данные из API не доверенные: Prepare и Verify проверяют их локально.
type API interface {
	// NowBlock возвращает ссылку на свежий блок для ref_block.
	NowBlock(ctx context.Context) (BlockRef, error)
	// Account возвращает баланс TRX в sun. Для несуществующего аккаунта
	// второе значение false, баланс ноль.
	Account(ctx context.Context, owner Addr) (sun uint64, exists bool, err error)
	// Resources возвращает свободные bandwidth и energy аккаунта.
	Resources(ctx context.Context, owner Addr) (Resources, error)
	// TRC20Balance читает balanceOf контракта.
	TRC20Balance(ctx context.Context, contract, owner Addr) (*big.Int, error)
	// TRC20Transfers возвращает переводы токена по аккаунту, новые первыми.
	// В ответ входят и неподтверждённые (поле Confirmed).
	TRC20Transfers(ctx context.Context, contract, owner Addr, limit int) ([]Transfer, error)
	// EstimateEnergy оценивает energy вызова transfer с этим calldata.
	EstimateEnergy(ctx context.Context, owner, contract Addr, calldata []byte) (uint64, error)
	// Prices возвращает цены сети в sun.
	Prices(ctx context.Context) (Prices, error)
	// Broadcast отправляет готовую транзакцию (Transaction protobuf).
	Broadcast(ctx context.Context, tx []byte) (BroadcastResult, error)
	// TxInfo возвращает исход транзакции; Found false, пока её нет в блоке.
	TxInfo(ctx context.Context, txid string) (TxInfo, error)
}

// BlockRef это данные блока для ref_block_bytes и ref_block_hash.
type BlockRef struct {
	Number    uint64
	ID        [32]byte // blockID: номер (8 байт) и хвост хеша
	Timestamp time.Time
}

// Resources это свободные ресурсы аккаунта на сейчас.
type Resources struct {
	FreeBandwidth   uint64 // свободный ежедневный bandwidth, байты
	StakedBandwidth uint64 // свободный застейканный/делегированный bandwidth, байты
	Energy          uint64
}

// Transfer это перевод TRC20 из истории.
type Transfer struct {
	TxID      string
	From, To  string // base58
	Value     domain.Amount
	Time      time.Time
	Confirmed bool
}

// Prices это цены ресурсов в sun.
type Prices struct {
	EnergyFee    uint64 // sun за единицу energy
	BandwidthFee uint64 // sun за байт bandwidth
}

// BroadcastResult это ответ узла на отправку.
type BroadcastResult struct {
	OK        bool
	Duplicate bool   // узел уже знает эту транзакцию
	Code      string // код отказа узла, без текста сообщения
	TxID      string
}

// TxInfo это итог выполнения транзакции.
type TxInfo struct {
	Found       bool
	BlockNumber uint64
	Success     bool
	FeeSun      uint64
	CurrentTip  uint64 // номер свежего блока для подсчёта подтверждений
}
