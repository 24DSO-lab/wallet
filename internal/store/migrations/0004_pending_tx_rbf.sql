-- Этап 4: сохраняем исходный unsigned draft и статус приёма сетью,
-- чтобы можно было безопасно заменить ещё не подтверждённую BTC-транзакцию.
ALTER TABLE pending_tx ADD COLUMN draft BLOB NOT NULL DEFAULT X'';
ALTER TABLE pending_tx ADD COLUMN broadcast_accepted INTEGER NOT NULL DEFAULT 0 CHECK (broadcast_accepted IN (0, 1));
ALTER TABLE pending_tx ADD COLUMN replaces_txid TEXT NOT NULL DEFAULT '';
