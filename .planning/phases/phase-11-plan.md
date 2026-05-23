# Phase 11 — Polish: Tunnel, Demo Script, README, Sliding JWT

**Status**: PENDING
**Goal**: Demo-ready state. `make demo-spray` runs automated end-to-end. `make tunnel` starts cloudflared. Sliding JWT renews sessions. README complete. `go vet ./...` clean.

---

## Current State (before this phase)

Phases 1-10 complete. All API endpoints implemented. External integrations wired. Cascade orchestration works.

What exists:
- `tools/` directory — check contents
- `scripts/` directory — check contents
- `README.md` — may have stub content from Phase 3
- `Makefile` — already has `gen-vapid`, `tunnel`, `demo-spray` targets (from Phase 3)
- `internal/middleware/auth.go` — `RequireAuth` does not do sliding renewal yet
- `internal/services/cascade.go` — `Shutdown()` implemented in Phase 8

Check existing `tools/` and `scripts/` contents before writing new files:
```bash
ls /Users/alinscreciu/work/hackathon/api/tools/
ls /Users/alinscreciu/work/hackathon/api/scripts/
```

If `tools/gen-vapid/main.go` and `scripts/demo-spray.sh` already exist (from Phase 3 skeleton), update them. If they're empty stubs, implement them.

---

## Prerequisites

Phases 1-10 complete. `make seed` run. Server starts cleanly with `go run ./cmd/server`.

---

## Files to Create

### `tools/tunnel.sh`

```bash
#!/bin/bash
set -e

echo "============================================="
echo "  Radarul Albinelor — Cloudflare Tunnel"
echo "============================================="
echo ""
echo "Starting tunnel to http://localhost:8080 ..."
echo ""
echo "INSTRUCTIONS:"
echo "1. Copy the HTTPS URL printed below (*.trycloudflare.com)"
echo "2. Set it in your .env file: APP_BASE_URL=<URL>"
echo "3. Restart the server for webhooks to work"
echo ""
echo "Twilio webhook URLs to configure:"
echo "  Voice webhook:  <URL>/api/v1/webhooks/twilio/voice/gather"
echo "  Voice status:   <URL>/api/v1/webhooks/twilio/voice/status"
echo "  SMS inbound:    <URL>/api/v1/webhooks/twilio/sms/inbound"
echo "  SMS status:     <URL>/api/v1/webhooks/twilio/sms/status"
echo ""
exec cloudflared tunnel --url http://localhost:8080
```

Make it executable: `chmod +x tools/tunnel.sh`. Also note that `Makefile` has `tunnel:` target that runs `cloudflared` directly — the shell script is supplementary.

### `scripts/demo-spray.sh`

```bash
#!/bin/bash
set -e

BASE_URL="${BASE_URL:-http://localhost:8080}"
COOKIE_FILE="/tmp/radarul-demo-cookies.txt"
POLL_INTERVAL=2
POLL_MAX=150  # 5 minutes

echo "======================================"
echo "  Radarul Albinelor — Demo Script"
echo "======================================"
echo "Server: $BASE_URL"
echo ""

# Cleanup cookie file
rm -f "$COOKIE_FILE"

# ─── Step 1: Login as Vasile Mureșan (fermier) ─────────────────────────────
echo "[1/5] Logging in as Vasile Mureșan (fermier)..."
CHALLENGE=$(curl -sf -X POST "$BASE_URL/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"cnp":"1920412111222","password":"parola123"}' | python3 -c "import sys,json; print(json.load(sys.stdin)['challenge_id'])")

echo "    Challenge ID: $CHALLENGE"
echo "    >>> Check server logs for '[2FA SMS mock]' line with 6-digit code <<<"
echo -n "    Enter 2FA code: "
read -r CODE

curl -sf -X POST "$BASE_URL/api/v1/auth/2fa/verify" \
  -H 'Content-Type: application/json' \
  -c "$COOKIE_FILE" \
  -d "{\"challenge_id\":\"$CHALLENGE\",\"code\":\"$CODE\"}" > /dev/null

echo "    Logged in successfully."

# ─── Step 2: Get first parcel ────────────────────────────────────────────────
echo ""
echo "[2/5] Fetching parcels..."
PARCEL_ID=$(curl -sf "$BASE_URL/api/v1/parcels" \
  -b "$COOKIE_FILE" | python3 -c "import sys,json; print(json.load(sys.stdin)['parcels'][0]['id'])")
echo "    Parcel ID: $PARCEL_ID"

# ─── Step 3: POST spray report with Confidor Energy (T+, radius 3000m) ───────
echo ""
echo "[3/5] Creating spray report (Confidor Energy, T+)..."
SPRAY=$(curl -sf -X POST "$BASE_URL/api/v1/spray-reports" \
  -H 'Content-Type: application/json' \
  -b "$COOKIE_FILE" \
  -d "{
    \"parcel_id\": \"$PARCEL_ID\",
    \"surface_ha\": 5.2,
    \"crop\": \"rapița\",
    \"substance\": \"Confidor Energy\",
    \"scheduled_at\": \"$(date -u -v+1d '+%Y-%m-%dT09:00:00Z' 2>/dev/null || date -u -d '+1 day' '+%Y-%m-%dT09:00:00Z')\",
    \"duration_hours\": 2.0
  }")
SPRAY_ID=$(echo "$SPRAY" | python3 -c "import sys,json; print(json.load(sys.stdin)['spray_report']['id'])")
AFFECTED=$(echo "$SPRAY" | python3 -c "import sys,json; print(json.load(sys.stdin)['affected_apiaries'])")
RADIUS=$(echo "$SPRAY" | python3 -c "import sys,json; print(json.load(sys.stdin)['risk_radius_m'])")
echo "    Spray ID: $SPRAY_ID"
echo "    Affected apiaries: $AFFECTED"
echo "    Risk radius: ${RADIUS}m"
echo "    >>> Check server logs for [MOCK CALL] and [MOCK SMS] lines <<<"

# ─── Step 4: Poll cascade status ─────────────────────────────────────────────
echo ""
echo "[4/5] Polling cascade status (max ${POLL_MAX}s)..."
ELAPSED=0
while [ $ELAPSED -lt $POLL_MAX ]; do
    STATUS=$(curl -sf "$BASE_URL/api/v1/spray-reports/$SPRAY_ID/cascade-status" \
      -b "$COOKIE_FILE")
    OVERALL=$(echo "$STATUS" | python3 -c "import sys,json; print(json.load(sys.stdin)['overall_status'])")
    CONFIRMED=$(echo "$STATUS" | python3 -c "import sys,json; d=json.load(sys.stdin)['summary']; print(d['confirmed'])")
    PENDING=$(echo "$STATUS" | python3 -c "import sys,json; d=json.load(sys.stdin)['summary']; print(d['pending'])")
    TOTAL=$(echo "$STATUS" | python3 -c "import sys,json; d=json.load(sys.stdin)['summary']; print(d['total'])")
    printf "    [%3ds] %s — confirmed:%s pending:%s total:%s\r" $ELAPSED "$OVERALL" "$CONFIRMED" "$PENDING" "$TOTAL"
    if [ "$OVERALL" = "complete" ]; then
        echo ""
        echo "    Cascade complete!"
        break
    fi
    sleep $POLL_INTERVAL
    ELAPSED=$((ELAPSED + POLL_INTERVAL))
done
echo ""

# ─── Step 5: Summary ─────────────────────────────────────────────────────────
echo "[5/5] Summary"
echo "──────────────────────────────────────────"
FINAL=$(curl -sf "$BASE_URL/api/v1/spray-reports/$SPRAY_ID/cascade-status" -b "$COOKIE_FILE")
echo "$FINAL" | python3 -c "
import sys, json
d = json.load(sys.stdin)
s = d['summary']
print(f'  Spray ID:    {d[\"spray_report_id\"]}')
print(f'  Status:      {d[\"overall_status\"]}')
print(f'  Total:       {s[\"total\"]}')
print(f'  Confirmed:   {s[\"confirmed\"]}')
print(f'  Pending:     {s[\"pending\"]}')
print(f'  Unconfirmed: {s[\"unconfirmed\"]}')
print(f'  Failed:      {s[\"failed\"]}')
"
echo ""

# Verify ledger
VERIFY=$(curl -sf "$BASE_URL/api/v1/events/verify" -b "$COOKIE_FILE")
VALID=$(echo "$VERIFY" | python3 -c "import sys,json; print(json.load(sys.stdin)['valid'])")
TOTAL_EVENTS=$(echo "$VERIFY" | python3 -c "import sys,json; print(json.load(sys.stdin)['total_events'])")
echo "  Ledger valid: $VALID (${TOTAL_EVENTS} events)"
echo ""
echo "======================================"
echo "  Demo complete!"
echo "======================================"
```

Make executable with `chmod +x scripts/demo-spray.sh`.

**NOTE**: The script uses `python3` for JSON parsing (more portable than `jq` in some CI environments). If `python3` is not available, substitute `jq`.

### `tools/gen-vapid/main.go`

Check if this file already exists from the Phase 3 skeleton. If yes, it may already have content. If empty or stub, implement:

```go
package main

import (
	"fmt"
	"log"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		log.Fatalf("Failed to generate VAPID keys: %v", err)
	}
	fmt.Println("# Add these to your .env file:")
	fmt.Println("VAPID_PRIVATE_KEY=" + privateKey)
	fmt.Println("VAPID_PUBLIC_KEY=" + publicKey)
}
```

---

## Files to Modify

### `internal/middleware/auth.go`

Add sliding JWT renewal. When `RequireAuth` validates a token, if the token expires in less than 4 hours, issue a renewed token via `Set-Cookie`.

The challenge: `RequireAuth` is an `http.Handler` middleware and has access to `http.ResponseWriter`. But it also needs `*platform.JWTService` (already has it) and needs to write a new cookie.

Current signature: `func RequireAuth(jwtSvc *platform.JWTService) func(http.Handler) http.Handler`

The renewal requires `Sign(user *domain.User)` which needs a full `domain.User`. But the JWT claims only have `{UserID, Role, CNP}` — enough to re-sign. So we don't need to DB-fetch the full user.

```go
func RequireAuth(jwtSvc *platform.JWTService) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            cookie, err := r.Cookie("ra_session")
            if err != nil { writeAuthError(w); return }

            claims, err := jwtSvc.Verify(cookie.Value)
            if err != nil { writeAuthError(w); return }

            user := &domain.User{
                ID:   claims.UserID,
                Role: claims.Role,
                CNP:  claims.CNP,
            }

            // Sliding renewal: if token expires in < 4 hours, issue new token
            if time.Until(claims.ExpiresAt.Time) < 4*time.Hour {
                if newToken, err := jwtSvc.Sign(user); err == nil {
                    newCookie := &http.Cookie{
                        Name:     "ra_session",
                        Value:    newToken,
                        HttpOnly: true,
                        SameSite: http.SameSiteLaxMode,
                        Path:     "/",
                        MaxAge:   86400,
                    }
                    http.SetCookie(w, newCookie)
                    slog.Debug("sliding JWT renewal", "user_id", user.ID)
                }
            }

            ctx := context.WithValue(r.Context(), userContextKey, user)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

Add imports: `"time"`, `"log/slog"`.

**Reason this works**: `http.SetCookie(w, cookie)` appends a `Set-Cookie` header to the response. When `next.ServeHTTP` runs, the cookie is already queued in the response headers. Huma handlers eventually call `w.WriteHeader` which flushes headers including the new cookie. This is straightforward middleware cookie renewal.

### `internal/services/cascade.go`

`Shutdown()` was implemented in Phase 8 — verify it exists. If not, add it:

```go
func (c *CascadeService) Shutdown() {
    slog.Info("cascade: shutting down, stopping all timers...")
    count := 0
    c.smsTimers.Range(func(k, v any) bool {
        v.(*time.Timer).Stop()
        count++
        return true
    })
    c.unconfirmedTimers.Range(func(k, v any) bool {
        v.(*time.Timer).Stop()
        count++
        return true
    })
    slog.Info("cascade: shutdown complete", "timers_stopped", count)
}
```

### `cmd/server/main.go`

`cascade.Shutdown()` was set up in Phase 8 — verify it's there. The `NewRouter` must return `(http.Handler, *services.CascadeService)` and `main.go` must call `cascadeSvc.Shutdown()` before `srv.Shutdown`.

If it wasn't done in Phase 8, do it now:

1. Change `api.NewRouter` signature:
   ```go
   // in router.go
   func NewRouter(cfg *config.Config, pool *pgxpool.Pool) (http.Handler, *services.CascadeService) {
       // ...existing code...
       return r, cascadeSvc
   }
   ```

2. In `main.go`:
   ```go
   router, cascadeSvc := api.NewRouter(cfg, pool)
   // ...
   <-ctx.Done()
   slog.Info("shutting down...")
   cascadeSvc.Shutdown()
   shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
   defer cancel()
   if err := srv.Shutdown(shutdownCtx); err != nil {
       slog.Error("shutdown error", "err", err)
   }
   ```

### `README.md`

Full rewrite. This is a comprehensive document for the hackathon demo.

```markdown
# Radarul Albinelor API

Sistem de alertare în timp real pentru apicultori și fermieri din România.
Apicultorii primesc notificări instant când fermierii vecini planifică tratamente cu pesticide.

## Arhitectură

- **Go 1.25** + Huma v2 (OpenAPI) + Chi router
- **PostgreSQL** (port 5433) — cu migrații Goose
- **Twilio** — apeluri vocale + SMS
- **ElevenLabs** — TTS în limba română
- **Web Push** — notificări browser
- **Resend (SMTP)** — email cu PDF

## Configurare rapidă

### 1. Pornire DB

```bash
docker compose up -d postgres
make migrate-up
make seed
```

### 2. Variabile de mediu

```bash
cp .env.example .env
# Editează .env cu credențialele tale
```

Variabile opționale (API-ul pornește și fără ele, cu mock-uri):
- `TWILIO_ACCOUNT_SID` + `TWILIO_AUTH_TOKEN` + `TWILIO_FROM_PHONE`
- `ELEVENLABS_API_KEY` + `ELEVENLABS_VOICE_ID`
- `VAPID_PUBLIC_KEY` + `VAPID_PRIVATE_KEY` (generează cu `make gen-vapid`)
- `RESEND_API_KEY`

### 3. Pornire server

```bash
make run
# sau: go run ./cmd/server
```

Server pornit la http://localhost:8080. OpenAPI generat la `openapi.json`.

## Conturi demo (parolă: parola123)

| CNP           | Rol       | Nume                    |
|---------------|-----------|-------------------------|
| 1850101123456 | apicultor | Andrei Berar            |
| 2900215654321 | apicultor | Maria Costea            |
| 1780530987654 | apicultor | Ioan Lupu               |
| 1920412111222 | fermier   | Vasile Mureșan          |
| 2880721333444 | fermier   | Elena Popa              |
| 1751103555666 | fermier   | Gheorghe Stan           |
| 1680808777888 | inspector | Inspector Județean Cluj |

## Demo complet

```bash
make demo-spray
```

Rulează automat: login → creare raport → cascade → polling status.

## Tunnel pentru Twilio webhooks

```bash
make tunnel
# Copiază URL-ul HTTPS și setează APP_BASE_URL în .env
```

Configurează în Twilio Console:
- Voice webhook: `{URL}/api/v1/webhooks/twilio/voice/gather`
- SMS inbound: `{URL}/api/v1/webhooks/twilio/sms/inbound`

## Generare chei VAPID

```bash
make gen-vapid
# Copiază output în .env
```

## Comenzi utile

```bash
make build       # compilare binară
make run         # pornire server
make seed        # populare DB demo
make migrate-up  # aplicare migrații
make sqlc-gen    # regenerare cod sqlc
make test        # teste
make lint        # go vet
make tunnel      # tunel cloudflared
make demo-spray  # demo automatizat
make gen-vapid   # chei VAPID
```

## Fluxul de autentificare

1. `POST /api/v1/auth/login` cu CNP + parolă → `challenge_id` + cod trimis via SMS/email
2. `POST /api/v1/auth/2fa/verify` cu challenge_id + cod → cookie `ra_session`
3. Cookie reînnoit automat dacă expiră în mai puțin de 4 ore

## Registrul tamper-evident

Toate evenimentele critice (rapoarte pesticide, alerte, confirmări, daune) sunt înregistrate
într-un lanț SHA256. Verificare:

```bash
curl -b cookies.txt http://localhost:8080/api/v1/events/verify
# → {"valid":true,"total_events":N,"last_hash":"..."}
```

## Structura proiectului

```
cmd/server/          # punct de intrare
internal/
  api/               # handlere HTTP (Huma + Chi)
  config/            # variabile de mediu
  db/
    migrations/      # migrații Goose SQL
    queries/         # query-uri sqlc
    sqlc/            # cod generat (gitignored, rulați make sqlc-gen)
  domain/            # tipuri de domeniu
  external/          # clienți externi (Twilio, ElevenLabs, etc.)
    email/
    elevenlabs/
    geoai/
    twilio/
    weather/
    webpush/
  middleware/        # autentificare, CORS, recovery
  platform/          # JWT
  services/          # logică de business
    auth.go
    cascade.go       # orchestrare notificări
    ledger.go        # lanț hash SHA256
    pdf.go           # generare PDF
    seed.go          # date demo
tools/
  gen-vapid/         # generator chei VAPID
scripts/
  demo-spray.sh      # demo automat
```
```

---

## Verification Steps

```bash
# Check tools directory
ls /Users/alinscreciu/work/hackathon/api/tools/
ls /Users/alinscreciu/work/hackathon/api/scripts/

# Verify gen-vapid tool works
make gen-vapid
# Expected: prints VAPID_PRIVATE_KEY=... VAPID_PUBLIC_KEY=...

# Verify demo script is executable
ls -la /Users/alinscreciu/work/hackathon/api/scripts/demo-spray.sh

# Build clean
go build ./...
go vet ./...

# Start server
go run ./cmd/server &

# Test sliding JWT renewal:
# 1. Log in to get a cookie
# 2. Manually inspect the JWT (base64 decode the ra_session cookie value)
# 3. Verify token is for 24h
# 4. Cannot easily test 4h threshold in unit test without mocking — 
#    verify code is correct by reading it

# Simulate token near expiry: modify auth.go to use < 23h (23h59m) for testing
# (revert after testing)

# Test tunnel script exists (don't run unless cloudflared installed)
ls -la /Users/alinscreciu/work/hackathon/api/tools/tunnel.sh

# Run demo (requires server running with seed data)
# (Interactive — will prompt for 2FA code)
# BASE_URL=http://localhost:8080 bash scripts/demo-spray.sh

# Final go vet
go vet ./...

# Verify all routes still work
curl -s http://localhost:8080/api/v1/healthz | jq .
# Expected: {"status":"ok"}

# Verify openapi.json is complete
cat openapi.json | python3 -c "import sys,json; spec=json.load(sys.stdin); print(f'Paths: {len(spec[\"paths\"])}')"
# Expected: Paths: 37 (or more)
```

---

## Common Issues in Phase 11

1. **`make gen-vapid` fails**: Check `tools/gen-vapid/main.go` compiles. Run `go build ./tools/gen-vapid/` to verify. The `webpush-go` dep is in `go.mod` as indirect — it may need to be made direct: `go get github.com/SherClockHolmes/webpush-go`.

2. **`scripts/demo-spray.sh` date command**: macOS `date` uses `-v+1d` for relative dates, Linux uses `-d '+1 day'`. The script handles both with `|| ` fallback.

3. **`scripts/demo-spray.sh` requires `python3`**: Available on macOS by default. If not, the script can use `jq` instead — replace all `python3 -c "..."` with `jq -r '...'` equivalents.

4. **Sliding JWT renewal in tests**: The renewal only triggers if `time.Until(claims.ExpiresAt.Time) < 4*time.Hour`. A freshly issued token has 24h remaining — renewal won't trigger. To test: temporarily change `4*time.Hour` to `25*time.Hour` (always renews) and verify a new `Set-Cookie` appears in responses.

5. **`cascade.Shutdown()` in main.go**: If Phase 8 changed `NewRouter` to return `(http.Handler, *services.CascadeService)`, verify main.go destructures correctly: `router, cascadeSvc := api.NewRouter(cfg, pool)`.

6. **README Romanian characters**: Ensure the file is saved as UTF-8. The `gofpdf` issue with Romanian chars (from Phase 9) does NOT affect the README — that's just a markdown file.

---

## After Completion

Write `/Users/alinscreciu/work/hackathon/api/.planning/phases/phase-11.md`:

```markdown
# Phase 11 — Polish: Tunnel, Demo Script, README, Sliding JWT
Status: COMPLETE
Completed: <date>

## What was built
- tools/tunnel.sh — cloudflared tunnel helper with Twilio URL instructions
- tools/gen-vapid/main.go — VAPID key generator
- scripts/demo-spray.sh — automated demo: login → spray → cascade poll → summary
- internal/middleware/auth.go — sliding JWT renewal (< 4h remaining → re-issue cookie)
- internal/services/cascade.go — Shutdown() verified/added
- cmd/server/main.go — cascade.Shutdown() on graceful shutdown
- README.md — complete: setup guide, demo credentials, architecture, commands

## Verification
- make gen-vapid → prints VAPID key pair
- make demo-spray → runs to completion
- go vet ./... → clean
- go build ./... → clean
- Sliding JWT: cookie renewed when token < 4h from expiry
```

---

## Final Checklist for Project Completion

After Phase 11, run this to confirm everything is working:

```bash
# 1. Clean build
go build ./... && echo "BUILD OK"
go vet ./... && echo "VET OK"

# 2. All make targets work
make build  # binary compiles
make gen-vapid  # VAPID keys generated

# 3. Server starts and healthz responds
go run ./cmd/server &
sleep 2
curl -s http://localhost:8080/api/v1/healthz | jq .status  # "ok"

# 4. Seed data loads
make seed  # should say "seed complete" (idempotent)

# 5. Full auth flow works
# (see phase 4 verification)

# 6. Ledger is valid
# (login, create spray, verify)

# 7. OpenAPI complete
jq '.paths | keys | length' openapi.json  # >= 30

kill %1  # stop server
```
