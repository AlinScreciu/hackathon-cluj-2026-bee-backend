# Phase 5 — Seed Data + Reference Endpoints + Read Paths

**Status**: PENDING
**Goal**: `make seed` populates the DB with demo users/apiaries/parcels; all GET-only endpoints (apiaries, parcels, substances, weather, push subscriptions) return real data.

---

## Current State (before this phase)

Phase 4 is COMPLETE. Auth flow works end-to-end. Now we need data to work with.

### Already done (as of 2026-05-23 / Phase 4 completion)

- `internal/services/seed.go` — EXISTS and works. `Seed()` inserts all 7 demo users (Andrei Berar, Maria Costea, Ioan Lupu, Vasile Mureșan, Elena Popa, Gheorghe Stan, Inspector Județean Cluj) with bcrypt cost 12. Idempotent via `GetUserByCNP` check before inserting. **User seeding is complete — do not redo it.**
- `cmd/server/main.go` — `--seed` flag already implemented; calls `services.Seed()` and exits.
- `make seed` works and has been verified end-to-end.

### Still missing / what Phase 5 must build

- `services.Seed()` does NOT yet insert apiaries or parcels — that extension is the seed work for this phase.
- `internal/api/apiaries.go`, `parcels.go`, `push.go`, `reference.go` — still 501 stubs (GET endpoints).

### All sqlc functions available

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

### Substances already seeded (migration 00002)

Check `internal/db/migrations/00002_seed_substances.sql` to see what's already there. The substances table has a `ListSubstances` query.

---

## Prerequisites

Phase 4 complete. DB running on port 5433. `make migrate-up` applied.

### Critical implementation notes

- **`dbsqlc.New` requires `*sql.DB`, not `*pgxpool.Pool`**: `pgxpool.Pool` does NOT implement the `DBTX` interface directly when using the stdlib-compat layer. Use `stdlib.OpenDBFromPool(pool)` (from `github.com/jackc/pgx/v5/stdlib`) to obtain a `*sql.DB`, then pass that to `dbsqlc.New`. Example:
  ```go
  sqlDB := stdlib.OpenDBFromPool(pool)
  q := dbsqlc.New(sqlDB)
  ```
  **Update any existing code** in seed.go or handlers that currently does `dbsqlc.New(pool)` — if it compiles without this it means the queries package accepts pgxpool directly via a generated interface; check the actual generated `db.go` to confirm before changing.
- **"Not found" error sentinel**: Use `sql.ErrNoRows` (from `database/sql`), NOT `pgx.ErrNoRows`. When checking for a missing row: `errors.Is(err, sql.ErrNoRows)`.
- **App port is 9090** (set in `.env`). All `curl` test commands below use `:9090`, not `:8080`.

---

## Files to Create / Modify

### `internal/services/seed.go` — EXTEND (user seeding already done)

The file already exists with `Seed()` inserting 7 users. **Do not touch the user-seeding code.**

Extend `Seed()` (or add a helper called from it) to also insert apiaries and parcels after the users have been ensured.

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

### `cmd/server/main.go` — ALREADY DONE (skip)

`--seed` flag is implemented and working. No changes needed.

---

## HTTP Handlers to Implement

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

7. **"Not found" error sentinel**: Use `sql.ErrNoRows` from `"database/sql"`. Do NOT use `pgx.ErrNoRows` — the sqlc-generated layer surfaces `sql.ErrNoRows`. Check: `errors.Is(err, sql.ErrNoRows)`.

---

## Verification Steps

```bash
# Run seed (extends existing users with apiaries + parcels)
make seed
# Expected output: "seed complete" then exit
# Users were already seeded in Phase 4; this run adds apiaries and parcels

# Re-run seed (idempotency test)
make seed
# Expected: same output, no error, no duplicate rows

# Check DB
psql "postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable" \
  -c "SELECT cnp, full_name, role FROM users ORDER BY role, full_name;"
# Expected: 7 users (already present from Phase 4)

psql "postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable" \
  -c "SELECT name, lat, lng FROM apiaries ORDER BY name;"
# Expected: 6 apiaries

psql "postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable" \
  -c "SELECT name, surface_ha FROM parcels ORDER BY name;"
# Expected: 7 parcels

# Start server (port 9090 as set in .env)
go run ./cmd/server &

# Login as apicultor Andrei Berar
CHALLENGE=$(curl -s -X POST http://localhost:9090/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"cnp":"1850101123456","password":"parola123"}' | jq -r .challenge_id)
echo "Challenge: $CHALLENGE"
# Read code from server stdout: "[2FA SMS mock] code=XXXXXX"
# Then verify:
curl -s -X POST http://localhost:9090/api/v1/auth/2fa/verify \
  -H 'Content-Type: application/json' \
  -c /tmp/beekeeper_cookies.txt \
  -d "{\"challenge_id\":\"$CHALLENGE\",\"code\":\"XXXXXX\"}" | jq .

# GET apiaries (as apicultor)
curl -s http://localhost:9090/api/v1/apiaries -b /tmp/beekeeper_cookies.txt | jq .
# Expected: {"apiaries":[{...Stupina Andrei 1...},{...Stupina Andrei 2...}]}

# Try parcels as apicultor → 403
curl -s http://localhost:9090/api/v1/parcels -b /tmp/beekeeper_cookies.txt | jq .
# Expected: {"error":{"code":"forbidden_role",...}} or 403

# Login as fermier Vasile Mureșan and test parcels
CHALLENGE2=$(curl -s -X POST http://localhost:9090/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"cnp":"1920412111222","password":"parola123"}' | jq -r .challenge_id)
# verify with code from stdout...
curl -s http://localhost:9090/api/v1/parcels -b /tmp/farmer_cookies.txt | jq .
# Expected: 2 parcels for Vasile Mureșan

# GET substances (any logged-in user)
curl -s http://localhost:9090/api/v1/reference/substances -b /tmp/beekeeper_cookies.txt | jq .
# Expected: array of substances with label + toxicity

# GET weather (hardcoded)
curl -s 'http://localhost:9090/api/v1/reference/weather?lat=46.77&lng=23.59' \
  -b /tmp/beekeeper_cookies.txt | jq .
# Expected: {"wind_direction_deg":45,"wind_speed_ms":3.2,"temperature_c":18.5,"fetched_at":"..."}

# Push subscription
curl -s -X POST http://localhost:9090/api/v1/push/subscriptions \
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
