# Phase 9 — External Integrations
Status: COMPLETE
Completed: 2026-05-23

## What was built

### New files
- `internal/external/twilio/client.go` — Twilio REST client implementing `services.Notifier` (SendSMS, MakeCall, ValidateSignature via HMAC-SHA1)
- `internal/external/elevenlabs/client.go` — ElevenLabs TTS client with SHA256 disk cache at `uploads/voice/`; uses `eleven_multilingual_v2`, preserves Romanian diacritics
- `internal/external/webpush/client.go` — Web push client using `webpush-go`, implements `services.PushSender`, nil-safe when VAPID keys absent
- `internal/services/pdf.go` — PDF generation via gofpdf: `GeneratePrimariePDF` (A4 portrait) and `GenerateANFExport` (A4 landscape); `romanize()` for Latin-1 compatibility; `maskCNP()` for PII compliance
- `justfile` — replaces Makefile targets with `set dotenv-load` for automatic .env loading
- `.envrc` — direnv config (`dotenv_if_exists`) for automatic env var loading

### Modified files
- `internal/services/cascade.go` — wired real push subscription lookup (`ListPushSubscriptionsByUser`) + `buildPushPayload` helper
- `internal/api/router.go` — Handlers struct expanded with `twilioClient`, `elevenLabs`, `pushClient`, `pdfSvc`, `emailClient`; conditional client construction; `/uploads/*` static server; raw chi route for primarie PDF
- `internal/api/webhooks_twilio.go` — Twilio signature validation guard on all four handlers; ElevenLabs `<Play>` TwiML for initial voice gather (falls back to `<Say>` when not configured)
- `internal/api/sprays.go` — `getPrimariePDF` changed to raw HTTP handler; PDF background goroutine launched after `cascade.Start` (context.WithoutCancel, panic recovery, 10s timeouts on all DB/external calls)
- `cmd/server/main.go` — `os.MkdirAll` for `uploads/pdfs`, `uploads/voice`, `uploads/photos` before DB connect

## Verification
- `go build ./...` — clean
- `go vet ./...` — clean
- `just --list` — all targets visible
- `.envrc` loads `.env` via direnv
- With empty credentials: cascade still works (nil-guarded paths), webhook handlers return 200
- With credentials set: POST /spray-reports triggers async PDF write to `uploads/pdfs/<id>-primarie.pdf`

## Notes
- Twilio SDK doesn't support context cancellation on API calls; 10s timeout enforced at caller level
- gofpdf built-in fonts are Latin-1 only — `romanize()` converts Romanian diacritics before rendering
- ElevenLabs cache is checked before every API call; cache write failure is non-fatal (soft warning)
- Port is 8080 (local), GEO AI at 8000 (local FastAPI)
