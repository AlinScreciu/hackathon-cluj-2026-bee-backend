# Phase 11 — Sliding JWT renewal, README, brand polish
Status: COMPLETE
Completed: 2026-05-24

## What was built

### Sliding JWT renewal
- `internal/api/router.go:63-86` (the passive session middleware) — when `Verify` succeeds, if `claims.ExpiresAt` is within 4h of expiry, re-sign the user's claims and emit a fresh `Set-Cookie` with the same shape as the login cookie (HttpOnly, SameSite=Lax, Path=/, MaxAge=86400). This is the only place renewal happens; the unused `middleware.RequireAuth` in `internal/middleware/auth.go` is deliberately not used because chi group middleware doesn't apply to Huma-registered routes (rule #18).

### Brand polish
- `cmd/server/main.go:77` — boot log message switched from `radarul-albinelor-api starting` to `beelive-api starting`.
- `internal/api/router.go:202` — Huma OpenAPI title switched from `Radarul Albinelor` to `BeeLive` (visible in `openapi.json` and the OpenAPI UI).
- `internal/services/pdf.go:44` — PDF subtitle switched from `Sistem Radarul Albinelor — document generat automat` to `Sistem BeeLive (beelive.ro) — document generat automat`. Every primărie PDF the system generates now carries the BeeLive brand.

The Go module path `github.com/radarul-albinelor/api` is unchanged — it's developer-facing only, never appears in any runtime output. Renaming the module is a tractable but larger find/replace + git mv across every `.go` file, so deferred until the repo itself is renamed.

### README
- New top-level `README.md` covering: what BeeLive does, three-role workflow, quick start, demo login table, an end-to-end curl demo, env-var reference, one-paragraph architecture summary, make-target table.

## Deferred (intentional)
- `make demo-spray` automated script — the demo accounts (CNPs / parcel IDs / apiary IDs) are not yet finalized, so any script baked now would need to be redone. Re-enable after demo-day account assignments.
- Go module rename — see above. User-visible surface is already BeeLive.
- Migration to rename `damage_photos.url` → `damage_photos.key` — the column name is a lie (it actually stores R2 keys), but renaming mid-hackathon is more risk than reward. Documented as a critical rule in CLAUDE.md.

## Verification

`go build ./...` and `go vet ./...` clean.

**Sliding JWT renewal:**
- Forged a JWT with `exp=now+3h` using the live `JWT_SECRET`. Hit `GET /api/v1/auth/me`. Response: `200 OK` + a fresh `Set-Cookie: ra_session=<new token>; Path=/; Max-Age=86400; HttpOnly; SameSite=Lax`.
- A fresh login token (`exp=now+24h`) → no `Set-Cookie` header on subsequent requests. Threshold check correct.

**Brand polish:**
- Boot log: `level=INFO msg="beelive-api starting" version=0.1.0 port=9090 env=development`.
- `openapi.json` regenerated on boot with `"title": "BeeLive"`.

**Final scan:** the only remaining `radarul` string in active code is the local Postgres DSN default (`radarul:radarul@.../radarul`) — internal DB credentials, not user-visible. Left alone.
