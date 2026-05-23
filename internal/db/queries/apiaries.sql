-- name: CreateApiary :one
INSERT INTO apiaries (id, owner_id, name, type, lat, lng, hive_count, start_date, end_date, notes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetApiary :one
SELECT * FROM apiaries WHERE id = $1 LIMIT 1;

-- name: ListApiariesByOwner :many
SELECT * FROM apiaries WHERE owner_id = $1 ORDER BY created_at DESC;

-- name: ListAllApiaries :many
SELECT * FROM apiaries ORDER BY created_at DESC;

-- name: UpdateApiary :one
UPDATE apiaries
SET name = $2, type = $3, lat = $4, lng = $5, hive_count = $6,
    start_date = $7, end_date = $8, notes = $9
WHERE id = $1
RETURNING *;

-- name: UpdateApiaryLedgerHash :exec
UPDATE apiaries SET ledger_hash = $2 WHERE id = $1;
