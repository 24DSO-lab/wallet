package tron

import "github.com/24DSO-lab/wallet/internal/domain"

// Адреса контрактов USDT (TRC20). Менять только отдельным PR с двумя
// проверяющими: подмена этой константы подменяет токен, который кошелёк
// считает USDT (ADR, «Правила работы с провайдерами»). Тест
// TestUSDTContractPinned падает при любой правке.
//
// Сверка адресов выполнена 2026-11-17 (см. USDTSources). Main подтверждён
// официальной страницей Tether Supported Protocols. Nile подтверждён
// официальной документацией TRON, которая ведёт на Nile faucet, и примером
// TRC20 в официальной документации Trident. Это тестовый токен Nile, а не
// mainnet-контракт Tether; он не имеет денежной стоимости.
const (
	// USDTContractMain это USDT на основной сети TRON.
	USDTContractMain = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	// USDTContractNile это тестовый USDT, который выдаёт официальный Nile faucet.
	USDTContractNile = "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf"
)

// ContractSource фиксирует, откуда взят адрес контракта и когда он сверен.
type ContractSource struct {
	Address   string
	Source    string // первоисточник
	CheckedOn string // дата сверки, ГГГГ-ММ-ДД
	Note      string // чем подтверждено и что осталось проверить
}

// USDTSources описывает происхождение адресов контрактов.
var USDTSources = map[domain.Profile]ContractSource{
	domain.ProfileMain: {
		Address: USDTContractMain, Source: "https://tether.to/en/supported-protocols/ (Tron, TRC20)", CheckedOn: "2026-11-17",
		Note: "совпадает с официальной страницей Tether и Tronscan",
	},
	domain.ProfileTest: {
		Address: USDTContractNile, Source: "https://developers.tron.network/docs/getting-testnet-tokens-on-tron (официальная ссылка на Nile faucet); https://nileex.io/join/getJoinPage; https://tronprotocol.github.io/trident/quickstart/getting-started.html", CheckedOn: "2026-11-17",
		Note: "официальный Nile faucet выдаёт тестовый USDT этого контракта; Trident использует тот же адрес. Только тестовый токен, не mainnet USDT",
	},
}

// USDTDecimals это число знаков USDT; берётся из справочника domain.
const USDTDecimals = 6

// USDTContractAddress returns the profile-specific verified USDT contract.
func USDTContractAddress(p domain.Profile) (string, error) { address, err := usdtContract(p); if err != nil { return "", err }; return address.String(), nil }

// usdtContract возвращает адрес контракта USDT профиля.
func usdtContract(p domain.Profile) (Addr, error) {
	s := USDTContractMain
	if p == domain.ProfileTest {
		s = USDTContractNile
	}
	return ParseAddress(s)
}
