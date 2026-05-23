# How to Resume Work

Use this file at the start of every new Claude Code session to restore full project context.

---

## Step 1 — Establish Current State

Run these commands to understand where things stand:

```bash
# What's committed
git log --oneline -10

# What's in progress (uncommitted changes)
git status
git diff

# Which phases are done vs pending
ls .planning/phases/
```

A phase is **complete** if `.planning/phases/phase-N.md` exists (has a status file).
A phase is **pending** if only `.planning/phases/phase-N-plan.md` exists (no status file yet).

The current phase to implement = the lowest N with a plan file but no status file.

---

## Step 2 — Read the Context Files (in order)

1. **`CLAUDE.md`** (repo root) — project overview, all critical rules, key commands, folder structure
2. **`.planning/IDEA.md`** — what the product does and why
3. **`.planning/ARCHITECTURE.md`** — request flow, cascade state machine, ledger mechanics
4. **`.planning/STACK.md`** — full dependency list with versions and rationale
5. **`API_CONTRACT.MD`** (repo root) — all endpoint signatures and types (source of truth)
6. **`.planning/phases/phase-N-plan.md`** — what the current phase needs to implement

---

## Step 3 — Verify the Environment

```bash
# Postgres must be running on port 5433
docker ps | grep radarul
# If not running:
make db-up

# Confirm DB is reachable
psql postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable -c "SELECT version();"

# Confirm migrations are applied (should show 3 applied)
goose -dir internal/db/migrations postgres "postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable" status

# Regenerate sqlc (internal/db/sqlc/ is gitignored)
make sqlc-gen

# Confirm the project builds
make build

# Confirm tests pass
make test

# Confirm linter is clean
make lint
```

---

## Step 4 — Check .env

```bash
# .env is gitignored — must exist for make run to work
ls -la .env
# If missing:
cp .env.example .env
# Then edit .env and fill in: TWILIO_*, ELEVENLABS_*, VAPID_*, RESEND_API_KEY
```

For Twilio webhook testing, you also need:
```bash
make tunnel   # prints an HTTPS URL — copy it into APP_BASE_URL in .env
```

---

## Step 5 — Start Work on the Current Phase

Read the plan file for the current phase:
```bash
# Replace N with the current phase number
cat .planning/phases/phase-N-plan.md
```

Then implement. After completing a phase, create `.planning/phases/phase-N.md` with:
```
# Phase N — <title>
Status: COMPLETE
Completed: <date>

## What was built
- <bullet list of files created/modified>

## Verification
- <how you confirmed it works>

## Notes
- <any gotchas or decisions made>
```

---

## Phase Status Snapshot

| Phase | Title | Status |
|---|---|---|
| 1 | Scaffold | COMPLETE |
| 2 | Database: Migrations + sqlc | COMPLETE |
| 3 | HTTP Skeleton: Huma + Chi, Middleware, OpenAPI | COMPLETE |
| 4 | Auth: login → 2FA → cookie → /me | COMPLETE |
| 5 | Seed data + GET-only endpoints (apiaries, parcels, substances) | COMPLETE |
| 6 | Ledger service (SHA256 hash chain) + PATCH /apiaries | COMPLETE |
| 7 | AI geo mock + Open-Meteo weather with cache | COMPLETE |
| 8 | Spray reports + cascade goroutines + Twilio webhook handling | COMPLETE |
| 9 | Real Twilio + ElevenLabs TTS + Web Push + PDF + email | COMPLETE |
| 10 | Inspector map endpoints + damage claims + photo upload | pending |
| 11 | Tunnel, demo script, README, sliding JWT | pending |

Update this table as phases complete.

---

## Detailed Phase Plans

Each phase has a self-contained plan file at:
`.planning/phases/phase-N-plan.md`

These files include:
- Current state before the phase (what already exists)
- Exact files to create (with package, types, function signatures)
- Exact files to modify (with what changes)
- Key implementation details (non-obvious patterns)
- Verification steps (exact curl commands)
- What to write when done

**To start a phase**: Read the plan file entirely, then implement. The plan is designed so a new session with zero context can execute it.

---

## Key Facts to Remember

- **Postgres port: 5433** (not 5432 — taken by k8s-watch-infra on this dev machine)
- **App port: 9090** (PORT=9090 in .env — not the default 8080)
- **`internal/db/sqlc/` is gitignored** — always run `make sqlc-gen` after checkout
- **`context.WithoutCancel(ctx)`** in all cascade goroutines (request context cancels on response)
- **`pg_advisory_xact_lock(42)`** before every ledger chain-tip read
- **T- toxicity skips voice call** — push + SMS fire simultaneously, no 60s wait
- **Twilio webhooks are raw chi routes**, not Huma operations
- **Toxicity is TEXT not PG ENUM** — sqlc generates it as `string`
- **CNP is PII** — never log, never return except in GET /auth/me
- **ElevenLabs model:** `eleven_multilingual_v2` — never strip diacritics
- **One file per resource** in `internal/api/`
- **`API_CONTRACT.MD`** is the source of truth for all types and endpoints

### Phase 4 implementation discoveries (critical for phases 5+)

- **`stdlib.OpenDBFromPool(pool)`** — `*pgxpool.Pool` does NOT implement `DBTX`. Use
  `github.com/jackc/pgx/v5/stdlib` to get a `*sql.DB`, then pass to `dbsqlc.New(db)`.
- **`sql.ErrNoRows`**, not `pgx.ErrNoRows** — when using the stdlib adapter, check
  `errors.Is(err, sql.ErrNoRows)` for not-found cases.
- **Chi group middleware does NOT apply to Huma routes** — Huma registers on the root router
  and bypasses any `r.Group` middleware. Auth is a passive session middleware on the root router
  that injects `*domain.User` into context; each handler calls `middleware.UserFromContext(ctx)`
  and returns 401/403 explicitly.
- **Phase 5 note:** `seed.go` (7 demo users) and the `--seed` flag in `cmd/server/main.go` are
  ALREADY implemented. Phase 5 only needs to extend `Seed()` with apiaries/parcels demo data
  and implement the GET endpoints.

### Phase 5 implementation discoveries (critical for phases 6+)

- **`domain.User.ID` is `string`, not `uuid.UUID`** — every authenticated handler that passes
  `user.ID` to a sqlc query param expecting `uuid.UUID` must call `uuid.Parse(user.ID)` first
  and return 401 on parse error. This applies to all resource endpoints going forward.

### Phase 6 implementation discoveries (critical for phases 7+)

- **`LedgerService.Append(ctx, tx *sql.Tx, ...)` takes `*sql.Tx`, not `pgx.Tx`** — DBTX is
  database/sql-style. Get transactions via `stdlib.OpenDBFromPool(pool).BeginTx(ctx, nil)`.
  Pass `nil` for tx to let Append manage its own transaction.
- **`pg_advisory_xact_lock(42)`** via `tx.ExecContext` serializes all ledger appends within a
  transaction. Always use this before reading the chain tip.
- **`ListLedgerEventsPaginated` actor UUID bug**: `$2::uuid IS NULL` evaluates to FALSE when
  zero `uuid.UUID{}` passed via stdlib driver. Use `ListLedgerEventsByType` when no actor
  filter is needed.
- **Cascade phases (8+)**: call `ledgerSvc.Append(ctx, tx, "spray.created", ...)` within the
  spray-report transaction, passing the same `*sql.Tx`.

### Phase 8 implementation discoveries (critical for phases 9+)

- **`NewRouter` returns `(http.Handler, func())`** — shutdown closure, not the concrete service. `main.go` calls it as `cascadeShutdown()`.
- **`stdlib.OpenDBFromPool(pool)` everywhere** — pgxpool.Pool doesn't implement `PrepareContext` so can't be passed directly to `dbsqlc.New`. All handlers create `sqlDB` from stdlib.
- **Spray transaction uses `*sql.Tx`** — `sqlDB.BeginTx(ctx, nil)` gives a `*sql.Tx` compatible with both `dbsqlc.New(tx)` and `ledgerSvc.Append(ctx, tx, ...)`. All spray + dispatch + ledger writes are atomic.
- **CascadeService.Start called after tx.Commit** — goroutines must not start before the spray/dispatch rows are visible.
- **GeoAI mock fires if server not started with `.env` sourced** — use `source .env && go run ./cmd/server` or `make run`.

### Phase 7 implementation discoveries (critical for phases 8+)

- **Real AI geo service endpoint is `POST /ai/risk-assess`** (not `/assess`). Running at `http://localhost:8000` (FastAPI). Set `GEO_AI_BASE_URL=http://localhost:8000` in `.env`.
- **`geoai.Request` uses `Center{Lat, Lon}` (not `Lng`)** — matches real API's `"lon"` JSON field.
- **`geoai.Request.Product.BeeToxicity`** should be set when calling from spray handler — values: `"low"`, `"medium"`, `"high"`, `"very_high"`. Maps from domain toxicity: T- → low, T → medium/high, T+ → very_high.
- **`geoai.Result.RiskRadiusM`** comes from `notifyBeekeepersWithinMeters` in the API response. Use this as the Haversine threshold when finding affected apiaries in Phase 8.
- **Weather cache is in `h.weatherClient`** — `*weather.CachedClient`. Phase 8 can call `h.weatherClient.Get(ctx, lat, lng)` to get wind direction for downwind bearing calculation.
