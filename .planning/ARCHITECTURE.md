# Radarul Albinelor — Technical Architecture

## Request Flow (ASCII)

```
Browser / Mobile App / Twilio
         │
         │  HTTP
         ▼
  ┌──────────────┐
  │  chi Router  │  ← middleware: RequestID, Logger, Recover, CORS
  └──────┬───────┘
         │
         ├── /api/v1/healthz ──────────────────────── inline handler
         │
         ├── /api/v1/auth/* ─────────────────────────── Huma (no auth)
         │                                               └─ AuthService
         │
         ├── /api/v1/* (protected) ─────────────────── Huma
         │   RequireAuth middleware                      ├─ AuthService
         │   (cookie → JWT → *domain.User in ctx)        ├─ LedgerService
         │                                               ├─ CascadeService
         │                                               ├─ weather.CachedClient
         │                                               ├─ geoai.Client
         │                                               └─ PDFService
         │
         └── /webhooks/twilio/* ──────────────────── Raw chi (TwiML/XML)
                                                         └─ CascadeService

         All handlers → pgxpool.Pool → Postgres 16 (port 5433)
```

---

## Layer Breakdown

### `cmd/server/main.go`
Entry point. Loads config, connects DB pool, builds router, runs HTTP server with graceful shutdown (30s drain on SIGINT/SIGTERM). Also handles `--seed` flag for demo data.

### `internal/api/` — Handlers
One file per resource. All handlers are registered with `huma.Register(...)` except Twilio webhooks, which use raw `r.Post(...)` on the chi router. Handlers are thin: they validate the request (Huma does this automatically from struct tags), call a service method, and return a typed response struct.

Handler output structs must always be concrete types — no `map[string]any`. This enables Huma to generate correct OpenAPI schemas.

### `internal/services/` — Business Logic
Services own all application logic. They take `*pgxpool.Pool` and any external clients they need. All methods take `ctx context.Context` as the first argument.

Key services:
- **AuthService**: login flow, 2FA challenge dispatch, JWT issuance
- **LedgerService**: append events with advisory lock + hash chain
- **CascadeService**: the asynchronous notification engine
- **GeoService**: Haversine distance, downwind bearing calculation
- **PDFService**: primărie report generation

### `internal/external/` — Third-Party Clients
Each subdirectory is a thin wrapper over one external API. All HTTP clients must have a 10-second timeout (`context.WithTimeout` or `http.Client.Timeout`). These are injected into services — services do not import `net/http` directly for external calls.

### `internal/db/sqlc/` — Generated Data Layer
All DB access goes through sqlc-generated functions in `internal/db/sqlc/`. Never write raw SQL in service or handler code. Add new queries to `internal/db/queries/*.sql` and run `make sqlc-gen`.

---

## Cascade State Machine

Each `alert_dispatches` row tracks three parallel state machines:

### Push State
```
pending → sent → delivered → opened
```
Push is fire-and-forget. No confirmation feedback loop (browser push doesn't support read receipts reliably). State is set to `sent` when the web push request succeeds, `delivered`/`opened` via service worker postMessage if implemented in the frontend.

### Call State (T / T+ only)
```
queued → ringing → answered → confirmed
                            → no_input
                 → no_answer
         → busy
         → failed
skipped  (for T-)
```
Twilio webhook callbacks drive these transitions. The Twilio call SID is stored in `twilio_call_sid` for webhook correlation.

### SMS State
```
queued → sent → delivered → confirmed
                           → no_reply
       → failed
skipped (when call is confirmed before SMS fires)
```
For T-, SMS goes to `queued → sent` immediately. For T/T+, SMS starts as `queued` and the actual send is held by a 60-second timer in the goroutine. If the call is confirmed first, the timer is cancelled and SMS goes to `skipped`.

### Final Status (resolved state)
```
confirmed_call   — beekeeper pressed 1
confirmed_sms    — beekeeper replied "DA"
confirmed_app    — beekeeper clicked acknowledge in web app
unconfirmed      — no response after all channels exhausted
failed           — all channels failed technically
```

---

## Cascade Goroutine Lifecycle

```
CascadeService.Start(bgCtx, sprayID, dispatches)
  │
  └── for each dispatch:
        go launchDispatch(context.WithoutCancel(bgCtx), dispatch)
              │
              ├── defer recover() — catches any panic, logs, does not crash server
              │
              ├── sendPush(ctx, dispatch)
              │     └── webpush.Client.Send(...)
              │
              ├── if toxicity == T-:
              │     updateCallState(ctx, "skipped")
              │     sendSMS(ctx, dispatch)
              │     return
              │
              ├── makeVoiceCall(ctx, dispatch)
              │     └── twilio.Client.MakeCall(twimlURL)
              │           twimlURL = APP_BASE_URL + /webhooks/twilio/voice/gather?dispatch_id=...
              │
              └── startSMSFallbackTimer(ctx, dispatch, 60*time.Second)
                    └── time.AfterFunc(60s, func() {
                          if !isCallConfirmed(dispatch.ID) {
                              fireSMSFallback(bgCtx, dispatch)
                          }
                        })
```

**Critical:** `context.WithoutCancel(bgCtx)` is used because the HTTP handler returns (and its context cancels) before the cascade finishes. Without this, all goroutines are killed when the POST /spray-reports response is sent.

---

## Twilio Webhook Flow

```
Twilio → POST /webhooks/twilio/voice/gather?dispatch_id=X
  └── handler returns TwiML:
        <Response>
          <Play>APP_BASE_URL/uploads/voice/<hash>.mp3</Play>
          <Gather numDigits="1" action="/webhooks/twilio/voice/status?dispatch_id=X">
            <Say>Apăsați 1 pentru confirmare.</Say>
          </Gather>
        </Response>

User presses 1 → Twilio → POST /webhooks/twilio/voice/status?dispatch_id=X
  └── handler:
        CascadeService.HandleCallConfirmed(ctx, dispatchID)
          ├── cancel SMS timer
          ├── UPDATE alert_dispatches SET call_state='confirmed', final_status='confirmed_call'
          ├── LedgerService.Append(ctx, "call_confirmed", ...)
          └── return TwiML <Response><Say>Mulțumim!</Say></Response>

User replies "DA" to SMS → Twilio → POST /webhooks/twilio/sms/inbound
  └── handler:
        CascadeService.HandleSMSConfirmed(ctx, fromPhone)
          ├── lookup dispatch by phone
          ├── UPDATE alert_dispatches SET sms_state='confirmed', final_status='confirmed_sms'
          ├── LedgerService.Append(ctx, "sms_confirmed", ...)
          └── return TwiML <Response><Message>Confirmat!</Message></Response>
```

---

## Ledger Hash Chain Mechanics

```
Genesis row:
  prev_hash = NULL
  hash = SHA256("" + event_type + actor_id + payload + timestamp)

Subsequent rows:
  prev_hash = hash of previous row
  hash = SHA256(prev_hash + event_type + actor_id + payload + timestamp)
```

### Advisory Lock Pattern (in LedgerService.Append)
```go
tx, _ := pool.Begin(ctx)
tx.Exec(ctx, "SELECT pg_advisory_xact_lock(42)")   // serialises all appends
row := tx.QueryRow(ctx, "SELECT hash FROM ledger_events ORDER BY created_at DESC LIMIT 1")
// compute new hash
tx.Exec(ctx, "INSERT INTO ledger_events ...")
tx.Commit(ctx)
```

The lock is released automatically when the transaction commits or rolls back. Lock ID 42 is a project convention — all append operations use the same ID to form a global critical section.

### Verification (GET /ledger/verify)
1. Fetch all rows ordered by `created_at ASC`
2. For each row, recompute `SHA256(prev_hash + type + actor_id + payload + created_at)`
3. Compare against stored `hash`
4. If any mismatch → return the first tampered event index

---

## Key Design Decisions

### Why Huma over pure net/http or gin/echo
Huma generates OpenAPI 3.1 from Go struct tags at zero extra effort. With 37 endpoints and a hackathon timeline, hand-writing an OpenAPI spec was not viable. Huma also provides automatic request validation — handlers receive already-validated input.

### Why raw chi for Twilio webhooks (not Huma)
Twilio sends `application/x-www-form-urlencoded` POST bodies and expects `text/xml` (TwiML) responses. Huma's content negotiation middleware rejects non-JSON responses. Rather than fighting Huma's middleware, Twilio routes are registered directly on chi before Huma sees them.

### Why sqlc over GORM or sqlx
GORM hides SQL behind an ORM abstraction that makes complex queries (advisory locks, CTEs, window functions) awkward. sqlx reduces boilerplate but still requires manual `rows.Scan`. sqlc generates type-safe functions from raw SQL — zero runtime reflection, compile-time safety, and full SQL expressiveness.

### Why TEXT for toxicity, not PG ENUM
PostgreSQL ENUMs with values like 'T-' and 'T' cause sqlc to generate Go constants with the same name (`ToxicityLevelT` for both). Using TEXT + CHECK constraint avoids this while still enforcing valid values at the DB level.

### Why google/uuid and not gofrs/uuid
sqlc's default UUID type is `github.com/google/uuid`. Mixing both UUID packages in the same codebase causes type mismatches where sqlc-generated functions return `uuid.UUID` (google) but application code might pass `uuid.UUID` (gofrs). Standardising on google/uuid throughout eliminates this class of bug. gofrs/uuid remains in go.mod as an indirect dependency only.

### Why context.WithoutCancel for cascade goroutines
The HTTP handler returns the 201 response and Go's HTTP server cancels the request context. Any goroutine still holding that context will see `ctx.Err() != nil` immediately. `context.WithoutCancel` creates a copy of the context values (user, logger) with cancellation stripped, so cascade goroutines can run to completion across multiple minutes without being killed by the HTTP lifecycle.

### Why pg_advisory_xact_lock for the ledger
The ledger requires strictly sequential hashes. Without serialisation, two concurrent requests can both read the same "latest hash", compute different new hashes with the same `prev_hash`, and produce a forked chain. A DB-level advisory lock is simpler than a Go-level mutex (which would not work across multiple server instances) and cheaper than a `SERIALIZABLE` transaction for a single-row read + insert pattern.
