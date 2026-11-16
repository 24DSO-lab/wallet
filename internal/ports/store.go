package ports

import (
	"context"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// Store это хранилище публичных данных. Ни один метод не знает числа кошельков
// и активов. Отсутствие записи это domain.ErrNotFound.
type Store interface {
	Wallets() WalletRepo
	Addresses() AddressRepo
	Balances() BalanceRepo
	UTXOs() UTXORepo
	Transactions() TransactionRepo
	Settings() SettingRepo
	PendingTxs() PendingTxRepo

	// WithTx выполняет fn в одной транзакции БД: при ошибке всё откатывается.
	// Репозитории внутри fn работают в этой транзакции.
	WithTx(ctx context.Context, fn func(ctx context.Context, tx Store) error) error
}

// WalletRepo хранит кошельки.
type WalletRepo interface {
	Create(ctx context.Context, w domain.Wallet) error
	Get(ctx context.Context, id string) (domain.Wallet, error)
	List(ctx context.Context) ([]domain.Wallet, error) // по SortOrder
	Rename(ctx context.Context, id, name string) error
	Delete(ctx context.Context, id string) error
}

// AddressRepo хранит адреса. Upsert идемпотентен по (WalletID, Network, Address).
type AddressRepo interface {
	Upsert(ctx context.Context, addrs []domain.Address) error
	List(ctx context.Context, walletID string, n domain.Network) ([]domain.Address, error)
}

// BalanceRepo это кэш балансов; источник истины всегда сеть.
type BalanceRepo interface {
	Put(ctx context.Context, walletID string, a domain.Asset, b domain.Balance, at time.Time) error
	Get(ctx context.Context, walletID string, a domain.Asset) (BalanceRecord, error)
}

// BalanceRecord это закэшированный баланс со временем обновления.
type BalanceRecord struct {
	Balance   domain.Balance
	UpdatedAt time.Time
}

// UTXORepo хранит UTXO BTC.
type UTXORepo interface {
	// Replace заменяет набор UTXO кошелька результатом свежего сканирования.
	Replace(ctx context.Context, walletID string, utxos []domain.UTXO) error
	List(ctx context.Context, walletID string) ([]domain.UTXO, error)
}

// TransactionRepo хранит историю. Upsert идемпотентен по (WalletID, Asset, TxID).
type TransactionRepo interface {
	Upsert(ctx context.Context, txs []domain.Tx) error
	List(ctx context.Context, walletID string, asset domain.Asset) ([]domain.Tx, error) // новые первыми; пустой asset означает все
	Get(ctx context.Context, walletID string, asset domain.Asset, id domain.TxID) (domain.Tx, error)
}

// SettingRepo хранит настройки. Ключи API чувствительны: List их не отдаёт,
// значение читается только через Get.
type SettingRepo interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	List(ctx context.Context) (map[string]string, error) // без чувствительных ключей
}

// PendingTx хранит подписанную транзакцию и исходный unsigned draft до
// подтверждения в блоке. Raw и Draft не содержат приватных ключей.
type PendingTx struct {
	TxID              domain.TxID
	WalletID          string
	Asset             domain.Asset
	Network           domain.Network
	Raw               []byte
	Draft             []byte // canonical unsigned transaction envelope, used for safe RBF replacement
	BroadcastAccepted bool   // true only after the provider accepted the transaction or status lookup found it
	ReplacesTxID      domain.TxID
	CreatedAt         time.Time
}

// PendingTxRepo хранит неподтверждённые подписанные транзакции.
type PendingTxRepo interface {
	Put(ctx context.Context, p PendingTx) error // идемпотентно по TxID
	Get(ctx context.Context, id domain.TxID) (PendingTx, error)
	List(ctx context.Context) ([]PendingTx, error)
	Delete(ctx context.Context, id domain.TxID) error
}
