-- Этап 1: кошельки, адреса, настройки.
--
-- Схема не знает ни числа кошельков, ни списка активов и сетей: актив и сеть
-- это обычный текст без CHECK на конкретные значения. Справочник живёт в коде
-- (domain.Asset.Info), число кошельков ограничивает интерфейс (config.MaxWallets).
--
-- Времена хранятся целым числом миллисекунд Unix (UTC). Таблицы STRICT:
-- SQLite сам отвергает значение не того типа.

CREATE TABLE wallet (
    id          TEXT    NOT NULL PRIMARY KEY CHECK (id <> ''),
    name        TEXT    NOT NULL,
    created_at  INTEGER NOT NULL,
    keystore_id TEXT    NOT NULL CHECK (keystore_id <> ''),
    btc_xpub    TEXT    NOT NULL DEFAULT '', -- публичный ключ, не секрет; доступа к средствам не даёт
    sort_order  INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX wallet_sort ON wallet (sort_order, created_at, id);

-- Естественный ключ адреса (wallet_id, network, address): повторное
-- сканирование не создаёт дублей. У BTC адресов много, у ETH и TRON по одному.
CREATE TABLE address (
    wallet_id TEXT    NOT NULL REFERENCES wallet (id) ON DELETE CASCADE,
    network   TEXT    NOT NULL CHECK (network <> ''),
    address   TEXT    NOT NULL CHECK (address <> ''),
    path      TEXT    NOT NULL,
    chain     TEXT    NOT NULL, -- receive или change
    idx       INTEGER NOT NULL CHECK (idx >= 0),
    used      INTEGER NOT NULL DEFAULT 0 CHECK (used IN (0, 1)),
    PRIMARY KEY (wallet_id, network, address)
) STRICT, WITHOUT ROWID;

CREATE INDEX address_chain ON address (wallet_id, network, chain, idx);

CREATE TABLE setting (
    key   TEXT NOT NULL PRIMARY KEY CHECK (key <> ''),
    value TEXT NOT NULL
) STRICT, WITHOUT ROWID;
