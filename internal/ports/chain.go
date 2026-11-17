package ports

import (
	"context"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// ChainAdapter это адаптер одной сети. Данные провайдеров не доверенные:
// Verify обязан разобрать черновик сам, а не верить полям Prepared.
//
// Все методы безопасны для вызова из нескольких горутин.
type ChainAdapter interface {
	// Network возвращает сеть адаптера.
	Network() domain.Network

	// ValidateAddress проверяет адрес правилами именно этой сети и текущего профиля.
	ValidateAddress(addr string) error

	// AddressFromPublicKey превращает публичный ключ в адрес сети (ETH и TRON).
	// Принимает несжатый ключ в 65 байт (0x04 || X || Y). Для BTC возвращает ошибку:
	// адреса BTC выводятся из xpub внутри адаптера.
	AddressFromPublicKey(pub []byte) (string, error)

	// Refresh загружает баланс, историю, найденные адреса и UTXO актива.
	// Актив обязан принадлежать сети адаптера (для TRON допускается TRX).
	Refresh(ctx context.Context, w domain.PublicWallet, a domain.Asset) (Snapshot, error)

	// Prepare собирает неподписанную транзакцию без ключей.
	Prepare(ctx context.Context, in domain.SendIntent) (Prepared, error)

	// Verify разбирает байты черновика самостоятельно и сверяет получателя,
	// сумму, комиссию и отправителя с намерением. Не верит полям Prepared.
	Verify(p Prepared, in domain.SendIntent) error

	// Finalize собирает подписанную транзакцию из подписей Keystore,
	// порядок подписей совпадает с порядком Prepared.Digests.
	Finalize(p Prepared, sigs []Signature) (Signed, error)

	// Broadcast отправляет подписанную транзакцию в сеть.
	Broadcast(ctx context.Context, s Signed) (domain.TxID, error)

	// Status возвращает состояние транзакции в сети.
	Status(ctx context.Context, id domain.TxID) (TxStatus, error)
}

// Replacement describes a verified Bitcoin fee-bump draft for an existing
// accepted but unconfirmed transaction.
type Replacement struct {
	ReplacesTxID domain.TxID
	Intent       domain.SendIntent
	Prepared     Prepared
}

// RBFAdapter is an optional capability implemented only by chains that
// support Replace-By-Fee. It must validate the original pending transaction
// and ensure the replacement preserves its recipient and inputs.
type RBFAdapter interface {
	PrepareReplacement(ctx context.Context, wallet domain.PublicWallet, original PendingTx) (Replacement, error)
	VerifyReplacement(ctx context.Context, wallet domain.PublicWallet, original PendingTx, replacement Replacement) error
}

// Snapshot это результат Refresh для одного актива.
// ReceiveAddressAdapter is an optional fast path for deriving the next
// Bitcoin receive address without refreshing balances, history or UTXOs.
// Implementations must scan the external BIP84 chain through its gap limit.
type ReceiveAddressAdapter interface {
	ReceiveAddress(ctx context.Context, wallet domain.PublicWallet, asset domain.Asset) (domain.Address, error)
}

type Snapshot struct {
	Asset     domain.Asset
	Balance   domain.Balance
	History   []domain.Tx
	Addresses []domain.Address // найденные адреса (BTC), для ETH и TRON пусто
	UTXOs     []domain.UTXO    // только BTC
	// FeeBalance это баланс комиссионной монеты (ETH для USDC, TRX для USDT);
	// для остальных активов нулевой.
	FeeBalance domain.Amount
	FeeAsset   domain.Asset
	FetchedAt  time.Time
}

// Prepared это неподписанная транзакция, собранная адаптером. Draft это байты
// черновика, которые Verify разбирает заново; остальные поля только для показа
// и для передачи подписи.
type Prepared struct {
	Network domain.Network
	Asset   domain.Asset
	Draft   []byte

	// Digests это запросы подписи в порядке Finalize: по одному на вход (BTC)
	// или один на транзакцию (ETH, TRON).
	Digests []DigestRequest

	Fee      domain.Amount
	FeeAsset domain.Asset
	Summary  Summary
	Warnings []domain.Warning
}

// Summary это описание для экрана подтверждения, собранное адаптером.
type Summary struct {
	Network  domain.Network
	Asset    domain.Asset
	To       string // получатель целиком
	Amount   domain.Amount
	Fee      domain.Amount
	FeeAsset domain.Asset
	// Total это сумма и комиссия в одной монете, если монета одна (BTC, ETH);
	// для токенов нулевой, показываются две строки.
	Total domain.Amount
}

// Signed это подписанная транзакция, готовая к отправке.
type Signed struct {
	Network domain.Network
	TxID    domain.TxID
	Raw     []byte
}

// TxStatus это состояние транзакции в сети.
type TxStatus struct {
	State         domain.TxStatus
	// Known means the queried node recognizes this transaction (mempool or
	// block). Pending with Known=false is an unresolved broadcast, not proof
	// of acceptance; callers must not release duplicate-send protection.
	Known         bool
	BlockHeight   uint64
	Confirmations uint32
}
