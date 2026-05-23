# Phase 8 — Spray Reports + Cascade Orchestration

**Status**: PENDING
**Goal**: `POST /spray-reports` triggers cascade goroutines that notify affected beekeepers. `GET /cascade-status` reflects live state. Twilio webhooks handle call/SMS confirmations. Alerts endpoint works.

---

## Current State (before this phase)

Phases 1-7 complete. Geo mock and weather adapter work. Now building the core business logic.

What exists:
- `internal/api/sprays.go` — all stubs (501)
- `internal/api/alerts.go` — all stubs (501)
- `internal/api/webhooks_twilio.go` — all stubs (501, registered via Huma)
- `internal/services/ledger.go` — `LedgerService.Append(ctx, tx pgx.Tx, ...)` works
- `internal/services/geo.go` — `Haversine(lat1,lng1,lat2,lng2) float64` available
- `internal/external/geoai/` — `geoai.Client` interface, `MockClient`, `NewClient(baseURL) Client`
- `internal/external/weather/` — `CachedClient.Get(ctx, lat, lng) (*domain.WeatherResult, error)`
- `internal/api/router.go` — `Handlers{..., geoAI geoai.Client, weatherClient *weather.CachedClient}`

sqlc functions available for this phase:
- `CreateSprayReport(ctx, CreateSprayReportParams) (SprayReport, error)` — params: `{ID, FarmerID, ParcelID, Crop, Substance, Toxicity, SurfaceHa, ScheduledAt, DurationHours, Notes sql.NullString, LedgerHash}`
- `GetSprayReport(ctx, id) (SprayReport, error)`
- `ListSprayReportsByFarmer(ctx, farmerID) ([]SprayReport, error)`
- `ListActiveSprayReports(ctx) ([]SprayReport, error)`
- `ListAllSprayReports(ctx) ([]SprayReport, error)`
- `UpdateSprayReportStatus(ctx, id, status) error`
- `UpdateSprayReportLedgerHash(ctx, id, hash) error`
- `UpdateSprayReportAffectedCount(ctx, id, count) error`
- `CreateAlertDispatch(ctx, CreateAlertDispatchParams) (AlertDispatch, error)` — params: `{ID, SprayReportID, BeekeeperID, ApiaryID, DistanceM, Downwind, CallState, SmsState, LedgerHash}`. Note: initial `call_state` in the row defaults differ by toxicity.
- `GetAlertDispatch(ctx, id) (AlertDispatch, error)`
- `ListDispatchesBySpray(ctx, sprayID) ([]AlertDispatch, error)`
- `ListActiveAlertsByBeekeeper(ctx, beekeeperID) ([]AlertDispatch, error)`
- `ListAllAlertsByBeekeeper(ctx, beekeeperID) ([]AlertDispatch, error)`
- `GetDispatchByTwilioCallSID(ctx, sid) (AlertDispatch, error)`
- `GetDispatchByTwilioSMSSID(ctx, sid) (AlertDispatch, error)`
- `UpdatePushState(ctx, id, state) error`
- `UpdateCallState(ctx, id, state) error`
- `SetTwilioCallSID(ctx, id, sid) error`
- `SetTwilioSMSSID(ctx, id, sid) error`
- `UpdateSMSState(ctx, id, state) error`
- `UpdateSMSStateByTwilioSID(ctx, sid, state) error`
- `UpdateCallStateByTwilioSID(ctx, sid, state) error`
- `UpdateFinalStatus(ctx, id, status) error`
- `UpdateFinalStatusByCallSID(ctx, sid, status) error`
- `UpdateFinalStatusBySMSSID(ctx, sid, status) error`
- `SetInAppConfirmed(ctx, id, action, finalStatus) error`
- `UpdateDispatchLedgerHash(ctx, id, hash) error`
- `GetSubstanceByLabel(ctx, label) (Substance, error)` — check if this exists in substances.sql.go; if not, it may be `ListSubstances` → filter in Go, or add query.
- `ListAllApiaries(ctx) ([]Apiary, error)` — get all apiaries for radius scan

**IMPORTANT — check `GetSubstanceByLabel`**: Look at `internal/db/queries/substances.sql`. If there's no such query, add it:
```sql
-- name: GetSubstanceByLabel :one
SELECT * FROM substances WHERE label = $1 LIMIT 1;
```
Then run `make sqlc-gen`.

**Twilio webhook registration**: Currently the Twilio webhooks are registered via Huma (stub). Phase 8 removes them from Huma and registers them as raw chi routes, because Twilio sends `application/x-www-form-urlencoded` POST bodies and expects `application/xml` responses (TwiML).

**`dbsqlc.DBTX` interface**: Before Phase 6 noted uncertainty about whether `pgx.Tx` implements `DBTX`. Clarify:
- `internal/db/sqlc/db.go` defines `DBTX` as the `database/sql`-style interface
- `pgxpool.Pool` wraps via `pgxpool.Pool.QueryRowContext`, etc. which implements `DBTX` via the `pgx/v5/stdlib` compatibility layer built into the pool
- `pgx.Tx` (from `pool.Begin(ctx)`) also implements the same interface
- In practice: `dbsqlc.New(tx)` where `tx` is `pgx.Tx` works because pgx v5's `pgx.Tx` type implements `ExecContext`, `QueryContext`, `QueryRowContext` methods

If there are compile errors about interface satisfaction, use `pgxpool.Pool.BeginTx(ctx, nil)` which returns `pgx.Tx`, and pass that directly.

---

## Prerequisites

Phases 1-7 complete. `make seed` run. `make sqlc-gen` run if new queries added.

---

## Files to Create

### `internal/services/cascade.go`

This is the most complex file. Read carefully.

```go
package services

import (
    "context"
    "database/sql"
    "fmt"
    "log/slog"
    "sync"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
    dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
    "github.com/radarul-albinelor/api/internal/domain"
)

// Notifier sends Twilio calls and SMS messages.
// In Phase 8 this is nil — mock logging is used instead.
// In Phase 9, twilio.Client implements this.
type Notifier interface {
    SendSMS(ctx context.Context, to, body string) (string, error)
    MakeCall(ctx context.Context, to, twimlURL string) (string, error)
}

// PushSender sends Web Push notifications.
// In Phase 8 this is nil.
// In Phase 9, webpush.Client implements this.
type PushSender interface {
    Send(ctx context.Context, sub domain.PushSubscription, payload []byte) error
}

type CascadeService struct {
    db       *dbsqlc.Queries
    pool     *pgxpool.Pool
    ledger   *LedgerService
    notifier Notifier   // nil in Phase 8
    pusher   PushSender // nil in Phase 8

    // In-memory timer tracking — lost on restart (v1 limitation)
    smsTimers         sync.Map // key: dispatchID → *time.Timer
    unconfirmedTimers sync.Map // key: "unconf:"+dispatchID → *time.Timer

    appBaseURL string // for TwiML callback URLs
}

func NewCascadeService(
    pool *pgxpool.Pool,
    ledger *LedgerService,
    notifier Notifier,
    pusher PushSender,
    appBaseURL string,
) *CascadeService {
    return &CascadeService{
        db:         dbsqlc.New(pool),
        pool:       pool,
        ledger:     ledger,
        notifier:   notifier,
        pusher:     pusher,
        appBaseURL: appBaseURL,
    }
}
```

#### `Start` method

Called AFTER the spray report transaction is committed. Launches goroutines for each dispatch.

```go
// Start begins cascade processing for a list of alert dispatches.
// MUST be called after the creating transaction is committed.
// Uses context.WithoutCancel to detach from the request context.
func (c *CascadeService) Start(ctx context.Context, sprayID string, dispatches []dbsqlc.AlertDispatch) {
    bgCtx := context.WithoutCancel(ctx) // detach from request
    for _, d := range dispatches {
        d := d // capture loop variable
        go c.launchDispatch(bgCtx, d)
    }
}
```

#### `launchDispatch` method

```go
func (c *CascadeService) launchDispatch(ctx context.Context, dispatch dbsqlc.AlertDispatch) {
    defer func() {
        if r := recover(); r != nil {
            slog.Error("cascade: panic in launchDispatch", "dispatch_id", dispatch.ID, "panic", r)
        }
    }()

    dispatchID := dispatch.ID.String()
    slog.Info("cascade: launching dispatch", "dispatch_id", dispatchID, "apiary_id", dispatch.ApiaryID)

    // 1. Send push notification (if pusher available)
    if c.pusher != nil {
        c.sendPushForDispatch(ctx, dispatch)
    } else {
        slog.Info("[MOCK PUSH] would send push notification", "dispatch_id", dispatchID)
        // Mark push state as sent
        _ = c.db.UpdatePushState(ctx, dbsqlc.UpdatePushStateParams{
            ID:        dispatch.ID,
            PushState: dbsqlc.PushStateSent,
        })
    }

    // 2. Determine notification path based on call_state
    // Dispatches created with call_state=skipped are T- (low toxicity)
    if dispatch.CallState == dbsqlc.CallStateSkipped {
        // T- path: skip call, wait 1 second, send SMS
        time.Sleep(1 * time.Second)
        c.fireSMS(ctx, dispatch)
        return
    }

    // T / T+ path: make phone call, schedule SMS fallback after 60 seconds
    c.makeCall(ctx, dispatch)
    c.scheduleSMSFallback(dispatchID, 60*time.Second)
    c.scheduleUnconfirmedTimeout(dispatchID)
}
```

#### `makeCall` method

```go
func (c *CascadeService) makeCall(ctx context.Context, dispatch dbsqlc.AlertDispatch) {
    dispatchID := dispatch.ID.String()

    // Build TwiML URL for this dispatch
    twimlURL := fmt.Sprintf("%s/api/v1/webhooks/twilio/voice/gather?dispatch_id=%s", c.appBaseURL, dispatchID)
    statusURL := fmt.Sprintf("%s/api/v1/webhooks/twilio/voice/status?dispatch_id=%s", c.appBaseURL, dispatchID)

    // Get beekeeper phone number
    beekeeper, err := c.db.GetUserByID(ctx, dispatch.BeekeeperID)
    if err != nil {
        slog.Error("cascade: failed to get beekeeper", "err", err, "dispatch_id", dispatchID)
        return
    }

    if c.notifier != nil {
        // Real Twilio call
        _ = statusURL // pass to Twilio as status callback — handle in MakeCall params or separate
        sid, err := c.notifier.MakeCall(ctx, beekeeper.Phone, twimlURL)
        if err != nil {
            slog.Error("cascade: call failed", "err", err, "dispatch_id", dispatchID)
            _ = c.db.UpdateCallState(ctx, dbsqlc.UpdateCallStateParams{
                ID:        dispatch.ID,
                CallState: dbsqlc.CallStateFailed,
            })
            return
        }
        _ = c.db.SetTwilioCallSID(ctx, dbsqlc.SetTwilioCallSIDParams{
            ID:           dispatch.ID,
            TwilioCallSid: sql.NullString{String: sid, Valid: true},
        })
        slog.Info("cascade: call initiated", "sid", sid, "dispatch_id", dispatchID)
    } else {
        // Mock: log and simulate call_state=queued
        mockSID := "MOCK-" + dispatchID
        slog.Info("[MOCK CALL] would call beekeeper",
            "phone", beekeeper.Phone,
            "twiml_url", twimlURL,
            "mock_sid", mockSID,
            "dispatch_id", dispatchID)
        _ = c.db.SetTwilioCallSID(ctx, dbsqlc.SetTwilioCallSIDParams{
            ID:            dispatch.ID,
            TwilioCallSid: sql.NullString{String: mockSID, Valid: true},
        })
    }
}
```

**IMPORTANT — `SetTwilioCallSIDParams` struct**: Check the generated code in `alert_dispatches.sql.go`. The params struct name and fields depend on sqlc generation. It likely uses positional params mapped to a struct. Find the exact field names.

#### `fireSMS` method

```go
func (c *CascadeService) fireSMS(ctx context.Context, dispatch dbsqlc.AlertDispatch) {
    dispatchID := dispatch.ID.String()

    // Re-read dispatch to check if already resolved
    current, err := c.db.GetAlertDispatch(ctx, dispatch.ID)
    if err != nil {
        slog.Error("cascade: failed to re-read dispatch for SMS", "err", err)
        return
    }
    if current.FinalStatus.Valid {
        slog.Info("cascade: dispatch already resolved, skipping SMS", "dispatch_id", dispatchID)
        return
    }

    beekeeper, err := c.db.GetUserByID(ctx, dispatch.BeekeeperID)
    if err != nil {
        slog.Error("cascade: failed to get beekeeper for SMS", "err", err)
        return
    }

    // Build SMS message — use spray + apiary info
    spray, _ := c.db.GetSprayReport(ctx, dispatch.SprayReportID)
    apiary, _ := c.db.GetApiary(ctx, dispatch.ApiaryID)
    msg := fmt.Sprintf(
        "ALERTĂ STUPINA: Tratament pesticid programat pentru %s pe parcela din %s la %.0fm de stupina %s. Substanță: %s (toxicitate %s). Răspundeți DA pentru confirmare.",
        spray.ScheduledAt.Format("02.01.2006 15:04"),
        spray.Substance,
        dispatch.DistanceM,
        apiary.Name,
        spray.Substance,
        spray.Toxicity,
    )

    if c.notifier != nil {
        sid, err := c.notifier.SendSMS(ctx, beekeeper.Phone, msg)
        if err != nil {
            slog.Error("cascade: SMS failed", "err", err, "dispatch_id", dispatchID)
            _ = c.db.UpdateSMSState(ctx, dbsqlc.UpdateSMSStateParams{
                ID:       dispatch.ID,
                SmsState: dbsqlc.SmsStateFailed,
            })
            return
        }
        _ = c.db.SetTwilioSMSSID(ctx, dbsqlc.SetTwilioSMSSIDParams{
            ID:          dispatch.ID,
            TwilioSmsSid: sql.NullString{String: sid, Valid: true},
        })
        slog.Info("cascade: SMS sent", "sid", sid, "dispatch_id", dispatchID)
    } else {
        mockSID := "MOCK-SMS-" + dispatchID
        slog.Info("[MOCK SMS] would send SMS",
            "to", beekeeper.Phone,
            "message", msg,
            "mock_sid", mockSID,
            "dispatch_id", dispatchID)
        _ = c.db.SetTwilioSMSSID(ctx, dbsqlc.SetTwilioSMSSIDParams{
            ID:           dispatch.ID,
            TwilioSmsSid: sql.NullString{String: mockSID, Valid: true},
        })
        // Simulate delivery
        _ = c.db.UpdateSMSState(ctx, dbsqlc.UpdateSMSStateParams{
            ID:       dispatch.ID,
            SmsState: dbsqlc.SmsStateSent,
        })
    }
}
```

#### Timer methods

```go
// scheduleSMSFallback schedules SMS to be sent after `delay` if dispatch is not yet resolved.
func (c *CascadeService) scheduleSMSFallback(dispatchID string, delay time.Duration) {
    dispatchUUID, _ := uuid.Parse(dispatchID)
    dispatch := dbsqlc.AlertDispatch{ID: dispatchUUID}

    timer := time.AfterFunc(delay, func() {
        c.fireSMS(context.Background(), dispatch)
        c.smsTimers.Delete(dispatchID)
    })
    c.smsTimers.Store(dispatchID, timer)
}

// cancelSMSFallback stops a pending SMS fallback timer.
func (c *CascadeService) cancelSMSFallback(dispatchID string) {
    if v, ok := c.smsTimers.LoadAndDelete(dispatchID); ok {
        v.(*time.Timer).Stop()
        slog.Debug("cascade: SMS fallback cancelled", "dispatch_id", dispatchID)
    }
}

// scheduleUnconfirmedTimeout sets final_status=unconfirmed after 30 minutes
// if the dispatch is still unresolved.
func (c *CascadeService) scheduleUnconfirmedTimeout(dispatchID string) {
    dispatchUUID, _ := uuid.Parse(dispatchID)
    timer := time.AfterFunc(30*time.Minute, func() {
        c.unconfirmedTimers.Delete("unconf:" + dispatchID)
        ctx := context.Background()
        // UpdateFinalStatus only sets if final_status IS NULL (idempotent)
        _ = c.db.UpdateFinalStatus(ctx, dbsqlc.UpdateFinalStatusParams{
            ID:          dispatchUUID,
            FinalStatus: dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusUnconfirmed, Valid: true},
        })
        slog.Info("cascade: dispatch timed out as unconfirmed", "dispatch_id", dispatchID)
    })
    c.unconfirmedTimers.Store("unconf:"+dispatchID, timer)
}
```

#### Webhook handler methods (called from API layer)

```go
// HandleCallConfirmed is called when Twilio voice gather receives digit "1".
func (c *CascadeService) HandleCallConfirmed(ctx context.Context, callSID string) error {
    dispatch, err := c.db.GetDispatchByTwilioCallSID(ctx, sql.NullString{String: callSID, Valid: true})
    if err != nil { return fmt.Errorf("get dispatch by call SID: %w", err) }

    c.cancelSMSFallback(dispatch.ID.String())
    c.unconfirmedTimers.Delete("unconf:" + dispatch.ID.String())

    _ = c.db.UpdateCallState(ctx, dbsqlc.UpdateCallStateParams{
        ID: dispatch.ID, CallState: dbsqlc.CallStateConfirmed,
    })
    _ = c.db.UpdateFinalStatus(ctx, dbsqlc.UpdateFinalStatusParams{
        ID: dispatch.ID,
        FinalStatus: dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedCall, Valid: true},
    })

    actorID := dispatch.BeekeeperID.String()
    _, err = c.ledger.Append(ctx, nil, "alert.confirmed", &actorID, map[string]any{
        "dispatch_id": dispatch.ID.String(),
        "method":      "call",
        "call_sid":    callSID,
    })
    return err
}

// HandleCallTerminal is called when a call ends without confirmation (no-answer, busy, failed).
func (c *CascadeService) HandleCallTerminal(ctx context.Context, callSID, callStatus string) error {
    dispatch, err := c.db.GetDispatchByTwilioCallSID(ctx, sql.NullString{String: callSID, Valid: true})
    if err != nil { return err }

    state := twilioCallStatusToState(callStatus)
    _ = c.db.UpdateCallState(ctx, dbsqlc.UpdateCallStateParams{
        ID: dispatch.ID, CallState: state,
    })

    // Fire SMS immediately (0 delay)
    c.scheduleSMSFallback(dispatch.ID.String(), 0)
    return nil
}

// HandleSMSConfirmed is called when an inbound SMS contains "DA".
func (c *CascadeService) HandleSMSConfirmed(ctx context.Context, smsSID string) error {
    dispatch, err := c.db.GetDispatchByTwilioSMSSID(ctx, sql.NullString{String: smsSID, Valid: true})
    if err != nil { return err }

    _ = c.db.UpdateSMSState(ctx, dbsqlc.UpdateSMSStateParams{
        ID: dispatch.ID, SmsState: dbsqlc.SmsStateConfirmed,
    })
    _ = c.db.UpdateFinalStatus(ctx, dbsqlc.UpdateFinalStatusParams{
        ID: dispatch.ID,
        FinalStatus: dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedSms, Valid: true},
    })
    c.unconfirmedTimers.Delete("unconf:" + dispatch.ID.String())

    actorID := dispatch.BeekeeperID.String()
    _, err = c.ledger.Append(ctx, nil, "alert.confirmed", &actorID, map[string]any{
        "dispatch_id": dispatch.ID.String(),
        "method":      "sms",
        "sms_sid":     smsSID,
    })
    return err
}

// HandleSMSStatus updates the SMS state based on Twilio delivery status webhooks.
func (c *CascadeService) HandleSMSStatus(ctx context.Context, smsSID, status string) error {
    state := twilioSMSStatusToState(status)
    return c.db.UpdateSMSStateByTwilioSID(ctx, dbsqlc.UpdateSMSStateByTwilioSIDParams{
        TwilioSmsSid: sql.NullString{String: smsSID, Valid: true},
        SmsState:     state,
    })
}

// HandleInAppConfirm is called when a beekeeper confirms via the app.
func (c *CascadeService) HandleInAppConfirm(ctx context.Context, dispatchID, action string) (string, error) {
    dispatchUUID, err := uuid.Parse(dispatchID)
    if err != nil { return "", huma.NewError(400, "invalid dispatch id") }

    inAppAction := dbsqlc.InAppAction(action)
    _ = c.db.SetInAppConfirmed(ctx, dbsqlc.SetInAppConfirmedParams{
        ID:          dispatchUUID,
        InAppAction: dbsqlc.NullInAppAction{InAppAction: inAppAction, Valid: true},
        FinalStatus: dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedApp, Valid: true},
    })

    c.cancelSMSFallback(dispatchID)
    c.unconfirmedTimers.Delete("unconf:" + dispatchID)

    dispatch, _ := c.db.GetAlertDispatch(ctx, dispatchUUID)
    actorID := dispatch.BeekeeperID.String()
    hash, err := c.ledger.Append(ctx, nil, "alert.confirmed", &actorID, map[string]any{
        "dispatch_id": dispatchID,
        "method":      "app",
        "action":      action,
    })
    return hash, err
}

// Shutdown stops all pending timers gracefully.
func (c *CascadeService) Shutdown() {
    slog.Info("cascade: shutting down, stopping timers...")
    c.smsTimers.Range(func(k, v any) bool {
        id := k.(string)
        v.(*time.Timer).Stop()
        slog.Info("cascade: stopped SMS timer", "dispatch_id", id)
        return true
    })
    c.unconfirmedTimers.Range(func(k, v any) bool {
        id := k.(string)
        v.(*time.Timer).Stop()
        slog.Info("cascade: stopped unconfirmed timer", "key", id)
        return true
    })
}
```

#### Helper functions (status mapping)

```go
func twilioCallStatusToState(status string) dbsqlc.CallState {
    switch status {
    case "completed": return dbsqlc.CallStateAnswered
    case "no-answer": return dbsqlc.CallStateNoAnswer
    case "busy":      return dbsqlc.CallStateBusy
    case "failed":    return dbsqlc.CallStateFailed
    case "canceled":  return dbsqlc.CallStateFailed
    default:          return dbsqlc.CallStateFailed
    }
}

func twilioSMSStatusToState(status string) dbsqlc.SmsState {
    switch status {
    case "queued":    return dbsqlc.SmsStateQueued
    case "sent":      return dbsqlc.SmsStateSent
    case "delivered": return dbsqlc.SmsStateDelivered
    case "failed":    return dbsqlc.SmsStateFailed
    case "undelivered": return dbsqlc.SmsStateFailed
    default:          return dbsqlc.SmsStateQueued
    }
}
```

**CRITICAL — `SetInAppConfirmedParams` struct**: Check `alert_dispatches.sql.go` for the generated struct. The SQL is:
```sql
UPDATE alert_dispatches
SET in_app_confirmed_at = NOW(), in_app_action = $2, final_status = $3
WHERE id = $1 AND final_status IS NULL;
```
So params are `{ID uuid.UUID, InAppAction NullInAppAction, FinalStatus NullFinalStatus}`. Confirm field names in generated code.

**CRITICAL — `UpdateCallStateParams` etc.**: Many of the `UpdateXxx` queries take 2 params. sqlc may generate inline params (no struct) for simple 2-param queries, or may use a struct. Check `alert_dispatches.sql.go` for each:
- `UpdatePushState(ctx, id, pushState)` or `UpdatePushState(ctx, UpdatePushStateParams{...})`
- After `make sqlc-gen`, verify exact signatures.

---

## Files to Modify

### `internal/db/queries/substances.sql` (if GetSubstanceByLabel is missing)

Add:
```sql
-- name: GetSubstanceByLabel :one
SELECT * FROM substances WHERE label = $1 LIMIT 1;
```

Then `make sqlc-gen`.

### `internal/api/sprays.go`

Implement all spray report endpoints. Define types:

```go
type CreateSprayInput struct {
    Body struct {
        ParcelID      string  `json:"parcel_id"`
        SurfaceHA     float64 `json:"surface_ha" minimum:"0.1"`
        Crop          string  `json:"crop" minLength:"1"`
        Substance     string  `json:"substance" minLength:"1"`
        ScheduledAt   string  `json:"scheduled_at"` // RFC3339
        DurationHours float64 `json:"duration_hours" minimum:"0.5"`
        Notes         *string `json:"notes,omitempty"`
    }
}

type SprayReportResponse struct {
    ID                    string    `json:"id"`
    FarmerID              string    `json:"farmer_id"`
    ParcelID              string    `json:"parcel_id"`
    Crop                  string    `json:"crop"`
    Substance             string    `json:"substance"`
    Toxicity              string    `json:"toxicity"`
    SurfaceHA             float64   `json:"surface_ha"`
    ScheduledAt           time.Time `json:"scheduled_at"`
    DurationHours         float64   `json:"duration_hours"`
    Notes                 *string   `json:"notes"`
    Status                string    `json:"status"`
    AffectedApiariesCount int       `json:"affected_apiaries_count"`
    LedgerHash            string    `json:"ledger_hash"`
    CreatedAt             time.Time `json:"created_at"`
}

type CreateSprayOutput struct {
    Body struct {
        SprayReport       SprayReportResponse `json:"spray_report"`
        AffectedApiaries  int                 `json:"affected_apiaries"`
        RiskRadiusM       float64             `json:"risk_radius_m"`
        LedgerHash        string              `json:"ledger_hash"`
    }
}
```

**`POST /spray-reports` implementation:**

```go
func (h *Handlers) createSprayReport(ctx context.Context, input *CreateSprayInput) (*CreateSprayOutput, error) {
    user := middleware.UserFromContext(ctx)
    if user.Role != domain.RoleFermier {
        return nil, huma.NewError(http.StatusForbidden, "only farmers can create spray reports")
    }

    // 1. Parse and validate
    parcelID, err := uuid.Parse(input.Body.ParcelID)
    if err != nil { return nil, huma.NewError(400, "invalid parcel_id") }
    scheduledAt, err := time.Parse(time.RFC3339, input.Body.ScheduledAt)
    if err != nil { return nil, huma.NewError(400, "invalid scheduled_at, use RFC3339") }
    farmerID, _ := uuid.Parse(user.ID)

    q := dbsqlc.New(h.pool)

    // 2. Verify parcel ownership
    parcel, err := q.GetParcel(ctx, parcelID)
    if err != nil { return nil, huma.NewError(404, "parcel not found") }
    if parcel.OwnerID != farmerID { return nil, huma.NewError(403, "not your parcel") }

    // 3. Look up substance for toxicity
    substance, err := q.GetSubstanceByLabel(ctx, input.Body.Substance)
    var toxicity string
    if err != nil {
        // Unknown substance — default to T (moderate), log warning
        slog.Warn("unknown substance, defaulting toxicity to T", "substance", input.Body.Substance)
        toxicity = "T"
    } else {
        toxicity = substance.Toxicity
    }

    // 4. AI geo assessment
    geoResult, err := h.geoAI.Assess(ctx, geoai.Request{
        TotalQuantity: input.Body.SurfaceHA,
        Type:          input.Body.Substance,
        CenterPoint:   geoai.LatLng{Lat: parcel.Lat, Lng: parcel.Lng},
    })
    if err != nil { return nil, fmt.Errorf("geo assessment: %w", err) }

    // 5. Find all apiaries within risk radius
    allApiaries, err := q.ListAllApiaries(ctx)
    if err != nil { return nil, fmt.Errorf("list apiaries: %w", err) }

    type affectedApiary struct {
        apiary    dbsqlc.Apiary
        distanceM float64
    }
    var affected []affectedApiary
    for _, a := range allApiaries {
        d := Haversine(parcel.Lat, parcel.Lng, a.Lat, a.Lng)
        if d <= geoResult.RiskRadiusM {
            affected = append(affected, affectedApiary{a, d})
        }
    }

    // 6. Begin transaction: create spray report + dispatches + ledger events
    tx, err := h.pool.Begin(ctx)
    if err != nil { return nil, fmt.Errorf("begin tx: %w", err) }
    defer tx.Rollback(ctx)

    qtx := dbsqlc.New(tx)
    sprayID := uuid.New()

    spray, err := qtx.CreateSprayReport(ctx, dbsqlc.CreateSprayReportParams{
        ID:            sprayID,
        FarmerID:      farmerID,
        ParcelID:      parcelID,
        Crop:          input.Body.Crop,
        Substance:     input.Body.Substance,
        Toxicity:      toxicity,
        SurfaceHa:     input.Body.SurfaceHA,
        ScheduledAt:   scheduledAt,
        DurationHours: input.Body.DurationHours,
        Notes:         sql.NullString{String: ptrStr(input.Body.Notes), Valid: input.Body.Notes != nil},
        LedgerHash:    "", // will be updated after ledger event
    })
    if err != nil { return nil, fmt.Errorf("create spray: %w", err) }

    // Ledger event for spray creation
    actorID := user.ID
    sprayHash, err := h.ledgerSvc.Append(ctx, tx, "spray.created", &actorID, map[string]any{
        "spray_id":   sprayID.String(),
        "substance":  input.Body.Substance,
        "toxicity":   toxicity,
        "parcel_id":  parcelID.String(),
        "surface_ha": input.Body.SurfaceHA,
    })
    if err != nil { return nil, fmt.Errorf("ledger spray.created: %w", err) }

    // Update spray's ledger hash
    _ = qtx.UpdateSprayReportLedgerHash(ctx, dbsqlc.UpdateSprayReportLedgerHashParams{
        ID:         sprayID,
        LedgerHash: sprayHash,
    })

    // Create dispatches
    dispatches := make([]dbsqlc.AlertDispatch, 0, len(affected))
    for _, aff := range affected {
        callState := dbsqlc.CallStateQueued
        smsState := dbsqlc.SmsStateQueued
        if toxicity == "T-" {
            callState = dbsqlc.CallStateSkipped
        }

        dispatchID := uuid.New()
        d, err := qtx.CreateAlertDispatch(ctx, dbsqlc.CreateAlertDispatchParams{
            ID:            dispatchID,
            SprayReportID: sprayID,
            BeekeeperID:   aff.apiary.OwnerID,
            ApiaryID:      aff.apiary.ID,
            DistanceM:     aff.distanceM,
            Downwind:      false, // simplified — wind direction analysis is Phase 9
            CallState:     callState,
            SmsState:      smsState,
            LedgerHash:    "",
        })
        if err != nil { return nil, fmt.Errorf("create dispatch: %w", err) }

        // Ledger event for each dispatch
        dispatchHash, err := h.ledgerSvc.Append(ctx, tx, "alert.dispatched", &actorID, map[string]any{
            "dispatch_id": dispatchID.String(),
            "spray_id":    sprayID.String(),
            "apiary_id":   aff.apiary.ID.String(),
            "distance_m":  aff.distanceM,
            "toxicity":    toxicity,
        })
        if err != nil { return nil, fmt.Errorf("ledger alert.dispatched: %w", err) }

        _ = qtx.UpdateDispatchLedgerHash(ctx, dbsqlc.UpdateDispatchLedgerHashParams{
            ID:         dispatchID,
            LedgerHash: dispatchHash,
        })

        d.LedgerHash = dispatchHash
        dispatches = append(dispatches, d)
    }

    // Update affected count
    _ = qtx.UpdateSprayReportAffectedCount(ctx, dbsqlc.UpdateSprayReportAffectedCountParams{
        ID:                    sprayID,
        AffectedApiariesCount: int32(len(affected)),
    })

    if err := tx.Commit(ctx); err != nil { return nil, fmt.Errorf("commit: %w", err) }

    // 7. Start cascade (after tx committed)
    h.cascade.Start(ctx, sprayID.String(), dispatches)

    return &CreateSprayOutput{Body: struct{
        SprayReport      SprayReportResponse
        AffectedApiaries int
        RiskRadiusM      float64
        LedgerHash       string
    }{
        SprayReport:      dbSprayToResponse(spray),
        AffectedApiaries: len(affected),
        RiskRadiusM:      geoResult.RiskRadiusM,
        LedgerHash:       sprayHash,
    }}, nil
}
```

Helper:
```go
func ptrStr(s *string) string { if s == nil { return "" }; return *s }

func dbSprayToResponse(s dbsqlc.SprayReport) SprayReportResponse {
    var notes *string
    if s.Notes.Valid { notes = &s.Notes.String }
    return SprayReportResponse{
        ID: s.ID.String(), FarmerID: s.FarmerID.String(), ParcelID: s.ParcelID.String(),
        Crop: s.Crop, Substance: s.Substance, Toxicity: s.Toxicity,
        SurfaceHA: s.SurfaceHa, ScheduledAt: s.ScheduledAt, DurationHours: s.DurationHours,
        Notes: notes, Status: string(s.Status), AffectedApiariesCount: int(s.AffectedApiariesCount),
        LedgerHash: s.LedgerHash, CreatedAt: s.CreatedAt,
    }
}
```

**`GET /spray-reports`**:
```go
func (h *Handlers) listSprayReports(ctx context.Context, _ *struct{}) (*ListSprayReportsOutput, error) {
    user := middleware.UserFromContext(ctx)
    q := dbsqlc.New(h.pool)
    var rows []dbsqlc.SprayReport
    var err error
    switch user.Role {
    case domain.RoleInspector:
        rows, err = q.ListAllSprayReports(ctx)
    case domain.RoleFermier:
        farmerID, _ := uuid.Parse(user.ID)
        rows, err = q.ListSprayReportsByFarmer(ctx, farmerID)
    default:
        return nil, huma.NewError(403, "forbidden_role")
    }
    if err != nil { return nil, err }
    items := make([]SprayReportResponse, len(rows))
    for i, r := range rows { items[i] = dbSprayToResponse(r) }
    return &ListSprayReportsOutput{Body: struct{SprayReports []SprayReportResponse `json:"spray_reports"`}{items}}, nil
}
```

**`GET /spray-reports/:id`**: Get spray + dispatches. Return `{spray_report, cascade_status}`.

**`GET /spray-reports/:id/cascade-status`**: Build `CascadeStatus` from dispatches.

```go
type CascadeStatus struct {
    SprayReportID string `json:"spray_report_id"`
    OverallStatus string `json:"overall_status"` // "in_progress" | "complete"
    Summary       struct {
        Total       int `json:"total"`
        Confirmed   int `json:"confirmed"`
        Pending     int `json:"pending"`
        Unconfirmed int `json:"unconfirmed"`
        Failed      int `json:"failed"`
    } `json:"summary"`
    Dispatches []AlertDispatchPublic `json:"dispatches"`
    PolledAt   time.Time             `json:"polled_at"`
}

type AlertDispatchPublic struct {
    ID          string  `json:"id"`
    ApiaryID    string  `json:"apiary_id"`
    DistanceM   float64 `json:"distance_m"`
    PushState   string  `json:"push_state"`
    CallState   string  `json:"call_state"`
    SmsState    string  `json:"sms_state"`
    FinalStatus *string `json:"final_status"`
    LedgerHash  string  `json:"ledger_hash"`
}
```

Build cascade status:
```go
func buildCascadeStatus(sprayID string, dispatches []dbsqlc.AlertDispatch) CascadeStatus {
    cs := CascadeStatus{SprayReportID: sprayID, PolledAt: time.Now().UTC()}
    cs.Summary.Total = len(dispatches)
    for _, d := range dispatches {
        pub := AlertDispatchPublic{
            ID: d.ID.String(), ApiaryID: d.ApiaryID.String(),
            DistanceM: d.DistanceM,
            PushState: string(d.PushState), CallState: string(d.CallState),
            SmsState: string(d.SmsState),
        }
        if d.FinalStatus.Valid { s := string(d.FinalStatus.FinalStatus); pub.FinalStatus = &s }
        pub.LedgerHash = d.LedgerHash
        cs.Dispatches = append(cs.Dispatches, pub)

        if d.FinalStatus.Valid {
            switch d.FinalStatus.FinalStatus {
            case dbsqlc.FinalStatusConfirmedCall, dbsqlc.FinalStatusConfirmedSms, dbsqlc.FinalStatusConfirmedApp:
                cs.Summary.Confirmed++
            case dbsqlc.FinalStatusUnconfirmed:
                cs.Summary.Unconfirmed++
            case dbsqlc.FinalStatusFailed:
                cs.Summary.Failed++
            }
        } else {
            cs.Summary.Pending++
        }
    }
    resolved := cs.Summary.Confirmed + cs.Summary.Unconfirmed + cs.Summary.Failed
    if resolved == cs.Summary.Total { cs.OverallStatus = "complete" } else { cs.OverallStatus = "in_progress" }
    return cs
}
```

**`POST /spray-reports/:id/cancel`**:
```go
func (h *Handlers) cancelSprayReport(ctx context.Context, input *CancelSprayInput) (*CancelSprayOutput, error) {
    user := middleware.UserFromContext(ctx)
    sprayID, _ := uuid.Parse(input.ID)
    q := dbsqlc.New(h.pool)
    spray, err := q.GetSprayReport(ctx, sprayID)
    if err != nil { return nil, huma.NewError(404, "not found") }
    if spray.FarmerID.String() != user.ID && user.Role != domain.RoleInspector {
        return nil, huma.NewError(403, "forbidden")
    }
    _ = q.UpdateSprayReportStatus(ctx, dbsqlc.UpdateSprayReportStatusParams{
        ID: sprayID, Status: dbsqlc.SprayStatusCancelled,
    })
    actorID := user.ID
    _, _ = h.ledgerSvc.Append(ctx, nil, "spray.cancelled", &actorID, map[string]any{"spray_id": sprayID.String()})
    return &CancelSprayOutput{Body: struct{Message string `json:"message"`}{"cancelled"}}, nil
}
```

### `internal/api/alerts.go`

Implement 3 handlers:

**`GET /alerts`** (beekeeper sees own active alerts):
```go
func (h *Handlers) listAlerts(ctx context.Context, _ *struct{}) (*ListAlertsOutput, error) {
    user := middleware.UserFromContext(ctx)
    if user.Role != domain.RoleApicultor { return nil, huma.NewError(403, "only beekeepers") }
    beekeeperID, _ := uuid.Parse(user.ID)
    q := dbsqlc.New(h.pool)
    rows, err := q.ListActiveAlertsByBeekeeper(ctx, beekeeperID)
    if err != nil { return nil, err }
    // convert to response, return
}
```

**`GET /alerts/:id`**: Get dispatch + linked spray info.

**`POST /alerts/:id/confirm`** (in-app confirmation):
```go
type ConfirmAlertInput struct {
    ID   string `path:"id"`
    Body struct {
        Action string `json:"action" enum:"move_hives,seal_in_place"`
    }
}

func (h *Handlers) confirmAlert(ctx context.Context, input *ConfirmAlertInput) (*ConfirmAlertOutput, error) {
    user := middleware.UserFromContext(ctx)
    // Verify dispatch belongs to this beekeeper
    dispatchID, _ := uuid.Parse(input.ID)
    q := dbsqlc.New(h.pool)
    dispatch, err := q.GetAlertDispatch(ctx, dispatchID)
    if err != nil { return nil, huma.NewError(404, "not found") }
    if dispatch.BeekeeperID.String() != user.ID { return nil, huma.NewError(403, "not your alert") }

    hash, err := h.cascade.HandleInAppConfirm(ctx, input.ID, input.Body.Action)
    if err != nil { return nil, err }
    return &ConfirmAlertOutput{Body: struct{LedgerHash string `json:"ledger_hash"`}{hash}}, nil
}
```

### `internal/api/webhooks_twilio.go`

**IMPORTANT**: Remove Huma stub registrations. Register as raw chi routes in `router.go`. The webhook handlers are `http.HandlerFunc`, not Huma handlers.

The Huma `registerTwilioWebhooks` function should be removed or converted to raw chi route registration.

Implement 4 raw handlers:

**`rawVoiceGather(w http.ResponseWriter, r *http.Request)`** — called when Twilio collects DTMF digit:
```go
func (h *Handlers) rawVoiceGather(w http.ResponseWriter, r *http.Request) {
    r.ParseForm()
    callSID := r.FormValue("CallSid")
    digits := r.FormValue("Digits")
    dispatchID := r.URL.Query().Get("dispatch_id")

    if digits == "1" && callSID != "" {
        if err := h.cascade.HandleCallConfirmed(r.Context(), callSID); err != nil {
            slog.Error("voice gather: handle confirmed failed", "err", err)
        }
        // Return simple TwiML acknowledgement
        w.Header().Set("Content-Type", "application/xml")
        w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response><Say language="ro-RO">Mulțumim! Confirmare înregistrată.</Say></Response>`))
        return
    }

    _ = dispatchID
    // No digit pressed — return the initial TwiML again or a "goodbye" message
    w.Header().Set("Content-Type", "application/xml")
    w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response><Say language="ro-RO">Nu am primit o confirmare. Vă rugăm contactați apicultorul direct.</Say></Response>`))
}
```

**`rawVoiceStatus(w http.ResponseWriter, r *http.Request)`** — Twilio call status callback:
```go
func (h *Handlers) rawVoiceStatus(w http.ResponseWriter, r *http.Request) {
    r.ParseForm()
    callSID := r.FormValue("CallSid")
    callStatus := r.FormValue("CallStatus")

    if callStatus == "completed" || callStatus == "no-answer" || callStatus == "busy" || callStatus == "failed" || callStatus == "canceled" {
        // Check if already confirmed via gather
        if callStatus != "completed" {
            _ = h.cascade.HandleCallTerminal(r.Context(), callSID, callStatus)
        }
    }
    w.WriteHeader(http.StatusNoContent)
}
```

**`rawSMSInbound(w http.ResponseWriter, r *http.Request)`** — inbound SMS "DA" reply:
```go
func (h *Handlers) rawSMSInbound(w http.ResponseWriter, r *http.Request) {
    r.ParseForm()
    smsSID := r.FormValue("SmsSid")
    body := strings.ToUpper(strings.TrimSpace(r.FormValue("Body")))

    if strings.Contains(body, "DA") && smsSID != "" {
        if err := h.cascade.HandleSMSConfirmed(r.Context(), smsSID); err != nil {
            slog.Error("sms inbound: handle confirmed failed", "err", err)
        }
    }
    // Return empty TwiML response
    w.Header().Set("Content-Type", "application/xml")
    w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`))
}
```

**`rawSMSStatus(w http.ResponseWriter, r *http.Request)`** — SMS delivery status:
```go
func (h *Handlers) rawSMSStatus(w http.ResponseWriter, r *http.Request) {
    r.ParseForm()
    smsSID := r.FormValue("SmsSid")
    status := r.FormValue("MessageStatus")
    if smsSID != "" && status != "" {
        _ = h.cascade.HandleSMSStatus(r.Context(), smsSID, status)
    }
    w.WriteHeader(http.StatusNoContent)
}
```

**Initial TwiML served at GET/POST `/api/v1/webhooks/twilio/voice/gather`** (before Twilio collects digits):

The `rawVoiceGather` is called BOTH when Twilio first makes the call (with no digits yet, initial gather) AND when Twilio submits gathered digits. Distinguish:
- No `Digits` form value → serve the initial gather TwiML
- Has `Digits` → process confirmation

Static TwiML for initial call:
```xml
<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say language="ro-RO">Atenție! Fermier aplică pesticide în apropierea stupinei dumneavoastră. Apăsați 1 pentru confirmare.</Say>
  <Gather numDigits="1" action="/api/v1/webhooks/twilio/voice/gather" method="POST" timeout="10">
  </Gather>
  <Say language="ro-RO">Nu am primit o confirmare. Vă rugăm contactați fermierul direct.</Say>
</Response>
```

### `internal/api/router.go`

1. Add `cascade *services.CascadeService` field to `Handlers`.

2. In `NewRouter`:
   ```go
   cascadeSvc := services.NewCascadeService(pool, ledgerSvc, nil, nil, cfg.AppBaseURL)
   h := &Handlers{..., cascade: cascadeSvc}
   ```

3. Remove Huma webhook registrations (delete `registerTwilioWebhooks(humaAPI, h)` call and the corresponding function, or at minimum don't call it).

4. Add raw chi routes for webhooks:
   ```go
   r.Post("/api/v1/webhooks/twilio/voice/gather", h.rawVoiceGather)
   r.Post("/api/v1/webhooks/twilio/voice/status", h.rawVoiceStatus)
   r.Post("/api/v1/webhooks/twilio/sms/inbound", h.rawSMSInbound)
   r.Post("/api/v1/webhooks/twilio/sms/status", h.rawSMSStatus)
   ```
   These go OUTSIDE the auth middleware group (Twilio doesn't send session cookies).

5. Update `Handlers` struct. The full struct after Phase 8:
   ```go
   type Handlers struct {
       cfg           *config.Config
       pool          *pgxpool.Pool
       jwt           *platform.JWTService
       authSvc       *services.AuthService
       ledgerSvc     *services.LedgerService
       geoAI         geoai.Client
       weatherClient *weather.CachedClient
       cascade       *services.CascadeService
   }
   ```

### `cmd/server/main.go`

Call `cascade.Shutdown()` before `srv.Shutdown`:
```go
// After <-ctx.Done():
cascadeSvc.Shutdown() // stop in-memory timers
shutdownCtx, cancel := context.WithTimeout(...)
defer cancel()
srv.Shutdown(shutdownCtx)
```

But `cascadeSvc` is created inside `api.NewRouter` — it needs to be accessible here. Refactor options:
1. Return `cascadeSvc` from `NewRouter` alongside the `http.Handler` — simplest.
2. Or store it in a package-level var (avoid).
3. Or have `NewRouter` accept a `*services.CascadeService` parameter.

**Recommended**: Change `NewRouter` signature to return `(http.Handler, *services.CascadeService)`:
```go
func NewRouter(cfg *config.Config, pool *pgxpool.Pool) (http.Handler, *services.CascadeService)
```
Then in `main.go`:
```go
router, cascadeSvc := api.NewRouter(cfg, pool)
// ...
<-ctx.Done()
cascadeSvc.Shutdown()
```

---

## Key Implementation Details

1. **`context.WithoutCancel`** — Go 1.21+. Creates a copy of ctx without cancellation, used for background goroutines that must outlive the request. The module uses Go 1.25 so this is available.

2. **Advisory lock in LedgerService.Append**: When called from within a transaction (`tx != nil`), the advisory lock `SELECT pg_advisory_xact_lock(42)` serializes concurrent ledger appends within that transaction scope. Multiple goroutines all wait for this lock before appending.

3. **`dbsqlc.New(tx)` type check**: The `DBTX` interface in `db.go` requires `ExecContext`, `QueryContext`, `QueryRowContext`. Both `pgxpool.Pool` and `pgx.Tx` implement these. If `pgx.Tx` doesn't compile, look at `db.go` for the exact interface — there may be a `PrepareContext` method too. In that case, use `pgxpool.Pool.BeginTx` → wrap the `pgx.Tx` into a `database/sql.Tx` via stdlib adapter, or find another approach.

4. **Dispatch creation with `call_state`**: For T- substances, set `CallState: dbsqlc.CallStateSkipped`. The `CreateAlertDispatch` query inserts with the provided `call_state` value. The default in SQL is `'queued'` but we override it by setting the field explicitly.

5. **Goroutine safety**: `sync.Map` operations are safe for concurrent use. `time.AfterFunc` callbacks run in separate goroutines — all DB operations inside callbacks use `context.Background()` (since the request context is gone). All sqlc operations are thread-safe as they go through the pgxpool.

6. **In-memory timer loss on restart**: Documented as v1 limitation. On restart, `smsTimers` and `unconfirmedTimers` maps are empty. Dispatches created before the restart won't have their timers. This is acceptable for demo/hackathon.

7. **Duplicate dispatch from multiple beekeepers**: If two apiaries belong to the same beekeeper and both are within range, the beekeeper gets two dispatches (two calls, two SMS). This is correct behavior — each apiary is a separate alerting entity.

8. **`UpdateSprayReportLedgerHash` and `UpdateSprayReportAffectedCount` params**: Check the generated sqlc code for exact struct field names.

9. **The `cascade` field in Handlers needs `*services.CascadeService`**: Import `"github.com/radarul-albinelor/api/internal/services"` in `router.go`.

---

## Verification Steps

```bash
# Ensure new query exists
grep "GetSubstanceByLabel" /Users/alinscreciu/work/hackathon/api/internal/db/sqlc/substances.sql.go

# Build check
go build ./...
go vet ./...

# Start server
go run ./cmd/server &

# Login as fermier Vasile Mureșan
# ... get FARMER_COOKIE ...

# Get parcel ID
PARCEL_ID=$(curl -s http://localhost:8080/api/v1/parcels -b /tmp/farmer_cookies.txt | jq -r '.parcels[0].id')
echo "Parcel: $PARCEL_ID"

# POST spray report near seeded apiaries (parcel lat 46.782, lng 23.608)
# Confidor Energy (T+) → 3000m radius → should hit all 3 beekeepers' apiaries
SPRAY=$(curl -s -X POST http://localhost:8080/api/v1/spray-reports \
  -H 'Content-Type: application/json' \
  -b /tmp/farmer_cookies.txt \
  -d "{
    \"parcel_id\": \"$PARCEL_ID\",
    \"surface_ha\": 5.2,
    \"crop\": \"rapiță\",
    \"substance\": \"Confidor Energy\",
    \"scheduled_at\": \"2026-05-25T08:00:00Z\",
    \"duration_hours\": 2.0
  }" | jq .)
echo "$SPRAY" | jq .
SPRAY_ID=$(echo "$SPRAY" | jq -r '.spray_report.id')
echo "Spray ID: $SPRAY_ID"
# Expected: affected_apiaries >= 1, risk_radius_m = 3000

# Check server stdout for "[MOCK CALL]" and "[MOCK SMS]" lines

# GET cascade status
curl -s "http://localhost:8080/api/v1/spray-reports/$SPRAY_ID/cascade-status" \
  -b /tmp/farmer_cookies.txt | jq .
# Expected: dispatches with call_state="queued" or "skipped", overall_status="in_progress"

# GET alerts as apicultor (Andrei Berar)
# Login as Andrei, get cookie...
curl -s http://localhost:8080/api/v1/alerts -b /tmp/beekeeper_cookies.txt | jq .
# Expected: alerts for Andrei's apiaries if within 3000m

# In-app confirm
DISPATCH_ID=$(curl -s http://localhost:8080/api/v1/alerts -b /tmp/beekeeper_cookies.txt | jq -r '.[0].id // .alerts[0].id')
curl -s -X POST "http://localhost:8080/api/v1/alerts/$DISPATCH_ID/confirm" \
  -H 'Content-Type: application/json' \
  -b /tmp/beekeeper_cookies.txt \
  -d '{"action":"move_hives"}' | jq .
# Expected: {"ledger_hash":"..."}

# Re-check cascade status — should show 1 confirmed
curl -s "http://localhost:8080/api/v1/spray-reports/$SPRAY_ID/cascade-status" \
  -b /tmp/farmer_cookies.txt | jq .

# Simulate Twilio voice gather webhook
curl -s -X POST http://localhost:8080/api/v1/webhooks/twilio/voice/gather \
  -d 'CallSid=CATEST123&Digits=1' | cat
# Expected: TwiML XML with <Say>Mulțumim</Say>

curl -s -X POST http://localhost:8080/api/v1/webhooks/twilio/voice/gather \
  -d 'CallSid=CATEST123' | cat
# Expected: Initial gather TwiML

# Simulate SMS inbound
curl -s -X POST http://localhost:8080/api/v1/webhooks/twilio/sms/inbound \
  -d 'SmsSid=SMTEST123&Body=DA' | cat
# Expected: empty TwiML response

# Verify ledger
curl -s http://localhost:8080/api/v1/events/verify -b /tmp/beekeeper_cookies.txt | jq .
# Expected: valid:true, total_events >= 3 (spray.created + alert.dispatched × N + alert.confirmed)

# Cancel a spray
curl -s -X POST "http://localhost:8080/api/v1/spray-reports/$SPRAY_ID/cancel" \
  -b /tmp/farmer_cookies.txt | jq .

go vet ./...
```

---

## After Completion

Write `/Users/alinscreciu/work/hackathon/api/.planning/phases/phase-8.md`:

```markdown
# Phase 8 — Spray Reports + Cascade Orchestration
Status: COMPLETE
Completed: <date>

## What was built
- internal/services/cascade.go — CascadeService with goroutine-per-dispatch, timer management, webhook handlers
- internal/api/sprays.go — all 5 spray endpoints implemented
- internal/api/alerts.go — all 3 alert endpoints implemented
- internal/api/webhooks_twilio.go — 4 raw chi handlers (no Huma)
- internal/api/router.go — cascade service wired, raw webhook routes registered
- cmd/server/main.go — cascade.Shutdown() on graceful shutdown

## Verification
- POST /spray-reports → cascade goroutines launch, [MOCK CALL]/[MOCK SMS] in stdout
- GET /cascade-status → live state
- POST /alerts/:id/confirm → final_status=confirmed_app, ledger event
- GET /events/verify → valid:true
- Twilio webhook simulation → correct TwiML responses
- go build ./... clean
```
