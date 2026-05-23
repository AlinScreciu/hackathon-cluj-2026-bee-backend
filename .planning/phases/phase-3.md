# Phase 3 — HTTP Skeleton: Huma + Chi, Middleware, OpenAPI Export
Status: COMPLETE
Completed: 2026-05-23

## What was built
- internal/domain/enums.go — all typed string constants (Role, Toxicity, PushState, CallState, SmsState, FinalStatus, SprayStatus, ApiaryType, DamageClaimStatus, AuthMethod, InAppAction)
- internal/domain/entities.go — all domain structs (User, Apiary, Parcel, SprayReport, AlertDispatch, DamageClaim, LedgerEvent, PushSubscription, WeatherResult, Substance)
- internal/platform/jwt.go — JWTService with Sign/Verify, HS256, 24h expiry
- internal/middleware/recover.go — panic recovery → 500 JSON
- internal/middleware/cors.go — CORS via rs/cors
- internal/middleware/auth.go — RequireAuth (cookie → user in ctx), RequireRole (403 if wrong role)
- internal/api/router.go — Huma+Chi composition root, writes openapi.json on startup
- internal/api/ — all 10 handler files with stub implementations returning 501/400

## Verification
- GET /api/v1/healthz → 200 {"status":"ok"}
- POST /api/v1/auth/login (empty body) → 400 (Huma validation — correct)
- openapi.json written with 37 paths matching the contract
- go build ./... clean

## Notes
- chimiddleware.RealIP removed (deprecated, IP spoofing risk)
- VAPID public key endpoint returns real config value (no stub needed, trivial)
