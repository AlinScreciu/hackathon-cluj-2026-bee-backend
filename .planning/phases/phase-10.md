# Phase 10 — Inspector Dashboard + Damage Claims
Status: COMPLETE
Completed: 2026-05-24

## What was built

### Endpoints (7 new + 1 fixed)
- `POST /api/v1/uploads/sign` — Apicultor-only. Mints a 15-minute S3 presigned PUT URL to R2 for a damage photo. Validates mime (jpeg/png/webp) and byte_size (≤ 10 MiB). Returns `{upload_url, key, expires_in}`. The **key** (not a baked-in URL) is what gets persisted.
- `POST /api/v1/damage-claims` — Apicultor-only. Validates apiary ownership, persists the claim with `status=filed`, links each photo key, appends a `damage.filed` ledger event, then UPDATEs `damage_claims.ledger_hash` via raw SQL (no sqlc query exists for that column).
- `GET /api/v1/damage-claims` — Apicultor sees own; inspector sees all. Photos hydrated as fresh 1h presigned GET URLs at read time.
- `GET /api/v1/damage-claims/{id}` — Same role policy; same fresh-signed-URL hydration.
- `GET /api/v1/inspector/map-data?bbox=lat1,lng1,lat2,lng2` — Inspector-only. Returns apiaries (with status: damaged if any open claim, otherwise safe), active sprays, and open damage claims (filed | under_review). Optional bbox filter applied in-Go.
- `GET /api/v1/inspector/farmers` — Inspector-only. Lists every fermier with Go-side spray and damage counts.
- `GET /api/v1/inspector/farmers/{id}` — Inspector-only. Farmer detail + sprays in the last 30 days + count of damage claims filed against any of their sprays.
- `POST /api/v1/spray-reports/anf-export` — Inspector-only. Returns `application/pdf` (3-year audit registry). Registered as a **raw chi route** (not Huma) because binary responses don't fit Huma's content-negotiation flow. Reuses the existing `PDFService.GenerateANFExport`.
- `POST /api/v1/apiaries` — Apicultor-only. Out of Phase 10's original scope but unblocked the FE "add hive" flow. Validates type / lat / lng / hive_count / start_date, requires `end_date` for pastoral apiaries, writes an `apiary.registered` ledger event.

### Cascade fix (in the same session)
- Removed the dormant `scheduleSMSFallback(dispatchID, 0)` call from `HandleCallTerminal` in `internal/services/cascade.go`. SMS is now co-primary and already fires during `launchDispatch`; re-firing it on a missed call (no-answer | busy | failed | canceled) was a duplicate-SMS bug.

### Storage layer
- `internal/storage/storage.go` — `Storage` interface gained `SignedPutURL(ctx, key, ttl, contentType) (string, error)`. R2 impl uses minio-go's `PresignedPutObject`. Local impl returns a URL pointing at a dev-only raw chi PUT handler (gated off when storage is R2 so production never accepts uploads through the API).
- `internal/api/damage.go` — Added `rawUploadPut` (dev parity). Confines writes to the `photos/` prefix and rejects path-escape attempts.

### Wiring
- `internal/api/router.go` — `Handlers` struct gained `storage storage.Storage` + `storageIsR2 bool`. `audioStore` is now reused for damage photos too — voice MP3s live under `voice/...`, damage photos under `photos/...`, same private R2 bucket.
- `internal/api/inspector.go`, `damage.go`, `apiaries.go`, `sprays.go` — handler bodies filled.
- `internal/api/sprays.go:162` — anf-export huma.Register removed; replaced by `r.Post("/api/v1/spray-reports/anf-export", h.rawANFExport)` in router.go.

## Verification

`go build ./...` and `go vet ./...` clean. End-to-end curl flow exercised:
- 2FA dev-bypass login for fermier + apicultor + inspector roles.
- `POST /uploads/sign` returns an R2 presigned PUT URL containing `X-Amz-Signature`; PUTting a 1KB JPEG body to it returns `200`.
- `POST /damage-claims` with that key creates a claim, writes a `damage.filed` ledger event (hash chained to the previous), and returns the claim with the photo as a fresh `X-Amz-...` presigned GET.
- `GET /damage-claims/{id}` re-mints the presigned GET on every call — no stale URLs.
- Cross-tenant create rejected with 403 (apicultor can't file against another beekeeper's apiary).
- `GET /inspector/map-data` returns 8 apiaries, 13 active sprays, 1 damaged apiary. Bbox `46.78,23.70,46.80,23.73` filters down to the two Apahida apiaries.
- `GET /inspector/farmers` returns 3 fermieri with their Go-aggregated spray/damage counts.
- `POST /spray-reports/anf-export` returns `Content-Type: application/pdf`, valid PDF document (gofpdf 1.3, landscape A4). Apicultor → 403. Invalid date format → 400.
- Role gates tested: inspector forbidden from `/uploads/sign`, apicultor forbidden from `/inspector/map-data`.

## Critical gotchas (worth documenting)

- **`damage_photos.url` stores an R2 key, not a URL.** The column name is a lie; renaming mid-hackathon is more risk than reward. All read paths convert the key to a fresh presigned GET via `signedPhotoURLs`.
- **No sqlc query exists for `damage_claims.ledger_hash`.** The handler uses raw SQL: `tx.ExecContext(ctx, "UPDATE damage_claims SET ledger_hash = $1 WHERE id = $2", hash, id)`.
- **anf-export is the only POST in `/spray-reports/*` that lives outside Huma.** Don't add Huma handlers shadowing the path.
- **The dev-only raw PUT route is gated** on the storage backend (`if !storageIsR2`). A prod misconfig where R2 client init fails *and* falls back to Local would *also* enable raw PUT — log line `storage R2 init failed, falling back to local disk` is the audit signal.

## Notes
- `ListUsersByRole` already filters by role, so we don't need a separate "list farmers" query — `dbsqlc.UserRoleFermier` reuses the enum.
- Inspector "alert" status (active dispatch in the last 24h) was skipped — would require either a new sqlc query or in-Go cross-table aggregation. Apiaries are currently labelled `damaged` (any open claim) or `safe`. Future enhancement, not blocking the demo.
