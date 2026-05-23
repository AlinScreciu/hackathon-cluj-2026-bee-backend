# BeeLive (Radarul Albinelor) — API

Romanian government-grade pesticide notification system for beekeepers. When a
farmer schedules a pesticide spray, every nearby beekeeper is alerted in
parallel via web push, voice call, and SMS. Every state change is written to a
tamper-evident SHA-256 hash-chain ledger so ANSVSA inspectors can audit the
notification trail.

Built for Cluj Hackathon 2026.

---

## What it does

**Three user roles, one cross-cutting workflow:**

- **Farmer** schedules a spray (`POST /spray-reports`) — substance, surface
  area, scheduled time. The API computes a downwind risk radius and identifies
  affected apiaries.
- **Beekeeper** receives the alert on every channel simultaneously:
  - Web push to the browser
  - Twilio voice call with ElevenLabs Romanian TTS
  - Twilio SMS as co-primary (no fallback delay)

  Confirming via any channel propagates the confirmation to every dispatch row
  for that beekeeper, so they only hear the call ringtone once even if they own
  multiple affected hives.
- **Inspector** (ANSVSA) opens a map view of every active spray, apiary, and
  damage claim; can drill into a farmer's compliance history; can export a
  3-year audit PDF for ANF compliance.

If the spray causes hive losses, the beekeeper files a damage claim with
photos uploaded directly to a private Cloudflare R2 bucket via short-lived
presigned PUT URLs — the API never proxies bytes.

---

## Quick start

Prerequisites: Docker, Go 1.25+, a Twilio account (for SMS/voice), an
ElevenLabs account (for TTS), and a Resend account (for email).

```bash
cp .env.example .env          # fill in secrets (see Env vars below)
make db-up                    # start Postgres on :5433
make migrate-up               # apply schema
make sqlc-gen                 # regenerate internal/db/sqlc/ (gitignored)
make seed                     # load demo users + reference data
make run                      # server on :9090
```

For Twilio webhook testing in dev (Twilio needs a public HTTPS endpoint):

```bash
make tunnel                   # cloudflared → trycloudflare.com URL
# copy that HTTPS URL into APP_BASE_URL in .env, then restart the server
```

### Demo login

After `make seed`, log in with any of:

| CNP             | Password    | Role      | Name                    |
| --------------- | ----------- | --------- | ----------------------- |
| `1920412111222` | `parola123` | farmer    | Vasile Mureșan          |
| `1850101123456` | `parola123` | beekeeper | Andrei Berar            |
| `1680808777888` | `parola123` | inspector | Inspector Județean Cluj |

In non-production, the 2FA code `000000` bypasses the SMS check. (Real codes
are also dispatched, so check `/tmp/beelive.log` for `[2FA CODE]` if you want
the live value.)

### Run a demo spray end-to-end

```bash
# As farmer Vasile
CHALLENGE=$(curl -s -c /tmp/ra.txt -X POST http://localhost:9090/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"cnp":"1920412111222","password":"parola123"}' | jq -r '.challenge_id')

curl -s -c /tmp/ra.txt -b /tmp/ra.txt -X POST http://localhost:9090/api/v1/auth/2fa/verify \
  -H "Content-Type: application/json" \
  -d "{\"challenge_id\":\"$CHALLENGE\",\"code\":\"000000\"}" >/dev/null

curl -s -b /tmp/ra.txt -X POST http://localhost:9090/api/v1/spray-reports \
  -H "Content-Type: application/json" \
  -d '{
    "parcel_id": "aaaaaaaa-0000-0000-0000-000000000001",
    "surface_ha": 5.0, "crop": "porumb", "substance": "Confidor",
    "scheduled_at": "2026-05-26T08:00:00Z", "duration_hours": 4
  }' | jq '.affected_apiaries, .risk_radius_m'
```

Expect `affected_apiaries: 2, risk_radius_m: 7000`. The beekeeper's phone
rings once (Romanian TTS), gets one SMS, and one push notification —
covering both affected apiaries in the alert text.

---

## Environment variables

Required for full functionality (see `.env.example` for the complete list):

| Variable                                                           | Required           | Notes                                                       |
| ------------------------------------------------------------------ | ------------------ | ----------------------------------------------------------- |
| `PORT`                                                             | no                 | default `8080` (project default is `9090` via `.env`)       |
| `APP_BASE_URL`                                                     | yes for Twilio     | cloudflared HTTPS URL when testing webhooks                 |
| `DB_CONN_STR`                                                      | no                 | defaults to local Postgres on `:5433`                       |
| `JWT_SECRET`                                                       | yes                | minimum 32 chars in production                              |
| `TWILIO_ACCOUNT_SID`, `TWILIO_AUTH_TOKEN`                          | yes (for SMS/call) | from console.twilio.com                                     |
| `TWILIO_FROM_PHONE`                                                | yes                | E.164, e.g. `+13143384926`                                  |
| `ELEVENLABS_API_KEY`                                               | yes                | from elevenlabs.io                                          |
| `ELEVENLABS_VOICE_ID`                                              | no                 | default `21m00Tcm4TlvDq8ikWAM`; project uses `HPdbgr...`    |
| `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY`                            | yes                | generate with `make gen-vapid`                              |
| `RESEND_API_KEY`                                                   | yes                | from resend.com (SMTP-based)                                |
| `R2_ACCOUNT_ID`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_BUCKET` | prod          | private bucket; voice MP3s + damage photos                  |
| `GEO_AI_BASE_URL`                                                  | no                 | leave empty to use the built-in geographic risk mock        |
| `ALLOWED_ORIGINS`                                                  | no                 | comma-separated, default `http://localhost:3000`            |

When R2 is configured the server logs `storage R2 enabled (private bucket,
presigned URLs)` at boot. Without R2, audio and uploads go to `./uploads/`
served by the static file handler — dev only.

---

## Architecture (one paragraph)

The HTTP layer is [Huma](https://huma.rocks/) on top of chi: handlers return
typed structs that double as the OpenAPI spec. Authentication is a passive
chi middleware that decodes the `ra_session` JWT cookie and injects the user
into the request context; per-handler guards enforce role policy. Auth
sessions slide on every request — when the token is within 4h of expiry, a
fresh 24h cookie is issued silently. Database access is pgx + sqlc: every
query is a named SQL file in `internal/db/queries/`, regenerated via
`make sqlc-gen`. The cascade orchestrator (`internal/services/cascade.go`)
runs one goroutine per beekeeper (not per dispatch) so multi-apiary owners
get a single push, single call, and single SMS. State propagates via
`propagateFinalStatus` from the primary dispatch row to its siblings, keeping
per-apiary ledger granularity without per-apiary notification fanout. The
SHA-256 hash-chain ledger (`internal/services/ledger.go`) records every
spray, dispatch, confirmation, PDF generation, and email send; concurrent
appends are serialized by a Postgres advisory lock so the chain never forks.
Voice audio + damage photos live in a private Cloudflare R2 bucket; the API
mints short-lived presigned GET/PUT URLs at read/write time and never
persists URLs with baked-in TTLs.

Deeper documentation lives in [.planning/ARCHITECTURE.md](.planning/ARCHITECTURE.md);
contributor-specific guidance is in [CLAUDE.md](CLAUDE.md); the full endpoint
contract is in [API_CONTRACT.MD](API_CONTRACT.MD).

---

## Make targets

| Command                  | What it does                                       |
| ------------------------ | -------------------------------------------------- |
| `make run`               | Start server on `:9090`                            |
| `make build`             | Build `./radarul-api` binary                       |
| `make test`              | `go test ./... -v -count=1`                        |
| `make lint`              | `go vet ./...`                                     |
| `make db-up` / `db-down` | Postgres container lifecycle                       |
| `make migrate-up/-down`  | Apply / roll back goose migrations                 |
| `make migrate-new NAME=` | Scaffold a new migration                           |
| `make sqlc-gen`          | Regenerate `internal/db/sqlc/` from queries        |
| `make seed`              | Populate demo users + reference data               |
| `make gen-vapid`         | Print a fresh VAPID key pair for web push          |
| `make tunnel`            | cloudflared HTTPS tunnel for Twilio webhooks       |

---

## License

Built for Cluj Hackathon 2026. Licensed for non-commercial use; commercial
licensing on request.
