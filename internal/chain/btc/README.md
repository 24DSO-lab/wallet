# Bitcoin adapter

Пакет реализует Bitcoin-часть `ports.ChainAdapter`. Сеть выбирается глобальным
профилем приложения, а не отдельно для каждого кошелька.

## Test profile

Для профиля `test` используется **Bitcoin Testnet4** через Esplora. Это отдельная сеть от Testnet3; общий префикс адресов `tb1` не означает, что сети взаимозаменяемы:

- Primary: `https://mempool.space/testnet4/api`
- Fallback: `https://blockstream.info/testnet4/api`

Оба адреса уже заданы в `internal/config`. Адреса BIP84 и coin type 1 остаются совместимыми; при переключении Testnet3 → Testnet4 менять seed, xpub или уже выданные адреса не требуется. Пакет не создаёт собственный
`http.Client`: все запросы идут через `provider.Client`, который отвечает
за таймауты, повторы чтения, fallback, rate limiting и очистку ошибок.

## Таймауты и нагрузка

В composition root Esplora настроен с таймаутом 5 секунд на HTTP-попытку,
одной попыткой на endpoint и fallback на второй endpoint; для публичного
Esplora это намеренно предпочитает быстрый failover повторным запросам при
rate limit. Между запросами действует интервал 150 мс. Receive и change
сканируются параллельно, до восьми адресов на каждую ветку; результаты
обрабатываются в порядке derivation index. Детали использованных адресов
(UTXO и первая страница истории) запускаются одновременно; последующие
страницы истории читаются последовательно. Одновременно обрабатывается не
более восьми использованных адресов на ветку. Wails ограничивает получение нового
receive-адреса 30 секундами, полный refresh — 45 секундами. Общий provider поддерживает повтор идемпотентных
GET/POST-чтений с backoff; BTC использует по одной попытке на каждый Esplora
endpoint и проверяет fallback.

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

## Интеграция HD

`NewAdapter(api)` подключает сканер и spending pipeline к публичному BIP32
дериватору из `internal/hd`. Адаптер не содержит собственной копии BIP32 и
не получает seed или приватные ключи. Полная цепочка подписи описана ниже.


## Spending pipeline

`NewAdapter(api)` builds the BTC `ports.ChainAdapter` around the shared Esplora
client and public-only BIP32 deriver. `Prepare` refreshes UTXOs, applies the
deterministic coin selector, uses the 3-block sat/vB estimate, and serializes a
native P2WPKH transaction with RBF sequence `0xfffffffd`. Each input gets a
BIP143 `SIGHASH_ALL` digest.

The JSON draft is a transport envelope around the unsigned transaction and
its prevout/path metadata. `Verify` decodes it afresh, enforces canonical
encoding, derives every input and change address again from the account xpub,
rebuilds the unsigned serialization and BIP143 requests, and checks the
confirmation summary against the parsed transaction. `Finalize` independently
recomputes digests, verifies each secp256k1 signature and derived compressed
public key, then assembles the P2WPKH witness. `Broadcast` checks the txid
locally before its single non-idempotent POST; `Status` reports pending or
confirmed state and confirmation depth.

The adapter never receives seed or private keys. Signing requests are passed
to `ports.Keystore.Sign`, which derives the private key and checks the
expected address before signing. RBF is enabled from the first version.


## BIP125 incremental relay fee policy

The RBF adapter uses an explicit incremental relay fee rate in sat/vB. The
default is Bitcoin Core's default policy of 1 sat/vB; this is not guaranteed
for every Esplora backend or node. Set `BTC_INCREMENTAL_RELAY_FEE_RATE` to
the actual target node's `incrementalrelayfee` policy when it differs. The
value is validated at startup and the replacement verifier independently
checks the absolute-fee increment against a conservative replacement vsize.
