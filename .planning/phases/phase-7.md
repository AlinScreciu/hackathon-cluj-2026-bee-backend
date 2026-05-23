# Phase 7 — AI Geo Service Mock + Weather Adapter
Status: COMPLETE
Completed: 2026-05-23

## What was built
- internal/external/geoai/types.go — Client interface, Request/Result structs, NewClient factory
- internal/external/geoai/mock.go — MockClient: uses BeeToxicity field first, falls back to commercial name keyword matching
- internal/external/geoai/http.go — HTTPClient: POST /ai/risk-assess, maps richer response to internal Result
- internal/external/weather/cache.go — CachedClient wrapping existing Fetch with sync.Map, 10-min TTL
- internal/services/geo.go — Haversine(lat1,lng1,lat2,lng2) float64
- internal/api/reference.go — GET /reference/weather wired to h.weatherClient.Get(), Cluj fallback on error
- internal/api/router.go — geoAI (geoai.Client) and weatherClient (*weather.CachedClient) added to Handlers

## Key discovery: real AI service
The AI geo service at hackathon-cluj-2026-bee-ai-backend is a FastAPI app running on :8000.
- Endpoint: POST /ai/risk-assess (not /assess as originally planned)
- Request: crop, parcelId, product{commercialName, activeSubstance, beeToxicity}, dose, applicationMethod, appliedAt (ISO8601), durationHours, areaHa, center{lat, lon}
- Response fields used: riskLevel → Severity, notifyBeekeepersWithinMeters → RiskRadiusM, weatherUsed.windDirectionDegrees → WindDirDeg
- GeoAIBaseURL should point to http://localhost:8000 when running locally

## Verification
- go build ./... clean
- go vet ./... clean
- MockClient: beeToxicity="high" → 3000m radius, severity="high"
- MockClient: beeToxicity="medium" → 1500m radius, severity="medium"
- MockClient: no toxicity, name "Confidor" → 3000m (keyword fallback)
- HTTPClient maps POST /ai/risk-assess response to internal Result
- CachedClient wraps Fetch, same fetched_at on repeated calls within TTL
