-- Этап 3 (A6): подписанная транзакция до подтверждения приёма сетью. Это не
-- секрет: raw уже готов к отправке в сеть.

CREATE TABLE pending_tx (
    txid       TEXT    NOT NULL PRIMARY KEY CHECK (txid <> ''),
    wallet_id  TEXT    NOT NULL REFERENCES wallet (id) ON DELETE CASCADE,
    asset      TEXT    NOT NULL CHECK (asset <> ''),
    network    TEXT    NOT NULL CHECK (network <> ''),
    raw        BLOB    NOT NULL CHECK (length(raw) > 0),
    created_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX pending_tx_wallet ON pending_tx (wallet_id);
