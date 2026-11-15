# Bitcoin adapter

Пакет реализует Bitcoin-часть `ports.ChainAdapter`. Сеть выбирается глобальным
профилем приложения, а не отдельно для каждого кошелька.

## Test profile

Для профиля `test` используется Bitcoin testnet через Esplora:

- Primary: `https://mempool.space/testnet/api`
- Fallback: `https://blockstream.info/testnet/api`

Оба адреса уже заданы в `internal/config`. Пакет не создаёт собственный
`http.Client`: все запросы идут через `provider.Client`, который отвечает
за таймауты, повторы чтения, fallback, rate limiting и очистку ошибок.

## Реализованные строительные блоки

- `ValidateAddress`: Bech32 checksum и native P2WPKH (BIP84), HRP `bc` для
  mainnet и `tb` для test profile. Другие типы скриптов намеренно не принимаются.
- `Esplora`: статистика адреса, UTXO, история с пагинацией, fee estimates,
  получение транзакции/статуса и broadcast.
- `ConservativeFeeRate`: оценка для цели в три блока с безопасным fallback
  на наиболее быстрый доступный target.
- `SelectCoins`: детерминированный bounded-search выбор P2WPKH UTXO, расчёт
  размера транзакции, сдача и порог пыли 294 sat.
- `Scanner.Refresh`: сканирование receive/change с gap limit 20, сбор баланса,
  UTXO и истории; HD-деривация подключается через интерфейс `AddressDeriver`.

## Broadcast safety

Отправка raw transaction выполняется неидемпотентным POST без повторов.
Если соединение оборвалось после отправки, результат неизвестен: перед
повторной отправкой нужно сверить txid через Esplora.

## Оставшаяся интеграция

`Scanner.Refresh` уже реализует сканирование BIP84 receive/change с gap limit 20
через интерфейс `AddressDeriver`. Для production-интеграции нужен тонкий адаптер
к согласованному публичному API `internal/hd`; пакет не реализует собственную
копию BIP32.
Полный spending pipeline (BIP143, Verify, Finalize и RBF) остаётся следующим
этапом.
