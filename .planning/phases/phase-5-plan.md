# Phase 5 — Seed Data + Reference Endpoints + Read Paths

**Status**: PENDING
**Goal**: `make seed` populates the DB with demo users/apiaries/parcels; all GET-only endpoints (apiaries, parcels, substances, weather, push subscriptions) return real data.

---

## Current State (before this phase)

Phase 4 is COMPLETE. Auth flow works end-to-end. Now we need data to work with.

What exists:
- Full auth system working (login → cookie → /me)
- `internal/api/apiaries.go`, `parcels.go`, `push.go`, `reference.go` — all still 501 stubs
- `internal/db/sqlc/` — generated code with all query functions
- Key sqlc functions available:
  - `CreateUser(ctx, CreateUserParams) (User, error)` — params: `{ID, Cnp, FullName, Email, Phone, Role UserRole, County, Locality, PasswordHash}`
  - `GetUserByCNP(ctx, cnp) (User, error)` — for idempotency check
  - `CreateApiary(ctx, CreateApiaryParams) (Apiary, error)` — params: `{ID, OwnerID, Name, Type ApiaryType, Lat, Lng, HiveCount int32, StartDate time.Time, EndDate sql.NullTime, Notes sql.NullString}`
  - `GetApiary(ctx, id) (Apiary, error)`
  - `ListApiariesByOwner(ctx, ownerID) ([]Apiary, error)`
  - `ListAllApiaries(ctx) ([]Apiary, error)`
  - `CreateParcel(ctx, CreateParcelParams) (Parcel, error)` — params: `{ID, OwnerID, Name, CadastralNumber, Lat, Lng, SurfaceHa, DefaultCrop sql.NullString, County, Locality}`
  - `ListParcelsByOwner(ctx, ownerID) ([]Parcel, error)`
  - `GetParcel(ctx, id) (Parcel, error)`
  - `ListSubstances(ctx) ([]Substance, error)` — in substances.sql.go
  - `CreatePushSubscription(ctx, CreatePushSubscriptionParams) (PushSubscription, error)`
  - `DeletePushSubscription(ctx, DeletePushSubscriptionParams{ID, UserID}) error`
  - `ListPushSubscriptionsByUser(ctx, userID) ([]PushSubscription, error)`
- `dbsqlc.Apiary` struct: `{ID uuid.UUID, OwnerID uuid.UUID, Name string, Type ApiaryType, Lat float64, Lng float64, HiveCount int32, StartDate time.Time, EndDate sql.NullTime, Notes sql.NullString, CreatedAt time.Time}`
- `dbsqlc.Parcel` struct: `{ID uuid.UUID, OwnerID uuid.UUID, Name string, CadastralNumber string, Lat float64, Lng float64, SurfaceHa float64, DefaultCrop sql.NullString, County string, Locality string}`
- `cmd/server/main.go` — currently does NOT parse `--seed` flag

### Substances already seeded (migration 00002)

Check `internal/db/migrations/00002_seed_substances.sql` to see what's already there. The substances table has a `ListSubstances` query.

---

## Prerequisites

Phase 4 complete. DB running on port 5433. `make migrate-up` applied.

---

## Files to Create

### `internal/services/seed.go`

Package: `services`

```go
package services

import (
    "context"
    "database/sql"
    "log/slog"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
    dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
    "golang.org/x/crypto/bcrypt"
)

type SeedService struct {
    db   *dbsqlc.Queries
    pool *pgxpool.Pool
}

func NewSeedService(pool *pgxpool.Pool) *SeedService {
    return &SeedService{db: dbsqlc.New(pool), pool: pool}
}

func (s *SeedService) Run(ctx context.Context) error {
    // hash "parola123" at cost 12 — do this once, reuse for all users
    hash, err := bcrypt.GenerateFromPassword([]byte("parola123"), 12)
    if err != nil { return fmt.Errorf("bcrypt: %w", err) }
    passwordHash := string(hash)

    // insert users, apiaries, parcels
    // all inserts are idempotent: check existence first
}
```

#### Seeded Users

| CNP           | Role      | FullName                  | Email                          | Phone           | County | Locality     |
|---------------|-----------|---------------------------|--------------------------------|-----------------|--------|--------------|
| 1850101123456 | apicultor | Andrei Berar              | andrei.berar@test.com          | +40721000001    | Cluj   | Apahida      |
| 2900215654321 | apicultor | Maria Costea              | maria.costea@test.com          | +40721000002    | Cluj   | Florești     |
| 1780530987654 | apicultor | Ioan Lupu                 | ioan.lupu@test.com             | +40721000003    | Cluj   | Jucu         |
| 1920412111222 | fermier   | Vasile Mureșan            | vasile.muresan@test.com        | +40722000001    | Cluj   | Apahida      |
| 2880721333444 | fermier   | Elena Popa                | elena.popa@test.com            | +40722000002    | Cluj   | Florești     |
| 1751103555666 | fermier   | Gheorghe Stan             | gheorghe.stan@test.com         | +40722000003    | Cluj   | Jucu         |
| 1680808777888 | inspector | Inspector Județean Cluj   | inspector.cluj@test.com        | +40733000001    | Cluj   | Cluj-Napoca  |

All passwords: `parola123` (bcrypt cost 12)

#### Idempotency Pattern

```go
func (s *SeedService) ensureUser(ctx context.Context, cnp string, params dbsqlc.CreateUserParams) (dbsqlc.User, error) {
    existing, err := s.db.GetUserByCNP(ctx, cnp)
    if err == nil {
        slog.Info("seed: user already exists", "cnp_prefix", cnp[:4])
        return existing, nil
    }
    // errors.Is(err, pgx.ErrNoRows) or sql.ErrNoRows
    return s.db.CreateUser(ctx, params)
}
```

#### Apiaries (2 per apicultor)

After ensuring users, seed apiaries. Use deterministic UUIDs based on a namespace so re-running is idempotent. Simplest: use `uuid.NewSHA1(uuid.NameSpaceURL, []byte("apiary:andrei:1"))` — this gives a deterministic UUID v5.

But since we don't have a "get apiary by owner+name" query, use a simpler approach: try to create, catch unique violation. Since there's no unique constraint on apiaries besides `id`, use a different strategy: before inserting apiaries, check if `ListApiariesByOwner(ownerID)` returns any rows. If > 0, skip.

```go
func (s *SeedService) seedApiariesForUser(ctx context.Context, ownerID uuid.UUID, apiaries []apiaryData) error {
    existing, _ := s.db.ListApiariesByOwner(ctx, ownerID)
    if len(existing) > 0 {
        slog.Info("seed: apiaries already exist for owner", "count", len(existing))
        return nil
    }
    for _, a := range apiaries {
        _, err := s.db.CreateApiary(ctx, dbsqlc.CreateApiaryParams{
            ID:        uuid.New(),
            OwnerID:   ownerID,
            Name:      a.name,
            Type:      dbsqlc.ApiaryTypePermanent,
            Lat:       a.lat,
            Lng:       a.lng,
            HiveCount: int32(a.hiveCount),
            StartDate: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
            EndDate:   sql.NullTime{},
            Notes:     sql.NullString{},
        })
        if err != nil { return fmt.Errorf("create apiary: %w", err) }
    }
    return nil
}
```

Apiary data struct:
```go
type apiaryData struct { name string; lat, lng float64; hiveCount int }
```

Apiary coordinates:
- **Andrei Berar**: `{name:"Stupina Andrei 1", lat:46.784, lng:23.612, hiveCount:20}`, `{name:"Stupina Andrei 2", lat:46.791, lng:23.598, hiveCount:15}`
- **Maria Costea**: `{name:"Stupina Maria 1", lat:46.757, lng:23.541, hiveCount:18}`, `{name:"Stupina Maria 2", lat:46.749, lng:23.556, hiveCount:12}`
- **Ioan Lupu**: `{name:"Stupina Ioan 1", lat:46.803, lng:23.649, hiveCount:25}`, `{name:"Stupina Ioan 2", lat:46.810, lng:23.635, hiveCount:22}`

#### Parcels (2-3 per fermier)

Same idempotency pattern: check `ListParcelsByOwner` first.

Parcel data struct:
```go
type parcelData struct { name, cadastral string; lat, lng, surfaceHA float64 }
```

- **Vasile Mureșan** (county:Cluj, locality:Apahida):
  - `{name:"Parcela Apahida 1", cadastral:"CJ-APH-001", lat:46.782, lng:23.608, surfaceHA:5.2}`
  - `{name:"Parcela Apahida 2", cadastral:"CJ-APH-002", lat:46.776, lng:23.618, surfaceHA:3.8}`
- **Elena Popa** (county:Cluj, locality:Florești):
  - `{name:"Parcela Florești 1", cadastral:"CJ-FLR-001", lat:46.754, lng:23.537, surfaceHA:7.1}`
  - `{name:"Parcela Florești 2", cadastral:"CJ-FLR-002", lat:46.762, lng:23.528, surfaceHA:4.5}`
- **Gheorghe Stan** (county:Cluj, locality:Jucu):
  - `{name:"Parcela Jucu 1", cadastral:"CJ-JCU-001", lat:46.801, lng:23.645, surfaceHA:6.3}`
  - `{name:"Parcela Jucu 2", cadastral:"CJ-JCU-002", lat:46.795, lng:23.658, surfaceHA:5.0}`
  - `{name:"Parcela Jucu 3", cadastral:"CJ-JCU-003", lat:46.808, lng:23.641, surfaceHA:4.2}`

CreateParcel params:
```go
dbsqlc.CreateParcelParams{
    ID:              uuid.New(),
    OwnerID:         ownerID,
    Name:            p.name,
    CadastralNumber: p.cadastral,
    Lat:             p.lat,
    Lng:             p.lng,
    SurfaceHa:       p.surfaceHA,
    DefaultCrop:     sql.NullString{},
    County:          county,
    Locality:        locality,
}
```

---

## Files to Modify

### `cmd/server/main.go`

Add `--seed` flag detection after DB connection established but before building the router:

```go
// After pool.Ping succeeds:
for _, arg := range os.Args[1:] {
    if arg == "--seed" {
        seedSvc := services.NewSeedService(pool)
        if err := seedSvc.Run(ctx); err != nil {
            slog.Error("seed failed", "err", err)
            os.Exit(1)
        }
        slog.Info("seed complete")
        os.Exit(0)
    }
}
```

Add import: `"github.com/radarul-albinelor/api/internal/services"`

### `internal/api/apiaries.go`

Replace stubs. Response shape for apiary includes computed fields.

Define response types at top of file:
```go
type ApiaryRisk struct {
    NearestSprayKm  *float64 `json:"nearest_spray_km"`
    NearestSprayETA *string  `json:"nearest_spray_eta"`
    ActiveAlerts    int      `json:"active_alerts"`
}

type ApiaryResponse struct {
    ID          string     `json:"id"`
    OwnerID     string     `json:"owner_id"`
    Name        string     `json:"name"`
    Type        string     `json:"type"`
    Lat         float64    `json:"lat"`
    Lng         float64    `json:"lng"`
    HiveCount   int        `json:"hive_count"`
    StartDate   string     `json:"start_date"`
    EndDate     *string    `json:"end_date"`
    Notes       *string    `json:"notes"`
    Status      string     `json:"status"` // always "safe" for now
    CurrentRisk ApiaryRisk `json:"current_risk"`
    LastLedgerHash string  `json:"last_ledger_hash"`
    CreatedAt   time.Time  `json:"created_at"`
    History     []any      `json:"history"` // always empty []
}
```

Helper to convert `dbsqlc.Apiary` → `ApiaryResponse`:
```go
func dbApiaryToResponse(a dbsqlc.Apiary) ApiaryResponse {
    var endDate *string
    if a.EndDate.Valid {
        s := a.EndDate.Time.Format("2006-01-02")
        endDate = &s
    }
    var notes *string
    if a.Notes.Valid { notes = &a.Notes.String }
    return ApiaryResponse{
        ID:      a.ID.String(),
        OwnerID: a.OwnerID.String(),
        Name:    a.Name,
        Type:    string(a.Type),
        Lat:     a.Lat,
        Lng:     a.Lng,
        HiveCount:      int(a.HiveCount),
        StartDate:      a.StartDate.Format("2006-01-02"),
        EndDate:        endDate,
        Notes:          notes,
        Status:         "safe",
        CurrentRisk:    ApiaryRisk{ActiveAlerts: 0},
        LastLedgerHash: "",
        CreatedAt:      a.CreatedAt,
        History:        []any{},
    }
}
```

**`GET /api/v1/apiaries`** — requires apicultor role. Input: none. List by owner.

The Huma handler already registered in `registerApiaries`. You need to implement the actual function body. The handler currently calls `h.listApiaries`. Implement:
```go
func (h *Handlers) listApiaries(ctx context.Context, _ *struct{}) (*ListApiariesOutput, error) {
    user := middleware.UserFromContext(ctx)
    if user == nil { return nil, huma.NewError(http.StatusUnauthorized, "unauthorized") }
    q := dbsqlc.New(h.pool)
    ownerID, err := uuid.Parse(user.ID)
    if err != nil { return nil, huma.NewError(http.StatusBadRequest, "invalid user id") }
    rows, err := q.ListApiariesByOwner(ctx, ownerID)
    if err != nil { return nil, fmt.Errorf("list apiaries: %w", err) }
    items := make([]ApiaryResponse, len(rows))
    for i, r := range rows { items[i] = dbApiaryToResponse(r) }
    return &ListApiariesOutput{Body: struct{ Apiaries []ApiaryResponse `json:"apiaries"` }{items}}, nil
}
```

NOTE: `RequireRole` is applied as chi middleware in the router group. Double-check `router.go` — if the role check is handled by middleware, the handler doesn't need to re-check. If not set up per-handler, apply `middleware.RequireRole(domain.RoleApicultor)` in the router.

**`GET /api/v1/apiaries/:id`** — return single apiary. Check ownership: `apiary.OwnerID != user.ID` → 403.

**`POST /api/v1/apiaries`** — STUB returning 501 for now (fully implemented in Phase 9).

**`PATCH /api/v1/apiaries/:id`** — STUB returning 501 for now (fully implemented in Phase 6).

Check the existing stub handler signatures in `apiaries.go` to match what `registerApiaries` registers. If names/signatures differ, update accordingly.

### `internal/api/parcels.go`

Define response type:
```go
type ParcelResponse struct {
    ID              string   `json:"id"`
    OwnerID         string   `json:"owner_id"`
    Name            string   `json:"name"`
    CadastralNumber string   `json:"cadastral_number"`
    Lat             float64  `json:"lat"`
    Lng             float64  `json:"lng"`
    SurfaceHA       float64  `json:"surface_ha"`
    DefaultCrop     *string  `json:"default_crop"`
    County          string   `json:"county"`
    Locality        string   `json:"locality"`
}
```

**`GET /api/v1/parcels`** — RequireRole fermier. `ListParcelsByOwner(ownerID)`.

**`GET /api/v1/parcels/:id`** — check ownership. Return ParcelResponse.

**`POST /api/v1/parcels`** — STUB 501 (Phase 8).

### `internal/api/reference.go`

**`GET /api/v1/reference/substances`** — requires auth. `dbsqlc.New(h.pool).ListSubstances(ctx)` — return array.

Check `internal/db/queries/substances.sql` for the `ListSubstances` query name. Response:
```go
type SubstanceResponse struct {
    ID       string `json:"id"`
    Label    string `json:"label"`
    Toxicity string `json:"toxicity"`
}
```

**`GET /api/v1/reference/weather`** — in Phase 5 return hardcoded response. In Phase 7 this gets wired to real client. For now:
```go
return &WeatherOutput{Body: domain.WeatherResult{
    WindDirectionDeg: 45.0,
    WindSpeedMs:      3.2,
    TemperatureC:     18.5,
    FetchedAt:        time.Now().UTC(),
}}, nil
```
The handler takes `lat`, `lng` query params (already defined in existing stub input struct if present).

### `internal/api/push.go`

**`POST /api/v1/push/subscriptions`** — create push subscription.

Input body:
```go
type PushSubscribeInput struct {
    Body struct {
        Endpoint string `json:"endpoint"`
        P256dh   string `json:"p256dh"`
        Auth     string `json:"auth"`
    }
}
```
Implementation: `CreatePushSubscription(ctx, dbsqlc.CreatePushSubscriptionParams{ID: uuid.New(), UserID: ownerID, Endpoint: body.Endpoint, P256dh: body.P256dh, Auth: body.Auth})`. ON CONFLICT DO UPDATE already in the SQL query so this is upsert-safe.

**`DELETE /api/v1/push/subscriptions/:id`** — delete where `id = param AND user_id = user.ID`. Use `DeletePushSubscription(ctx, dbsqlc.DeletePushSubscriptionParams{ID: subID, UserID: userID})`.

**`GET /api/v1/push/vapid-public-key`** — already implemented in Phase 3 (returns `cfg.VAPIDPublicKey`), no change needed.

---

## Key Implementation Details

1. **`sql.NullTime` for dates**: `StartDate` in `CreateApiaryParams` is `time.Time` (NOT NULL). `EndDate` is `sql.NullTime`. For seeds without end date: `sql.NullTime{Valid: false}`.

2. **`dbsqlc.New(pool)` vs `dbsqlc.New(tx)`**: For seed, all operations can be non-transactional since idempotency is handled at the service level. Use `dbsqlc.New(pool)` directly.

3. **Role filtering in handlers**: The router in Phase 3 applies `middleware.RequireAuth` to the protected group. However, role-specific checks (apicultor for apiaries, fermier for parcels) need to be enforced per-handler, since all protected routes share one chi group. Apply role check inside handler using `middleware.RequireRole`. But `RequireRole` is an `http.Handler` middleware, not callable directly from a Huma handler. Instead, do the check manually:
   ```go
   user := middleware.UserFromContext(ctx)
   if user.Role != domain.RoleApicultor {
       return nil, huma.NewError(http.StatusForbidden, "forbidden_role")
   }
   ```
   OR: In `router.go` registerApiaries function, use chi sub-groups with role middleware. Look at how router.go currently calls `registerApiaries(humaAPI, h)` — you can split into role-specific groups by registering routes on a chi sub-router with the role middleware.

   Recommended: Do the role check inside the handler directly (simpler for now). The `middleware.RequireRole` function can be used at chi route level if needed in a later cleanup.

4. **UUID parsing**: `uuid.Parse(user.ID)` — import `github.com/google/uuid`. The ID in `domain.User` is a string (stored as UUID string from JWT).

5. **`ListSubstances` in sqlc**: Check that the generated function is `Queries.ListSubstances(ctx) ([]Substance, error)`. The substances.sql query should define this. If it's named differently, check `internal/db/sqlc/substances.sql.go`.

6. **`dbsqlc.UserRole`**: When creating users in seed, use the enum constants: `dbsqlc.UserRoleApicultor`, `dbsqlc.UserRoleFermier`, `dbsqlc.UserRoleInspector`.

7. **`pgx.ErrNoRows` import**: Use `"github.com/jackc/pgx/v5"` for the sentinel. Or catch with `errors.Is(err, sql.ErrNoRows)` if using the database/sql-compatible layer. Check what error is returned by testing: `errors.Is(err, pgx.ErrNoRows)` should work since pgxpool wraps it.

---

## Verification Steps

```bash
# Run seed
make seed
# Expected output: "seed complete" then exit

# Re-run seed (idempotency test)
make seed
# Expected: same output, no error, no duplicate rows

# Check DB
psql "postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable" \
  -c "SELECT cnp, full_name, role FROM users ORDER BY role, full_name;"
# Expected: 7 users

psql "postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable" \
  -c "SELECT name, lat, lng FROM apiaries ORDER BY name;"
# Expected: 6 apiaries

psql "postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable" \
  -c "SELECT name, surface_ha FROM parcels ORDER BY name;"
# Expected: 7 parcels

# Start server
go run ./cmd/server &

# Login as apicultor Andrei Berar
CHALLENGE=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"cnp":"1850101123456","password":"parola123"}' | jq -r .challenge_id)
echo "Challenge: $CHALLENGE"
# Read code from server stdout: "[2FA SMS mock] code=XXXXXX"
# Then verify:
curl -s -X POST http://localhost:8080/api/v1/auth/2fa/verify \
  -H 'Content-Type: application/json' \
  -c /tmp/beekeeper_cookies.txt \
  -d "{\"challenge_id\":\"$CHALLENGE\",\"code\":\"XXXXXX\"}" | jq .

# GET apiaries (as apicultor)
curl -s http://localhost:8080/api/v1/apiaries -b /tmp/beekeeper_cookies.txt | jq .
# Expected: {"apiaries":[{...Stupina Andrei 1...},{...Stupina Andrei 2...}]}

# Try parcels as apicultor → 403
curl -s http://localhost:8080/api/v1/parcels -b /tmp/beekeeper_cookies.txt | jq .
# Expected: {"error":{"code":"forbidden_role",...}} or 403

# Login as fermier Vasile Mureșan and test parcels
CHALLENGE2=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"cnp":"1920412111222","password":"parola123"}' | jq -r .challenge_id)
# verify with code from stdout...
curl -s http://localhost:8080/api/v1/parcels -b /tmp/farmer_cookies.txt | jq .
# Expected: 2 parcels for Vasile Mureșan

# GET substances (any logged-in user)
curl -s http://localhost:8080/api/v1/reference/substances -b /tmp/beekeeper_cookies.txt | jq .
# Expected: array of substances with label + toxicity

# GET weather (hardcoded)
curl -s 'http://localhost:8080/api/v1/reference/weather?lat=46.77&lng=23.59' \
  -b /tmp/beekeeper_cookies.txt | jq .
# Expected: {"wind_direction_deg":45,"wind_speed_ms":3.2,"temperature_c":18.5,"fetched_at":"..."}

# Push subscription
curl -s -X POST http://localhost:8080/api/v1/push/subscriptions \
  -H 'Content-Type: application/json' \
  -b /tmp/beekeeper_cookies.txt \
  -d '{"endpoint":"https://fcm.googleapis.com/test","p256dh":"abc","auth":"def"}' | jq .

# go vet
go vet ./...
go build ./...
```

---

## After Completion

Write `/Users/alinscreciu/work/hackathon/api/.planning/phases/phase-5.md`:

```markdown
# Phase 5 — Seed Data + Reference Endpoints + Read Paths
Status: COMPLETE
Completed: <date>

## What was built
- internal/services/seed.go — SeedService, 7 users / 6 apiaries / 7 parcels, idempotent
- cmd/server/main.go — --seed flag support
- internal/api/apiaries.go — GET /apiaries, GET /apiaries/:id implemented
- internal/api/parcels.go — GET /parcels, GET /parcels/:id implemented
- internal/api/reference.go — GET /reference/substances (real DB), GET /reference/weather (hardcoded)
- internal/api/push.go — POST /push/subscriptions, DELETE /push/subscriptions/:id

## Verification
- make seed: populates 7 users, 6 apiaries, 7 parcels idempotently
- GET /apiaries as apicultor → correct apiaries
- GET /parcels as fermier → correct parcels
- GET /reference/substances → 10 substances from DB
- go build ./... clean
```
