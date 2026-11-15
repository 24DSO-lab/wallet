package tron

import "github.com/24DSO-lab/wallet/internal/domain"

// Адреса контрактов USDT (TRC20). Менять только отдельным PR с двумя
// проверяющими: подмена этой константы подменяет токен, который кошелёк
// считает USDT (ADR, «Правила работы с провайдерами»). Тест
// TestUSDTContractPinned падает при любой правке.
//
// ВАЖНО (перед слиянием): значения ниже занесены без сверки с живыми
// источниками, потому что в среде разработки этого PR нет доступа к
// tether.to и nile.tronscan.org. Владелец PR обязан сверить каждое значение
// и заполнить Source и CheckedOn реальными данными. Пока CheckedOn пуст,
// TestUSDTSourcesRecorded падает.
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
	Source    string // URL официальной документации
	CheckedOn string // дата сверки, ГГГГ-ММ-ДД; пусто, пока не сверено
}

// USDTSources описывает происхождение адресов контрактов.
var USDTSources = map[domain.Profile]ContractSource{
	domain.ProfileMain: {Address: USDTContractMain, Source: "https://tether.to/en/supported-protocols/ (Tron TRC20)", CheckedOn: ""},
	domain.ProfileTest: {Address: USDTContractNile, Source: "https://nile.tronscan.org (справочник токенов Nile)", CheckedOn: ""},
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
