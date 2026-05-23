# Phase 4 — Auth
Status: COMPLETE
Completed: 2026-05-23

## What was built
- `internal/external/email/client.go` — EmailClient using go-mail, SMTP implicit TLS port 465
- `internal/services/auth.go` — AuthService: Login, Switch2FAMethod, Verify2FA, GetUser; sendTwilioSMS mock (stdout when no creds); maskedPhone/maskedEmail helpers
- `internal/services/seed.go` — Seed() inserts all 7 demo users (bcrypt cost 12); idempotent
- `internal/api/auth.go` — all 5 handlers fully implemented
- `internal/api/router.go` — AuthService instantiated + injected; session middleware on root chi router (passive cookie→context injection); chi group middleware removed (doesn't apply to Huma routes)
- `internal/middleware/auth.go` — added WithUser() helper
- `cmd/server/main.go` — --seed flag: inserts demo users then exits
- `.env` — PORT=9090, DB_CONN_STR port 5433

## Key decisions
- Used `stdlib.OpenDBFromPool(pool)` to adapt pgxpool for dbsqlc.DBTX (database/sql interface)
- Error sentinel is `sql.ErrNoRows` (not `pgx.ErrNoRows`) when using stdlib adapter
- Auth middleware lives on root chi router (passive, sets user when cookie valid) — individual handlers enforce auth by checking UserFromContext

## Verification
- Login → mock SMS code → verify 2FA → Set-Cookie: ra_session → /me returns full user ✓
- Logout clears cookie → /me returns 401 ✓
- Wrong password → 401 invalid_credentials ✓
- Bad CNP format (length < 13) → 422 from Huma schema ✓
- Switch 2FA method (sms→email) → new challenge, masked email ✓
- `go build ./... ` clean ✓
- `make seed` idempotent (7 users) ✓
