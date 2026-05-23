package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/domain"
)

// Notifier abstracts Twilio (or any SMS/voice provider).
type Notifier interface {
	SendSMS(ctx context.Context, to, body string) (string, error)
	MakeCall(ctx context.Context, to, twimlURL string) (string, error)
}

// PushSender abstracts the web-push delivery layer.
type PushSender interface {
	Send(ctx context.Context, sub domain.PushSubscription, payload []byte) error
}

// CascadeService orchestrates the full pesticide-spray alert notification cascade:
// push → voice call (T/T+) / SMS (T-) → SMS fallback → unconfirmed timeout.
type CascadeService struct {
	db     *dbsqlc.Queries
	sqlDB  *sql.DB
	pool   *pgxpool.Pool
	ledger *LedgerService

	notifier Notifier   // nil until Phase 8 wires real Twilio client
	pusher   PushSender // nil until Phase 8 wires real push client

	// key: dispatchID string → *time.Timer
	smsTimers         sync.Map
	unconfirmedTimers sync.Map

	appBaseURL string
}

// NewCascadeService constructs the service. notifier and pusher may be nil
// (mock/log behaviour is used in that case).
func NewCascadeService(
	pool *pgxpool.Pool,
	ledger *LedgerService,
	notifier Notifier,
	pusher PushSender,
	appBaseURL string,
) *CascadeService {
	sqlDB := stdlib.OpenDBFromPool(pool)
	return &CascadeService{
		db:         dbsqlc.New(sqlDB),
		sqlDB:      sqlDB,
		pool:       pool,
		ledger:     ledger,
		notifier:   notifier,
		pusher:     pusher,
		appBaseURL: appBaseURL,
	}
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

// Start launches a background goroutine for each dispatch.
// It detaches from the HTTP request context via context.WithoutCancel so the
// goroutines are not killed when the response is sent.
func (c *CascadeService) Start(ctx context.Context, sprayID string, dispatches []dbsqlc.AlertDispatch) {
	bgCtx := context.WithoutCancel(ctx)
	for _, d := range dispatches {
		go c.launchDispatch(bgCtx, d)
	}
}

// ---------------------------------------------------------------------------
// Internal per-dispatch orchestration
// ---------------------------------------------------------------------------

func (c *CascadeService) launchDispatch(ctx context.Context, dispatch dbsqlc.AlertDispatch) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("cascade panic in launchDispatch",
				"recover", r,
				"dispatch_id", dispatch.ID,
			)
		}
	}()

	dispatchID := dispatch.ID.String()
	slog.Info("cascade: launching dispatch", "dispatch_id", dispatchID, "call_state", dispatch.CallState)

	// --- Push notification ---
	if c.pusher == nil {
		slog.Info("cascade: [mock] push notification sent", "dispatch_id", dispatchID)
	} else {
		pSubCtx, pSubCancel := context.WithTimeout(ctx, 10*time.Second)
		subs, err := c.db.ListPushSubscriptionsByUser(pSubCtx, dispatch.BeekeeperID)
		pSubCancel()
		if err != nil {
			slog.Error("cascade: list push subscriptions", "dispatch_id", dispatchID, "err", err)
		} else {
			payload := buildPushPayload(dispatch)
			for _, sub := range subs {
				domSub := domain.PushSubscription{
					ID:       sub.ID.String(),
					UserID:   sub.UserID.String(),
					Endpoint: sub.Endpoint,
					P256dh:   sub.P256dh,
					Auth:     sub.Auth,
				}
				pCtx, pCancel := context.WithTimeout(ctx, 10*time.Second)
				if sendErr := c.pusher.Send(pCtx, domSub, payload); sendErr != nil {
					slog.Error("cascade: push send", "dispatch_id", dispatchID, "err", sendErr)
				}
				pCancel()
			}
		}
	}

	tCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := c.db.UpdatePushState(tCtx, dbsqlc.UpdatePushStateParams{
		ID:        dispatch.ID,
		PushState: dbsqlc.PushStateSent,
	}); err != nil {
		slog.Error("cascade: update push state", "dispatch_id", dispatchID, "err", err)
	}

	// --- Branch by call_state (set by spray handler before calling Start) ---
	if dispatch.CallState == dbsqlc.CallStateSkipped {
		// T- path: voice call is skipped, SMS is co-primary.
		// Small deliberate pause to let the push notification settle.
		time.Sleep(1 * time.Second)
		c.fireSMS(ctx, dispatch)
		return
	}

	// T / T+ path: make the voice call first, then arm the SMS fallback timer.
	c.makeCall(ctx, dispatch)
	c.scheduleSMSFallback(dispatchID, 60*time.Second)
	c.scheduleUnconfirmedTimeout(dispatchID)
}

// ---------------------------------------------------------------------------
// Voice call
// ---------------------------------------------------------------------------

func (c *CascadeService) makeCall(ctx context.Context, dispatch dbsqlc.AlertDispatch) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("cascade panic in makeCall", "recover", r, "dispatch_id", dispatch.ID)
		}
	}()

	dispatchID := dispatch.ID.String()

	twimlURL := fmt.Sprintf("%s/api/v1/webhooks/twilio/voice/gather?dispatch_id=%s",
		c.appBaseURL, dispatchID)

	// Fetch beekeeper to get their phone number.
	uCtx, uCancel := context.WithTimeout(ctx, 10*time.Second)
	defer uCancel()

	beekeeper, err := c.db.GetUserByID(uCtx, dispatch.BeekeeperID)
	if err != nil {
		slog.Error("cascade: get beekeeper for call", "dispatch_id", dispatchID, "err", err)
		return
	}

	var callSID string
	if c.notifier != nil {
		callCtx, callCancel := context.WithTimeout(ctx, 10*time.Second)
		defer callCancel()

		callSID, err = c.notifier.MakeCall(callCtx, beekeeper.Phone, twimlURL)
		if err != nil {
			slog.Error("cascade: make call failed", "dispatch_id", dispatchID, "err", err)
			// Do not abort; the SMS fallback timer will fire.
		}
	} else {
		callSID = "MOCK-" + dispatchID
		slog.Info("cascade: [mock] voice call initiated",
			"dispatch_id", dispatchID,
			"to", beekeeper.Phone,
			"twiml_url", twimlURL,
			"mock_call_sid", callSID,
		)
	}

	// SetTwilioCallSID also sets call_state='queued' and call_at=NOW().
	sCtx, sCancel := context.WithTimeout(ctx, 10*time.Second)
	defer sCancel()

	if err := c.db.SetTwilioCallSID(sCtx, dbsqlc.SetTwilioCallSIDParams{
		ID:            dispatch.ID,
		TwilioCallSid: sql.NullString{String: callSID, Valid: true},
	}); err != nil {
		slog.Error("cascade: set twilio call sid", "dispatch_id", dispatchID, "err", err)
	}
	// NOTE: we deliberately do NOT call UpdateCallState here.
	// SetTwilioCallSID already sets call_state = 'queued' in the same statement.
}

// ---------------------------------------------------------------------------
// SMS
// ---------------------------------------------------------------------------

func (c *CascadeService) fireSMS(ctx context.Context, dispatch dbsqlc.AlertDispatch) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("cascade panic in fireSMS", "recover", r, "dispatch_id", dispatch.ID)
		}
	}()

	dispatchID := dispatch.ID.String()

	// Re-read the dispatch to check whether it has already been resolved.
	freshCtx, freshCancel := context.WithTimeout(ctx, 10*time.Second)
	defer freshCancel()

	fresh, err := c.db.GetAlertDispatch(freshCtx, dispatch.ID)
	if err != nil {
		slog.Error("cascade: re-read dispatch for SMS", "dispatch_id", dispatchID, "err", err)
		return
	}
	if fresh.FinalStatus.Valid {
		slog.Info("cascade: dispatch already resolved, skipping SMS",
			"dispatch_id", dispatchID,
			"final_status", fresh.FinalStatus.FinalStatus,
		)
		return
	}

	// Fetch beekeeper for phone number.
	uCtx, uCancel := context.WithTimeout(ctx, 10*time.Second)
	defer uCancel()

	beekeeper, err := c.db.GetUserByID(uCtx, dispatch.BeekeeperID)
	if err != nil {
		slog.Error("cascade: get beekeeper for SMS", "dispatch_id", dispatchID, "err", err)
		return
	}

	// Fetch spray report and apiary for message content.
	srCtx, srCancel := context.WithTimeout(ctx, 10*time.Second)
	defer srCancel()

	spray, err := c.db.GetSprayReport(srCtx, dispatch.SprayReportID)
	if err != nil {
		slog.Error("cascade: get spray report for SMS", "dispatch_id", dispatchID, "err", err)
		return
	}

	aCtx, aCancel := context.WithTimeout(ctx, 10*time.Second)
	defer aCancel()

	apiary, err := c.db.GetApiary(aCtx, dispatch.ApiaryID)
	if err != nil {
		slog.Error("cascade: get apiary for SMS", "dispatch_id", dispatchID, "err", err)
		return
	}

	body := fmt.Sprintf(
		"ALERTĂ STUPINA: Tratament pesticid programat pentru %s pe parcela din %s la %.0fm de stupina %s. Substanță: %s (toxicitate %s). Răspundeți DA pentru confirmare.",
		spray.ScheduledAt.Format("02.01.2006 15:04"),
		spray.Substance,
		dispatch.DistanceM,
		apiary.Name,
		spray.Substance,
		spray.Toxicity,
	)

	var smsSID string
	if c.notifier != nil {
		smsCtx, smsCancel := context.WithTimeout(ctx, 10*time.Second)
		defer smsCancel()

		smsSID, err = c.notifier.SendSMS(smsCtx, beekeeper.Phone, body)
		if err != nil {
			slog.Error("cascade: send SMS failed", "dispatch_id", dispatchID, "err", err)
			return
		}
	} else {
		smsSID = "MOCK-SMS-" + dispatchID
		slog.Info("cascade: [mock] SMS sent",
			"dispatch_id", dispatchID,
			"to", beekeeper.Phone,
			"body", body,
			"mock_sms_sid", smsSID,
		)
	}

	// Persist the Twilio SMS SID (also sets sms_state='queued', sms_at=NOW()).
	sidCtx, sidCancel := context.WithTimeout(ctx, 10*time.Second)
	defer sidCancel()

	if err := c.db.SetTwilioSMSSID(sidCtx, dbsqlc.SetTwilioSMSSIDParams{
		ID:           dispatch.ID,
		TwilioSmsSid: sql.NullString{String: smsSID, Valid: true},
	}); err != nil {
		slog.Error("cascade: set twilio sms sid", "dispatch_id", dispatchID, "err", err)
	}

	// Update sms_state to 'sent'.
	stCtx, stCancel := context.WithTimeout(ctx, 10*time.Second)
	defer stCancel()

	if err := c.db.UpdateSMSState(stCtx, dbsqlc.UpdateSMSStateParams{
		ID:       dispatch.ID,
		SmsState: dbsqlc.SmsStateSent,
	}); err != nil {
		slog.Error("cascade: update sms state to sent", "dispatch_id", dispatchID, "err", err)
	}
}

// ---------------------------------------------------------------------------
// Timers
// ---------------------------------------------------------------------------

// scheduleSMSFallback arms a timer that fires fireSMS after delay.
// A delay of 0 fires immediately (asynchronously).
func (c *CascadeService) scheduleSMSFallback(dispatchID string, delay time.Duration) {
	dispatchUUID, err := uuid.Parse(dispatchID)
	if err != nil {
		slog.Error("cascade: scheduleSMSFallback invalid uuid", "dispatch_id", dispatchID, "err", err)
		return
	}

	stub := dbsqlc.AlertDispatch{ID: dispatchUUID}

	timer := time.AfterFunc(delay, func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("cascade panic in SMS fallback timer", "recover", r, "dispatch_id", dispatchID)
			}
		}()
		// Use a fresh background context — the original request context is long gone.
		c.fireSMS(context.Background(), stub)
	})

	c.smsTimers.Store(dispatchID, timer)
	slog.Info("cascade: SMS fallback timer armed", "dispatch_id", dispatchID, "delay", delay)
}

// cancelSMSFallback stops and removes the SMS fallback timer for a dispatch.
func (c *CascadeService) cancelSMSFallback(dispatchID string) {
	if v, ok := c.smsTimers.LoadAndDelete(dispatchID); ok {
		if t, ok := v.(*time.Timer); ok {
			t.Stop()
			slog.Info("cascade: SMS fallback timer cancelled", "dispatch_id", dispatchID)
		}
	}
}

// scheduleUnconfirmedTimeout fires after 30 minutes and marks the dispatch
// as unconfirmed if it has not already been resolved.
func (c *CascadeService) scheduleUnconfirmedTimeout(dispatchID string) {
	dispatchUUID, err := uuid.Parse(dispatchID)
	if err != nil {
		slog.Error("cascade: scheduleUnconfirmedTimeout invalid uuid", "dispatch_id", dispatchID, "err", err)
		return
	}

	timer := time.AfterFunc(30*time.Minute, func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("cascade panic in unconfirmed timer", "recover", r, "dispatch_id", dispatchID)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := c.db.UpdateFinalStatus(ctx, dbsqlc.UpdateFinalStatusParams{
			ID: dispatchUUID,
			FinalStatus: dbsqlc.NullFinalStatus{
				FinalStatus: dbsqlc.FinalStatusUnconfirmed,
				Valid:       true,
			},
		}); err != nil {
			slog.Error("cascade: mark unconfirmed", "dispatch_id", dispatchID, "err", err)
		} else {
			slog.Info("cascade: dispatch marked unconfirmed", "dispatch_id", dispatchID)
		}

		c.unconfirmedTimers.Delete("unconf:" + dispatchID)
	})

	c.unconfirmedTimers.Store("unconf:"+dispatchID, timer)
	slog.Info("cascade: unconfirmed timeout scheduled", "dispatch_id", dispatchID)
}

// ---------------------------------------------------------------------------
// Webhook handlers (called from internal/api/webhooks_twilio.go)
// ---------------------------------------------------------------------------

// HandleCallConfirmed is called when the beekeeper presses 1 during the voice
// call (Twilio DTMF gather callback).
func (c *CascadeService) HandleCallConfirmed(ctx context.Context, callSID string) error {
	dispatch, err := c.db.GetDispatchByTwilioCallSID(ctx, sql.NullString{String: callSID, Valid: true})
	if err != nil {
		return fmt.Errorf("get dispatch by call sid: %w", err)
	}

	dispatchID := dispatch.ID.String()
	c.cancelSMSFallback(dispatchID)

	// Cancel the unconfirmed-timeout timer.
	if v, ok := c.unconfirmedTimers.LoadAndDelete("unconf:" + dispatchID); ok {
		if t, ok := v.(*time.Timer); ok {
			t.Stop()
		}
	}

	tCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := c.db.UpdateCallState(tCtx, dbsqlc.UpdateCallStateParams{
		ID:        dispatch.ID,
		CallState: dbsqlc.CallStateConfirmed,
	}); err != nil {
		return fmt.Errorf("update call state confirmed: %w", err)
	}

	fCtx, fCancel := context.WithTimeout(ctx, 10*time.Second)
	defer fCancel()

	if err := c.db.UpdateFinalStatus(fCtx, dbsqlc.UpdateFinalStatusParams{
		ID: dispatch.ID,
		FinalStatus: dbsqlc.NullFinalStatus{
			FinalStatus: dbsqlc.FinalStatusConfirmedCall,
			Valid:       true,
		},
	}); err != nil {
		return fmt.Errorf("update final status confirmed_call: %w", err)
	}

	actorID := dispatch.BeekeeperID.String()
	if _, err := c.ledger.Append(ctx, nil, "alert.confirmed", &actorID, map[string]any{
		"dispatch_id": dispatchID,
		"method":      "call",
		"call_sid":    callSID,
	}); err != nil {
		slog.Error("cascade: ledger append call confirmed", "dispatch_id", dispatchID, "err", err)
	}

	slog.Info("cascade: call confirmed", "dispatch_id", dispatchID, "call_sid", callSID)
	return nil
}

// HandleCallTerminal is called when Twilio reports a terminal call status
// (no-answer, busy, failed, etc.) via the status callback webhook.
func (c *CascadeService) HandleCallTerminal(ctx context.Context, callSID, callStatus string) error {
	dispatch, err := c.db.GetDispatchByTwilioCallSID(ctx, sql.NullString{String: callSID, Valid: true})
	if err != nil {
		return fmt.Errorf("get dispatch by call sid: %w", err)
	}

	state := c.twilioCallStatusToState(callStatus)

	tCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := c.db.UpdateCallState(tCtx, dbsqlc.UpdateCallStateParams{
		ID:        dispatch.ID,
		CallState: state,
	}); err != nil {
		return fmt.Errorf("update call state terminal: %w", err)
	}

	// Fire SMS fallback immediately (delay=0).
	c.scheduleSMSFallback(dispatch.ID.String(), 0)

	slog.Info("cascade: call terminal, SMS fallback fired",
		"dispatch_id", dispatch.ID,
		"call_status", callStatus,
		"mapped_state", state,
	)
	return nil
}

// HandleSMSConfirmed is called when the beekeeper replies "DA" to the SMS.
func (c *CascadeService) HandleSMSConfirmed(ctx context.Context, smsSID string) error {
	dispatch, err := c.db.GetDispatchByTwilioSMSSID(ctx, sql.NullString{String: smsSID, Valid: true})
	if err != nil {
		return fmt.Errorf("get dispatch by sms sid: %w", err)
	}

	dispatchID := dispatch.ID.String()

	tCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := c.db.UpdateSMSState(tCtx, dbsqlc.UpdateSMSStateParams{
		ID:       dispatch.ID,
		SmsState: dbsqlc.SmsStateConfirmed,
	}); err != nil {
		return fmt.Errorf("update sms state confirmed: %w", err)
	}

	fCtx, fCancel := context.WithTimeout(ctx, 10*time.Second)
	defer fCancel()

	if err := c.db.UpdateFinalStatus(fCtx, dbsqlc.UpdateFinalStatusParams{
		ID: dispatch.ID,
		FinalStatus: dbsqlc.NullFinalStatus{
			FinalStatus: dbsqlc.FinalStatusConfirmedSms,
			Valid:       true,
		},
	}); err != nil {
		return fmt.Errorf("update final status confirmed_sms: %w", err)
	}

	// Cancel the unconfirmed-timeout timer.
	if v, ok := c.unconfirmedTimers.LoadAndDelete("unconf:" + dispatchID); ok {
		if t, ok := v.(*time.Timer); ok {
			t.Stop()
		}
	}

	actorID := dispatch.BeekeeperID.String()
	if _, err := c.ledger.Append(ctx, nil, "alert.confirmed", &actorID, map[string]any{
		"dispatch_id": dispatchID,
		"method":      "sms",
		"sms_sid":     smsSID,
	}); err != nil {
		slog.Error("cascade: ledger append sms confirmed", "dispatch_id", dispatchID, "err", err)
	}

	slog.Info("cascade: SMS confirmed", "dispatch_id", dispatchID, "sms_sid", smsSID)
	return nil
}

// HandleSMSStatus updates the SMS delivery state from a Twilio status callback.
func (c *CascadeService) HandleSMSStatus(ctx context.Context, smsSID, status string) error {
	state := c.twilioSMSStatusToState(status)

	tCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := c.db.UpdateSMSStateByTwilioSID(tCtx, dbsqlc.UpdateSMSStateByTwilioSIDParams{
		TwilioSmsSid: sql.NullString{String: smsSID, Valid: true},
		SmsState:     state,
	}); err != nil {
		return fmt.Errorf("update sms state by twilio sid: %w", err)
	}

	slog.Info("cascade: SMS status updated", "sms_sid", smsSID, "status", status, "state", state)
	return nil
}

// HandleInAppConfirm is called when a beekeeper confirms an alert via the
// in-app action (move hives / seal in place). Returns the new ledger hash.
func (c *CascadeService) HandleInAppConfirm(ctx context.Context, dispatchID, action string) (string, error) {
	dispatchUUID, err := uuid.Parse(dispatchID)
	if err != nil {
		return "", fmt.Errorf("invalid dispatch id: %w", err)
	}

	var inAppAction dbsqlc.InAppAction
	switch action {
	case string(dbsqlc.InAppActionMoveHives):
		inAppAction = dbsqlc.InAppActionMoveHives
	case string(dbsqlc.InAppActionSealInPlace):
		inAppAction = dbsqlc.InAppActionSealInPlace
	default:
		return "", fmt.Errorf("unknown in-app action: %q", action)
	}

	tCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := c.db.SetInAppConfirmed(tCtx, dbsqlc.SetInAppConfirmedParams{
		ID:          dispatchUUID,
		InAppAction: dbsqlc.NullInAppAction{InAppAction: inAppAction, Valid: true},
		FinalStatus: dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedApp, Valid: true},
	}); err != nil {
		return "", fmt.Errorf("set in-app confirmed: %w", err)
	}

	// Cancel outstanding timers.
	c.cancelSMSFallback(dispatchID)
	if v, ok := c.unconfirmedTimers.LoadAndDelete("unconf:" + dispatchID); ok {
		if t, ok := v.(*time.Timer); ok {
			t.Stop()
		}
	}

	// Re-read dispatch for beekeeper actor.
	dCtx, dCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dCancel()

	dispatch, err := c.db.GetAlertDispatch(dCtx, dispatchUUID)
	if err != nil {
		return "", fmt.Errorf("get dispatch after in-app confirm: %w", err)
	}

	actorID := dispatch.BeekeeperID.String()
	hash, err := c.ledger.Append(ctx, nil, "alert.confirmed", &actorID, map[string]any{
		"dispatch_id": dispatchID,
		"method":      "app",
		"action":      action,
	})
	if err != nil {
		slog.Error("cascade: ledger append in-app confirmed", "dispatch_id", dispatchID, "err", err)
		return "", fmt.Errorf("ledger append: %w", err)
	}

	slog.Info("cascade: in-app confirmed", "dispatch_id", dispatchID, "action", action)
	return hash, nil
}

// ---------------------------------------------------------------------------
// Shutdown
// ---------------------------------------------------------------------------

// Shutdown stops all pending timers gracefully. Call during server shutdown.
func (c *CascadeService) Shutdown() {
	slog.Info("cascade: shutting down, stopping all timers")

	c.smsTimers.Range(func(key, value any) bool {
		if t, ok := value.(*time.Timer); ok {
			t.Stop()
			slog.Info("cascade: stopped SMS timer", "dispatch_id", key)
		}
		c.smsTimers.Delete(key)
		return true
	})

	c.unconfirmedTimers.Range(func(key, value any) bool {
		if t, ok := value.(*time.Timer); ok {
			t.Stop()
			slog.Info("cascade: stopped unconfirmed timer", "key", key)
		}
		c.unconfirmedTimers.Delete(key)
		return true
	})

	slog.Info("cascade: shutdown complete")
}

// ---------------------------------------------------------------------------
// Status mapping helpers
// ---------------------------------------------------------------------------

// twilioCallStatusToState maps a Twilio CallStatus string to our CallState enum.
func (c *CascadeService) twilioCallStatusToState(status string) dbsqlc.CallState {
	switch status {
	case "queued":
		return dbsqlc.CallStateQueued
	case "ringing":
		return dbsqlc.CallStateRinging
	case "in-progress":
		return dbsqlc.CallStateAnswered
	case "completed":
		return dbsqlc.CallStateAnswered
	case "busy":
		return dbsqlc.CallStateBusy
	case "no-answer":
		return dbsqlc.CallStateNoAnswer
	case "canceled", "cancelled":
		return dbsqlc.CallStateFailed
	case "failed":
		return dbsqlc.CallStateFailed
	default:
		slog.Warn("cascade: unknown twilio call status", "status", status)
		return dbsqlc.CallStateFailed
	}
}

// buildPushPayload marshals a JSON alert payload for web push notifications.
func buildPushPayload(d dbsqlc.AlertDispatch) []byte {
	type payload struct {
		Type          string  `json:"type"`
		DispatchID    string  `json:"dispatch_id"`
		SprayReportID string  `json:"spray_report_id"`
		DistanceM     float64 `json:"distance_m"`
	}
	data, _ := json.Marshal(payload{
		Type:          "pesticide_alert",
		DispatchID:    d.ID.String(),
		SprayReportID: d.SprayReportID.String(),
		DistanceM:     d.DistanceM,
	})
	return data
}

// twilioSMSStatusToState maps a Twilio MessageStatus string to our SmsState enum.
func (c *CascadeService) twilioSMSStatusToState(status string) dbsqlc.SmsState {
	switch status {
	case "queued", "accepted":
		return dbsqlc.SmsStateQueued
	case "sending", "sent":
		return dbsqlc.SmsStateSent
	case "delivered":
		return dbsqlc.SmsStateDelivered
	case "undelivered", "failed":
		return dbsqlc.SmsStateFailed
	default:
		slog.Warn("cascade: unknown twilio sms status", "status", status)
		return dbsqlc.SmsStateFailed
	}
}
