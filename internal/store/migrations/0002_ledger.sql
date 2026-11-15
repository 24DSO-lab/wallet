-- Этап 2: балансы, UTXO, история.
--
-- Суммы это текст из десятичных цифр (минимальные единицы): ETH в wei не
-- помещается в int64, а REAL для денег запрещён. CHECK не пускает в колонку
-- ничего, кроме цифр, даже если ошибётся код.

CREATE TABLE balance (
    wallet_id  TEXT    NOT NULL REFERENCES wallet (id) ON DELETE CASCADE,
    asset      TEXT    NOT NULL CHECK (asset <> ''),
    confirmed  TEXT    NOT NULL CHECK (confirmed <> '' AND confirmed NOT GLOB '*[^0-9]*'),
    pending    TEXT    NOT NULL CHECK (pending <> '' AND pending NOT GLOB '*[^0-9]*'),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (wallet_id, asset)
) STRICT, WITHOUT ROWID;

-- Только BTC. Адрес хранится строкой, как в domain.UTXO.
CREATE TABLE utxo (
    wallet_id     TEXT    NOT NULL REFERENCES wallet (id) ON DELETE CASCADE,
    txid          TEXT    NOT NULL CHECK (txid <> ''),
    vout          INTEGER NOT NULL CHECK (vout >= 0),
    address       TEXT    NOT NULL,
    amount        TEXT    NOT NULL CHECK (amount <> '' AND amount NOT GLOB '*[^0-9]*'),
    confirmations INTEGER NOT NULL CHECK (confirmations >= 0),
    spent         INTEGER NOT NULL CHECK (spent IN (0, 1)),
    PRIMARY KEY (wallet_id, txid, vout)
) STRICT, WITHOUT ROWID;

CREATE INDEX utxo_address ON utxo (wallet_id, address);

-- Таблица transaction из ADR названа tx: TRANSACTION это ключевое слово SQL.
-- Естественный ключ записи (wallet_id, asset, txid); id присваивает Store.
CREATE TABLE tx (
    id           TEXT    NOT NULL PRIMARY KEY,
    wallet_id    TEXT    NOT NULL REFERENCES wallet (id) ON DELETE CASCADE,
    asset        TEXT    NOT NULL CHECK (asset <> ''),
    txid         TEXT    NOT NULL CHECK (txid <> ''),
    direction    TEXT    NOT NULL,
    amount       TEXT    NOT NULL CHECK (amount <> '' AND amount NOT GLOB '*[^0-9]*'),
    fee          TEXT    NOT NULL CHECK (fee <> '' AND fee NOT GLOB '*[^0-9]*'),
    fee_asset    TEXT    NOT NULL DEFAULT '',
    status       TEXT    NOT NULL,
    block_height INTEGER NOT NULL DEFAULT 0 CHECK (block_height >= 0),
    ts           INTEGER NOT NULL,
    counterparty TEXT    NOT NULL DEFAULT '',
    UNIQUE (wallet_id, asset, txid)
) STRICT;

-- Выборки истории: по кошельку и по кошельку с активом, новые первыми.
CREATE INDEX tx_wallet_ts ON tx (wallet_id, ts DESC);
CREATE INDEX tx_wallet_asset_ts ON tx (wallet_id, asset, ts DESC);
