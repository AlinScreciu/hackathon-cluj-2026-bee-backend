# Radarul Albinelor API — Claude Code Guide

**"Bee Radar"** — Romanian government-grade pesticide notification system for beekeepers.
When a farmer schedules a spray, all nearby beekeepers are alerted via web push → voice call → SMS fallback.
All state changes are written to a tamper-evident SHA256 hash-chain ledger.
Built for Cluj Hackathon 2026.

Module: `github.com/radarul-albinelor/api`
Go version: 1.25 (go.mod says 1.25.0; toolchain resolves to 1.22+ features)

---

## Quick Start

```bash
cp .env.example .env          # fill in secrets (see Env Vars section)
make db-up                    # postgres:16 on port 5433 (5432 is taken)
make migrate-up               # apply 3 migrations
make sqlc-gen                 # regenerate internal/db/sqlc/ (gitignored)
make seed                     # load demo users + reference data
make run                      # go run ./cmd/server  →  :9090
```

For Twilio webhook testing:
```bash
make tunnel                   # cloudflared → copy HTTPS URL into APP_BASE_URL
```

---

## Key Commands

| Command | What it does |
|---|---|
| `make run` | Start server on :9090 |
| `make build` | Build binary `./radarul-api` |
| `make test` | `go test ./... -v -count=1` |
| `make lint` | `go vet ./...` |
| `make db-up` | Start Postgres Docker container |
| `make db-down` | Stop Postgres Docker container |
| `make migrate-up` | Apply pending goose migrations |
| `make migrate-down` | Roll back last migration |
| `make migrate-new NAME=foo` | Create new migration file |
| `make sqlc-gen` | Regenerate `internal/db/sqlc/` from queries |
| `make seed` | Populate demo data (`go run ./cmd/server --seed`) |
| `make gen-vapid` | Print new VAPID key pair |
| `make tunnel` | Start cloudflared tunnel for Twilio webhooks |
| `make demo-spray` | Run automated E2E demo script |

Database connection string (default): `postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable`

---

## Folder Structure

```
cmd/server/main.go              entry point, graceful shutdown
internal/
  api/                          Huma handlers — one file per resource
    router.go                   NewRouter, all wiring, writes openapi.json
    auth.go                     POST /auth/login, POST /auth/2fa/verify, GET /auth/me
    apiaries.go                 CRUD for beekeepers' apiaries
    parcels.go                  CRUD for farmers' parcels
    sprays.go                   POST spray-reports, cascade status polling
    alerts.go                   Beekeeper alert management + in-app actions
    damage.go                   Damage claims + photo upload
    inspector.go                Inspector map view, farmer/apiary details
    ledger.go                   Ledger event list + verify
    push.go                     Web push subscription management
    reference.go                GET /substances, GET /weather
    webhooks_twilio.go          Raw chi handlers (TwiML, NOT Huma)
  config/config.go              Env var struct (caarlos0/env)
  db/
    migrations/                 goose SQL (00001_init, 00002_substances, 00003_indexes)
    queries/                    sqlc SQL source files
    sqlc/                       GENERATED — gitignored, run make sqlc-gen
  domain/
    enums.go                    All typed string constants (Role, Toxicity, states…)
    entities.go                 All domain structs (User, Apiary, SprayReport…)
  services/                     Business logic — stubs filled as phases complete
    auth.go                     AuthService (login, 2FA, verify)
    cascade.go                  CascadeService (THE critical async notification engine)
    ledger.go                   LedgerService (SHA256 hash chain, advisory lock)
    geo.go                      Haversine distance + downwind bearing math
    pdf.go                      PDF generation for primărie reports
    seed.go                     Demo data seeding
  external/                     Third-party clients — stubs filled as phases complete
    twilio/                     Twilio REST (voice calls, SMS)
    elevenlabs/                 ElevenLabs TTS (Romanian MP3)
    webpush/                    Web push via VAPID
    weather/                    Open-Meteo adapter + 10-min cache
    email/                      go-mail SMTP (Resend)
    geoai/                      AI geo assessment client + mock
  middleware/
    auth.go                     RequireAuth (cookie→ctx), RequireRole (403)
    cors.go                     CORS via rs/cors
    recover.go                  Panic recovery → 500 JSON
  platform/
    jwt.go                      JWTService Sign/Verify (HS256, 24h)
.planning/
  phases/                       phase-N-plan.md (pending) / phase-N.md (complete)
  RESUME.md                     How to resume work
  STACK.md                      Full dependency reference
  IDEA.md                       Product concept and user roles
  ARCHITECTURE.md               Request flow, cascade state machine, ledger mechanics
API_CONTRACT.MD                 Source of truth for all endpoint types
sqlc.yaml                       sqlc configuration
docker-compose.yml              postgres:16, port 5433
```

---

## Handlers Struct

`internal/api/router.go` — expand as each service is implemented:

```go
type Handlers struct {
    cfg      *config.Config
    pool     *pgxpool.Pool
    jwt      *platform.JWTService
    authSvc  *services.AuthService     // DONE Phase 4
    ledger   *services.LedgerService   // add Phase 6
    cascade  *services.CascadeService  // add Phase 8
    weather  *weather.CachedClient     // add Phase 7
    geoai    geoai.Client              // add Phase 7
    pdf      *services.PDFService      // add Phase 9
    email    *email.EmailClient        // DONE Phase 4
}
```

---

## Auth Flow

1. `POST /auth/login` → create `auth_challenges` row (6-digit code, bcrypt-hashed, 10-min expiry), dispatch via chosen method, return `challenge_id`
2. `POST /auth/2fa/verify` → bcrypt compare → sign JWT → set `ra_session` cookie (HttpOnly, SameSite=Lax, Path=/, MaxAge=86400)
3. Protected routes → `RequireAuth` reads cookie, verifies JWT, injects `*domain.User` into context
4. `middleware.UserFromContext(ctx)` → `*domain.User{ID, Role, CNP}`

---

## Cascade Flow

1. `POST /spray-reports` → validate → `geoai.Assess` → Haversine radius find → DB transaction (spray_report + alert_dispatches + ledger events) → commit → `cascade.Start(bgCtx, sprayID, dispatches)`
2. `cascade.Start` → goroutine per dispatch: `launchDispatch`
3. `launchDispatch` → send push notification → branch by toxicity:
   - **T- (low):** set `call_state = "skipped"`, send SMS directly (no wait, SMS is primary alongside push)
   - **T / T+ (med/high):** make Twilio voice call + start 60s SMS fallback timer
4. Twilio calls `POST /webhooks/twilio/voice/gather` → return TwiML with ElevenLabs MP3 URL + `<Gather>`
5. User presses 1 → Twilio calls back → `HandleCallConfirmed` → cancel SMS timer, set `final_status = "confirmed_call"`, append ledger event
6. If 60s timer fires and no confirmation → `fireSMSFallback` → `SendSMS`
7. User replies "DA" to SMS → `POST /webhooks/twilio/sms/inbound` → `HandleSMSConfirmed`

---

## Critical Rules — Follow in Every Phase

**1. `context.WithoutCancel` in cascade goroutines**
HTTP request context cancels when the response is sent. All cascade goroutines must use `context.WithoutCancel(ctx)` (Go 1.21+) or they get killed mid-flight.

**2. Ledger advisory lock**
`LedgerService.Append` must call `SELECT pg_advisory_xact_lock(42)` inside the transaction before reading the chain tip. Without this, concurrent requests create duplicate `prev_hash` values and corrupt the chain.

**3. Wind direction convention**
Meteorological wind direction = direction FROM which wind blows. To check if apiary is downwind: wind blows TOWARD `(windDirDeg + 180) mod 360`. Compare that bearing against the spray→apiary vector bearing.

**4. CNP is PII**
Never log CNP. Never return it in API responses except for the authenticated user themselves (`GET /auth/me`). Mask in all other contexts.

**5. Toxicity is TEXT not PG ENUM**
DB column: `TEXT CHECK (toxicity IN ('T-','T','T+'))`. sqlc generates it as `string`. Use domain constants: `domain.ToxicityLow = "T-"`, `domain.ToxicityMed = "T"`, `domain.ToxicityHigh = "T+"`.

**6. UUID type: `github.com/google/uuid` throughout**
sqlc is configured for `google/uuid`. All application code must use the same package. `uuid.New()` for new IDs. Do not mix in `gofrs/uuid`.

**7. Ledger is INSERT-only**
Never write UPDATE or DELETE queries for `ledger_events`. The table is append-only by convention.

**8. Twilio webhooks bypass Huma**
Register Twilio handlers as raw chi routes (`r.Post(...)`), NOT as Huma operations. They return TwiML (XML) and Huma doesn't handle XML content negotiation.

**9. T- cascade skips voice call**
For `toxicity == "T-"`, set `call_state = "skipped"` immediately. Send push + SMS as co-primary channels (no 60s wait, SMS is not a fallback for T-).

**10. ElevenLabs model**
Always use `eleven_multilingual_v2`. Never strip Romanian diacritics from TTS text (ș, ț, ă, â, î must be preserved).

**11. One file per resource in `internal/api/`**
Do not consolidate handlers. Each resource has its own file.

**12. All handler responses use typed Huma structs**
No `map[string]any` in responses. Define proper input/output structs for every handler.

**13. Every service method takes `ctx` first**
Signature: `func (s *Service) Method(ctx context.Context, ...) (..., error)`

**14. Every external call has a 10-second timeout**
Use `context.WithTimeout` or set timeouts on the `http.Client`. No unbounded external calls.

**15. Cascade goroutine panics are recovered**
Every goroutine launched in `cascade.go` must have:
```go
defer func() {
    if r := recover(); r != nil {
        slog.Error("cascade panic", "recover", r)
    }
}()
```

**16. Use `stdlib.OpenDBFromPool` to get `*sql.DB` from `*pgxpool.Pool`**
`*pgxpool.Pool` does NOT implement `DBTX` directly. To use sqlc-generated queries, call
`stdlib.OpenDBFromPool(pool)` (from `github.com/jackc/pgx/v5/stdlib`) to obtain a `*sql.DB`,
then pass it to `dbsqlc.New(db)`. Import path: `github.com/jackc/pgx/v5/stdlib`.

**17. Error sentinel for "not found" is `sql.ErrNoRows` (not `pgx.ErrNoRows`)**
When using the `stdlib` adapter (`stdlib.OpenDBFromPool`), the database/sql layer wraps pgx errors.
Check for `errors.Is(err, sql.ErrNoRows)`, NOT `pgx.ErrNoRows`.

**18. Chi group middleware does NOT apply to Huma-registered routes**
Huma registers operations directly on the root chi router, bypassing any `r.Group(...)` or
`r.Use(...)` middleware added after Huma setup. Auth is enforced via a passive session middleware
on the root router (injects `*domain.User` into context if cookie is valid) plus per-handler
`middleware.UserFromContext(ctx)` guards that return 401/403 explicitly.

**19. `domain.User.ID` is `string`, not `uuid.UUID`**
The `domain.User` struct stores `ID` as a plain `string` (it comes from the JWT claims).
Any handler that passes `user.ID` to a sqlc parameter that expects `uuid.UUID` must call
`uuid.Parse(user.ID)` first and return a 401 on error.

---

## Seeded Demo Users (after `make seed`)

| CNP           | Password  | Role      | Name                    |
|---------------|-----------|-----------|-------------------------|
| 1850101123456 | parola123 | apicultor | Andrei Berar            |
| 2900215654321 | parola123 | apicultor | Maria Costea            |
| 1780530987654 | parola123 | apicultor | Ioan Lupu               |
| 1920412111222 | parola123 | fermier   | Vasile Mureșan          |
| 2880721333444 | parola123 | fermier   | Elena Popa              |
| 1751103555666 | parola123 | fermier   | Gheorghe Stan           |
| 1680808777888 | parola123 | inspector | Inspector Județean Cluj |

---

## Environment Variables

Copy `.env.example` → `.env` and fill in:

| Variable | Required | Notes |
|---|---|---|
| `PORT` | no | default 8080 |
| `APP_BASE_URL` | yes (Twilio) | set to cloudflared HTTPS URL when testing webhooks |
| `DB_CONN_STR` | no | defaults to port 5433 |
| `JWT_SECRET` | yes | min 32 chars in production |
| `TWILIO_ACCOUNT_SID` | yes | from console.twilio.com |
| `TWILIO_AUTH_TOKEN` | yes | from console.twilio.com |
| `TWILIO_FROM_PHONE` | yes | E.164 format e.g. +40xxxxxxxxx |
| `ELEVENLABS_API_KEY` | yes | from elevenlabs.io |
| `ELEVENLABS_VOICE_ID` | no | default: 21m00Tcm4TlvDq8ikWAM |
| `VAPID_PUBLIC_KEY` | yes | generate with `make gen-vapid` |
| `VAPID_PRIVATE_KEY` | yes | generate with `make gen-vapid` |
| `RESEND_API_KEY` | yes | from resend.com |
| `RESEND_FROM_EMAIL` | no | default: noreply@beelive.ro |
| `PRIMARIE_EMAIL` | no | primărie recipient, default: primarie@beelive.ro |
| `GEO_AI_BASE_URL` | no | leave empty to use built-in mock |
| `ALLOWED_ORIGINS` | no | comma-separated, default: http://localhost:3000 |

Email is sent via Resend SMTP: host=`smtp.resend.com`, port=`465`, user=`apikey`, pass=`RESEND_API_KEY`.

---

## Phase Tracking

Phases 1–5 are COMPLETE (scaffold, DB migrations, HTTP skeleton, auth + seed users, seed data + reference endpoints + read paths).
Phases 6–11 are planned and waiting to be implemented.

Each phase has two files in `.planning/phases/`:
- `phase-N-plan.md` — what to implement (exists = pending)
- `phase-N.md` — status after completion (exists = done)

**To find the current phase:** look for the lowest N that has `phase-N-plan.md` but no `phase-N.md`.

See `.planning/RESUME.md` for the full resume checklist.

---

## Source of Truth

`API_CONTRACT.MD` in the repo root defines every endpoint, request/response type, and status code. When in doubt about what a handler should return, check there first.
