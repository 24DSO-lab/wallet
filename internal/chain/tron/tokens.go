package tron

import "github.com/24DSO-lab/wallet/internal/domain"

// Адреса контрактов USDT (TRC20). Менять только отдельным PR с двумя
// проверяющими: подмена этой константы подменяет токен, который кошелёк
// считает USDT (ADR, «Правила работы с провайдерами»). Тест
// TestUSDTContractPinned падает при любой правке.
//
// Сверка адресов выполнена 2026-11-16 (см. USDTSources). Main подтверждён
// страницей Tether Supported Protocols, tron.network/usdt и Tronscan. Nile:
// Tether токен в Nile не выпускает, адрес это тестовый USDT, который
// перечисляют документация BVNK и Turnkey; перед гейтом сверить с краном Nile
// (токены, которые он выдаёт, должны быть именно этим контрактом).
const (
	// USDTContractMain это USDT на основной сети TRON.
	USDTContractMain = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	// USDTContractNile это тестовый USDT в сети Nile. Tether не выпускает
	// официальный токен в Nile, адрес берётся из справочника Nile.
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
		Address: USDTContractMain, Source: "https://tether.to/en/supported-protocols/ (раздел Tron, TRC20)", CheckedOn: "2026-11-16",
		Note: "совпадает на странице Tether, tron.network/usdt и Tronscan",
	},
	domain.ProfileTest: {
		Address: USDTContractNile, Source: "тестовый USDT сети Nile; документация BVNK и Turnkey, кран Nile", CheckedOn: "2026-11-16",
		Note: "официального источника Tether для Nile нет; сверить с токеном из крана Nile перед гейтом",
	},
}

// USDTDecimals это число знаков USDT; берётся из справочника domain.
const USDTDecimals = 6

// usdtContract возвращает адрес контракта USDT профиля.
func usdtContract(p domain.Profile) (Addr, error) {
	s := USDTContractMain
	if p == domain.ProfileTest {
		s = USDTContractNile
	}
	return ParseAddress(s)
}
