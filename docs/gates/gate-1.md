# Gate 1 — каркас и ключи

Дата фиксации технических доказательств: 17 ноября 2026.
Проверенный commit: `46f530397b94c1ebbe5ee4b495fab7894ea5dfb5`.

## Статус

- **TECHNICAL EVIDENCE: PASS** — production-адаптеры BTC, ETH и TRON сверяются с опубликованными независимыми тест-векторами; CI для проверенного commit завершился успешно.
- **OWNER SIGN-OFF: PENDING** — гейт не считается формально закрытым до явной приёмки владельцем.

Это проверки детерминированного вывода публичных тестовых векторов. Приведённая мнемоника и passphrase опубликованы как тестовые данные и не являются секретами пользовательского кошелька. Не использовать их для хранения средств.

## Автоматизированные проверки

Тестовый файл: `internal/hd/adapter_acceptance_test.go`.

- `TestProductionAdaptersMatchIndependentAddressVectors` проверяет production-адаптеры на публичной мнемонике BIP39 `abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about` без passphrase.
- `TestProductionAdaptersMatchIndependentPassphraseVectors` проверяет те же production-адаптеры на опубликованном варианте с passphrase `TREZOR`.
- BTC проверяется через production `btc.HDAddressDeriver`, включая адреса, BIP84 derivation paths и `btc.ValidateAddress`.
- ETH и TRON проверяются через production `eth.Adapter` и `tron.Adapter`, то есть не только через тестовую реализацию derivation.

### Ожидаемые адреса

| Публичный тест-вектор | BTC mainnet (receive 0) | BTC testnet (receive 0) | ETH | TRON |
| --- | --- | --- | --- | --- |
| BIP39, пустая passphrase | `bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu` | `tb1q6rz28mcfaxtmd6v789l9rrlrusdprr9pqcpvkl` | `0x9858EfFD232B4033E47d90003D41EC34EcaEda94` | `TUEZSdKsoDHQMeZwihtdoBiN46zxhGWYdH` |
| BIP39, passphrase `TREZOR` | `bc1qv5rmq0kt9yz3pm36wvzct7p3x6mtgehjul0feu` | `tb1qfwxzlmn42rh6wu6gt4f0yrfpe4gf0jtwgxagrr` | `0x9c32F71D4DB8Fb9e1A58B0a80dF79935e7256FA6` | `TAyDUYP5rcf56xFwrg8cU1qQwvnWpkeapM` |

BTC paths:
- Mainnet receive 0: `m/84'/0'/0'/0/0`
- Mainnet receive 1: `m/84'/0'/0'/0/1`
- Mainnet change 0: `m/84'/0'/0'/1/0`
- Testnet receive 0: `m/84'/1'/0'/0/0`

Для варианта `TREZOR` тест также проверяет BTC mainnet/testnet receive 0 и соответствующие пути.

## Независимые опубликованные источники векторов

- [BIP-84 — официальная спецификация Bitcoin](https://bitcoin.org/en/bip-0084/) — опубликованные BIP84 mnemonic/address vectors и пути.
- [BitTool Signer — Passphrase recovery vectors](https://bittool.com/docs/recovery/passphrase/) — опубликованные адреса BTC, EVM и TRON для тестовой мнемоники с пустой passphrase и `TREZOR`.
- [Nethereum Wallet documentation](https://docs.nethereum.com/docs/wallet-sdk/nethereum-wallet/) — независимая документация EVM HD wallet и пути `m/44'/60'/0'/0/{index}`.
- [TRON Wallet Developer Guide](https://developers.tron.network/docs/wallet-developer-guide) — путь derivation для coin type 195.

Сверка с опубликованными векторами — независимая проверка ожидаемых результатов, но не заявление о криптографической сертификации или аудите реализации.

## CI

[GitHub Actions run #324](https://github.com/24DSO-lab/wallet/actions/runs/44008717494), commit `46f530397b94c1ebbe5ee4b495fab7894ea5dfb5`:

| Job | Result |
| --- | --- |
| lint | PASS |
| frontend | PASS |
| go (windows-latest) | PASS |
| go (ubuntu-latest) | PASS |

Ubuntu job выполняет Go race-тесты; workflow также проверяет frontend и Playwright, lint, Windows build и проверки безопасности.

## Что ещё не подтверждено

Этот результат не подтверждает:
- фактические балансы и историю в BTC testnet, Ethereum Sepolia и TRON Nile;
- отправку и подтверждение тестовых BTC, ETH, USDC и USDT транзакций;
- восстановление кошелька из seed на чистом профиле/установке.

Эти проверки относятся к гейтам 2, 3 и 5 и должны выполняться отдельно. Mainnet остаётся заблокированным до закрытия обязательных гейтов 1, 2, 3 и 5.
