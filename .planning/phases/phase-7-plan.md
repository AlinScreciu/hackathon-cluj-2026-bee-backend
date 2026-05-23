# Phase 7 — AI Geo Service Mock + Weather Adapter

**Status**: PENDING
**Goal**: Create the AI geo assessment interface with a mock implementation. Wire real weather data from Open-Meteo with a 10-minute in-process cache.

---

## Current State (before this phase)

Phases 1-6 complete. Ledger works, auth works, read endpoints work.

What exists:
- `internal/external/` directory — subdirectories `elevenlabs/`, `email/`, `geoai/`, `twilio/`, `weather/`, `webpush/` exist but contain only placeholder/empty files (created in Phase 3 skeleton). Need to check actual content.
- `internal/config/config.go` — has `GeoAIBaseURL string` field
- `internal/domain/entities.go` — has `WeatherResult{WindDirectionDeg, WindSpeedMs, TemperatureC, FetchedAt}`
- `internal/api/reference.go` — `GET /reference/weather` currently returns hardcoded response; in this phase, wire to real CachedClient
- `internal/api/router.go` — `Handlers{cfg, pool, jwt, authSvc, ledgerSvc}`; need to add `geoAI geoai.Client` and `weatherClient *weather.CachedClient`

Check the existing files in `internal/external/` before writing:
- If `geoai/`, `weather/` have content already, update rather than overwrite.
- The Phase 3 skeleton likely created empty placeholder files.

---

## Prerequisites

Phases 1-6 complete. Server runs, seed data loaded.

---

## Files to Create

### `internal/external/geoai/types.go`

```go
package geoai

import "context"

type LatLng struct {
    Lat float64 `json:"lat"`
    Lng float64 `json:"lng"`
}

// Request is the payload sent to the AI geo assessment service.
type Request struct {
    TotalQuantity float64 `json:"total_quantity"`
    Type          string  `json:"type"`       // substance name or toxicity level
    CenterPoint   LatLng  `json:"center_point"`
}

// Result is the risk assessment returned by the AI geo service.
type Result struct {
    RiskRadiusM    float64 `json:"risk_radius_m"`
    AffectedAreaKm float64 `json:"affected_area_km2"`
    WindDirDeg     float64 `json:"wind_direction_deg"`
    Severity       string  `json:"severity"` // "low" | "medium" | "high"
}

// Client is the interface implemented by both MockClient and HTTPClient.
type Client interface {
    Assess(ctx context.Context, req Request) (*Result, error)
}

// NewClient returns MockClient if baseURL is empty, HTTPClient otherwise.
func NewClient(baseURL string) Client {
    if baseURL == "" {
        return &MockClient{}
    }
    return &HTTPClient{BaseURL: baseURL}
}
```

### `internal/external/geoai/mock.go`

```go
package geoai

import (
    "context"
    "log/slog"
    "math"
    "strings"
)

// MockClient implements Client for local development.
// It uses toxicity keyword matching to select a risk radius.
type MockClient struct{}

func (m *MockClient) Assess(ctx context.Context, req Request) (*Result, error) {
    slog.Info("[MOCK GeoAI] assessing risk",
        "total_quantity", req.TotalQuantity,
        "type", req.Type,
        "center", req.CenterPoint)

    radius := selectRadius(req.Type)
    // Affected area: circle with given radius (simplified, in km2)
    radiusKm := radius / 1000.0
    area := math.Pi * radiusKm * radiusKm

    severity := "low"
    if radius >= 3000 { severity = "high" } else if radius >= 1500 { severity = "medium" }

    return &Result{
        RiskRadiusM:    radius,
        AffectedAreaKm: area,
        WindDirDeg:     45.0, // constant mock wind direction (NE)
        Severity:       severity,
    }, nil
}

// selectRadius returns the risk radius in meters based on substance name keywords.
// T+ (highly toxic): 3000m
// T (toxic): 1500m
// T- (low toxicity): 750m
func selectRadius(substanceName string) float64 {
    name := strings.ToLower(substanceName)
    // T+ substances
    for _, kw := range []string{"confidor", "actara", "bulldock"} {
        if strings.Contains(name, kw) { return 3000 }
    }
    // T substances
    for _, kw := range []string{"mospilan", "karate", "calypso", "nurelle"} {
        if strings.Contains(name, kw) { return 1500 }
    }
    // T- default
    return 750
}
```

### `internal/external/geoai/http.go`

```go
package geoai

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

// HTTPClient calls the real AI geo service over HTTP.
// It is not used in development — MockClient is used instead.
type HTTPClient struct {
    BaseURL    string
    httpClient *http.Client
}

func newHTTPClient(baseURL string) *HTTPClient {
    return &HTTPClient{
        BaseURL: baseURL,
        httpClient: &http.Client{Timeout: 10 * time.Second},
    }
}

func (c *HTTPClient) Assess(ctx context.Context, req Request) (*Result, error) {
    // TODO: implement when real AI service is deployed
    // POST {BaseURL}/assess with JSON body
    b, _ := json.Marshal(req)
    httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/assess", bytes.NewReader(b))
    if err != nil { return nil, err }
    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := c.httpClient.Do(httpReq)
    if err != nil { return nil, fmt.Errorf("geoai http: %w", err) }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("geoai http status %d: %s", resp.StatusCode, body)
    }

    var result Result
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, fmt.Errorf("geoai decode: %w", err)
    }
    return &result, nil
}
```

### `internal/external/weather/client.go`

```go
package weather

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"

    "github.com/radarul-albinelor/api/internal/domain"
)

// WeatherClient fetches current weather from Open-Meteo.
type WeatherClient struct {
    httpClient *http.Client
}

func NewWeatherClient() *WeatherClient {
    return &WeatherClient{
        httpClient: &http.Client{Timeout: 5 * time.Second},
    }
}

// Fetch retrieves current weather at the given coordinates.
func (c *WeatherClient) Fetch(ctx context.Context, lat, lng float64) (*domain.WeatherResult, error) {
    url := fmt.Sprintf(
        "https://api.open-meteo.com/v1/forecast?latitude=%.6f&longitude=%.6f&current=wind_direction_10m,wind_speed_10m,temperature_2m&wind_speed_unit=ms",
        lat, lng)

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil { return nil, err }

    resp, err := c.httpClient.Do(req)
    if err != nil { return nil, fmt.Errorf("open-meteo fetch: %w", err) }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("open-meteo status %d", resp.StatusCode)
    }

    // Parse Open-Meteo response structure
    var body struct {
        Current struct {
            WindDirection10m float64 `json:"wind_direction_10m"`
            WindSpeed10m     float64 `json:"wind_speed_10m"`
            Temperature2m    float64 `json:"temperature_2m"`
        } `json:"current"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
        return nil, fmt.Errorf("open-meteo decode: %w", err)
    }

    return &domain.WeatherResult{
        WindDirectionDeg: body.Current.WindDirection10m,
        WindSpeedMs:      body.Current.WindSpeed10m,
        TemperatureC:     body.Current.Temperature2m,
        FetchedAt:        time.Now().UTC(),
    }, nil
}
```

### `internal/external/weather/cache.go`

```go
package weather

import (
    "context"
    "fmt"
    "sync"
    "time"

    "github.com/radarul-albinelor/api/internal/domain"
)

type cacheEntry struct {
    result *domain.WeatherResult
    expiry time.Time
}

// CachedClient wraps WeatherClient with an in-process TTL cache.
// Cache key: "lat,lng" rounded to 4 decimal places.
// This means requests within ~11m of each other share a cache entry.
type CachedClient struct {
    inner *WeatherClient
    ttl   time.Duration
    cache sync.Map // key: string → *cacheEntry
}

func NewCachedClient(ttl time.Duration) *CachedClient {
    return &CachedClient{
        inner: NewWeatherClient(),
        ttl:   ttl,
    }
}

// Get returns weather for the given coordinates, using cache if available.
func (c *CachedClient) Get(ctx context.Context, lat, lng float64) (*domain.WeatherResult, error) {
    key := fmt.Sprintf("%.4f,%.4f", lat, lng)

    if v, ok := c.cache.Load(key); ok {
        entry := v.(*cacheEntry)
        if time.Now().Before(entry.expiry) {
            return entry.result, nil // cache hit
        }
        c.cache.Delete(key) // expired
    }

    // Cache miss — fetch from upstream
    result, err := c.inner.Fetch(ctx, lat, lng)
    if err != nil { return nil, err }

    c.cache.Store(key, &cacheEntry{
        result: result,
        expiry: time.Now().Add(c.ttl),
    })
    return result, nil
}
```

### `internal/services/geo.go`

```go
package services

import "math"

// Haversine returns the distance in meters between two lat/lng points
// using the Haversine formula with Earth radius 6,371,000 m.
func Haversine(lat1, lng1, lat2, lng2 float64) float64 {
    const earthRadius = 6_371_000.0 // meters

    dLat := toRad(lat2 - lat1)
    dLng := toRad(lng2 - lng1)

    a := math.Sin(dLat/2)*math.Sin(dLat/2) +
        math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*
            math.Sin(dLng/2)*math.Sin(dLng/2)

    c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
    return earthRadius * c
}

func toRad(deg float64) float64 {
    return deg * math.Pi / 180
}
```

---

## Files to Modify

### `internal/api/reference.go`

Wire `GET /reference/weather` to real `CachedClient`.

The handler signature probably looks like:
```go
type WeatherInput struct {
    Lat float64 `query:"lat"`
    Lng float64 `query:"lng"`
}
type WeatherOutput struct {
    Body domain.WeatherResult
}
func (h *Handlers) getWeather(ctx context.Context, input *WeatherInput) (*WeatherOutput, error)
```

Replace hardcoded response with:
```go
func (h *Handlers) getWeather(ctx context.Context, input *WeatherInput) (*WeatherOutput, error) {
    lat, lng := input.Lat, input.Lng
    if lat == 0 && lng == 0 {
        // Default to Cluj-Napoca center if not provided
        lat, lng = 46.7712, 23.6236
    }
    result, err := h.weatherClient.Get(ctx, lat, lng)
    if err != nil {
        // Fallback to static data on error (don't fail the request)
        slog.Warn("weather fetch failed, using fallback", "err", err)
        result = &domain.WeatherResult{
            WindDirectionDeg: 45.0,
            WindSpeedMs:      3.2,
            TemperatureC:     18.5,
            FetchedAt:        time.Now().UTC(),
        }
    }
    return &WeatherOutput{Body: *result}, nil
}
```

**NOTE**: Check the existing stub in `reference.go` for exact handler/type names. Match them exactly.

### `internal/api/router.go`

Add fields to `Handlers`:
```go
type Handlers struct {
    cfg           *config.Config
    pool          *pgxpool.Pool
    jwt           *platform.JWTService
    authSvc       *services.AuthService
    ledgerSvc     *services.LedgerService
    geoAI         geoai.Client          // interface — MockClient or HTTPClient
    weatherClient *weather.CachedClient
}
```

In `NewRouter`:
```go
// Geo AI — use mock unless GEO_AI_BASE_URL is set
geoAIClient := geoai.NewClient(cfg.GeoAIBaseURL)

// Weather with 10-minute cache
weatherClient := weather.NewCachedClient(10 * time.Minute)

h := &Handlers{
    cfg:           cfg,
    pool:          pool,
    jwt:           jwtSvc,
    authSvc:       authSvc,
    ledgerSvc:     ledgerSvc,
    geoAI:         geoAIClient,
    weatherClient: weatherClient,
}
```

Add imports:
```go
"github.com/radarul-albinelor/api/internal/external/geoai"
"github.com/radarul-albinelor/api/internal/external/weather"
```

---

## Key Implementation Details

1. **`geoai.Client` is an interface**: The `Handlers` struct stores `geoai.Client` (interface), not a concrete type. This allows easy testing/swapping. `geoai.NewClient("")` returns `*MockClient` which implements `Client`.

2. **Weather cache is NOT shared across instances**: `sync.Map` is in-process. For a multi-instance deployment, each instance has its own cache. Acceptable for hackathon — noted as v1 limitation.

3. **Cache TTL**: 10 minutes matches the description. Subsequent requests within 10 minutes return the same `FetchedAt` timestamp, which is correct behavior (it shows when the data was fetched).

4. **Open-Meteo API is free, no key required**: The URL format is:
   `https://api.open-meteo.com/v1/forecast?latitude={lat}&longitude={lng}&current=wind_direction_10m,wind_speed_10m,temperature_2m&wind_speed_unit=ms`
   This returns the current conditions. The response shape:
   ```json
   {
     "current": {
       "wind_direction_10m": 180,
       "wind_speed_10m": 3.5,
       "temperature_2m": 19.2,
       "time": "2026-05-23T14:00"
     }
   }
   ```

5. **Haversine usage in Phase 8**: `services.Haversine(parcelLat, parcelLng, apiaryLat, apiaryLng)` — returns meters. Used to filter apiaries within `geoAI.Result.RiskRadiusM` in the spray report cascade.

6. **`geoai.MockClient` vs `geoai.HTTPClient`**: `NewClient` in `types.go` returns one or the other based on `baseURL`. The `HTTPClient` is a stub that returns an error for now. Only `MockClient` is used in development.

7. **Existing files in `internal/external/`**: Before creating files, check if they already have content. If the Phase 3 skeleton already created these files with placeholder content, overwrite with the actual implementation above. The subdirectories `geoai/`, `weather/`, etc. were created by Phase 3 but their content was likely empty or stub.

8. **Package naming**: The weather package is `package weather`, geoai is `package geoai`. Import paths:
   - `github.com/radarul-albinelor/api/internal/external/geoai`
   - `github.com/radarul-albinelor/api/internal/external/weather`
   - `github.com/radarul-albinelor/api/internal/services` (for Haversine)

9. **`domain.WeatherResult` JSON tags**: Verify the tags match what the API contract expects. From `entities.go`: `WindDirectionDeg float64 \`json:"wind_direction_deg"\``, etc. The reference handler returns this struct directly in the body.

---

## Verification Steps

```bash
# Build check after all changes
go build ./...
go vet ./...

# Start server
go run ./cmd/server &

# Login as any user to get cookie
# ... (see Phase 4 verification) ...

# Test weather endpoint with real data
curl -s 'http://localhost:8080/api/v1/reference/weather?lat=46.7712&lng=23.6236' \
  -b /tmp/beekeeper_cookies.txt | jq .
# Expected: real wind/temp data from Open-Meteo, fetched_at = now

# Call again within 10 minutes — should return SAME fetched_at (cache hit)
curl -s 'http://localhost:8080/api/v1/reference/weather?lat=46.7712&lng=23.6236' \
  -b /tmp/beekeeper_cookies.txt | jq .fetched_at
# Expected: identical timestamp to first call

# Test with different coordinates (different cache key)
curl -s 'http://localhost:8080/api/v1/reference/weather?lat=44.4268&lng=26.1025' \
  -b /tmp/beekeeper_cookies.txt | jq .
# Expected: different data (Bucharest coordinates), new fetched_at

# Test geoai mock (not directly exposed via API yet, tested via unit test or phase 8)
# Write a quick test:
cat > /tmp/test_geo.go << 'EOF'
package main
import (
    "context"
    "fmt"
    "github.com/radarul-albinelor/api/internal/external/geoai"
)
func main() {
    c := geoai.NewClient("")
    r, _ := c.Assess(context.Background(), geoai.Request{
        TotalQuantity: 50.0,
        Type:          "Confidor Energy",
        CenterPoint:   geoai.LatLng{Lat: 46.782, Lng: 23.608},
    })
    fmt.Printf("radius: %.0fm, severity: %s\n", r.RiskRadiusM, r.Severity)
    // Expected: radius: 3000m, severity: high

    r2, _ := c.Assess(context.Background(), geoai.Request{Type: "Teldor 500 SC"})
    fmt.Printf("radius: %.0fm, severity: %s\n", r2.RiskRadiusM, r2.Severity)
    // Expected: radius: 750m, severity: low
}
EOF
go run /tmp/test_geo.go

# Test Haversine
# Distance from Andrei's apiary (46.784, 23.612) to Vasile's parcel (46.782, 23.608)
# Should be ~350m
cat > /tmp/test_haversine.go << 'EOF'
package main
import (
    "fmt"
    "github.com/radarul-albinelor/api/internal/services"
)
func main() {
    d := services.Haversine(46.784, 23.612, 46.782, 23.608)
    fmt.Printf("distance: %.0fm\n", d) // Expected: ~370m
}
EOF
go run /tmp/test_haversine.go
```

---

## After Completion

Write `/Users/alinscreciu/work/hackathon/api/.planning/phases/phase-7.md`:

```markdown
# Phase 7 — AI Geo Service Mock + Weather Adapter
Status: COMPLETE
Completed: <date>

## What was built
- internal/external/geoai/types.go — Client interface, Request, Result, NewClient factory
- internal/external/geoai/mock.go — MockClient with toxicity keyword → radius lookup
- internal/external/geoai/http.go — HTTPClient stub (not used in dev)
- internal/external/weather/client.go — WeatherClient fetching from Open-Meteo
- internal/external/weather/cache.go — CachedClient with sync.Map, 10-min TTL
- internal/services/geo.go — Haversine(lat1,lng1,lat2,lng2) float64
- internal/api/reference.go — GET /reference/weather wired to CachedClient
- internal/api/router.go — geoAI and weatherClient added to Handlers

## Verification
- GET /reference/weather → real Open-Meteo data
- Second call within 10 min → same fetched_at (cache hit)
- MockClient: "Confidor" → 3000m radius, "Teldor" → 750m
- Haversine test: ~370m between two nearby Cluj coordinates
- go build ./... clean
```
