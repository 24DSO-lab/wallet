package domain

import "time"

// PublicWallet это публичные данные кошелька: всё, что нужно адаптерам и
// сервисам, и ничего секретного. xpub раскрывает все BTC-адреса кошелька, но
// не даёт доступа к средствам; через границу Wails он не выходит никогда.
type PublicWallet struct {
	ID          string
	Name        string
	BTCXpub     string // xpub (main) или tpub (test), счёт m/84'/coin'/0', сериализация BIP32 (A8)
	ETHAddress  string // общий адрес ETH и USDC, путь m/44'/60'/0'/0/0, EIP-55
	TronAddress string // адрес TRON (USDT и TRX), путь m/44'/195'/0'/0/0
}

// Wallet это запись кошелька, как её хранит Store: публичные данные плюс
// служебные поля. Seed здесь нет, только ссылка на запись keystore.
//
// Store собирает ETHAddress и TronAddress из записей Address.
type Wallet struct {
	PublicWallet
	KeystoreID string
	CreatedAt  time.Time
	SortOrder  int
}

// AddressChain это цепочка адресов HD-кошелька BTC (BIP44).
type AddressChain string

// Цепочки адресов. Для ETH и TRON, где адрес один (индекс 0), используется
// ChainReceive.
const (
	ChainReceive AddressChain = "receive"
	ChainChange  AddressChain = "change"
)

// Address это адрес кошелька: у BTC их много, у ETH и TRON по одному.
type Address struct {
	WalletID string
	Network  Network
	Address  string
	Path     string // путь деривации, например m/84'/0'/0'/0/5
	Chain    AddressChain
	Index    uint32
	Used     bool // на адресе были операции: сканирование идёт дальше него
}
