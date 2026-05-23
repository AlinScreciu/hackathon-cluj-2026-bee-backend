# Phase 8 — Spray Reports + Cascade Orchestration

Status: COMPLETE
Completed: 2026-05-23

## What was built

- `internal/services/cascade.go` — CascadeService with goroutine-per-dispatch, sync.Map timer management, mock/real notifier+pusher, all webhook handlers
- `internal/api/sprays.go` — all 5 spray endpoints implemented (POST, GET list, GET by ID, GET cascade-status, POST cancel); anfExport and getPrimariePDF remain 501 stubs for Phase 9
- `internal/api/alerts.go` — all 3 alert endpoints (list, get, confirm)
- `internal/api/webhooks_twilio.go` — 4 raw `http.HandlerFunc` handlers (voice gather, voice status, SMS inbound, SMS status); Huma stubs removed
- `internal/api/router.go` — cascade service wired, raw webhook chi routes registered, `NewRouter` returns `(http.Handler, func())` shutdown closure
- `cmd/server/main.go` — calls `cascadeShutdown()` before HTTP server shutdown

## Verification

- `POST /spray-reports` → 2 affected apiaries found, cascade goroutines launched, `[mock] voice call initiated` × 2, SMS fallback timer armed, unconfirmed timeout scheduled
- `GET /spray-reports/:id/cascade-status` → `{overall:"in_progress", total:2, pending:2}`
- `POST /alerts/:id/confirm` → `{ledger_hash:"..."}`, cascade status updates to confirmed=1
- `GET /events/verify` → `{valid:true, total_events:8}`
- Twilio voice gather (no digits) → initial gather TwiML with Romanian `<Say>`
- Twilio voice gather (Digits=1) → `Mulțumim! Confirmare înregistrată.` TwiML
- Twilio SMS inbound (Body=DA) → empty `<Response>` TwiML
- `go build ./... && go vet ./...` clean

## Notes

- `NewRouter` returns `(http.Handler, func())` not `(http.Handler, *services.CascadeService)` — cleaner coupling, main.go only needs a shutdown closure
- `stdlib.OpenDBFromPool(pool)` used everywhere pgxpool is passed to sqlc (pool doesn't implement PrepareContext)
- `LedgerService.Append(ctx, tx, ...)` called with the spray transaction's `*sql.Tx` so all DB writes (spray + dispatches + ledger events) are atomic
- Seeded parcels are all >7km from seeded apiaries; inserted test parcel at (46.587, 23.793) near Stupina Turda Nord for E2E testing
- GeoAI mock fires when server is started without `.env` sourced; use `source .env && go run ./cmd/server` or `make run` for real service
