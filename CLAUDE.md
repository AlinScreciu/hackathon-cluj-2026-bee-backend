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
    elevenlabs/                 ElevenLabs TTS — takes storage.Storage, has TextToSpeechURL (singleflight + in-mem cache + Prewarm)
    webpush/                    Web push via VAPID
    weather/                    Open-Meteo adapter + 10-min cache
    email/                      go-mail SMTP (Resend)
    geoai/                      AI geo assessment client + mock
  storage/                      Audio-blob store: Local (dev) / R2 (deploy, presigned URLs)
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
2. `cascade.Start` → **groups dispatches by beekeeperID**, sorts each group by distance, launches **one goroutine per beekeeper** (the closest-apiary dispatch is the "primary"). Sibling dispatches stay in the DB for ledger granularity.
3. `launchDispatch(primary, siblings)` → send push notification once per device → branch by toxicity:
   - **T- (low):** set `call_state = "skipped"`, send SMS directly (push + SMS co-primary)
   - **T / T+ (med/high):** push + Twilio voice call + SMS fired **simultaneously as co-primary** (no 60s fallback wait). Unconfirmed timeout still arms (30min escalation).
4. Twilio calls `POST /webhooks/twilio/voice/gather` → return TwiML with ElevenLabs `<Play>` URL (R2 presigned, 1h TTL) + `<Gather>`. Voice text lists every affected apiary the beekeeper owns for this spray, not just the primary's.
5. User presses 1 → Twilio calls back → `HandleCallConfirmed` → set `final_status = "confirmed_call"` on primary → `propagateFinalStatus` mirrors the status onto every sibling dispatch
6. User replies "DA" to SMS → `POST /webhooks/twilio/sms/inbound` → `HandleSMSConfirmed` → same propagation pattern
7. If 30min unconfirmed timer fires → mark primary `unconfirmed` → propagate to siblings

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

**20. `geoai.Request` uses `Center{Lat, Lon}` (lon not lng)**
The real AI geo service uses `"lon"` in its JSON (not `"lng"`). The `geoai.Center` struct has `Lon float64 \`json:"lon"\``. Phase 8 spray handler must use `geoai.Center{Lat: parcel.Lat, Lon: parcel.Lng}` when constructing the request.

**21. AI geo service endpoint is `POST /ai/risk-assess`**
The real service (hackathon-cluj-2026-bee-ai-backend, FastAPI on :8000) uses `/ai/risk-assess`. Set `GEO_AI_BASE_URL=http://localhost:8000` locally. `geoai.HTTPClient` already targets this path.

**22. Voice audio cache uses `internal/storage` with R2 presigned URLs**
The ElevenLabs client takes a `storage.Storage` dependency. R2 bucket stays **private**; `Storage.SignedURL(ctx, key, ttl)` mints short-lived S3 presigned GETs (1h TTL) for `<Play>` URLs. GDPR rationale: audio contains beekeeper first name + apiary name, which is indirectly identifying via the public ANSVSA registry. **Do NOT enable Public Access on the R2 bucket.** `R2_PUBLIC_BASE_URL` is intentionally NOT a config var.

**23. TwiML `<Play>` URLs must be XML-escaped**
Presigned URLs contain `?X-Amz-...&X-Amz-...&...`. Raw `&` in TwiML text breaks Twilio's XML parser. Use `xmlTextEscaper` (in `webhooks_twilio.go`) before injecting any URL into `<Play>`.

**24. Per-beekeeper notification dedup, with state propagation**
`cascade.Start` groups dispatches by `beekeeper_id` and launches one goroutine per beekeeper (closest apiary = "primary"). Outbound notifications (push/call/SMS) are one per beekeeper, text lists every affected apiary via `buildApiaryClause` / `buildVoiceApiaryClause`. When the primary resolves (any path), `propagateFinalStatus` mirrors the status onto sibling dispatch rows. Sibling rows keep their own apiary_id / distance_m for ledger granularity but never have their own Twilio SIDs.

**25. SMS is co-primary for T/T+ (since 2026-05-24)**
For all toxicities, SMS fires immediately alongside push (and call for T/T+). No 60s fallback timer. The 30-min unconfirmed-timeout timer remains. Beekeepers get push + (call if T/T+) + SMS simultaneously — any one channel confirms the alert via `propagateFinalStatus`.

**26. Brand pronunciation: BeeLive is voiced as "Bi-Liv"**
In any voice TTS template, write the brand as `Bi-Liv` (phonetic Romanian). Written/SMS contexts keep `BeeLive`.

**27. `ListDispatchSiblings(spray_id, beekeeper_id)` is the canonical sibling lookup**
Use it (not `ListDispatchesBySpray + filter`) when grouping a beekeeper's dispatches for a single spray. Returns rows sorted ascending by `distance_m`.

**28. `damage_photos.url` stores an R2 key, not a URL**
The column name is a lie — the value is an R2 key like `photos/abc-123.jpg`. Every read path (`GET /damage-claims*`, future inspector views) must convert it to a fresh 1h presigned GET via `h.storage.SignedURL`. Never persist a URL with a baked-in TTL; never return the raw key to the frontend. See `internal/api/damage.go:signedPhotoURLs`.

**29. Sliding JWT renewal lives in the passive session middleware, not `RequireAuth`**
`internal/middleware/auth.go:RequireAuth` exists but is unused (chi group middleware doesn't apply to Huma-registered routes — see rule #18). All cookie issuance/renewal must happen in the passive session middleware in `internal/api/router.go:63-86`. Adding a renewal hook to `RequireAuth` would be silently dead code.

**30. anf-export is a raw chi route, not Huma**
`POST /api/v1/spray-reports/anf-export` returns `application/pdf` and is registered via `r.Post(...)` in router.go alongside the Twilio webhooks. Don't add a `huma.Register` for that path — Huma can't easily emit binary bodies, and a Huma registration would shadow the chi route. Body parsing is manual JSON decode in `rawANFExport`.

**31. SMS only fires once per cascade; never re-fire from terminal callbacks**
`fireSMS` is called exactly once from `launchDispatch`, immediately. No 60s fallback, no on-call-terminal re-fire. `HandleCallTerminal` only updates DB state; it must not call `scheduleSMSFallback`. The `cancelSMSFallback` calls and `smsTimers` sync.Map are now harmless no-ops kept for code-stability — do not re-introduce a code path that arms a timer there.

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
| `R2_ACCOUNT_ID` | prod | Cloudflare R2 account ID (subdomain of `*.r2.cloudflarestorage.com` endpoint) |
| `R2_ACCESS_KEY_ID` | prod | from R2 → Manage R2 API Tokens (the S3-style creds, not the bearer token) |
| `R2_SECRET_ACCESS_KEY` | prod | same source — shown only once at token creation |
| `R2_BUCKET` | prod | e.g. `beelive-voice`. Bucket **must stay private** (no Public Access). |

Email is sent via Resend SMTP: host=`smtp.resend.com`, port=`465`, user=`resend`, pass=`RESEND_API_KEY`.

R2: leave all four vars empty in dev → voice MP3s go to `./uploads/voice/` via the static file server. Set all four in prod → MP3s upload to R2 and Twilio fetches via short-lived presigned URLs (see Critical Rule #22).

---

## Phase Tracking

Phases 1–11 are COMPLETE.

- **Phases 1–9** (scaffold → real external integrations): see `.planning/phases/phase-N.md` for each.
- **Phase 9.5 (post-Phase-9, ad-hoc, complete 2026-05-24):** dev-bypass 2FA `000000`, dual SMS+email 2FA dispatch, BeeLive branding rename, cascade nil-interface fixes, Twilio sig validation skipped in dev, dynamic personalised voice alert text, ElevenLabs cache key includes voice ID, `internal/storage` package (Local + Cloudflare R2 with S3 presigned GETs for GDPR), `singleflight` + in-memory cache in elevenlabs client, prewarm static phrases on boot, XML-escape `<Play>` URLs, per-beekeeper notification dedup + state propagation (`propagateFinalStatus`), SMS co-primary for all toxicities, brand voiced as "Bi-Liv", SMS/voice templates reworded for clarity, "verificați SMS-ul" wording on timeout.
- **Phase 10 (complete 2026-05-24):** Inspector dashboard + damage claims. `POST /apiaries`, `POST /uploads/sign` (real R2 presigned PUTs), `POST/GET /damage-claims`, `GET /inspector/map-data`, `GET /inspector/farmers`, `GET /inspector/farmers/:id`, `POST /spray-reports/anf-export`. Also removed the dormant duplicate-SMS path from `HandleCallTerminal` (SMS is co-primary; no need to re-fire on missed call).
- **Phase 11 (complete 2026-05-24):** Sliding JWT renewal in passive session middleware; top-level `README.md`; user-visible brand polish (boot log, OpenAPI title, PDF subtitle now all say BeeLive).

**Deferred:** `make demo-spray` automated script (waits on finalized demo accounts so the script doesn't bake in throwaway CNPs/IDs). Go-module rename `github.com/radarul-albinelor/api` → something matching the BeeLive brand is also deferred — internal-only path, never user-visible.

Each phase has a file `phase-N.md` in `.planning/phases/`. A `phase-N-plan.md` next to it means the phase is still pending; once complete the plan file is deleted.

**To find the current phase:** look for the lowest N that has `phase-N-plan.md` but no `phase-N.md`. (Currently none — all merged work is shipped.)

See `.planning/RESUME.md` for the full resume checklist.

---

## Source of Truth

`API_CONTRACT.MD` in the repo root defines every endpoint, request/response type, and status code. When in doubt about what a handler should return, check there first.
