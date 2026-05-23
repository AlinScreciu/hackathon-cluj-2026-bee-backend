# Radarul Albinelor — Full Stack Reference

## Language & Runtime

| | |
|---|---|
| **Go 1.25** | go.mod declares `go 1.25.0`. Uses `context.WithoutCancel` (1.21+) and `log/slog` (1.21+). |

---

## HTTP Framework

### `github.com/danielgtaylor/huma/v2` v2.38.0
Type-safe REST framework on top of any Go HTTP router. Provides automatic OpenAPI 3.1 generation from Go struct tags, request validation, and response serialization. Chosen because it eliminates hand-writing OpenAPI specs and generates correct schemas for Huma input/output structs.

**Adapter used:** `humachi` — the official Chi adapter (`github.com/danielgtaylor/huma/v2/adapters/humachi`).

**Alternative ruled out:** `gin` — no first-class OpenAPI generation; `echo` — same issue; raw `net/http` — too verbose for 37 endpoints.

### `github.com/go-chi/chi/v5` v5.3.0
Lightweight, idiomatic Go HTTP router. Used as the underlying mux for Huma. Also used directly for Twilio webhook routes (raw chi handlers, not Huma operations) because Twilio expects TwiML (XML) responses which Huma's content negotiation does not handle well.

---

## Database

### Postgres 16
Runs in Docker via `docker-compose.yml`. Mapped to **port 5433** (5432 is occupied by another local service). Database name: `radarul`, user: `radarul`, pass: `radarul`.

10 tables: `users`, `auth_challenges`, `apiaries`, `parcels`, `substances`, `spray_reports`, `alert_dispatches`, `damage_claims`, `push_subscriptions`, `ledger_events`.

### `github.com/jackc/pgx/v5` v5.9.2
Postgres driver. Used via `pgxpool.Pool` for connection pooling. Preferred over `database/sql` + `lib/pq` because pgx natively supports Postgres-specific types and is significantly faster.

### sqlc (`sqlc generate`)
Generates type-safe Go code from SQL query files in `internal/db/queries/`. Output goes to `internal/db/sqlc/` (gitignored — regenerate with `make sqlc-gen`). Config in `sqlc.yaml`.

**Why sqlc over an ORM:** ORMs hide SQL, making complex queries hard to reason about; sqlc preserves full SQL expressiveness while eliminating `rows.Scan` boilerplate.

**Important:** `toxicity` is `TEXT CHECK (IN ('T-','T','T+'))` — NOT a PG ENUM. PG ENUMs caused sqlc to generate duplicate Go constant names for 'T-' and 'T'.

**UUID driver:** configured for `github.com/google/uuid` — do not mix with `github.com/gofrs/uuid/v5` (caused import conflicts during Phase 2).

### goose (migration tool)
SQL migration runner. Migrations live in `internal/db/migrations/`. Run with `make migrate-up`. Create new migrations with `make migrate-new NAME=description`.

---

## Authentication & Security

### `github.com/golang-jwt/jwt/v5` v5.3.1
JWT signing and verification. Algorithm: HS256. Expiry: 24 hours. Token is set as an HttpOnly cookie named `ra_session` (SameSite=Lax, Path=/, MaxAge=86400). Implementation: `internal/platform/jwt.go`.

### `golang.org/x/crypto`
bcrypt for password hashing and 2FA code hashing. All auth challenge codes are bcrypt-hashed before storing.

---

## Notification Channels

### `github.com/twilio/twilio-go` v1.30.9
Official Twilio Go SDK. Used for:
- Outbound voice calls (read ElevenLabs MP3 URLs as TwiML)
- Outbound SMS
- Inbound webhook handling (voice gather, call status, SMS reply)

Implementation: `internal/external/twilio/`.

### ElevenLabs TTS (HTTP client, no official SDK)
Custom HTTP client in `internal/external/elevenlabs/`. Converts Romanian notification text to MP3 audio files. Model: **`eleven_multilingual_v2`** (required for Romanian diacritics). MP3 files are cached in `uploads/voice/` (named by content hash to avoid duplicate synthesis). Voice ID is configurable via `ELEVENLABS_VOICE_ID`.

### `github.com/SherClockHolmes/webpush-go` v1.4.0
Web Push Protocol implementation with VAPID key support. Sends push notifications to browsers. VAPID keys must be generated with `make gen-vapid` and set in env vars. Implementation: `internal/external/webpush/`.

---

## Email

### `github.com/wneessen/go-mail` v0.7.3
SMTP email client. Used with Resend's SMTP relay:
- Host: `smtp.resend.com`
- Port: `465` (TLS)
- User: `apikey`
- Password: `RESEND_API_KEY`
- From: `noreply@beelive.ro`

Chosen over the Resend HTTP SDK because go-mail gives better control over MIME types, attachments (PDF reports), and is pure Go with no CGO.

---

## PDF Generation

### `github.com/jung-kurt/gofpdf` v1.16.2
Pure Go PDF generation. Used to produce primărie notification reports (official documents) that are emailed to local authorities. Implementation: `internal/services/pdf.go`. PDFs are stored in `uploads/pdfs/`.

**Alternative ruled out:** headless Chrome / wkhtmltopdf — requires system binary, not suitable for a Docker-based hackathon deploy.

---

## Configuration

### `github.com/caarlos0/env/v11` v11.3.1
Struct-tag-based environment variable parsing. All config lives in `internal/config/config.go`. Supports defaults, comma-separated slices (`ALLOWED_ORIGINS`). No YAML/TOML files needed.

---

## Geospatial

### Haversine (stdlib math, no external package)
Implemented in `internal/services/geo.go`. Computes great-circle distance between two lat/lng coordinates. Used to find all apiaries within the spray's notification radius.

### Wind direction math (stdlib)
Also in `internal/services/geo.go`. Meteorological convention: wind direction = direction FROM which wind blows. To determine if an apiary is downwind: wind blows toward `(windDirDeg + 180) mod 360`. Compare against spray→apiary bearing.

### Open-Meteo (HTTP client, free, no auth)
Custom HTTP client in `internal/external/weather/`. Fetches current wind speed, wind direction, and temperature for a given lat/lng. Results are cached for 10 minutes (`weather.CachedClient`) to avoid hammering the API during demo.

### AI Geo Assessment (`internal/external/geoai/`)
HTTP client that posts spray details to an external AI service for risk assessment (affected radius, risk level). When `GEO_AI_BASE_URL` is empty, the mock implementation is used automatically. The mock returns a fixed 2 km radius for demo purposes.

---

## Observability

### `log/slog` (stdlib, Go 1.21+)
Structured logging. In development (`APP_ENV=development`): text handler at DEBUG level. In production: JSON handler at INFO level. No third-party logging library needed.

---

## CORS

### `github.com/rs/cors` v1.11.1
CORS middleware. Allowed origins configured via `ALLOWED_ORIGINS` env var (comma-separated). Implementation: `internal/middleware/cors.go`.

---

## UUID

### `github.com/google/uuid` v1.6.0
UUID v4 generation. Used everywhere: `uuid.New()` for new entity IDs. sqlc is also configured to use this package. Do not use `github.com/gofrs/uuid` — it caused import conflicts in Phase 2 and is listed in go.mod only as an indirect dependency.

---

## Concurrency

### `golang.org/x/sync`
Available but not heavily used. The cascade uses bare goroutines with `sync.WaitGroup` patterns. `errgroup` may be used in future phases for structured concurrency.

---

## Dev Tools

| Tool | Purpose |
|---|---|
| `goose` | Database migrations (CLI, must be installed separately) |
| `sqlc` | Code generation from SQL (CLI, must be installed separately) |
| `cloudflared` | Tunnel for Twilio webhook development (`make tunnel`) |
| Docker / Docker Compose | Postgres container (Rancher Desktop on this machine) |

Install dev tools:
```bash
go install github.com/pressly/goose/v3/cmd/goose@latest
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
```
