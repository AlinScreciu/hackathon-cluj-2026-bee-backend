package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
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

// ElevenLabsCaller is the subset of the ElevenLabs client used for outbound calls.
type ElevenLabsCaller interface {
	OutboundCall(ctx context.Context, toNumber string) (string, error)
}

// CascadeService orchestrates the full pesticide-spray alert notification cascade:
// push → voice call (T/T+) / SMS (T-) → SMS fallback → unconfirmed timeout.
type CascadeService struct {
	db     *dbsqlc.Queries
	sqlDB  *sql.DB
	pool   *pgxpool.Pool
	ledger *LedgerService

	notifier    Notifier        // Twilio — nil when not configured
	pusher      PushSender      // web push — nil when not configured
	elevenLabs  ElevenLabsCaller // ElevenLabs outbound call — nil when not configured

	// key: dispatchID string → *time.Timer
	smsTimers         sync.Map
	unconfirmedTimers sync.Map

	appBaseURL string
}

// NewCascadeService constructs the service. notifier, pusher, and elevenLabs may be nil
// (mock/log behaviour is used in that case).
func NewCascadeService(
	pool *pgxpool.Pool,
	ledger *LedgerService,
	notifier Notifier,
	pusher PushSender,
	elevenLabs ElevenLabsCaller,
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
		elevenLabs: elevenLabs,
		appBaseURL: appBaseURL,
	}
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

// Start launches a background goroutine PER BEEKEEPER (not per dispatch).
// Each beekeeper with N affected apiaries gets one push + one call + one SMS
// covering all their apiaries; the closest-apiary dispatch is the "primary"
// and owns the Twilio SIDs. Sibling dispatch rows stay in the DB for ledger
// granularity and have their final_status mirrored when the primary resolves.
// Detaches from the HTTP request context via context.WithoutCancel so
// goroutines are not killed when the response is sent.
func (c *CascadeService) Start(ctx context.Context, sprayID string, dispatches []dbsqlc.AlertDispatch) {
	bgCtx := context.WithoutCancel(ctx)

	groups := make(map[uuid.UUID][]dbsqlc.AlertDispatch)
	for _, d := range dispatches {
		groups[d.BeekeeperID] = append(groups[d.BeekeeperID], d)
	}

	for _, group := range groups {
		sort.Slice(group, func(i, j int) bool {
			return group[i].DistanceM < group[j].DistanceM
		})
		go c.launchDispatch(bgCtx, group[0], group[1:])
	}
}

// ---------------------------------------------------------------------------
// Internal per-dispatch orchestration
// ---------------------------------------------------------------------------

func (c *CascadeService) launchDispatch(ctx context.Context, primary dbsqlc.AlertDispatch, siblings []dbsqlc.AlertDispatch) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("cascade panic in launchDispatch",
				"recover", r,
				"dispatch_id", primary.ID,
			)
		}
	}()

	dispatchID := primary.ID.String()
	slog.Info("cascade: launching dispatch",
		"dispatch_id", dispatchID, "call_state", primary.CallState, "sibling_count", len(siblings))

	// --- Push notification (one per device, regardless of apiary count) ---
	if c.pusher == nil {
		slog.Info("cascade: [mock] push notification sent", "dispatch_id", dispatchID)
	} else {
		pSubCtx, pSubCancel := context.WithTimeout(ctx, 10*time.Second)
		subs, err := c.db.ListPushSubscriptionsByUser(pSubCtx, primary.BeekeeperID)
		pSubCancel()
		if err != nil {
			slog.Error("cascade: list push subscriptions", "dispatch_id", dispatchID, "err", err)
		} else {
			payload := buildPushPayload(primary)
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

	// Mark push_state=sent on the primary AND every sibling so the alerts UI
	// doesn't show siblings stuck in "queued".
	for _, d := range append([]dbsqlc.AlertDispatch{primary}, siblings...) {
		tCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := c.db.UpdatePushState(tCtx, dbsqlc.UpdatePushStateParams{
			ID:        d.ID,
			PushState: dbsqlc.PushStateSent,
		}); err != nil {
			slog.Error("cascade: update push state", "dispatch_id", d.ID, "err", err)
		}
		cancel()
	}

	// --- Branch by call_state (set by spray handler before calling Start) ---
	if primary.CallState == dbsqlc.CallStateSkipped {
		// T- path: voice call is skipped, SMS is co-primary.
		// Small deliberate pause to let the push notification settle.
		time.Sleep(1 * time.Second)
		c.fireSMS(ctx, primary)
		return
	}

	// T / T+ path: push + voice call + SMS all fire immediately as co-primary
	// channels. Push has already gone out above; SMS is independent of the
	// call's outcome to maximise reach (any of the three confirms the alert).
	// Unconfirmed timeout still arms — 30min escalation if none confirm.
	c.makeCall(ctx, primary)
	c.fireSMS(ctx, primary)
	c.scheduleUnconfirmedTimeout(dispatchID)
}

// buildApiaryClause builds a Romanian phrase listing all affected apiaries
// with their distances. Inputs come from a sorted-ascending-by-distance
// slice of sibling dispatches. Examples:
//
//	1 apiary:  "la 0.2 km de Stupina Apahida Sud"
//	2 apiary:  "la 0.2 km de Stupina Apahida Sud și 1.3 km de Stupina Apahida Nord"
//	3+ apiary: "la 0.2 km de Stupina A, 0.8 km de Stupina B și 1.3 km de Stupina C"
func (c *CascadeService) buildApiaryClause(ctx context.Context, dispatches []dbsqlc.AlertDispatch) (string, error) {
	if len(dispatches) == 0 {
		return "", fmt.Errorf("no dispatches")
	}
	parts := make([]string, 0, len(dispatches))
	for i, d := range dispatches {
		aCtx, aCancel := context.WithTimeout(ctx, 5*time.Second)
		apiary, err := c.db.GetApiary(aCtx, d.ApiaryID)
		aCancel()
		if err != nil {
			return "", fmt.Errorf("get apiary %s: %w", d.ApiaryID, err)
		}
		km := d.DistanceM / 1000.0
		if i == 0 {
			parts = append(parts, fmt.Sprintf("la %.1f km de %s", km, apiary.Name))
		} else {
			parts = append(parts, fmt.Sprintf("%.1f km de %s", km, apiary.Name))
		}
	}
	switch len(parts) {
	case 1:
		return parts[0], nil
	case 2:
		return parts[0] + " și " + parts[1], nil
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " și " + parts[len(parts)-1], nil
	}
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
	callCtx, callCancel := context.WithTimeout(ctx, 30*time.Second)
	defer callCancel()

	switch {
	case c.notifier != nil:
		callSID, err = c.notifier.MakeCall(callCtx, beekeeper.Phone, twimlURL)
		if err != nil {
			slog.Error("cascade: twilio make call failed", "dispatch_id", dispatchID, "err", err)
		}
	case c.elevenLabs != nil:
		callSID, err = c.elevenLabs.OutboundCall(callCtx, beekeeper.Phone)
		if err != nil {
			slog.Error("cascade: elevenlabs outbound call failed", "dispatch_id", dispatchID, "err", err)
		}
	default:
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

	// Load all siblings (same beekeeper + spray) so the SMS lists every
	// affected apiary in a single message.
	sibCtx, sibCancel := context.WithTimeout(ctx, 10*time.Second)
	siblings, err := c.db.ListDispatchSiblings(sibCtx, dbsqlc.ListDispatchSiblingsParams{
		SprayReportID: fresh.SprayReportID,
		BeekeeperID:   fresh.BeekeeperID,
	})
	sibCancel()
	if err != nil {
		slog.Warn("cascade: list siblings for SMS, falling back to primary only", "dispatch_id", dispatchID, "err", err)
		siblings = []dbsqlc.AlertDispatch{fresh}
	}

	uCtx, uCancel := context.WithTimeout(ctx, 10*time.Second)
	defer uCancel()

	beekeeper, err := c.db.GetUserByID(uCtx, fresh.BeekeeperID)
	if err != nil {
		slog.Error("cascade: get beekeeper for SMS", "dispatch_id", dispatchID, "err", err)
		return
	}

	srCtx, srCancel := context.WithTimeout(ctx, 10*time.Second)
	defer srCancel()

	spray, err := c.db.GetSprayReport(srCtx, fresh.SprayReportID)
	if err != nil {
		slog.Error("cascade: get spray report for SMS", "dispatch_id", dispatchID, "err", err)
		return
	}

	apiaryClause, err := c.buildApiaryClause(ctx, siblings)
	if err != nil {
		slog.Error("cascade: build apiary clause", "dispatch_id", dispatchID, "err", err)
		return
	}

	body := fmt.Sprintf(
		"BeeLive — Salut %s! Un fermier va aplica %s pe %.0f ha pe %s, %s. Protejați stupii! Răspundeți DA pentru confirmare.",
		beekeeper.FullName,
		spray.Substance,
		spray.SurfaceHa,
		spray.ScheduledAt.Format("02.01"),
		apiaryClause,
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

	// Persist the Twilio SMS SID on the primary only (it owns the SID for
	// reply-correlation). Siblings share the sms_state but not the SID.
	sidCtx, sidCancel := context.WithTimeout(ctx, 10*time.Second)
	defer sidCancel()

	if err := c.db.SetTwilioSMSSID(sidCtx, dbsqlc.SetTwilioSMSSIDParams{
		ID:           dispatch.ID,
		TwilioSmsSid: sql.NullString{String: smsSID, Valid: true},
	}); err != nil {
		slog.Error("cascade: set twilio sms sid", "dispatch_id", dispatchID, "err", err)
	}

	// sms_state=sent on primary and every sibling — so the alerts UI doesn't
	// show siblings stuck in "queued".
	for _, d := range siblings {
		stCtx, stCancel := context.WithTimeout(ctx, 10*time.Second)
		if err := c.db.UpdateSMSState(stCtx, dbsqlc.UpdateSMSStateParams{
			ID:       d.ID,
			SmsState: dbsqlc.SmsStateSent,
		}); err != nil {
			slog.Error("cascade: update sms state to sent", "dispatch_id", d.ID, "err", err)
		}
		stCancel()
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

		// Propagate the unconfirmed status to siblings so the beekeeper's UI
		// shows every apiary in the same final state.
		gCtx, gCancel := context.WithTimeout(context.Background(), 10*time.Second)
		primary, err := c.db.GetAlertDispatch(gCtx, dispatchUUID)
		gCancel()
		if err == nil {
			c.propagateFinalStatus(context.Background(), primary, dbsqlc.FinalStatusUnconfirmed)
		} else {
			slog.Warn("cascade: load primary for unconfirmed propagation", "dispatch_id", dispatchID, "err", err)
		}

		c.unconfirmedTimers.Delete("unconf:" + dispatchID)
	})

	c.unconfirmedTimers.Store("unconf:"+dispatchID, timer)
	slog.Info("cascade: unconfirmed timeout scheduled", "dispatch_id", dispatchID)
}

// ---------------------------------------------------------------------------
// State propagation: mirror the primary's resolution onto sibling dispatches
// so the beekeeper's alert list shows every affected apiary as confirmed
// (or unconfirmed) consistently — one phone confirmation covers them all.
// ---------------------------------------------------------------------------

// propagateFinalStatus copies the given final_status onto every sibling of
// the primary (same spray + beekeeper, excluding the primary itself).
// Failures are logged but not fatal — the primary's status is the source of
// truth; sibling drift is recoverable.
func (c *CascadeService) propagateFinalStatus(ctx context.Context, primary dbsqlc.AlertDispatch, status dbsqlc.FinalStatus) {
	sibCtx, sibCancel := context.WithTimeout(ctx, 10*time.Second)
	siblings, err := c.db.ListDispatchSiblings(sibCtx, dbsqlc.ListDispatchSiblingsParams{
		SprayReportID: primary.SprayReportID,
		BeekeeperID:   primary.BeekeeperID,
	})
	sibCancel()
	if err != nil {
		slog.Warn("cascade: list siblings for propagation", "primary_id", primary.ID, "err", err)
		return
	}
	for _, d := range siblings {
		if d.ID == primary.ID {
			continue
		}
		uCtx, uCancel := context.WithTimeout(ctx, 10*time.Second)
		if err := c.db.UpdateFinalStatus(uCtx, dbsqlc.UpdateFinalStatusParams{
			ID:          d.ID,
			FinalStatus: dbsqlc.NullFinalStatus{FinalStatus: status, Valid: true},
		}); err != nil {
			slog.Error("cascade: propagate final_status to sibling", "sibling_id", d.ID, "err", err)
		}
		uCancel()
		// Cancel any pending timers a sibling might have inherited from legacy
		// code paths. Belt and suspenders.
		c.cancelSMSFallback(d.ID.String())
		if v, ok := c.unconfirmedTimers.LoadAndDelete("unconf:" + d.ID.String()); ok {
			if t, ok := v.(*time.Timer); ok {
				t.Stop()
			}
		}
	}
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

	c.propagateFinalStatus(ctx, dispatch, dbsqlc.FinalStatusConfirmedCall)

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

	// SMS is co-primary and already fired during launchDispatch; the 30-min
	// unconfirmed timeout is the only escalation path now.
	slog.Info("cascade: call terminal",
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

	c.propagateFinalStatus(ctx, dispatch, dbsqlc.FinalStatusConfirmedSms)

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

	c.propagateFinalStatus(ctx, dispatch, dbsqlc.FinalStatusConfirmedApp)

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
