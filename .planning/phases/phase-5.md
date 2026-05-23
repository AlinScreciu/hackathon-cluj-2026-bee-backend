# Phase 5 — Seed data + GET-only endpoints (apiaries, parcels, substances)

Status: COMPLETE
Completed: 2026-05-23

## Goal

Populate the database with realistic Cluj County demo data and expose the first read-only
endpoints so the frontend can list apiaries, parcels, substances, and live weather.

## What was built

- **`internal/services/seed.go`** — extended `Seed()` to insert 6 apiaries (2 per beekeeper)
  and 7 parcels (2–3 per farmer) with real Cluj County coordinates; all inserts are
  idempotent (keyed per owner so repeated `--seed` runs are safe)
- **`internal/api/apiaries.go`** — `GET /api/v1/apiaries` and `GET /api/v1/apiaries/:id`;
  apicultor-only; typed Huma structs; `status:"safe"` and `current_risk` stubbed for Phase 8
- **`internal/api/parcels.go`** — `GET /api/v1/parcels` and `GET /api/v1/parcels/:id`;
  fermier-only; typed Huma structs
- **`internal/external/weather/weather.go`** — Open-Meteo HTTP client with a 10 s timeout;
  caches last response for 10 minutes (per `CachedClient`)
- **`internal/api/reference.go`** — `GET /api/v1/reference/substances` (queries DB, returns typed
  list) and `GET /api/v1/reference/weather?lat=&lng=` (calls Open-Meteo, returns typed struct)
- **`internal/api/push.go`** — `POST /api/v1/push/subscriptions` and
  `DELETE /api/v1/push/subscriptions/:id`; authenticated; typed Huma structs

## Key Discoveries

- **`domain.User.ID` is `string`, not `uuid.UUID`.**
  Any handler that passes `user.ID` to a sqlc query param that expects `uuid.UUID` must call
  `uuid.Parse(user.ID)` first and return 401 on error. This affects every authenticated handler
  in phases 5 onward.

## Verification

- `make seed` populates apiaries and parcels without error on a clean or already-seeded DB
- `GET /api/v1/apiaries` returns the two apiaries for the logged-in apicultor
- `GET /api/v1/parcels` returns the parcels for the logged-in fermier
- `GET /api/v1/reference/substances` returns the substance list from DB
- `GET /api/v1/reference/weather?lat=46.77&lng=23.59` returns current wind/temp data
- Push subscription endpoints accept/delete subscriptions for an authenticated user
