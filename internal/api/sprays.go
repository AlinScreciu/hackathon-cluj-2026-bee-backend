package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/domain"
	"github.com/radarul-albinelor/api/internal/external/geoai"
	"github.com/radarul-albinelor/api/internal/middleware"
	"github.com/radarul-albinelor/api/internal/services"
	"github.com/sqlc-dev/pqtype"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type CreateSprayInput struct {
	Body struct {
		ParcelID      string  `json:"parcel_id"`
		SurfaceHA     float64 `json:"surface_ha" minimum:"0.1"`
		DoseKgHa      float64 `json:"dose_kg_ha" minimum:"0.01"`
		Crop          string  `json:"crop" minLength:"1"`
		Substance     string  `json:"substance" minLength:"1"`
		ScheduledAt   string  `json:"scheduled_at"`
		DurationHours float64 `json:"duration_hours" minimum:"0.5"`
		Notes         *string `json:"notes,omitempty"`
	}
}

// ParcelEmbedded is the minimal parcel snapshot returned inline on every
// SprayReportResponse. The frontend uses {name, lat, lng} for list cards,
// map markers, and the primărie locality fallback.
type ParcelEmbedded struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
}

type SprayReportResponse struct {
	ID                    string         `json:"id"`
	FarmerID              string         `json:"farmer_id"`
	ParcelID              string         `json:"parcel_id"`
	Parcel                ParcelEmbedded `json:"parcel"`
	Crop                  string    `json:"crop"`
	Substance             string    `json:"substance"`
	Toxicity              string    `json:"toxicity"`
	SurfaceHA             float64   `json:"surface_ha"`
	DoseKgHa              float64   `json:"dose_kg_ha"`
	ScheduledAt           time.Time `json:"scheduled_at"`
	DurationHours         float64   `json:"duration_hours"`
	Notes                 *string   `json:"notes"`
	Status                string    `json:"status"`
	AffectedApiariesCount int       `json:"affected_apiaries_count"`
	LedgerHash            string    `json:"ledger_hash"`
	CreatedAt             time.Time `json:"created_at"`

	// AI assessment (populated after the AI service responds).
	AIRiskScore         *float64        `json:"ai_risk_score,omitempty"`
	AIRiskLevel         string          `json:"ai_risk_level,omitempty"`
	AIExplanationRO     string          `json:"ai_explanation_ro,omitempty"`
	AIRecommendedAction string          `json:"ai_recommended_action,omitempty"`
	AIWarnings          []string        `json:"ai_warnings,omitempty"`
	AIZones             json.RawMessage `json:"ai_zones,omitempty"`
}

// RiskPreviewInput is the body for POST /spray-reports/risk-preview. Same shape
// as CreateSprayInput so the frontend can reuse its form state.
type RiskPreviewInput struct {
	Body struct {
		ParcelID      string  `json:"parcel_id"`
		SurfaceHA     float64 `json:"surface_ha" minimum:"0.1"`
		DoseKgHa      float64 `json:"dose_kg_ha" minimum:"0.01"`
		Crop          string  `json:"crop" minLength:"1"`
		Substance     string  `json:"substance" minLength:"1"`
		ScheduledAt   string  `json:"scheduled_at"`
		DurationHours float64 `json:"duration_hours" minimum:"0.5"`
	}
}

type AffectedApiaryPublic struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	HiveCount int     `json:"hive_count"`
	DistanceM float64 `json:"distance_m"`
	// InZone is true when the apiary sits inside one of the AI's risk zone
	// polygons (and therefore should be notified). When false, the apiary is
	// "nearby" but safe due to wind direction / distance — the frontend can
	// render it muted so users see the AI made an informed decision.
	InZone bool `json:"in_zone"`
}

type RiskPreviewOutput struct {
	Body struct {
		RiskRadiusM       float64                `json:"risk_radius_m"`
		AffectedApiaries  int                    `json:"affected_apiaries"`
		NearbyApiaries    []AffectedApiaryPublic `json:"nearby_apiaries"`
		ParcelLat         float64                `json:"parcel_lat"`
		ParcelLng         float64                `json:"parcel_lng"`
		Toxicity          string                 `json:"toxicity"`
		Severity          string                 `json:"severity"`
		RiskScore         float64                `json:"risk_score"`
		WindDirDeg        float64                `json:"wind_direction_deg"`
		WindSpeedKmh      float64                `json:"wind_speed_kmh"`
		ExplanationRO     string                 `json:"explanation_ro"`
		RecommendedAction string                 `json:"recommended_action"`
		Warnings          []string               `json:"warnings"`
		Zones             json.RawMessage        `json:"zones,omitempty"`
	}
}

type CreateSprayOutput struct {
	Body struct {
		SprayReport      SprayReportResponse `json:"spray_report"`
		AffectedApiaries int                 `json:"affected_apiaries"`
		RiskRadiusM      float64             `json:"risk_radius_m"`
		LedgerHash       string              `json:"ledger_hash"`
	}
}

type ListSprayReportsOutput struct {
	Body struct {
		Items      []SprayReportResponse `json:"items"`
		NextCursor *string               `json:"next_cursor"`
	}
}

type GetSprayReportOutput struct {
	Body struct {
		SprayReport SprayReportResponse  `json:"spray_report"`
		Cascade     CascadeStatus        `json:"cascade"`
		History     []LedgerEventSummary `json:"history"`
	}
}

type GetCascadeStatusOutput struct {
	Body CascadeStatus `json:"body"`
}

type CancelSprayInput struct {
	ID string `path:"id"`
}

type CancelSprayOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

type CascadeStatus struct {
	SprayReportID string `json:"spray_report_id"`
	OverallStatus string `json:"overall_status"`
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

type ChannelPushState struct {
	State string     `json:"state"`
	At    *time.Time `json:"at"`
}

type ChannelCallState struct {
	State    string     `json:"state"`
	At       *time.Time `json:"at"`
	Attempts int        `json:"attempts"`
}

type ChannelSmsState struct {
	State string     `json:"state"`
	At    *time.Time `json:"at"`
}

type ChannelStates struct {
	Push ChannelPushState `json:"push"`
	Call ChannelCallState `json:"call"`
	Sms  ChannelSmsState  `json:"sms"`
}

type AlertDispatchPublic struct {
	AlertDispatchID   string        `json:"alert_dispatch_id"`
	BeekeeperInitials string        `json:"beekeeper_initials"`
	ApiaryName        string        `json:"apiary_name"`
	DistanceKm        float64       `json:"distance_km"`
	Downwind          bool          `json:"downwind"`
	Channels          ChannelStates `json:"channels"`
	FinalStatus       *string       `json:"final_status"`
	LedgerHash        string        `json:"ledger_hash"`
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func registerSprayReports(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "create-spray-report",
		Method:      http.MethodPost,
		Path:        "/api/v1/spray-reports",
		Summary:     "Create a spray report",
		Tags:        []string{"spray-reports"},
	}, h.createSprayReport)

	huma.Register(api, huma.Operation{
		OperationID: "risk-preview",
		Method:      http.MethodPost,
		Path:        "/api/v1/spray-reports/risk-preview",
		Summary:     "Preview AI risk assessment without persisting a spray report",
		Tags:        []string{"spray-reports"},
	}, h.riskPreview)

	huma.Register(api, huma.Operation{
		OperationID: "list-spray-reports",
		Method:      http.MethodGet,
		Path:        "/api/v1/spray-reports",
		Summary:     "List spray reports",
		Tags:        []string{"spray-reports"},
	}, h.listSprayReports)

	huma.Register(api, huma.Operation{
		OperationID: "get-spray-report",
		Method:      http.MethodGet,
		Path:        "/api/v1/spray-reports/{id}",
		Summary:     "Get spray report by ID",
		Tags:        []string{"spray-reports"},
	}, h.getSprayReport)

	huma.Register(api, huma.Operation{
		OperationID: "get-cascade-status",
		Method:      http.MethodGet,
		Path:        "/api/v1/spray-reports/{id}/cascade-status",
		Summary:     "Poll cascade status",
		Tags:        []string{"spray-reports"},
	}, h.getCascadeStatus)

	huma.Register(api, huma.Operation{
		OperationID: "cancel-spray-report",
		Method:      http.MethodPost,
		Path:        "/api/v1/spray-reports/{id}/cancel",
		Summary:     "Cancel spray report",
		Tags:        []string{"spray-reports"},
	}, h.cancelSprayReport)

	// anf-export is registered as a raw chi route in router.go because it
	// returns application/pdf rather than JSON.
}

// ---------------------------------------------------------------------------
// POST /spray-reports
// ---------------------------------------------------------------------------

func (h *Handlers) createSprayReport(ctx context.Context, input *CreateSprayInput) (*CreateSprayOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "unauthorized")
	}
	if user.Role != domain.RoleFermier {
		return nil, huma.NewError(http.StatusForbidden, "only farmers can create spray reports")
	}

	parcelID, err := uuid.Parse(input.Body.ParcelID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "invalid parcel_id")
	}
	scheduledAt, err := time.Parse(time.RFC3339, input.Body.ScheduledAt)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "invalid scheduled_at, use RFC3339")
	}
	farmerID, err := uuid.Parse(user.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusUnauthorized, "invalid user id")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)

	parcel, err := q.GetParcel(ctx, parcelID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.NewError(http.StatusNotFound, "parcel not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get parcel: %w", err)
	}
	if parcel.OwnerID != farmerID {
		return nil, huma.NewError(http.StatusForbidden, "not your parcel")
	}

	substance, err := q.GetSubstanceByLabel(ctx, input.Body.Substance)
	var toxicity string
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		toxicity = string(domain.ToxicityMed)
	} else {
		toxicity = substance.Toxicity
	}

	geoResult, err := h.geoAI.Assess(ctx, geoai.Request{
		Crop:     input.Body.Crop,
		ParcelID: parcelID.String(),
		Product: geoai.Product{
			CommercialName: input.Body.Substance,
			BeeToxicity:    toxicityToBeeTox(toxicity),
		},
		Dose: geoai.Dose{
			AmountPerHectareKg: input.Body.DoseKgHa,
			TotalAmountKg:      input.Body.DoseKgHa * input.Body.SurfaceHA,
		},
		AppliedAt:     input.Body.ScheduledAt,
		DurationHours: input.Body.DurationHours,
		AreaHa:        input.Body.SurfaceHA,
		Center: geoai.Center{
			Lat: parcel.Lat,
			Lon: parcel.Lng,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("geo assessment: %w", err)
	}

	allApiaries, err := q.ListAllApiaries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list apiaries: %w", err)
	}

	type affectedApiary struct {
		apiary    dbsqlc.Apiary
		distanceM float64
	}
	var affected []affectedApiary
	for _, a := range allApiaries {
		d := services.Haversine(parcel.Lat, parcel.Lng, a.Lat, a.Lng)
		if d <= geoResult.RiskRadiusM {
			affected = append(affected, affectedApiary{a, d})
		}
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

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
		DoseKgHa:      input.Body.DoseKgHa,
		ScheduledAt:   scheduledAt,
		DurationHours: input.Body.DurationHours,
		Notes:         sql.NullString{String: ptrStr(input.Body.Notes), Valid: input.Body.Notes != nil},
		LedgerHash:    "",
	})
	if err != nil {
		return nil, fmt.Errorf("create spray: %w", err)
	}

	// Persist the AI assessment alongside the spray report so it shows up on
	// detail pages and in audits without having to re-call the AI.
	if err := persistAIAssessment(ctx, qtx, sprayID, geoResult); err != nil {
		return nil, fmt.Errorf("persist ai assessment: %w", err)
	}
	applyAIToSprayRow(&spray, geoResult)

	actorID := user.ID
	sprayHash, err := h.ledgerSvc.Append(ctx, tx, "spray.created", &actorID, map[string]any{
		"spray_id":   sprayID.String(),
		"substance":  input.Body.Substance,
		"toxicity":   toxicity,
		"parcel_id":  parcelID.String(),
		"surface_ha": input.Body.SurfaceHA,
	})
	if err != nil {
		return nil, fmt.Errorf("ledger spray.created: %w", err)
	}

	if err := qtx.UpdateSprayReportLedgerHash(ctx, dbsqlc.UpdateSprayReportLedgerHashParams{
		ID:         sprayID,
		LedgerHash: sprayHash,
	}); err != nil {
		return nil, fmt.Errorf("update spray ledger hash: %w", err)
	}

	dispatches := make([]dbsqlc.AlertDispatch, 0, len(affected))
	for _, aff := range affected {
		callState := dbsqlc.CallStateQueued
		if toxicity == string(domain.ToxicityLow) {
			callState = dbsqlc.CallStateSkipped
		}

		dispatchID := uuid.New()
		d, err := qtx.CreateAlertDispatch(ctx, dbsqlc.CreateAlertDispatchParams{
			ID:            dispatchID,
			SprayReportID: sprayID,
			BeekeeperID:   aff.apiary.OwnerID,
			ApiaryID:      aff.apiary.ID,
			DistanceM:     aff.distanceM,
			Downwind:      false,
			CallState:     callState,
			SmsState:      dbsqlc.SmsStateQueued,
			LedgerHash:    "",
		})
		if err != nil {
			return nil, fmt.Errorf("create dispatch: %w", err)
		}

		dispatchHash, err := h.ledgerSvc.Append(ctx, tx, "alert.dispatched", &actorID, map[string]any{
			"dispatch_id": dispatchID.String(),
			"spray_id":    sprayID.String(),
			"apiary_id":   aff.apiary.ID.String(),
			"distance_m":  aff.distanceM,
			"toxicity":    toxicity,
		})
		if err != nil {
			return nil, fmt.Errorf("ledger alert.dispatched: %w", err)
		}

		if err := qtx.UpdateDispatchLedgerHash(ctx, dbsqlc.UpdateDispatchLedgerHashParams{
			ID:         dispatchID,
			LedgerHash: dispatchHash,
		}); err != nil {
			return nil, fmt.Errorf("update dispatch ledger hash: %w", err)
		}

		d.LedgerHash = dispatchHash
		dispatches = append(dispatches, d)
	}

	if err := qtx.UpdateSprayReportAffectedCount(ctx, dbsqlc.UpdateSprayReportAffectedCountParams{
		ID:                    sprayID,
		AffectedApiariesCount: int32(len(affected)),
	}); err != nil {
		return nil, fmt.Errorf("update affected count: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	h.cascade.Start(ctx, sprayID.String(), dispatches)

	spray.LedgerHash = sprayHash
	spray.AffectedApiariesCount = int32(len(affected))

	if h.pdfSvc != nil {
		bgCtx := context.WithoutCancel(ctx)
		capturedSprayID := sprayID
		capturedFarmerID := farmerID
		capturedParcelID := parcelID
		capturedSprayHash := sprayHash
		capturedSpray := spray
		affectedCount := len(affected)
		capturedActorID := user.ID
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("pdf goroutine panic", "recover", r, "spray_id", capturedSprayID)
				}
			}()

			pdfQ := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))

			fCtx, fCancel := context.WithTimeout(bgCtx, 10*time.Second)
			farmerRow, err := pdfQ.GetUserByID(fCtx, capturedFarmerID)
			fCancel()
			if err != nil {
				slog.Error("pdf goroutine: get farmer", "err", err, "spray_id", capturedSprayID)
				return
			}

			pCtx, pCancel := context.WithTimeout(bgCtx, 10*time.Second)
			parcelRow, err := pdfQ.GetParcel(pCtx, capturedParcelID)
			pCancel()
			if err != nil {
				slog.Error("pdf goroutine: get parcel", "err", err, "spray_id", capturedSprayID)
				return
			}

			domainFarmer := domain.User{
				ID:       farmerRow.ID.String(),
				FullName: farmerRow.FullName,
				CNP:      farmerRow.Cnp,
				Email:    farmerRow.Email,
				Phone:    farmerRow.Phone,
				County:   farmerRow.County,
				Locality: farmerRow.Locality,
			}
			domainParcel := domain.Parcel{
				ID:       parcelRow.ID.String(),
				Name:     parcelRow.Name,
				Lat:      parcelRow.Lat,
				Lng:      parcelRow.Lng,
				County:   parcelRow.County,
				Locality: parcelRow.Locality,
			}
			domainSpray := domain.SprayReport{
				ID:                    capturedSprayID.String(),
				Substance:             capturedSpray.Substance,
				Toxicity:              domain.Toxicity(capturedSpray.Toxicity),
				SurfaceHA:             capturedSpray.SurfaceHa,
				ScheduledAt:           capturedSpray.ScheduledAt,
				DurationHours:         capturedSpray.DurationHours,
				Status:                domain.SprayStatus(capturedSpray.Status),
				AffectedApiariesCount: affectedCount,
				LedgerHash:            capturedSprayHash,
			}

			pdfBytes, err := h.pdfSvc.GeneratePrimariePDF(domainSpray, domainFarmer, domainParcel, affectedCount, capturedSprayHash)
			if err != nil {
				slog.Error("pdf goroutine: generate", "err", err, "spray_id", capturedSprayID)
				return
			}

			pdfPath := filepath.Join("uploads", "pdfs", capturedSprayID.String()+"-primarie.pdf")
			if err := os.WriteFile(pdfPath, pdfBytes, 0644); err != nil {
				slog.Error("pdf goroutine: write file", "err", err, "spray_id", capturedSprayID)
				return
			}

			if _, err := h.ledgerSvc.Append(bgCtx, nil, "pdf.generated", &capturedActorID, map[string]any{
				"spray_id": capturedSprayID.String(),
				"path":     pdfPath,
			}); err != nil {
				slog.Error("pdf goroutine: ledger pdf.generated", "err", err)
			}

			if h.emailClient != nil && h.cfg.PrimarieEmail != "" {
				pdfURL := h.cfg.AppBaseURL + "/api/v1/spray-reports/" + capturedSprayID.String() + "/primarie-pdf"
				subject := fmt.Sprintf("Notificare tratament pesticid - %s", capturedSpray.ScheduledAt.Format("02.01.2006"))
				body := fmt.Sprintf(
					"A fost inregistrat un tratament pesticid.\n\nSubstanta: %s\nData: %s\nSuprafata: %.2f ha\n\nDescarcati documentul PDF:\n%s\n\nHash registru: %s",
					capturedSpray.Substance,
					capturedSpray.ScheduledAt.Format("02.01.2006 15:04"),
					capturedSpray.SurfaceHa,
					pdfURL,
					capturedSprayHash,
				)
				eCtx, eCancel := context.WithTimeout(bgCtx, 10*time.Second)
				if err := h.emailClient.Send(eCtx, h.cfg.PrimarieEmail, subject, body); err != nil {
					slog.Error("pdf goroutine: send email", "err", err)
				}
				eCancel()

				if _, err := h.ledgerSvc.Append(bgCtx, nil, "email.sent", &capturedActorID, map[string]any{
					"spray_id": capturedSprayID.String(),
					"to":       h.cfg.PrimarieEmail,
				}); err != nil {
					slog.Error("pdf goroutine: ledger email.sent", "err", err)
				}
			}
		}()
	}

	out := &CreateSprayOutput{}
	out.Body.SprayReport = dbSprayToResponse(spray, parcel)
	out.Body.AffectedApiaries = len(affected)
	out.Body.RiskRadiusM = geoResult.RiskRadiusM
	out.Body.LedgerHash = sprayHash
	return out, nil
}

// ---------------------------------------------------------------------------
// GET /spray-reports
// ---------------------------------------------------------------------------

func (h *Handlers) listSprayReports(ctx context.Context, _ *struct {
	Status string `query:"status" enum:"active,past" required:"false"`
	Limit  int    `query:"limit" required:"false"`
	Cursor string `query:"cursor" required:"false"`
}) (*ListSprayReportsOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "unauthorized")
	}

	q := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))
	var rows []dbsqlc.SprayReport
	var err error

	switch user.Role {
	case domain.RoleInspector:
		rows, err = q.ListAllSprayReports(ctx)
	case domain.RoleFermier:
		farmerID, parseErr := uuid.Parse(user.ID)
		if parseErr != nil {
			return nil, huma.NewError(http.StatusUnauthorized, "invalid user id")
		}
		rows, err = q.ListSprayReportsByFarmer(ctx, farmerID)
	default:
		return nil, huma.NewError(http.StatusForbidden, "forbidden for role")
	}
	if err != nil {
		return nil, fmt.Errorf("list spray reports: %w", err)
	}

	// Resolve parcels per unique ID so dbSprayToResponse can embed {name, lat, lng}.
	// Small dispatch counts in demo make N+1 fine; switch to ANY($1) batching if it grows.
	parcelByID := make(map[uuid.UUID]dbsqlc.Parcel)
	for _, r := range rows {
		if _, seen := parcelByID[r.ParcelID]; seen {
			continue
		}
		p, perr := q.GetParcel(ctx, r.ParcelID)
		if perr != nil {
			return nil, fmt.Errorf("get parcel for spray %s: %w", r.ID, perr)
		}
		parcelByID[r.ParcelID] = p
	}

	items := make([]SprayReportResponse, len(rows))
	for i, r := range rows {
		items[i] = dbSprayToResponse(r, parcelByID[r.ParcelID])
	}

	out := &ListSprayReportsOutput{}
	out.Body.Items = items
	out.Body.NextCursor = nil
	return out, nil
}

// ---------------------------------------------------------------------------
// GET /spray-reports/:id
// ---------------------------------------------------------------------------

func (h *Handlers) getSprayReport(ctx context.Context, input *struct {
	ID string `path:"id"`
}) (*GetSprayReportOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "unauthorized")
	}

	sprayID, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "invalid id")
	}

	q := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))
	spray, err := q.GetSprayReport(ctx, sprayID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.NewError(http.StatusNotFound, "not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get spray: %w", err)
	}

	if user.Role == domain.RoleFermier && spray.FarmerID.String() != user.ID {
		return nil, huma.NewError(http.StatusForbidden, "forbidden")
	}

	dispatches, err := q.ListDispatchesBySpray(ctx, sprayID)
	if err != nil {
		return nil, fmt.Errorf("list dispatches: %w", err)
	}

	apiaryNames, beekeeperInitials, err := h.resolveDispatchLookups(ctx, dispatches)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	parcel, err := q.GetParcel(ctx, spray.ParcelID)
	if err != nil {
		return nil, fmt.Errorf("get parcel: %w", err)
	}

	events, err := h.ledgerSvc.ListBySprayID(ctx, sprayID.String())
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	history := make([]LedgerEventSummary, len(events))
	for i, e := range events {
		history[i] = LedgerEventSummary{
			Hash:      e.Hash,
			Type:      e.Type,
			CreatedAt: e.CreatedAt,
		}
	}

	out := &GetSprayReportOutput{}
	out.Body.SprayReport = dbSprayToResponse(spray, parcel)
	out.Body.Cascade = buildCascadeStatus(input.ID, dispatches, apiaryNames, beekeeperInitials)
	out.Body.History = history
	return out, nil
}

// ---------------------------------------------------------------------------
// GET /spray-reports/:id/cascade-status
// ---------------------------------------------------------------------------

func (h *Handlers) getCascadeStatus(ctx context.Context, input *struct {
	ID string `path:"id"`
}) (*GetCascadeStatusOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "unauthorized")
	}

	sprayID, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "invalid id")
	}

	q := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))

	spray, err := q.GetSprayReport(ctx, sprayID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.NewError(http.StatusNotFound, "not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get spray: %w", err)
	}
	if user.Role == domain.RoleFermier && spray.FarmerID.String() != user.ID {
		return nil, huma.NewError(http.StatusForbidden, "forbidden")
	}

	dispatches, err := q.ListDispatchesBySpray(ctx, sprayID)
	if err != nil {
		return nil, fmt.Errorf("list dispatches: %w", err)
	}

	apiaryNames, beekeeperInitials, err := h.resolveDispatchLookups(ctx, dispatches)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	cs := buildCascadeStatus(input.ID, dispatches, apiaryNames, beekeeperInitials)
	out := &GetCascadeStatusOutput{}
	out.Body = cs
	return out, nil
}

// ---------------------------------------------------------------------------
// POST /spray-reports/:id/cancel
// ---------------------------------------------------------------------------

func (h *Handlers) cancelSprayReport(ctx context.Context, input *CancelSprayInput) (*CancelSprayOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "unauthorized")
	}

	sprayID, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "invalid id")
	}

	q := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))
	spray, err := q.GetSprayReport(ctx, sprayID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.NewError(http.StatusNotFound, "not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get spray: %w", err)
	}

	if spray.FarmerID.String() != user.ID && user.Role != domain.RoleInspector {
		return nil, huma.NewError(http.StatusForbidden, "forbidden")
	}

	if err := q.UpdateSprayReportStatus(ctx, dbsqlc.UpdateSprayReportStatusParams{
		ID:     sprayID,
		Status: dbsqlc.SprayStatusCancelled,
	}); err != nil {
		return nil, fmt.Errorf("cancel spray: %w", err)
	}

	actorID := user.ID
	_, _ = h.ledgerSvc.Append(ctx, nil, "spray.cancelled", &actorID, map[string]any{
		"spray_id": sprayID.String(),
	})

	out := &CancelSprayOutput{}
	out.Body.Message = "cancelled"
	return out, nil
}

// ---------------------------------------------------------------------------
// Stubs for Phase 9+
// ---------------------------------------------------------------------------

func (h *Handlers) getPrimariePDF(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := chi.URLParam(r, "id")
	pdfPath := filepath.Join("uploads", "pdfs", id+"-primarie.pdf")
	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`-primarie.pdf"`)
	http.ServeFile(w, r, pdfPath)
}

// rawANFExport handles POST /api/v1/spray-reports/anf-export.
// Returns application/pdf — written via raw chi (not Huma) so binary bytes
// flow without content-negotiation gymnastics.
// Body: {farmer_id?: string, from: "YYYY-MM-DD", to: "YYYY-MM-DD"}.
// Inspector role: farmer_id optional — omit to export every farmer in the
// window. Fermier role: farmer_id ignored — always force-scoped to the
// authenticated user so a fermier can only download their own register.
func (h *Handlers) rawANFExport(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != domain.RoleInspector && user.Role != domain.RoleFermier {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		FarmerID *string `json:"farmer_id,omitempty"`
		From     string  `json:"from"`
		To       string  `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	// Fermier: ignore any farmer_id in the body and force-scope to self.
	if user.Role == domain.RoleFermier {
		body.FarmerID = &user.ID
	}

	from, err := time.Parse("2006-01-02", body.From)
	if err != nil {
		http.Error(w, "invalid from date (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	to, err := time.Parse("2006-01-02", body.To)
	if err != nil {
		http.Error(w, "invalid to date (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	// End-of-day on "to".
	to = to.Add(24*time.Hour - time.Second)
	if to.Before(from) {
		http.Error(w, "to must be after from", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	q := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))

	var rows []dbsqlc.SprayReport
	var farmerForHeader dbsqlc.User
	if body.FarmerID != nil && *body.FarmerID != "" {
		farmerID, err := uuid.Parse(*body.FarmerID)
		if err != nil {
			http.Error(w, "invalid farmer_id", http.StatusBadRequest)
			return
		}
		farmer, err := q.GetUserByID(ctx, farmerID)
		if err != nil {
			http.Error(w, "farmer not found", http.StatusNotFound)
			return
		}
		farmerForHeader = farmer
		rows, err = q.ListSprayReportsByFarmer(ctx, farmerID)
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
	} else {
		rows, err = q.ListAllSprayReports(ctx)
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		farmerForHeader = dbsqlc.User{FullName: "Toți fermierii"}
	}

	filtered := make([]dbsqlc.SprayReport, 0, len(rows))
	for _, s := range rows {
		if s.ScheduledAt.Before(from) || s.ScheduledAt.After(to) {
			continue
		}
		filtered = append(filtered, s)
	}

	domainSprays := make([]domain.SprayReport, 0, len(filtered))
	lastHash := ""
	for _, s := range filtered {
		var notes *string
		if s.Notes.Valid {
			notes = &s.Notes.String
		}
		domainSprays = append(domainSprays, domain.SprayReport{
			ID:                    s.ID.String(),
			FarmerID:              s.FarmerID.String(),
			ParcelID:              s.ParcelID.String(),
			Crop:                  s.Crop,
			Substance:             s.Substance,
			Toxicity:              domain.Toxicity(s.Toxicity),
			SurfaceHA:             s.SurfaceHa,
			ScheduledAt:           s.ScheduledAt,
			DurationHours:         s.DurationHours,
			Notes:                 notes,
			Status:                domain.SprayStatus(s.Status),
			AffectedApiariesCount: int(s.AffectedApiariesCount),
			LedgerHash:            s.LedgerHash,
			CreatedAt:             s.CreatedAt,
		})
		if s.LedgerHash != "" {
			lastHash = s.LedgerHash
		}
	}

	domainFarmer := domain.User{
		ID:       farmerForHeader.ID.String(),
		CNP:      farmerForHeader.Cnp,
		FullName: farmerForHeader.FullName,
		Email:    farmerForHeader.Email,
		Phone:    farmerForHeader.Phone,
		Role:     domain.Role(farmerForHeader.Role),
		County:   farmerForHeader.County,
		Locality: farmerForHeader.Locality,
	}

	if h.pdfSvc == nil {
		http.Error(w, "pdf service unavailable", http.StatusServiceUnavailable)
		return
	}
	pdfBytes, err := h.pdfSvc.GenerateANFExport(domainSprays, domainFarmer, lastHash)
	if err != nil {
		http.Error(w, "pdf generation failed", http.StatusInternalServerError)
		return
	}

	filename := "anf-export-" + body.From + "_" + body.To + ".pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(pdfBytes)))
	_, _ = w.Write(pdfBytes)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func dbSprayToResponse(s dbsqlc.SprayReport, p dbsqlc.Parcel) SprayReportResponse {
	var notes *string
	if s.Notes.Valid {
		notes = &s.Notes.String
	}
	resp := SprayReportResponse{
		ID:                    s.ID.String(),
		FarmerID:              s.FarmerID.String(),
		ParcelID:              s.ParcelID.String(),
		Parcel:                ParcelEmbedded{Name: p.Name, Lat: p.Lat, Lng: p.Lng},
		Crop:                  s.Crop,
		Substance:             s.Substance,
		Toxicity:              s.Toxicity,
		SurfaceHA:             s.SurfaceHa,
		DoseKgHa:              s.DoseKgHa,
		ScheduledAt:           s.ScheduledAt,
		DurationHours:         s.DurationHours,
		Notes:                 notes,
		Status:                string(s.Status),
		AffectedApiariesCount: int(s.AffectedApiariesCount),
		LedgerHash:            s.LedgerHash,
		CreatedAt:             s.CreatedAt,
	}
	if s.AiRiskScore.Valid {
		v := s.AiRiskScore.Float64
		resp.AIRiskScore = &v
	}
	if s.AiRiskLevel.Valid {
		resp.AIRiskLevel = s.AiRiskLevel.String
	}
	if s.AiExplanationRo.Valid {
		resp.AIExplanationRO = s.AiExplanationRo.String
	}
	if s.AiRecommendedAction.Valid {
		resp.AIRecommendedAction = s.AiRecommendedAction.String
	}
	if s.AiWarnings.Valid {
		_ = json.Unmarshal(s.AiWarnings.RawMessage, &resp.AIWarnings)
	}
	if s.AiZones.Valid {
		resp.AIZones = json.RawMessage(s.AiZones.RawMessage)
	}
	return resp
}

// persistAIAssessment writes the AI fields onto the just-created spray row.
// Run inside the same transaction as the spray insert so a failure rolls back.
func persistAIAssessment(ctx context.Context, q *dbsqlc.Queries, sprayID uuid.UUID, r *geoai.Result) error {
	if r == nil {
		return nil
	}
	warningsJSON, _ := json.Marshal(r.Warnings)
	if r.Warnings == nil {
		warningsJSON, _ = json.Marshal([]string{})
	}
	zones := r.Zones
	return q.UpdateSprayAIAssessment(ctx, dbsqlc.UpdateSprayAIAssessmentParams{
		ID:                  sprayID,
		AiRiskScore:         sql.NullFloat64{Float64: r.RiskScore, Valid: true},
		AiRiskLevel:         sql.NullString{String: r.Severity, Valid: r.Severity != ""},
		AiExplanationRo:     sql.NullString{String: r.ExplanationRO, Valid: r.ExplanationRO != ""},
		AiRecommendedAction: sql.NullString{String: r.RecommendedAction, Valid: r.RecommendedAction != ""},
		AiWarnings:          pqtype.NullRawMessage{RawMessage: warningsJSON, Valid: true},
		AiZones:             pqtype.NullRawMessage{RawMessage: zones, Valid: len(zones) > 0},
	})
}

// buildNotificationZones overrides the AI's symmetric ellipses with a simpler,
// product-aligned model: ALWAYS notify within 7 km (the A1 baseline circle),
// plus a downwind cone extending out to the AI's risk_radius. This matches
// user mental model — "wind only carries the cloud downstream" — and avoids
// alerting upwind beekeepers who are physically out of reach.
//
// Returns a GeoJSON FeatureCollection with two Polygon features:
//   - A1: 7 km circle around the parcel
//   - AW: downwind pie sector from the parcel out to risk_radius_m,
//         half-angle 50° centered on (wind_dir + 180)°
//
// The frontend renders this directly via its existing GeoJSON path, and the
// point-in-polygon affected-apiary filter uses the same geometry — so visual
// and logic are guaranteed to agree.
func buildNotificationZones(centerLat, centerLng, windDirDeg, riskRadiusM float64) json.RawMessage {
	const baselineM = 7000.0
	a1 := circlePolygonGeo(centerLat, centerLng, baselineM, 64)

	type feature struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
		Geometry   map[string]any `json:"geometry"`
	}
	features := []feature{{
		Type:       "Feature",
		Properties: map[string]any{"zone": "A1", "radius_m": baselineM, "label": "Baseline 7 km"},
		Geometry: map[string]any{
			"type":        "Polygon",
			"coordinates": [][][2]float64{a1},
		},
	}}

	// Only add the cone if wind extends the radius meaningfully past baseline.
	if riskRadiusM > baselineM+200 {
		driftBearing := math.Mod(windDirDeg+180, 360)
		cone := conePolygon(centerLat, centerLng, driftBearing, riskRadiusM, 50, 32)
		features = append(features, feature{
			Type: "Feature",
			Properties: map[string]any{
				"zone":     "AW",
				"radius_m": riskRadiusM,
				"label":    "Extensie vânt",
			},
			Geometry: map[string]any{
				"type":        "Polygon",
				"coordinates": [][][2]float64{cone},
			},
		})
	}

	fc := map[string]any{"type": "FeatureCollection", "features": features}
	b, _ := json.Marshal(fc)
	return b
}

// circlePolygonGeo returns a closed [lng, lat] ring approximating a circle.
func circlePolygonGeo(lat, lng, radiusM float64, segments int) [][2]float64 {
	const earthR = 6371000.0
	d := radiusM / earthR
	lat1 := lat * math.Pi / 180
	lng1 := lng * math.Pi / 180
	ring := make([][2]float64, 0, segments+1)
	for i := 0; i <= segments; i++ {
		brng := float64(i) / float64(segments) * 2 * math.Pi
		lat2 := math.Asin(math.Sin(lat1)*math.Cos(d) + math.Cos(lat1)*math.Sin(d)*math.Cos(brng))
		lng2 := lng1 + math.Atan2(
			math.Sin(brng)*math.Sin(d)*math.Cos(lat1),
			math.Cos(d)-math.Sin(lat1)*math.Sin(lat2),
		)
		ring = append(ring, [2]float64{lng2 * 180 / math.Pi, lat2 * 180 / math.Pi})
	}
	return ring
}

// conePolygon returns a pie-sector polygon: parcel center → arc at radiusM
// from (bearing - halfAngle) to (bearing + halfAngle) → back to center.
// bearingDeg is the direction the cone points (e.g. wind drift direction).
func conePolygon(lat, lng, bearingDeg, radiusM, halfAngleDeg float64, arcSteps int) [][2]float64 {
	const earthR = 6371000.0
	d := radiusM / earthR
	lat1 := lat * math.Pi / 180
	lng1 := lng * math.Pi / 180

	ring := make([][2]float64, 0, arcSteps+3)
	ring = append(ring, [2]float64{lng, lat}) // start at center

	for i := 0; i <= arcSteps; i++ {
		angleDeg := bearingDeg - halfAngleDeg + (2*halfAngleDeg)*float64(i)/float64(arcSteps)
		brng := angleDeg * math.Pi / 180
		lat2 := math.Asin(math.Sin(lat1)*math.Cos(d) + math.Cos(lat1)*math.Sin(d)*math.Cos(brng))
		lng2 := lng1 + math.Atan2(
			math.Sin(brng)*math.Sin(d)*math.Cos(lat1),
			math.Cos(d)-math.Sin(lat1)*math.Sin(lat2),
		)
		ring = append(ring, [2]float64{lng2 * 180 / math.Pi, lat2 * 180 / math.Pi})
	}
	ring = append(ring, [2]float64{lng, lat}) // close back to center
	return ring
}

// parseZonePolygons pulls the outer rings out of a GeoJSON FeatureCollection
// into slices of [lng, lat] pairs ready for ray-casting. We ignore holes
// (zones don't use them) and skip non-Polygon geometries.
func parseZonePolygons(zones json.RawMessage) [][][2]float64 {
	if len(zones) == 0 {
		return nil
	}
	var fc struct {
		Features []struct {
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(zones, &fc); err != nil {
		return nil
	}
	out := make([][][2]float64, 0, len(fc.Features))
	for _, f := range fc.Features {
		if f.Geometry.Type != "Polygon" {
			continue
		}
		// Polygon coordinates: [[[lng,lat], ...], ...]  (outer ring first)
		var rings [][][2]float64
		if err := json.Unmarshal(f.Geometry.Coordinates, &rings); err != nil {
			continue
		}
		if len(rings) == 0 {
			continue
		}
		out = append(out, rings[0])
	}
	return out
}

// pointInPolygon: standard ray-casting test in lng/lat space. Polygons here
// are at most a few tens of km wide, so treating lng/lat as planar is fine
// for the in/out decision — no projection needed.
func pointInPolygon(x, y float64, poly [][2]float64) bool {
	inside := false
	n := len(poly)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		xi, yi := poly[i][0], poly[i][1]
		xj, yj := poly[j][0], poly[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			inside = !inside
		}
	}
	return inside
}

// applyAIToSprayRow mutates the in-memory dbsqlc.SprayReport to carry the AI
// fields we just persisted, so the response mapper doesn't need to re-read
// the row from the database.
func applyAIToSprayRow(s *dbsqlc.SprayReport, r *geoai.Result) {
	if s == nil || r == nil {
		return
	}
	s.AiRiskScore = sql.NullFloat64{Float64: r.RiskScore, Valid: true}
	s.AiRiskLevel = sql.NullString{String: r.Severity, Valid: r.Severity != ""}
	s.AiExplanationRo = sql.NullString{String: r.ExplanationRO, Valid: r.ExplanationRO != ""}
	s.AiRecommendedAction = sql.NullString{String: r.RecommendedAction, Valid: r.RecommendedAction != ""}
	warningsJSON, _ := json.Marshal(r.Warnings)
	if r.Warnings == nil {
		warningsJSON, _ = json.Marshal([]string{})
	}
	s.AiWarnings = pqtype.NullRawMessage{RawMessage: warningsJSON, Valid: true}
	s.AiZones = pqtype.NullRawMessage{RawMessage: r.Zones, Valid: len(r.Zones) > 0}
}

// ---------------------------------------------------------------------------
// POST /spray-reports/risk-preview
// ---------------------------------------------------------------------------
//
// Runs the same AI risk assessment the create endpoint would run, but does
// not persist anything. Used by the new-spray form to give the farmer a
// preview of the affected radius, zones, and AI explanation before they hit
// submit.

func (h *Handlers) riskPreview(ctx context.Context, input *RiskPreviewInput) (*RiskPreviewOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "unauthorized")
	}
	if user.Role != domain.RoleFermier {
		return nil, huma.NewError(http.StatusForbidden, "only farmers can preview risk")
	}

	parcelID, err := uuid.Parse(input.Body.ParcelID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "invalid parcel_id")
	}
	farmerID, err := uuid.Parse(user.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusUnauthorized, "invalid user id")
	}

	q := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))
	parcel, err := q.GetParcel(ctx, parcelID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.NewError(http.StatusNotFound, "parcel not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get parcel: %w", err)
	}
	if parcel.OwnerID != farmerID {
		return nil, huma.NewError(http.StatusForbidden, "not your parcel")
	}

	substance, err := q.GetSubstanceByLabel(ctx, input.Body.Substance)
	toxicity := string(domain.ToxicityMed)
	if err == nil {
		toxicity = substance.Toxicity
	}

	scheduledAt := input.Body.ScheduledAt
	if scheduledAt == "" {
		scheduledAt = time.Now().UTC().Format(time.RFC3339)
	}

	result, err := h.geoAI.Assess(ctx, geoai.Request{
		Crop:     input.Body.Crop,
		ParcelID: parcelID.String(),
		Product: geoai.Product{
			CommercialName: input.Body.Substance,
			BeeToxicity:    toxicityToBeeTox(toxicity),
		},
		Dose: geoai.Dose{
			AmountPerHectareKg: input.Body.DoseKgHa,
			TotalAmountKg:      input.Body.DoseKgHa * input.Body.SurfaceHA,
		},
		AppliedAt:     scheduledAt,
		DurationHours: input.Body.DurationHours,
		AreaHa:        input.Body.SurfaceHA,
		Center: geoai.Center{
			Lat: parcel.Lat,
			Lon: parcel.Lng,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("geo assessment: %w", err)
	}

	// Find apiaries inside the AI's risk zones. The zones encode wind
	// direction (A2–A4 are ellipses stretched downwind), so point-in-polygon
	// gives us the semantically correct answer: upwind hives outside A1 are
	// NOT affected even if they're within the notify_radius cutoff. Falls
	// back to plain Haversine if the AI returned no zones (defensive).
	allApiaries, err := q.ListAllApiaries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list apiaries: %w", err)
	}
	// Override the AI's symmetric zones with our simpler product model:
	// always-notify 7 km baseline + downwind cone for the AI's risk extension.
	// Aligns notification logic with the user's mental model and ensures
	// visual = logic on the frontend.
	result.Zones = buildNotificationZones(parcel.Lat, parcel.Lng, result.WindDirDeg, result.RiskRadiusM)

	// Build the "nearby" set — every apiary within a generous search radius
	// (1.5× the AI's risk radius, min 15km) so the map can show context.
	// For each, point-in-polygon against our zones decides `in_zone`.
	// Frontend renders in-zone apiaries bright + counts them as "affected";
	// out-of-zone nearby apiaries render muted so users see the wind-aware
	// decision instead of an empty map.
	zonePolys := parseZonePolygons(result.Zones)
	searchRadius := result.RiskRadiusM * 1.5
	if searchRadius < 15000 {
		searchRadius = 15000
	}
	nearby := make([]AffectedApiaryPublic, 0, 8)
	affectedCount := 0
	for _, a := range allApiaries {
		d := services.Haversine(parcel.Lat, parcel.Lng, a.Lat, a.Lng)
		if d > searchRadius {
			continue
		}
		inZone := false
		if len(zonePolys) > 0 {
			for _, poly := range zonePolys {
				if pointInPolygon(a.Lng, a.Lat, poly) {
					inZone = true
					break
				}
			}
		} else {
			inZone = d <= result.RiskRadiusM
		}
		if inZone {
			affectedCount++
		}
		nearby = append(nearby, AffectedApiaryPublic{
			ID:        a.ID.String(),
			Name:      a.Name,
			Lat:       a.Lat,
			Lng:       a.Lng,
			HiveCount: int(a.HiveCount),
			DistanceM: d,
			InZone:    inZone,
		})
	}

	out := &RiskPreviewOutput{}
	out.Body.RiskRadiusM = result.RiskRadiusM
	out.Body.AffectedApiaries = affectedCount
	out.Body.NearbyApiaries = nearby
	out.Body.ParcelLat = parcel.Lat
	out.Body.ParcelLng = parcel.Lng
	out.Body.Toxicity = toxicity
	out.Body.Severity = result.Severity
	out.Body.RiskScore = result.RiskScore
	out.Body.WindDirDeg = result.WindDirDeg
	out.Body.WindSpeedKmh = result.WindSpeedKmh
	out.Body.ExplanationRO = result.ExplanationRO
	out.Body.RecommendedAction = result.RecommendedAction
	out.Body.Warnings = result.Warnings
	out.Body.Zones = result.Zones
	return out, nil
}

func buildCascadeStatus(
	sprayID string,
	dispatches []dbsqlc.AlertDispatch,
	apiaryNames map[uuid.UUID]string,
	beekeeperInitials map[uuid.UUID]string,
) CascadeStatus {
	cs := CascadeStatus{
		SprayReportID: sprayID,
		PolledAt:      time.Now().UTC(),
	}
	cs.Summary.Total = len(dispatches)

	for _, d := range dispatches {
		var pushAt, callAt, smsAt *time.Time
		if d.PushAt.Valid {
			t := d.PushAt.Time
			pushAt = &t
		}
		if d.CallAt.Valid {
			t := d.CallAt.Time
			callAt = &t
		}
		if d.SmsAt.Valid {
			t := d.SmsAt.Time
			smsAt = &t
		}

		pub := AlertDispatchPublic{
			AlertDispatchID:   d.ID.String(),
			BeekeeperInitials: beekeeperInitials[d.BeekeeperID],
			ApiaryName:        apiaryNames[d.ApiaryID],
			DistanceKm:        d.DistanceM / 1000.0,
			Downwind:          d.Downwind,
			Channels: ChannelStates{
				Push: ChannelPushState{State: string(d.PushState), At: pushAt},
				Call: ChannelCallState{State: string(d.CallState), At: callAt, Attempts: int(d.CallAttempts)},
				Sms:  ChannelSmsState{State: string(d.SmsState), At: smsAt},
			},
			LedgerHash: d.LedgerHash,
		}
		if d.FinalStatus.Valid {
			s := string(d.FinalStatus.FinalStatus)
			pub.FinalStatus = &s
		}
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
	if cs.Summary.Total == 0 || resolved == cs.Summary.Total {
		cs.OverallStatus = "complete"
	} else {
		cs.OverallStatus = "in_progress"
	}
	return cs
}

func (h *Handlers) resolveDispatchLookups(
	ctx context.Context,
	dispatches []dbsqlc.AlertDispatch,
) (map[uuid.UUID]string, map[uuid.UUID]string, error) {
	q := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))

	apiaryIDs := map[uuid.UUID]struct{}{}
	beekeeperIDs := map[uuid.UUID]struct{}{}
	for _, d := range dispatches {
		apiaryIDs[d.ApiaryID] = struct{}{}
		beekeeperIDs[d.BeekeeperID] = struct{}{}
	}

	apiaryNames := make(map[uuid.UUID]string, len(apiaryIDs))
	for id := range apiaryIDs {
		a, err := q.GetApiary(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		apiaryNames[id] = a.Name
	}

	initials := make(map[uuid.UUID]string, len(beekeeperIDs))
	for id := range beekeeperIDs {
		u, err := q.GetUserByID(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		initials[id] = initialsOf(u.FullName)
	}

	return apiaryNames, initials, nil
}

func initialsOf(fullName string) string {
	parts := strings.Fields(fullName)
	var b strings.Builder
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		r, _ := utf8.DecodeRuneInString(p)
		b.WriteRune(unicode.ToUpper(r))
		b.WriteByte('.')
	}
	return b.String()
}

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func toxicityToBeeTox(toxicity string) string {
	switch toxicity {
	case string(domain.ToxicityLow):
		return "low"
	case string(domain.ToxicityHigh):
		return "very_high"
	default:
		return "medium"
	}
}
