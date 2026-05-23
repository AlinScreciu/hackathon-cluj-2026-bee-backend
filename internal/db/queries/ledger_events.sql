-- name: InsertLedgerEvent :one
INSERT INTO ledger_events (id, hash, prev_hash, type, actor_id, payload, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetLedgerEventByHash :one
SELECT * FROM ledger_events WHERE hash = $1 LIMIT 1;

-- name: GetNextLedgerEvent :one
SELECT * FROM ledger_events WHERE prev_hash = $1 LIMIT 1;

-- name: GetLastLedgerEvent :one
SELECT * FROM ledger_events ORDER BY created_at DESC LIMIT 1;

-- name: ListLedgerEventsOrdered :many
SELECT * FROM ledger_events ORDER BY created_at ASC;

-- name: ListLedgerEventsPaginated :many
SELECT * FROM ledger_events
WHERE ($1::text = '' OR type = $1)
  AND ($2::uuid IS NULL OR actor_id = $2)
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: CountLedgerEvents :one
SELECT COUNT(*) FROM ledger_events;
