# Phase 6 — Ledger Service
Status: COMPLETE
Completed: 2026-05-23

## What was built
- internal/db/migrations/00004_apiary_ledger_hash.sql — adds ledger_hash TEXT NOT NULL DEFAULT '' to apiaries
- internal/db/queries/apiaries.sql — fixed UpdateApiaryLedgerHash (was no-op, now actually stores hash)
- internal/db/queries/ledger_events.sql — added ListLedgerEventsByType query for type-only filtering
- internal/services/ledger.go — LedgerService: Append (advisory lock 42, sha256 chain), List, GetByHash, Verify
- internal/api/ledger.go — GET /events, GET /events/:hash, GET /events/verify fully implemented
- internal/api/apiaries.go — PATCH /apiaries/:id implemented with atomic ledger event; mapApiary uses row.LedgerHash
- internal/api/router.go — LedgerService instantiated and injected into Handlers

## Verification
- PATCH apiary → ledger_hash in response (64-char hex)
- Two PATCHes → GET /events/verify returns valid:true with total_events:2
- GET /events → lists both events
- GET /events/:hash → returns event + chain with prev/next hashes
- go build ./... + go vet ./... clean

## Notes
- DBTX is database/sql-style; transactions use *sql.Tx from stdlib.OpenDBFromPool(pool).BeginTx()
- ListLedgerEventsPaginated has a zero-UUID bug (actor filter always on when uuid.UUID{} passed via stdlib driver); added ListLedgerEventsByType as workaround for type-only filtering
- updated from UpdateApiary does not include new ledgerHash; PATCH handler overrides resp.LastLedgerHash explicitly after Append
