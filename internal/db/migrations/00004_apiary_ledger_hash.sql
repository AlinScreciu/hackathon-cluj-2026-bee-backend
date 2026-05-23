-- +goose Up
ALTER TABLE apiaries ADD COLUMN IF NOT EXISTS ledger_hash TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE apiaries DROP COLUMN IF EXISTS ledger_hash;
