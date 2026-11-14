package domain

import "time"

// TxID это идентификатор транзакции в её сети: для BTC и ETH hex, для TRON hex
// от SHA-256 raw_data. Регистр и префикс 0x задаёт адаптер сети, остальные
// слои считают идентификатор непрозрачной строкой.
type TxID string

// TxStatus это статус транзакции. Для BTC подробнее см. Balance.
type TxStatus string

// Статусы транзакций (ADR: «в ожидании», «подтверждено», «ошибка»).
const (
	TxPending   TxStatus = "pending"
	TxConfirmed TxStatus = "confirmed"
	TxFailed    TxStatus = "failed"
)

// Valid сообщает, что статус известен.
func (s TxStatus) Valid() bool {
	switch s {
	case TxPending, TxConfirmed, TxFailed:
		return true
	}
	return false
}

// Direction это направление операции относительно кошелька.
type Direction string

// Направления: входящая, исходящая, между собственными адресами.
const (
	DirectionIn   Direction = "in"
	DirectionOut  Direction = "out"
	DirectionSelf Direction = "self"
)

// Tx это единая запись истории для всех сетей (таблица transaction в ADR).
// Естественный ключ записи это (WalletID, Asset, TxID).
type Tx struct {
	ID           string // присваивает Store при вставке, адаптеры оставляют пустым
	WalletID     string
	Asset        Asset
	TxID         TxID
	Direction    Direction
	Amount       Amount
	Fee          Amount
	FeeAsset     Asset
	Status       TxStatus
	BlockHeight  uint64 // 0, пока транзакция не попала в блок
	Time         time.Time
	Counterparty string // адрес другой стороны, пусто для self
}

// Balance это баланс актива. Для BTC подтверждённая и ожидающая части
// показываются раздельно (ADR).
//
//   - Confirmed это сумма подтверждённых средств, уже за вычетом исходящих
//     операций, ожидающих подтверждения.
//   - Pending это сумма ожидающих подтверждения входящих поступлений.
//     Для сетей без такого понятия (ETH, USDC, USDT) всегда ноль.
type Balance struct {
	Confirmed Amount
	Pending   Amount
}

// UTXO это непотраченный выход BTC, нужный для расчёта и подписи отправки.
type UTXO struct {
	WalletID      string
	TxID          TxID
	Vout          uint32
	Address       string
	Amount        Amount
	Confirmations uint32
	Spent         bool
}
