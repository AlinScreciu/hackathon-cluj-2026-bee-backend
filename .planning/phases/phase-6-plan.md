# Phase 6 — Ledger Service: SHA256 Hash Chain

**Status**: PENDING
**Goal**: Build a tamper-evident SHA256 hash chain ledger service. `GET /events/verify` returns `{"valid":true}`. `PATCH /apiaries/:id` writes a ledger event.

---

## Current State (before this phase)

Phases 1-5 complete. Auth works, seed data loaded. Now building the cryptographic ledger.

What exists:
- `internal/api/ledger.go` — stub handlers for `GET /events`, `GET /events/:hash`, `GET /events/verify`
- `internal/api/apiaries.go` — GET handlers work, PATCH stub still returns 501
- `internal/db/sqlc/ledger_events.sql.go` — full generated code:
  - `InsertLedgerEvent(ctx, InsertLedgerEventParams) (LedgerEvent, error)` — params: `{ID uuid.UUID, Hash string, PrevHash sql.NullString, Type string, ActorID uuid.NullUUID, Payload json.RawMessage, CreatedAt time.Time}`
  - `GetLastLedgerEvent(ctx) (LedgerEvent, error)`
  - `GetLedgerEventByHash(ctx, hash string) (LedgerEvent, error)`
  - `GetNextLedgerEvent(ctx, prevHash sql.NullString) (LedgerEvent, error)`
  - `ListLedgerEventsOrdered(ctx) ([]LedgerEvent, error)` — ASC by created_at
  - `ListLedgerEventsPaginated(ctx, ListLedgerEventsPaginatedParams) ([]LedgerEvent, error)` — params: `{Column1 string (type filter), Column2 uuid.UUID (actor filter, zero UUID = all), Limit int32, Offset int32}`
  - `CountLedgerEvents(ctx) (int64, error)`
- `dbsqlc.LedgerEvent` struct: `{ID uuid.UUID, Hash string, PrevHash sql.NullString, Type string, ActorID uuid.NullUUID, Payload json.RawMessage, CreatedAt time.Time}`
- `internal/api/router.go` — `Handlers{cfg, pool, jwt, authSvc}`. Need to add `ledgerSvc`.
- `UpdateApiaryLedgerHash` query exists but is a no-op stub in queries. Need to check: `internal/db/queries/apiaries.sql` has `UpdateApiaryLedgerHash` but the implementation is `UPDATE apiaries SET notes = notes WHERE id = $1` — this is wrong/placeholder. Phase 6 should fix this query if possible, or work around it.

**IMPORTANT — UpdateApiaryLedgerHash query bug**: Looking at the existing SQL:
```sql
-- name: UpdateApiaryLedgerHash :exec
UPDATE apiaries SET notes = notes WHERE id = $1;
```
This is a placeholder that does nothing. To actually store the ledger hash on the apiary, you have two options:
1. Add a new query `UpdateApiary` that includes a ledger_hash field (but `apiaries` table has no `ledger_hash` column — check migration 00001_init.sql — it doesn't!)
2. Store the ledger hash in the response only (not persisted on the apiary row itself).

**Resolution**: The `apiaries` table has no `ledger_hash` column. The `UpdateApiaryLedgerHash` query is a no-op. For Phase 6, the ledger hash is returned in the PATCH response but NOT stored on the apiary. The `last_ledger_hash` field in the apiary response is computed from the latest ledger event for that apiary (or empty string). This is acceptable for Phase 6 — Phase 10 can add a migration if needed.

Alternative: Add migration `00004_apiary_ledger_hash.sql` that adds `ledger_hash TEXT NOT NULL DEFAULT ''` to `apiaries`, then a real `UpdateApiaryLedgerHash` query. This is cleaner — do this in Phase 6.

**Decision**: Add a small migration to add `ledger_hash` to `apiaries`, then update the sqlc query to actually work.

---

## Prerequisites

Phase 5 complete. `make seed` has been run. DB has users, apiaries, parcels.

---

## Files to Create

### `internal/services/ledger.go`

Package: `services`

```go
package services

import (
    "context"
    "crypto/sha256"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "log/slog"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
    dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
    "github.com/radarul-albinelor/api/internal/domain"
)

type LedgerService struct {
    pool *pgxpool.Pool
}

type LedgerChain struct {
    Event    *domain.LedgerEvent  `json:"event"`
    PrevHash *string              `json:"prev_hash"`
    NextHash *string              `json:"next_hash"`
}

type VerifyResult struct {
    Valid       bool      `json:"valid"`
    TotalEvents int       `json:"total_events"`
    LastHash    string    `json:"last_hash"`
    CheckedAt   time.Time `json:"checked_at"`
    Error       string    `json:"error,omitempty"` // set if invalid
}

func NewLedgerService(pool *pgxpool.Pool) *LedgerService

// Append appends a new event to the ledger chain.
// If tx is nil, Append manages its own transaction.
// If tx is non-nil, Append uses that transaction (caller must commit/rollback).
// Returns the new event's hash.
func (s *LedgerService) Append(ctx context.Context, tx pgx.Tx, eventType string, actorID *string, payload any) (string, error)

// List returns paginated ledger events with optional filters.
func (s *LedgerService) List(ctx context.Context, typeFilter, actorFilter string, limit, offset int) ([]domain.LedgerEvent, error)

// GetByHash returns a single event and its chain context (prev/next hashes).
func (s *LedgerService) GetByHash(ctx context.Context, hash string) (*domain.LedgerEvent, *LedgerChain, error)

// Verify walks all events in order and recomputes each hash to detect tampering.
func (s *LedgerService) Verify(ctx context.Context) (*VerifyResult, error)
```

#### `Append` implementation

```go
func (s *LedgerService) Append(ctx context.Context, tx pgx.Tx, eventType string, actorID *string, payload any) (string, error) {
    ownTx := tx == nil
    if ownTx {
        var err error
        tx, err = s.pool.Begin(ctx)
        if err != nil { return "", fmt.Errorf("begin tx: %w", err) }
        defer func() {
            if ownTx { tx.Rollback(ctx) } // no-op if already committed
        }()
    }

    // Serialize all appends within this transaction using an advisory lock.
    // pg_advisory_xact_lock takes a bigint. 42 is our arbitrary app-specific lock key.
    if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(42)"); err != nil {
        return "", fmt.Errorf("advisory lock: %w", err)
    }

    q := dbsqlc.New(tx)

    // Get previous hash
    prevHash := ""
    last, err := q.GetLastLedgerEvent(ctx)
    if err != nil && !errors.Is(err, pgx.ErrNoRows) {
        return "", fmt.Errorf("get last event: %w", err)
    }
    if err == nil {
        prevHash = last.Hash
    }

    // Canonical JSON of payload
    canonical, err := canonicalJSON(payload)
    if err != nil { return "", fmt.Errorf("canonical json: %w", err) }

    createdAt := time.Now().UTC()

    // Compute hash: sha256(prevHash + canonicalPayload + createdAt_RFC3339)
    hash := sha256hex(prevHash + canonical + createdAt.Format(time.RFC3339))

    // Build params
    eventID := uuid.New()
    var prevHashNull sql.NullString
    if prevHash != "" { prevHashNull = sql.NullString{String: prevHash, Valid: true} }

    var actorIDNull uuid.NullUUID
    if actorID != nil {
        parsed, err := uuid.Parse(*actorID)
        if err == nil { actorIDNull = uuid.NullUUID{UUID: parsed, Valid: true} }
    }

    payloadJSON, _ := json.Marshal(payload)

    _, err = q.InsertLedgerEvent(ctx, dbsqlc.InsertLedgerEventParams{
        ID:        eventID,
        Hash:      hash,
        PrevHash:  prevHashNull,
        Type:      eventType,
        ActorID:   actorIDNull,
        Payload:   json.RawMessage(payloadJSON),
        CreatedAt: createdAt,
    })
    if err != nil { return "", fmt.Errorf("insert event: %w", err) }

    if ownTx {
        if err := tx.Commit(ctx); err != nil { return "", fmt.Errorf("commit: %w", err) }
    }

    slog.Debug("ledger event appended", "type", eventType, "hash", hash[:8])
    return hash, nil
}
```

#### `canonicalJSON` helper

```go
func canonicalJSON(v any) (string, error) {
    // Step 1: marshal to JSON
    b, err := json.Marshal(v)
    if err != nil { return "", err }
    // Step 2: unmarshal to map (this normalizes types)
    var m any
    if err := json.Unmarshal(b, &m); err != nil { return "", err }
    // Step 3: re-marshal (Go's json.Marshal sorts map keys alphabetically)
    b2, err := json.Marshal(m)
    if err != nil { return "", err }
    return string(b2), nil
}
```

#### `sha256hex` helper

```go
func sha256hex(s string) string {
    sum := sha256.Sum256([]byte(s))
    return fmt.Sprintf("%x", sum)
}
```

#### `Verify` implementation

```go
func (s *LedgerService) Verify(ctx context.Context) (*VerifyResult, error) {
    q := dbsqlc.New(s.pool)
    events, err := q.ListLedgerEventsOrdered(ctx)
    if err != nil { return nil, err }

    result := &VerifyResult{
        Valid:       true,
        TotalEvents: len(events),
        CheckedAt:   time.Now().UTC(),
    }

    prevHash := ""
    for _, e := range events {
        expectedPrevHash := ""
        if e.PrevHash.Valid { expectedPrevHash = e.PrevHash.String }
        if expectedPrevHash != prevHash {
            result.Valid = false
            result.Error = fmt.Sprintf("chain broken at event %s: expected prev_hash=%q got=%q", e.ID, prevHash, expectedPrevHash)
            return result, nil
        }

        // Recompute hash
        canonical, err := canonicalJSON(e.Payload)
        if err != nil { return nil, err }
        expected := sha256hex(prevHash + canonical + e.CreatedAt.UTC().Format(time.RFC3339))

        if expected != e.Hash {
            result.Valid = false
            result.Error = fmt.Sprintf("hash mismatch at event %s: expected %s got %s", e.ID, expected[:8], e.Hash[:8])
            return result, nil
        }
        prevHash = e.Hash
    }

    result.LastHash = prevHash
    return result, nil
}
```

**IMPORTANT — Hash computation must match exactly**: The payload stored in the DB is `json.RawMessage`. When verifying, `canonicalJSON(e.Payload)` receives `json.RawMessage` which marshals to its raw bytes (the stored JSON). This must produce the same string as during `Append`. This works because: during Append, we marshal the original payload, unmarshal to `any`, re-marshal. During Verify, `e.Payload` is `json.RawMessage` (the stored bytes). `json.Marshal(json.RawMessage(...))` returns the raw bytes unchanged. Then `json.Unmarshal` → re-marshal should give the same canonical form. BUT there's a subtlety: if the stored JSON has different whitespace or key ordering, re-canonicalization will normalize it. This is fine as long as the normalization is deterministic and idempotent (it is, since Go maps sort keys consistently).

#### `List` implementation

```go
func (s *LedgerService) List(ctx context.Context, typeFilter, actorFilter string, limit, offset int) ([]domain.LedgerEvent, error) {
    q := dbsqlc.New(s.pool)
    var actorUUID uuid.UUID
    if actorFilter != "" {
        parsed, err := uuid.Parse(actorFilter)
        if err == nil { actorUUID = parsed }
    }
    rows, err := q.ListLedgerEventsPaginated(ctx, dbsqlc.ListLedgerEventsPaginatedParams{
        Column1: typeFilter,
        Column2: actorUUID, // zero UUID when no filter
        Limit:   int32(limit),
        Offset:  int32(offset),
    })
    if err != nil { return nil, err }
    result := make([]domain.LedgerEvent, len(rows))
    for i, r := range rows { result[i] = dbLedgerToDomain(r) }
    return result, nil
}
```

Helper converter:
```go
func dbLedgerToDomain(e dbsqlc.LedgerEvent) domain.LedgerEvent {
    var prevHash *string
    if e.PrevHash.Valid { prevHash = &e.PrevHash.String }
    var actorID *string
    if e.ActorID.Valid { s := e.ActorID.UUID.String(); actorID = &s }
    var payload map[string]any
    json.Unmarshal(e.Payload, &payload)
    return domain.LedgerEvent{
        ID:        e.ID.String(),
        Hash:      e.Hash,
        PrevHash:  prevHash,
        Type:      e.Type,
        ActorID:   actorID,
        Payload:   payload,
        CreatedAt: e.CreatedAt,
    }
}
```

---

## Files to Create (DB Migration)

### `internal/db/migrations/00004_apiary_ledger_hash.sql`

```sql
-- +goose Up
ALTER TABLE apiaries ADD COLUMN IF NOT EXISTS ledger_hash TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE apiaries DROP COLUMN IF EXISTS ledger_hash;
```

After creating this file, update the sqlc query and regenerate.

### Update `internal/db/queries/apiaries.sql`

Fix the broken `UpdateApiaryLedgerHash` query:
```sql
-- name: UpdateApiaryLedgerHash :exec
UPDATE apiaries SET ledger_hash = $2 WHERE id = $1;
```

Also update `UpdateApiary` to include `ledger_hash` in RETURNING (it already returns `*` implicitly via the SELECT fields — check the generated code).

After fixing the query, run `make sqlc-gen` to regenerate. The generated `Apiary` struct in `models.go` will gain a `LedgerHash string` field. The `UpdateApiaryLedgerHash` function signature becomes:
```go
func (q *Queries) UpdateApiaryLedgerHash(ctx context.Context, id uuid.UUID, ledgerHash string) error
```
But sqlc needs a params struct. Check: the query has 2 params so sqlc generates `UpdateApiaryLedgerHashParams{ID uuid.UUID, LedgerHash string}`. Wait — sqlc may inline params for 2-arg exec. Confirm after generation.

**IMPORTANT**: After adding the migration, run:
```bash
make migrate-up    # applies 00004
make sqlc-gen      # regenerates db/sqlc/*.go
```

The `dbApiaryToResponse` function in `apiaries.go` needs updating to use `a.LedgerHash` for `LastLedgerHash` field.

---

## Files to Modify

### `internal/api/ledger.go`

Implement all 3 handlers. Define types:

```go
type ListEventsInput struct {
    Type   string `query:"type"`
    Actor  string `query:"actor"`
    Limit  int    `query:"limit"`
    Offset int    `query:"offset"`
}

type ListEventsOutput struct {
    Body struct {
        Events []domain.LedgerEvent `json:"events"`
    }
}

type GetEventByHashInput struct {
    Hash string `path:"hash"`
}

type GetEventByHashOutput struct {
    Body struct {
        Event *domain.LedgerEvent `json:"event"`
        Chain *services.LedgerChain `json:"chain"`
    }
}

type VerifyOutput struct {
    Body *services.VerifyResult
}
```

**`GET /events`** — `h.ledgerSvc.List(ctx, input.Type, input.Actor, limit, offset)`. Default limit=50.

**`GET /events/:hash`** — `h.ledgerSvc.GetByHash(ctx, input.Hash)`.

**`GET /events/verify`** — `h.ledgerSvc.Verify(ctx)`.

Check the existing stub signatures in `ledger.go` — match function names exactly.

### `internal/api/apiaries.go`

Implement `PATCH /apiaries/:id`:

Input:
```go
type PatchApiaryInput struct {
    ID   string `path:"id"`
    Body struct {
        Name      *string `json:"name,omitempty"`
        HiveCount *int    `json:"hive_count,omitempty"`
        Notes     *string `json:"notes,omitempty"`
        Type      *string `json:"type,omitempty"`
    }
}

type PatchApiaryOutput struct {
    Body struct {
        Apiary     ApiaryResponse `json:"apiary"`
        LedgerHash string         `json:"ledger_hash"`
    }
}
```

Implementation:
```go
func (h *Handlers) patchApiary(ctx context.Context, input *PatchApiaryInput) (*PatchApiaryOutput, error) {
    user := middleware.UserFromContext(ctx)
    apiaryID, _ := uuid.Parse(input.ID)

    // Begin transaction
    tx, err := h.pool.Begin(ctx)
    if err != nil { return nil, fmt.Errorf("begin: %w", err) }
    defer tx.Rollback(ctx)

    q := dbsqlc.New(tx)

    // Fetch current apiary
    current, err := q.GetApiary(ctx, apiaryID)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) { return nil, huma.NewError(404, "apiary not found") }
        return nil, err
    }

    // Ownership check
    if current.OwnerID.String() != user.ID {
        return nil, huma.NewError(403, "not your apiary")
    }

    // Apply partial updates (use current values as defaults)
    name := current.Name
    if input.Body.Name != nil { name = *input.Body.Name }
    hiveCount := current.HiveCount
    if input.Body.HiveCount != nil { hiveCount = int32(*input.Body.HiveCount) }
    notes := current.Notes
    if input.Body.Notes != nil { notes = sql.NullString{String: *input.Body.Notes, Valid: true} }
    apiaryType := current.Type
    if input.Body.Type != nil { apiaryType = dbsqlc.ApiaryType(*input.Body.Type) }

    // Update apiary
    updated, err := q.UpdateApiary(ctx, dbsqlc.UpdateApiaryParams{
        ID:        apiaryID,
        Name:      name,
        Type:      apiaryType,
        Lat:       current.Lat,
        Lng:       current.Lng,
        HiveCount: hiveCount,
        StartDate: current.StartDate,
        EndDate:   current.EndDate,
        Notes:     notes,
    })
    if err != nil { return nil, fmt.Errorf("update apiary: %w", err) }

    // Append ledger event (uses same tx)
    actorID := user.ID
    ledgerHash, err := h.ledgerSvc.Append(ctx, tx, "apiary.updated", &actorID, map[string]any{
        "apiary_id":  apiaryID.String(),
        "name":       name,
        "hive_count": hiveCount,
    })
    if err != nil { return nil, fmt.Errorf("ledger append: %w", err) }

    // Update apiary's ledger_hash
    if err := q.UpdateApiaryLedgerHash(ctx, dbsqlc.UpdateApiaryLedgerHashParams{
        ID:         apiaryID,
        LedgerHash: ledgerHash,
    }); err != nil {
        return nil, fmt.Errorf("update ledger hash: %w", err)
    }

    if err := tx.Commit(ctx); err != nil { return nil, fmt.Errorf("commit: %w", err) }

    resp := dbApiaryToResponse(updated)
    resp.LastLedgerHash = ledgerHash
    return &PatchApiaryOutput{Body: struct{Apiary ApiaryResponse; LedgerHash string}{resp, ledgerHash}}, nil
}
```

**Note on `UpdateApiary` params**: Check `internal/db/sqlc/apiaries.sql.go` for the exact `UpdateApiaryParams` struct. It was defined in Phase 3 sqlc generation. It may not include `LedgerHash` since the old migration didn't have that column. After running `make sqlc-gen` post-migration, the params struct updates automatically.

### `internal/api/router.go`

Add `ledgerSvc *services.LedgerService` field to `Handlers` struct.

In `NewRouter`:
```go
ledgerSvc := services.NewLedgerService(pool)
h := &Handlers{cfg: cfg, pool: pool, jwt: jwtSvc, authSvc: authSvc, ledgerSvc: ledgerSvc}
```

---

## Key Implementation Details

1. **Transaction passing to LedgerService.Append**: `pgx.Tx` is an interface from `github.com/jackc/pgx/v5`. The `h.pool.Begin(ctx)` returns `pgx.Tx`. Pass this directly. `dbsqlc.New(tx)` works because `pgx.Tx` implements the `DBTX` interface (it has `QueryRowContext`, `QueryContext`, `ExecContext` — wait, actually pgx.Tx has `QueryRow`, `Query`, `Exec` methods that DON'T use the `Context` variants by name. Let's verify.

   **CRITICAL**: `dbsqlc.DBTX` is defined in `internal/db/sqlc/db.go`. Read it:
   ```go
   type DBTX interface {
       ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
       PrepareContext(context.Context, string) (*sql.Stmt, error)
       QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
       QueryRowContext(context.Context, string, ...interface{}) *sql.Row
   }
   ```
   This is the `database/sql` interface pattern. `pgxpool.Pool` and `pgx.Tx` from pgx v5 DO NOT implement `database/sql` interface directly — they use a different interface pattern.

   Check `internal/db/sqlc/db.go` for the actual DBTX definition. Given that the project uses `pgxpool` and the generated code uses `database/sql` style (`QueryRowContext`, `QueryContext`, `ExecContext`), the sqlc config must be set to use `pgx/v5` pgxcompat mode or similar.

   Actually, looking at the generated models: `database/sql` is imported in `models.go` for `sql.NullTime`, `sql.NullString`. The queries use `q.db.QueryRowContext(ctx, ...)`. For this to work with `pgxpool.Pool`, the pool must be used via `stdlib.OpenDBFromPool` which wraps it into `*sql.DB`, OR sqlc is configured to use the pgx stdlib adapter.

   **Check `sqlc.yaml`** for the driver mode. Also check `internal/db/sqlc/db.go` for the DBTX definition and how `New` accepts the pool.

   Run: `cat /Users/alinscreciu/work/hackathon/api/sqlc.yaml` and `cat /Users/alinscreciu/work/hackathon/api/internal/db/sqlc/db.go` to confirm.

   If `DBTX` uses `database/sql`-style methods, then passing `pgx.Tx` directly won't work — you'd need `pgxpool.Pool.BeginTx` → `*sql.Tx` via stdlib adapter. But the Phase 3 code uses `*pgxpool.Pool` directly with `dbsqlc.New(pool)`, which means the pool must be a `*sql.DB`-compatible thing or sqlc uses pgx driver directly.

   **Pragmatic resolution**: Look at `internal/db/sqlc/db.go`. If it defines `DBTX` with `pgx`-style methods, then `pgx.Tx` works. If `database/sql`-style, use `pgxpool`'s stdlib adapter for transactions. For simplicity in Phase 6, if transaction passing doesn't work due to interface mismatch, make `Append` always manage its own transaction (ignore the `tx` parameter):
   ```go
   // In Append, always use s.pool directly
   // Accept tx parameter but don't use it — caller must pass nil
   // Document this limitation clearly
   ```
   This means the PATCH endpoint does two separate transactions: one for the apiary update, one for the ledger event. The ledger event is appended after the apiary update commits. This is slightly less atomic but acceptable for Phase 6.

2. **Advisory lock scope**: `pg_advisory_xact_lock(42)` only works within a transaction (it's released on commit/rollback). If `Append` doesn't use a transaction, use `pg_advisory_lock(42)` / `pg_advisory_unlock(42)` instead. The advisory lock ensures serial ledger appends even with concurrent requests.

3. **Hash determinism**: The hash formula is: `sha256( prevHash + canonicalPayload + createdAt.Format(time.RFC3339) )`. `time.RFC3339` in Go formats as `"2006-01-02T15:04:05Z07:00"`. Always use `UTC()` before formatting to ensure `Z` suffix consistency.

4. **Empty chain case**: First event has `prevHash = ""` and `PrevHash sql.NullString{Valid: false}` in the DB. When verifying, the first event's `PrevHash.Valid` is false, so `expectedPrevHash = ""` matches `prevHash = ""` (the outer loop variable starts at "").

5. **`UpdateApiaryLedgerHashParams` struct name**: After sqlc-gen, the exact struct name may differ. Check the generated code. It might be inline params or a named struct. Adjust the call accordingly.

---

## Verification Steps

```bash
# Apply migration
make migrate-up
# Expected: "00004_apiary_ledger_hash.sql: applied"

# Regenerate sqlc
make sqlc-gen

# Verify models.go has LedgerHash on Apiary struct
grep "LedgerHash" /Users/alinscreciu/work/hackathon/api/internal/db/sqlc/models.go

# Build check
go build ./...

# Start server
go run ./cmd/server &

# Login as apicultor (use existing cookie or re-authenticate)
# ... (see Phase 4 verification for login flow) ...

# Verify chain is empty and valid
curl -s http://localhost:8080/api/v1/events/verify -b /tmp/beekeeper_cookies.txt | jq .
# Expected: {"valid":true,"total_events":0,"last_hash":"","checked_at":"..."}

# PATCH an apiary
APIARY_ID=$(curl -s http://localhost:8080/api/v1/apiaries -b /tmp/beekeeper_cookies.txt | jq -r '.apiaries[0].id')
curl -s -X PATCH "http://localhost:8080/api/v1/apiaries/$APIARY_ID" \
  -H 'Content-Type: application/json' \
  -b /tmp/beekeeper_cookies.txt \
  -d '{"hive_count":25}' | jq .
# Expected: {"apiary":{...,"hive_count":25,"last_ledger_hash":"<sha256hex>"},"ledger_hash":"<sha256hex>"}

# PATCH again with different data
curl -s -X PATCH "http://localhost:8080/api/v1/apiaries/$APIARY_ID" \
  -H 'Content-Type: application/json' \
  -b /tmp/beekeeper_cookies.txt \
  -d '{"hive_count":30,"notes":"updated notes"}' | jq .
# Expected: different ledger_hash from first PATCH

# List events
curl -s http://localhost:8080/api/v1/events -b /tmp/beekeeper_cookies.txt | jq .
# Expected: 2 events of type "apiary.updated"

# Verify chain is valid
curl -s http://localhost:8080/api/v1/events/verify -b /tmp/beekeeper_cookies.txt | jq .
# Expected: {"valid":true,"total_events":2,"last_hash":"...","checked_at":"..."}

# Manually tamper and verify
HASH=$(curl -s http://localhost:8080/api/v1/events -b /tmp/beekeeper_cookies.txt | jq -r '.events[0].hash')
psql "postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable" \
  -c "UPDATE ledger_events SET hash='tampered123' WHERE hash='$HASH';"
curl -s http://localhost:8080/api/v1/events/verify -b /tmp/beekeeper_cookies.txt | jq .
# Expected: {"valid":false,"error":"hash mismatch at event ..."}

# Restore (undo tamper)
psql "postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable" \
  -c "UPDATE ledger_events SET hash='$HASH' WHERE hash='tampered123';"

# GET by hash
curl -s "http://localhost:8080/api/v1/events/$HASH" -b /tmp/beekeeper_cookies.txt | jq .
# Expected: event + chain context

go vet ./...
```

---

## After Completion

Write `/Users/alinscreciu/work/hackathon/api/.planning/phases/phase-6.md`:

```markdown
# Phase 6 — Ledger Service
Status: COMPLETE
Completed: <date>

## What was built
- internal/db/migrations/00004_apiary_ledger_hash.sql — adds ledger_hash column to apiaries
- internal/db/queries/apiaries.sql — fixed UpdateApiaryLedgerHash query
- internal/services/ledger.go — LedgerService: Append (advisory lock, sha256 chain), List, GetByHash, Verify
- internal/api/ledger.go — GET /events, GET /events/:hash, GET /events/verify implemented
- internal/api/apiaries.go — PATCH /apiaries/:id implemented with ledger event
- internal/api/router.go — LedgerService instantiated and injected

## Verification
- PATCH apiary → ledger_hash in response
- GET /events/verify → valid:true with 2 events
- Manually corrupt hash → valid:false
- go build ./... clean
```
