# Phase 10 — Inspector Endpoints + Damage Claims

**Status**: PENDING
**Goal**: All API surface complete. Inspector map endpoint works. Damage claims with photo upload work. Ledger still valid after all operations.

---

## Current State (before this phase)

Phases 1-9 complete. All major flows work. Remaining stubs:
- `internal/api/inspector.go` — all 3 endpoints still 501 stubs
- `internal/api/damage.go` — all damage claim endpoints still 501 stubs
- `internal/api/router.go` — no raw route for `/uploads/sign`

What exists for this phase:
- sqlc functions for damage claims:
  - `CreateDamageClaim(ctx, CreateDamageClaimParams) (DamageClaim, error)` — params: `{ID, BeekeeperID, ApiaryID, RelatedSprayID uuid.NullUUID, Description, HiveLossCount int32, GpsLat, GpsLng, LedgerHash}`
  - `GetDamageClaim(ctx, id) (DamageClaim, error)`
  - `ListDamageClaimsByBeekeeper(ctx, beekeeperID) ([]DamageClaim, error)`
  - `ListAllDamageClaims(ctx) ([]DamageClaim, error)`
  - `AddDamagePhoto(ctx, AddDamagePhotoParams) (DamagePhoto, error)` — params: `{ID, DamageClaimID, Url}`
  - `ListDamagePhotos(ctx, damageClaimID) ([]DamagePhoto, error)`
- sqlc functions for inspector:
  - `ListAllApiaries(ctx) ([]Apiary, error)`
  - `ListAllSprayReports(ctx) ([]SprayReport, error)` — actually `ListActiveSprayReports(ctx)` for active ones
  - `ListAllDamageClaims(ctx) ([]DamageClaim, error)`
  - `ListUsersByRole(ctx, role) ([]User, error)`
  - `GetUserByID(ctx, id) (User, error)`
  - `CountLedgerEvents(ctx) (int64, error)` (from ledger_events.sql.go)
- `dbsqlc.DamageClaim` struct: `{ID uuid.UUID, BeekeeperID uuid.UUID, ApiaryID uuid.UUID, RelatedSprayID uuid.NullUUID, Description string, HiveLossCount int32, GpsLat float64, GpsLng float64, Status DamageClaimStatus, LedgerHash string, CreatedAt time.Time}`
- `dbsqlc.DamagePhoto` struct: `{ID uuid.UUID, DamageClaimID uuid.UUID, Url string}`
- Uploads directory: `uploads/` exists (created by Phase 9). `uploads/photos/` needs to be created.
- `middleware.RequireRole(domain.RoleInspector)` available

There are NO sqlc queries for counting sprays by farmer or damages by farmer — those must be done in Go by filtering the full list, OR via raw SQL queries. Use Go-side counting for simplicity.

---

## Prerequisites

Phases 1-9 complete. Seed data loaded. `make migrate-up` applied through migration 00004.

---

## Files to Modify

### `internal/api/inspector.go`

Implement all 3 endpoints. Role check: all require `domain.RoleInspector`.

**Define response types:**

```go
type ApiaryMapPoint struct {
    ID        string  `json:"id"`
    Name      string  `json:"name"`
    OwnerName string  `json:"owner_name"`
    Lat       float64 `json:"lat"`
    Lng       float64 `json:"lng"`
    Status    string  `json:"status"` // "safe" | "alert" | "damaged"
    HiveCount int     `json:"hive_count"`
}

type ParcelMapPoint struct {
    ID        string  `json:"id"`
    OwnerName string  `json:"owner_name"`
    Lat       float64 `json:"lat"`
    Lng       float64 `json:"lng"`
}

type SprayMapPoint struct {
    ID          string    `json:"id"`
    FarmerName  string    `json:"farmer_name"`
    Substance   string    `json:"substance"`
    Toxicity    string    `json:"toxicity"`
    ScheduledAt time.Time `json:"scheduled_at"`
    Status      string    `json:"status"`
    AffectedCount int     `json:"affected_apiaries_count"`
}

type MapDataOutput struct {
    Body struct {
        Apiaries []ApiaryMapPoint  `json:"apiaries"`
        Sprays   []SprayMapPoint   `json:"active_sprays"`
        Damages  []DamageMapPoint  `json:"damage_claims"`
        Stats    struct {
            TotalApiaries int `json:"total_apiaries"`
            ActiveSprays  int `json:"active_sprays"`
            OpenDamages   int `json:"open_damage_claims"`
        } `json:"stats"`
    }
}

type DamageMapPoint struct {
    ID        string    `json:"id"`
    Lat       float64   `json:"gps_lat"`
    Lng       float64   `json:"gps_lng"`
    Status    string    `json:"status"`
    CreatedAt time.Time `json:"created_at"`
}
```

**`GET /inspector/map-data?bbox=lat1,lng1,lat2,lng2`**:

```go
type MapDataInput struct {
    Bbox string `query:"bbox"` // "lat1,lng1,lat2,lng2"
}

func (h *Handlers) getMapData(ctx context.Context, input *MapDataInput) (*MapDataOutput, error) {
    user := middleware.UserFromContext(ctx)
    if user.Role != domain.RoleInspector { return nil, huma.NewError(403, "inspector only") }

    // Parse bbox
    var lat1, lng1, lat2, lng2 float64
    if input.Bbox != "" {
        parts := strings.SplitN(input.Bbox, ",", 4)
        if len(parts) == 4 {
            lat1, _ = strconv.ParseFloat(parts[0], 64)
            lng1, _ = strconv.ParseFloat(parts[1], 64)
            lat2, _ = strconv.ParseFloat(parts[2], 64)
            lng2, _ = strconv.ParseFloat(parts[3], 64)
        }
    }

    q := dbsqlc.New(h.pool)

    // Load all data
    allApiaries, _ := q.ListAllApiaries(ctx)
    activeSprays, _ := q.ListActiveSprayReports(ctx)
    damages, _ := q.ListAllDamageClaims(ctx)
    allUsers, _ := q.ListAllUsers(ctx) // to get names

    // Build user name lookup map
    userNames := make(map[uuid.UUID]string, len(allUsers))
    for _, u := range allUsers { userNames[u.ID] = u.FullName }

    // Filter apiaries by bbox
    inBbox := func(lat, lng float64) bool {
        if lat1 == 0 && lat2 == 0 { return true } // no bbox filter
        return lat >= lat1 && lat <= lat2 && lng >= lng1 && lng <= lng2
    }

    // Build active alert set for apiary status
    activeAlertApiaries := make(map[uuid.UUID]bool)
    for _, s := range activeSprays {
        // Could look up dispatches, but for simplicity check if spray is recent
        _ = s
    }

    // Damaged apiaries from claims
    damagedApiaries := make(map[uuid.UUID]bool)
    for _, d := range damages {
        if d.Status == dbsqlc.DamageClaimStatusFiled || d.Status == dbsqlc.DamageClaimStatusUnderReview {
            damagedApiaries[d.ApiaryID] = true
        }
    }

    var apiaryPoints []ApiaryMapPoint
    for _, a := range allApiaries {
        if !inBbox(a.Lat, a.Lng) { continue }
        status := "safe"
        if damagedApiaries[a.ID] { status = "damaged" }
        if activeAlertApiaries[a.ID] { status = "alert" }
        apiaryPoints = append(apiaryPoints, ApiaryMapPoint{
            ID:        a.ID.String(),
            Name:      a.Name,
            OwnerName: userNames[a.OwnerID],
            Lat:       a.Lat,
            Lng:       a.Lng,
            Status:    status,
            HiveCount: int(a.HiveCount),
        })
    }

    var sprayPoints []SprayMapPoint
    for _, s := range activeSprays {
        sprayPoints = append(sprayPoints, SprayMapPoint{
            ID:          s.ID.String(),
            FarmerName:  userNames[s.FarmerID],
            Substance:   s.Substance,
            Toxicity:    s.Toxicity,
            ScheduledAt: s.ScheduledAt,
            Status:      string(s.Status),
            AffectedCount: int(s.AffectedApiariesCount),
        })
    }

    var damagePoints []DamageMapPoint
    for _, d := range damages {
        damagePoints = append(damagePoints, DamageMapPoint{
            ID:        d.ID.String(),
            Lat:       d.GpsLat,
            Lng:       d.GpsLng,
            Status:    string(d.Status),
            CreatedAt: d.CreatedAt,
        })
    }

    if apiaryPoints == nil { apiaryPoints = []ApiaryMapPoint{} }
    if sprayPoints == nil { sprayPoints = []SprayMapPoint{} }
    if damagePoints == nil { damagePoints = []DamageMapPoint{} }

    out := &MapDataOutput{}
    out.Body.Apiaries = apiaryPoints
    out.Body.Sprays = sprayPoints
    out.Body.Damages = damagePoints
    out.Body.Stats.TotalApiaries = len(apiaryPoints)
    out.Body.Stats.ActiveSprays = len(sprayPoints)
    out.Body.Stats.OpenDamages = len(damagePoints)
    return out, nil
}
```

**`GET /inspector/farmers`** — list all farmers with spray/damage counts:

```go
type FarmerSummary struct {
    ID         string `json:"id"`
    FullName   string `json:"full_name"`
    County     string `json:"county"`
    Locality   string `json:"locality"`
    SprayCount int    `json:"spray_count"`
    DamageCount int   `json:"damage_count_against"`
}

type ListFarmersOutput struct {
    Body struct {
        Farmers []FarmerSummary `json:"farmers"`
    }
}

func (h *Handlers) listFarmers(ctx context.Context, _ *struct{}) (*ListFarmersOutput, error) {
    user := middleware.UserFromContext(ctx)
    if user.Role != domain.RoleInspector { return nil, huma.NewError(403, "inspector only") }

    q := dbsqlc.New(h.pool)
    farmers, _ := q.ListUsersByRole(ctx, dbsqlc.UserRoleFermier)
    allSprays, _ := q.ListAllSprayReports(ctx)
    allDamages, _ := q.ListAllDamageClaims(ctx)
    allApiaries, _ := q.ListAllApiaries(ctx)

    // Build farmer → apiary set (for damage correlation)
    farmerApiaries := make(map[uuid.UUID]map[uuid.UUID]bool)
    for _, a := range allApiaries {
        // This is beekeeper → apiary, not farmer. Damages are filed against sprays by farmers.
        // Correlate via related_spray_id → spray.farmer_id
        _ = a
    }
    _ = farmerApiaries

    // Count sprays per farmer
    sprayCount := make(map[uuid.UUID]int)
    for _, s := range allSprays { sprayCount[s.FarmerID]++ }

    // Count damages where related_spray is from this farmer
    // Build spray → farmer map
    sprayFarmer := make(map[uuid.UUID]uuid.UUID)
    for _, s := range allSprays { sprayFarmer[s.ID] = s.FarmerID }

    damageCount := make(map[uuid.UUID]int)
    for _, d := range allDamages {
        if d.RelatedSprayID.Valid {
            if farmerID, ok := sprayFarmer[d.RelatedSprayID.UUID]; ok {
                damageCount[farmerID]++
            }
        }
    }

    summaries := make([]FarmerSummary, len(farmers))
    for i, f := range farmers {
        summaries[i] = FarmerSummary{
            ID:          f.ID.String(),
            FullName:    f.FullName,
            County:      f.County,
            Locality:    f.Locality,
            SprayCount:  sprayCount[f.ID],
            DamageCount: damageCount[f.ID],
        }
    }
    return &ListFarmersOutput{Body: struct{Farmers []FarmerSummary `json:"farmers"`}{summaries}}, nil
}
```

**`GET /inspector/farmers/:id`** — single farmer detail:

```go
type FarmerDetailInput struct {
    ID string `path:"id"`
}

type FarmerDetailOutput struct {
    Body struct {
        Farmer  FarmerSummary   `json:"farmer"`
        Sprays  []SprayReportResponse `json:"recent_sprays"`
        Damages []DamageResponse      `json:"damage_claims_against"`
    }
}

func (h *Handlers) getFarmerDetail(ctx context.Context, input *FarmerDetailInput) (*FarmerDetailOutput, error) {
    user := middleware.UserFromContext(ctx)
    if user.Role != domain.RoleInspector { return nil, huma.NewError(403, "inspector only") }

    farmerID, err := uuid.Parse(input.ID)
    if err != nil { return nil, huma.NewError(400, "invalid farmer id") }

    q := dbsqlc.New(h.pool)
    farmer, err := q.GetUserByID(ctx, farmerID)
    if err != nil { return nil, huma.NewError(404, "farmer not found") }

    sprays, _ := q.ListSprayReportsByFarmer(ctx, farmerID)
    allDamages, _ := q.ListAllDamageClaims(ctx)

    // Filter damages related to this farmer's sprays
    sprayIDs := make(map[uuid.UUID]bool)
    for _, s := range sprays { sprayIDs[s.ID] = true }

    var relatedDamages []DamageResponse
    for _, d := range allDamages {
        if d.RelatedSprayID.Valid && sprayIDs[d.RelatedSprayID.UUID] {
            relatedDamages = append(relatedDamages, dbDamageToResponse(d, nil))
        }
    }

    sprayResponses := make([]SprayReportResponse, len(sprays))
    for i, s := range sprays { sprayResponses[i] = dbSprayToResponse(s) }

    if relatedDamages == nil { relatedDamages = []DamageResponse{} }

    summary := FarmerSummary{
        ID: farmer.ID.String(), FullName: farmer.FullName,
        County: farmer.County, Locality: farmer.Locality,
        SprayCount: len(sprays), DamageCount: len(relatedDamages),
    }
    return &FarmerDetailOutput{Body: struct{
        Farmer  FarmerSummary
        Sprays  []SprayReportResponse
        Damages []DamageResponse
    }{summary, sprayResponses, relatedDamages}}, nil
}
```

Check the existing stub function names in `inspector.go` — match them exactly to what `registerInspector` registers.

### `internal/api/damage.go`

Implement all damage claim endpoints.

**Define types:**

```go
type CreateDamageClaimInput struct {
    Body struct {
        ApiaryID        string  `json:"apiary_id"`
        RelatedSprayID  *string `json:"related_spray_id,omitempty"`
        Description     string  `json:"description" minLength:"10"`
        HiveLossCount   int     `json:"hive_loss_count" minimum:"0"`
        GpsLat          float64 `json:"gps_lat"`
        GpsLng          float64 `json:"gps_lng"`
        Photos          []string `json:"photos,omitempty"` // pre-uploaded photo URLs
    }
}

type DamageResponse struct {
    ID             string    `json:"id"`
    BeekeeperID    string    `json:"beekeeper_id"`
    ApiaryID       string    `json:"apiary_id"`
    RelatedSprayID *string   `json:"related_spray_id"`
    Description    string    `json:"description"`
    HiveLossCount  int       `json:"hive_loss_count"`
    GpsLat         float64   `json:"gps_lat"`
    GpsLng         float64   `json:"gps_lng"`
    Status         string    `json:"status"`
    Photos         []string  `json:"photos"`
    LedgerHash     string    `json:"ledger_hash"`
    CreatedAt      time.Time `json:"created_at"`
}
```

Helper converter:
```go
func dbDamageToResponse(d dbsqlc.DamageClaim, photos []dbsqlc.DamagePhoto) DamageResponse {
    var relatedSprayID *string
    if d.RelatedSprayID.Valid { s := d.RelatedSprayID.UUID.String(); relatedSprayID = &s }
    photoURLs := make([]string, len(photos))
    for i, p := range photos { photoURLs[i] = p.Url }
    if photoURLs == nil { photoURLs = []string{} }
    return DamageResponse{
        ID:             d.ID.String(),
        BeekeeperID:    d.BeekeeperID.String(),
        ApiaryID:       d.ApiaryID.String(),
        RelatedSprayID: relatedSprayID,
        Description:    d.Description,
        HiveLossCount:  int(d.HiveLossCount),
        GpsLat:         d.GpsLat,
        GpsLng:         d.GpsLng,
        Status:         string(d.Status),
        Photos:         photoURLs,
        LedgerHash:     d.LedgerHash,
        CreatedAt:      d.CreatedAt,
    }
}
```

**`POST /damage-claims`** — apicultor creates a claim:

```go
func (h *Handlers) createDamageClaim(ctx context.Context, input *CreateDamageClaimInput) (*CreateDamageClaimOutput, error) {
    user := middleware.UserFromContext(ctx)
    if user.Role != domain.RoleApicultor { return nil, huma.NewError(403, "only beekeepers") }

    apiaryID, err := uuid.Parse(input.Body.ApiaryID)
    if err != nil { return nil, huma.NewError(400, "invalid apiary_id") }
    beekeeperID, _ := uuid.Parse(user.ID)

    q := dbsqlc.New(h.pool)

    // Verify apiary ownership
    apiary, err := q.GetApiary(ctx, apiaryID)
    if err != nil { return nil, huma.NewError(404, "apiary not found") }
    if apiary.OwnerID.String() != user.ID { return nil, huma.NewError(403, "not your apiary") }

    // Parse optional related spray ID
    var relatedSprayID uuid.NullUUID
    if input.Body.RelatedSprayID != nil {
        parsed, err := uuid.Parse(*input.Body.RelatedSprayID)
        if err == nil { relatedSprayID = uuid.NullUUID{UUID: parsed, Valid: true} }
    }

    // Begin transaction
    tx, err := h.pool.Begin(ctx)
    if err != nil { return nil, err }
    defer tx.Rollback(ctx)

    qtx := dbsqlc.New(tx)
    claimID := uuid.New()

    claim, err := qtx.CreateDamageClaim(ctx, dbsqlc.CreateDamageClaimParams{
        ID:             claimID,
        BeekeeperID:    beekeeperID,
        ApiaryID:       apiaryID,
        RelatedSprayID: relatedSprayID,
        Description:    input.Body.Description,
        HiveLossCount:  int32(input.Body.HiveLossCount),
        GpsLat:         input.Body.GpsLat,
        GpsLng:         input.Body.GpsLng,
        LedgerHash:     "", // updated after ledger append
    })
    if err != nil { return nil, fmt.Errorf("create claim: %w", err) }

    // Append ledger event
    actorID := user.ID
    hash, err := h.ledgerSvc.Append(ctx, tx, "damage.filed", &actorID, map[string]any{
        "claim_id":        claimID.String(),
        "apiary_id":       apiaryID.String(),
        "hive_loss_count": input.Body.HiveLossCount,
        "related_spray":   input.Body.RelatedSprayID,
    })
    if err != nil { return nil, err }

    // NOTE: damage_claims table has ledger_hash column but no UpdateDamageClaimLedgerHash query.
    // Use raw SQL or add a query. For simplicity, use raw SQL in the tx:
    _, _ = tx.Exec(ctx, "UPDATE damage_claims SET ledger_hash = $1 WHERE id = $2", hash, claimID)

    // Insert photos if provided
    var insertedPhotos []dbsqlc.DamagePhoto
    for _, url := range input.Body.Photos {
        p, err := qtx.AddDamagePhoto(ctx, dbsqlc.AddDamagePhotoParams{
            ID:            uuid.New(),
            DamageClaimID: claimID,
            Url:           url,
        })
        if err != nil { return nil, fmt.Errorf("add photo: %w", err) }
        insertedPhotos = append(insertedPhotos, p)
    }

    if err := tx.Commit(ctx); err != nil { return nil, err }

    claim.LedgerHash = hash
    resp := dbDamageToResponse(claim, insertedPhotos)
    return &CreateDamageClaimOutput{Body: struct{Claim DamageResponse `json:"claim"`}{resp}}, nil
}
```

**`GET /damage-claims`** — apicultor sees own, inspector sees all:

```go
func (h *Handlers) listDamageClaims(ctx context.Context, _ *struct{}) (*ListDamageClaimsOutput, error) {
    user := middleware.UserFromContext(ctx)
    q := dbsqlc.New(h.pool)
    var rows []dbsqlc.DamageClaim
    var err error
    switch user.Role {
    case domain.RoleApicultor:
        id, _ := uuid.Parse(user.ID)
        rows, err = q.ListDamageClaimsByBeekeeper(ctx, id)
    case domain.RoleInspector:
        rows, err = q.ListAllDamageClaims(ctx)
    default:
        return nil, huma.NewError(403, "forbidden")
    }
    if err != nil { return nil, err }

    results := make([]DamageResponse, len(rows))
    for i, r := range rows {
        photos, _ := q.ListDamagePhotos(ctx, r.ID)
        results[i] = dbDamageToResponse(r, photos)
    }
    return &ListDamageClaimsOutput{Body: struct{Claims []DamageResponse `json:"claims"`}{results}}, nil
}
```

**`GET /damage-claims/:id`**:

```go
type GetDamageClaimInput struct {
    ID string `path:"id"`
}

func (h *Handlers) getDamageClaim(ctx context.Context, input *GetDamageClaimInput) (*GetDamageClaimOutput, error) {
    user := middleware.UserFromContext(ctx)
    claimID, err := uuid.Parse(input.ID)
    if err != nil { return nil, huma.NewError(400, "invalid id") }

    q := dbsqlc.New(h.pool)
    claim, err := q.GetDamageClaim(ctx, claimID)
    if err != nil { return nil, huma.NewError(404, "not found") }

    // Access control: beekeepers can only see their own claims
    if user.Role == domain.RoleApicultor && claim.BeekeeperID.String() != user.ID {
        return nil, huma.NewError(403, "not your claim")
    }

    photos, _ := q.ListDamagePhotos(ctx, claimID)
    resp := dbDamageToResponse(claim, photos)
    return &GetDamageClaimOutput{Body: struct{Claim DamageResponse `json:"claim"`}{resp}}, nil
}
```

**`POST /uploads/sign`** — photo upload:

This is a raw chi handler (binary upload), NOT a Huma handler.

```go
func (h *Handlers) rawUploadPhoto(w http.ResponseWriter, r *http.Request) {
    // Limit upload size to 10MB
    r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

    // Parse file from form or raw body
    var data []byte
    var ext string

    contentType := r.Header.Get("Content-Type")
    if strings.HasPrefix(contentType, "multipart/") {
        if err := r.ParseMultipartForm(10 << 20); err != nil {
            http.Error(w, "bad request", http.StatusBadRequest)
            return
        }
        file, header, err := r.FormFile("photo")
        if err != nil {
            http.Error(w, "missing photo field", http.StatusBadRequest)
            return
        }
        defer file.Close()
        data, err = io.ReadAll(file)
        if err != nil { http.Error(w, "read error", http.StatusInternalServerError); return }
        ext = filepath.Ext(header.Filename)
    } else {
        // Raw binary body
        var err error
        data, err = io.ReadAll(r.Body)
        if err != nil { http.Error(w, "read error", http.StatusInternalServerError); return }
        // Detect extension from Content-Type
        switch contentType {
        case "image/jpeg": ext = ".jpg"
        case "image/png":  ext = ".png"
        case "image/gif":  ext = ".gif"
        case "image/webp": ext = ".webp"
        default:           ext = ".jpg"
        }
    }

    if len(data) == 0 {
        http.Error(w, "empty file", http.StatusBadRequest)
        return
    }

    // Save file
    fileID := uuid.New().String()
    filename := fileID + ext
    savePath := filepath.Join("uploads", "photos", filename)

    if err := os.MkdirAll(filepath.Dir(savePath), 0755); err != nil {
        http.Error(w, "storage error", http.StatusInternalServerError)
        return
    }
    if err := os.WriteFile(savePath, data, 0644); err != nil {
        http.Error(w, "write error", http.StatusInternalServerError)
        return
    }

    publicURL := "/uploads/photos/" + filename
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{
        "upload_url": "",
        "public_url": publicURL,
        "id":         fileID,
    })
}
```

Register in `router.go`:
```go
// Photo upload (protected — requires auth)
r.Group(func(r chi.Router) {
    r.Use(middleware.RequireAuth(jwtSvc))
    r.Post("/uploads/sign", h.rawUploadPhoto)
})
```

**NOTE**: The `/uploads/photos/` path is also served by the file server mounted at `/uploads/*`. Ensure the file server is mounted and the `uploads/photos/` directory is created on startup.

### `internal/services/seed.go`

Add a sample damage claim so the inspector map has data:

In `Run()`, after seeding apiaries and parcels, add:
```go
// Seed 1 damage claim (idempotent: check if any claims exist first)
func (s *SeedService) seedDamageClaim(ctx context.Context, beekeeperID, apiaryID uuid.UUID) error {
    existing, _ := s.db.ListDamageClaimsByBeekeeper(ctx, beekeeperID)
    if len(existing) > 0 { return nil }

    claimID := uuid.New()
    _, err := s.db.CreateDamageClaim(ctx, dbsqlc.CreateDamageClaimParams{
        ID:             claimID,
        BeekeeperID:    beekeeperID,
        ApiaryID:       apiaryID,
        RelatedSprayID: uuid.NullUUID{},
        Description:    "Găsit albine moarte în urma unui tratament observat în zonă. Aproximativ 30% din stup afectat.",
        HiveLossCount:  3,
        GpsLat:         46.784,
        GpsLng:         23.612,
        LedgerHash:     "",
    })
    return err
}
```

Call after seeding apiaries:
```go
// Seed damage claim for Andrei Berar's first apiary
if andreiUser != nil && andreiFirstApiary != nil {
    _ = s.seedDamageClaim(ctx, andreiUser.ID, andreiFirstApiary.ID)
}
```

This requires tracking the returned user/apiary IDs during seed — restructure `Run()` to save these:
```go
andrei, _ := s.ensureUser(ctx, "1850101123456", ...)
andreiApiaries, _ := s.seedApiariesForUser(ctx, andrei.ID, andreiData)
// ...
if len(andreiApiaries) > 0 {
    _ = s.seedDamageClaim(ctx, andrei.ID, andreiApiaries[0].ID)
}
```

This means `seedApiariesForUser` should return the list of `dbsqlc.Apiary` created (or existing).

### `cmd/server/main.go`

Add `uploads/photos/` to the directory creation on startup:
```go
for _, dir := range []string{"uploads/pdfs", "uploads/voice", "uploads/photos"} {
    if err := os.MkdirAll(dir, 0755); err != nil {
        slog.Error("failed to create upload dir", "dir", dir, "err", err)
        os.Exit(1)
    }
}
```

---

## Key Implementation Details

1. **No `UpdateDamageClaimLedgerHash` sqlc query**: The `damage_claims` table has a `ledger_hash` column but there's no generated query for it. Use raw `tx.Exec(ctx, "UPDATE damage_claims SET ledger_hash = $1 WHERE id = $2", hash, claimID)` directly on the transaction. This is acceptable — adding a full sqlc query would require running `make sqlc-gen` again.

2. **Inspector map bbox parsing**: `bbox=46.7,23.5,46.9,23.7` — parse as `strings.SplitN(bbox, ",", 4)`. If bbox is empty or malformed, return all data (no filter). The inspector needs to see everything in extreme cases.

3. **Performance**: `ListAllApiaries`, `ListAllSprayReports`, `ListAllDamageClaims` can return large datasets. For the hackathon with <100 records this is fine. No pagination needed for Phase 10.

4. **`ListAllUsers` query**: Needed for the inspector map to look up owner names. This is `q.ListAllUsers(ctx)` (check `internal/db/sqlc/users.sql.go` for the function name — it was `ListAllUsers`).

5. **Photo URL validation**: Photos submitted in `POST /damage-claims` should be pre-uploaded URLs (from `POST /uploads/sign`). No validation of URL format — trust the client to provide valid URLs from the upload endpoint.

6. **`filepath.Ext` for raw body uploads**: If no Content-Type matches, default to `.jpg`. This is acceptable for photos.

7. **`AddDamagePhotoParams` struct**: Check `internal/db/sqlc/damage_claims.sql.go` for the exact struct name. From the query: `INSERT INTO damage_photos (id, damage_claim_id, url) VALUES ($1, $2, $3)`. The struct is likely `AddDamagePhotoParams{ID uuid.UUID, DamageClaimID uuid.UUID, Url string}`.

8. **`dbsqlc.ListDamageClaimsByBeekeeper` return type**: Returns `[]DamageClaim` (not nullable). If empty result, returns `[]DamageClaim{}` (not nil). Safe to range over.

9. **Function name matching**: The stub functions in `inspector.go` and `damage.go` were registered in Phase 3 router. Match the exact function names. Check the existing `registerInspector` and `registerDamage` functions in those files to see what function names were used.

10. **Twilio webhook change**: Check that `rawUploadPhoto` is behind auth middleware but Twilio webhooks are NOT. In `router.go`, ensure the upload route is in the protected group.

---

## Verification Steps

```bash
# Build check
go build ./...
go vet ./...

# Re-seed to get damage claim
make seed

# Start server
go run ./cmd/server &

# Login as inspector (1680808777888 / parola123)
CHALLENGE=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"cnp":"1680808777888","password":"parola123"}' | jq -r .challenge_id)
# verify with code from stdout...
# inspect_cookies.txt = /tmp/inspector_cookies.txt

# GET map data (all data, no bbox filter)
curl -s 'http://localhost:8080/api/v1/inspector/map-data' \
  -b /tmp/inspector_cookies.txt | jq .
# Expected: apiaries:6, active_sprays depends on what's been created, damages:1

# GET map data with bbox (should include all seeded data near Cluj)
curl -s 'http://localhost:8080/api/v1/inspector/map-data?bbox=46.7,23.5,46.9,23.7' \
  -b /tmp/inspector_cookies.txt | jq '{apiaries:.body.stats.total_apiaries, sprays:.body.stats.active_sprays}'

# GET farmers list
curl -s http://localhost:8080/api/v1/inspector/farmers \
  -b /tmp/inspector_cookies.txt | jq .
# Expected: 3 farmers (Vasile, Elena, Gheorghe) with counts

# GET farmer detail
FARMER_ID=$(curl -s http://localhost:8080/api/v1/inspector/farmers \
  -b /tmp/inspector_cookies.txt | jq -r '.farmers[0].id')
curl -s "http://localhost:8080/api/v1/inspector/farmers/$FARMER_ID" \
  -b /tmp/inspector_cookies.txt | jq .

# Upload photo (as apicultor)
# Login as Andrei Berar first...
curl -s -X POST http://localhost:8080/uploads/sign \
  -H 'Content-Type: image/jpeg' \
  -b /tmp/beekeeper_cookies.txt \
  --data-binary @/path/to/test-image.jpg | jq .
# Expected: {"public_url":"/uploads/photos/<uuid>.jpg","id":"..."}

# Create damage claim
APIARY_ID=$(curl -s http://localhost:8080/api/v1/apiaries \
  -b /tmp/beekeeper_cookies.txt | jq -r '.apiaries[0].id')
PHOTO_URL=$(curl -s -X POST http://localhost:8080/uploads/sign \
  -H 'Content-Type: image/jpeg' \
  -b /tmp/beekeeper_cookies.txt \
  --data-binary @/dev/urandom 2>/dev/null || echo '{"public_url":"/uploads/photos/test.jpg"}' | jq -r .public_url)
# Use a simple multipart test:
curl -s -X POST http://localhost:8080/api/v1/damage-claims \
  -H 'Content-Type: application/json' \
  -b /tmp/beekeeper_cookies.txt \
  -d "{
    \"apiary_id\": \"$APIARY_ID\",
    \"description\": \"Test damage from pesticide spray, hives weakened.\",
    \"hive_loss_count\": 2,
    \"gps_lat\": 46.784,
    \"gps_lng\": 23.612,
    \"photos\": [\"/uploads/photos/test.jpg\"]
  }" | jq .
# Expected: claim created with ledger_hash

# GET damage claims as apicultor
curl -s http://localhost:8080/api/v1/damage-claims -b /tmp/beekeeper_cookies.txt | jq .
# Expected: claims for Andrei's apiaries

# GET damage claims as inspector
curl -s http://localhost:8080/api/v1/damage-claims -b /tmp/inspector_cookies.txt | jq '.claims | length'
# Expected: >= 1

# Verify ledger still valid after all operations
curl -s http://localhost:8080/api/v1/events/verify -b /tmp/beekeeper_cookies.txt | jq .
# Expected: {"valid":true,"total_events":N,...}

go vet ./...
go build ./...
```

---

## After Completion

Write `/Users/alinscreciu/work/hackathon/api/.planning/phases/phase-10.md`:

```markdown
# Phase 10 — Inspector Endpoints + Damage Claims
Status: COMPLETE
Completed: <date>

## What was built
- internal/api/inspector.go — GET /inspector/map-data (bbox filter), GET /inspector/farmers, GET /inspector/farmers/:id
- internal/api/damage.go — POST /damage-claims (ledger event), GET /damage-claims, GET /damage-claims/:id
- internal/api/router.go — POST /uploads/sign raw route, uploads/photos/ dir
- internal/services/seed.go — 1 sample damage claim added
- cmd/server/main.go — uploads/photos/ dir created on startup

## Verification
- Inspector map-data shows all apiaries, active sprays, damage claims
- POST /damage-claims creates claim with photos and ledger event
- GET /events/verify → valid:true
- go build ./... clean
```
