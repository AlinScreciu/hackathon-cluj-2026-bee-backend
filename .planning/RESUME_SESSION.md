# Session Resume — BeeLive (after 2026-05-24 session)

## App name
**BeeLive** (brand). Voice TTS pronounces it as **"Bi-Liv"** (phonetic Romanian — do not change without checking).

## What's working — current state of main

### Full notification pipeline
- ✅ `POST /spray-reports` → cascade fires → push + voice + SMS reach beekeeper
- ✅ **Per-beekeeper dedup**: a beekeeper with N apiaries in radius now gets **one** push, **one** call, **one** SMS listing every affected apiary. State propagates from primary to siblings on any confirmation path.
- ✅ **SMS co-primary**: SMS fires alongside push (+ call for T/T+), no 60s fallback wait. 30-min unconfirmed-timeout still arms.
- ✅ Voice TTS via ElevenLabs (`HPdbgrGubKiBta6Pq21b`, multilingual_v2). All TwiML verbs (alert + post-confirmation + timeout + invalid-input) play ElevenLabs MP3s. Diacritics preserved.
- ✅ Twilio Romanian voice + SMS geo-permissions enabled in the Twilio console.

### Voice audio storage
- ✅ `internal/storage` package: `Storage` interface, `Local` (dev) + `R2` (prod, minio-go) impls.
- ✅ R2 bucket `beelive-voice` is **private** (no Public Access). `<Play>` URLs are short-lived (1h TTL) S3 presigned GETs. GDPR-safe.
- ✅ XML-escape applied to `<Play>` URLs so Twilio's parser handles the `&` in presigned querystrings.
- ✅ Cost defenses: `singleflight` dedupes concurrent gens, in-memory `sync.Map` skips R2 HEAD on cache hits in same process, `Prewarm` runs on boot for the 3 fixed messages.

### Auth / dev conveniences
- ✅ SMS 2FA real (sends to `+40770241335` in seed)
- ✅ Dev bypass code `000000` works in non-production
- ✅ 2FA code logged to terminal as `[2FA CODE] code=XXXXXX`
- ✅ Dispatches to BOTH SMS and email when both configured

### Other
- ✅ Server runs on `:9090`
- ✅ `go build ./... && go vet ./...` clean
- ✅ Resend email working (SMTP user is `resend`, not `apikey`)
- ✅ Cloudflared tunnel via `make tunnel` → set `APP_BASE_URL` to the trycloudflare URL

## Remaining work, in dependency order

### Phase 10 — Inspector endpoints + Damage claims (planned, not started)
**Plan**: `.planning/phases/phase-10-plan.md`
Stubbed endpoints:
- `internal/api/inspector.go` — `inspectorMap`, `inspectorFarmer`, `inspectorApiary` (all 501)
- `internal/api/damage.go` — `createDamageClaim`, `getDamageClaim`, `addDamagePhoto`, list (all 501)
- `internal/api/sprays.go:675` — `anfExport`
- `internal/api/apiaries.go:147` — `createApiary` (small, ~30 min, unblocks FE flow for adding hives via UI)

### Phase 11 — Polish (planned, not started)
**Plan**: `.planning/phases/phase-11-plan.md`
- Sliding JWT renewal in `RequireAuth` (currently hard 24h cap)
- ~~`make demo-spray` end-to-end automated script~~ — **deferred** until the demo accounts are finalized (script bakes in CNPs/IDs; doing it now would be wasted work)
- README

### Out-of-band quick wins
- `POST /apiaries` — small, ~30 min, blocks the FE "add hive" flow
- Move ElevenLabs alert text rendering to a service so it can be reused by SMS path (currently SMS body is built in cascade.go, voice text in webhooks_twilio.go — duplication)

## Key config in .env (do not commit)

```
APP_BASE_URL=https://<your-trycloudflare-subdomain>.trycloudflare.com  # changes when tunnel restarts
TWILIO_ACCOUNT_SID=<from Twilio console — AC...>
TWILIO_FROM_PHONE=<E.164 sender number>
ELEVENLABS_API_KEY=sk_...
ELEVENLABS_VOICE_ID=HPdbgrGubKiBta6Pq21b
R2_ACCOUNT_ID=<from Cloudflare dashboard>
R2_ACCESS_KEY_ID=<from R2 → Manage R2 API Tokens>
R2_SECRET_ACCESS_KEY=<same source, shown only once>
R2_BUCKET=beelive-voice
```

If the tunnel restarts → new URL → update `APP_BASE_URL` in `.env` → restart server.

## DB state — seeded demo

- **Farmer**: Vasile Mureșan, CNP `1920412111222`, pass `parola123`, phone `+40770241335`, email `alin.screciu01@gmail.com`
  - Parcel `aaaaaaaa-0000-0000-0000-000000000001` "Parcela Apahida Test" at `(46.781, 23.716)`
- **Beekeeper**: Andrei Berar, CNP `1850101123456`, pass `parola123`, phone `+40770241335`, email `alin.screciu01@gmail.com`
  - Apiary "Stupina Apahida Sud" at `(46.783, 23.7148)` ~ 240m from parcel
  - Apiary "Stupina Apahida Nord" at `(46.792, 23.722)` ~ 1300m from parcel

## Quick demo flow

```bash
# Terminal 1 — tunnel (keep running)
make tunnel
# copy https://xxxx.trycloudflare.com → APP_BASE_URL in .env

# Terminal 2 — server
go run ./cmd/server | tee /tmp/beelive.log
# look for: "storage R2 enabled (private bucket, presigned URLs) bucket=beelive-voice"

# Terminal 3 — login as farmer with dev bypass + fire spray
CHALLENGE=$(curl -s -c /tmp/ra.txt -X POST http://localhost:9090/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"cnp":"1920412111222","password":"parola123"}' | jq -r '.challenge_id')
curl -s -c /tmp/ra.txt -b /tmp/ra.txt \
  -X POST http://localhost:9090/api/v1/auth/2fa/verify \
  -H "Content-Type: application/json" \
  -d "{\"challenge_id\":\"$CHALLENGE\",\"code\":\"000000\"}" > /dev/null
curl -s -b /tmp/ra.txt -X POST http://localhost:9090/api/v1/spray-reports \
  -H "Content-Type: application/json" \
  -d '{
    "parcel_id": "aaaaaaaa-0000-0000-0000-000000000001",
    "surface_ha": 5.0, "crop": "porumb", "substance": "Confidor",
    "scheduled_at": "2026-05-26T08:00:00Z", "duration_hours": 4,
    "notes": "demo"
  }' | jq '.affected_apiaries, .risk_radius_m'
# expect: 2, 7000
```

Phone rings ONCE; SMS arrives ONCE; both mention both apiaries. Press 1 (or reply DA) → both dispatch rows get `confirmed_call` (or `confirmed_sms`).

## Files changed in the 2026-05-24 session

- `internal/storage/storage.go` — new package
- `internal/external/elevenlabs/client.go` — Storage dep, TextToSpeechURL, singleflight, in-mem cache, Prewarm
- `internal/services/cascade.go` — per-beekeeper grouping in Start, propagateFinalStatus, buildApiaryClause, SMS co-primary, ListDispatchSiblings usage
- `internal/api/webhooks_twilio.go` — playOrSay helper, multi-apiary voice text, XML escape for Play URLs, "Bi-Liv" pronunciation, "verificați SMS-ul" timeout text
- `internal/api/router.go` — storage init (R2 vs Local), prewarm wiring
- `internal/config/config.go` — R2 env vars + R2Enabled()
- `internal/db/queries/alert_dispatches.sql` — ListDispatchSiblings
- `.env.example` — R2 docs, GDPR note
- `CLAUDE.md` — critical rules 22-27, cascade-flow update, env vars, phase tracking
