package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/domain"
	"github.com/radarul-albinelor/api/internal/external/geoai"
	"github.com/radarul-albinelor/api/internal/middleware"
	"github.com/radarul-albinelor/api/internal/services"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type CreateSprayInput struct {
	Body struct {
		ParcelID      string  `json:"parcel_id"`
		SurfaceHA     float64 `json:"surface_ha" minimum:"0.1"`
		Crop          string  `json:"crop" minLength:"1"`
		Substance     string  `json:"substance" minLength:"1"`
		ScheduledAt   string  `json:"scheduled_at"`
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
		SprayReport      SprayReportResponse `json:"spray_report"`
		AffectedApiaries int                 `json:"affected_apiaries"`
		RiskRadiusM      float64             `json:"risk_radius_m"`
		LedgerHash       string              `json:"ledger_hash"`
	}
}

type ListSprayReportsOutput struct {
	Body struct {
		SprayReports []SprayReportResponse `json:"spray_reports"`
	}
}

type GetSprayReportOutput struct {
	Body struct {
		SprayReport   SprayReportResponse `json:"spray_report"`
		CascadeStatus CascadeStatus       `json:"cascade_status"`
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
			AmountPerHectareKg: 1.0,
			TotalAmountKg:      input.Body.SurfaceHA,
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
		ScheduledAt:   scheduledAt,
		DurationHours: input.Body.DurationHours,
		Notes:         sql.NullString{String: ptrStr(input.Body.Notes), Valid: input.Body.Notes != nil},
		LedgerHash:    "",
	})
	if err != nil {
		return nil, fmt.Errorf("create spray: %w", err)
	}

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
	out.Body.SprayReport = dbSprayToResponse(spray)
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

	items := make([]SprayReportResponse, len(rows))
	for i, r := range rows {
		items[i] = dbSprayToResponse(r)
	}

	out := &ListSprayReportsOutput{}
	out.Body.SprayReports = items
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

	out := &GetSprayReportOutput{}
	out.Body.SprayReport = dbSprayToResponse(spray)
	out.Body.CascadeStatus = buildCascadeStatus(input.ID, dispatches)
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

	cs := buildCascadeStatus(input.ID, dispatches)
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
// When farmer_id is omitted the export covers every farmer in the window.
func (h *Handlers) rawANFExport(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != domain.RoleInspector {
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

func dbSprayToResponse(s dbsqlc.SprayReport) SprayReportResponse {
	var notes *string
	if s.Notes.Valid {
		notes = &s.Notes.String
	}
	return SprayReportResponse{
		ID:                    s.ID.String(),
		FarmerID:              s.FarmerID.String(),
		ParcelID:              s.ParcelID.String(),
		Crop:                  s.Crop,
		Substance:             s.Substance,
		Toxicity:              s.Toxicity,
		SurfaceHA:             s.SurfaceHa,
		ScheduledAt:           s.ScheduledAt,
		DurationHours:         s.DurationHours,
		Notes:                 notes,
		Status:                string(s.Status),
		AffectedApiariesCount: int(s.AffectedApiariesCount),
		LedgerHash:            s.LedgerHash,
		CreatedAt:             s.CreatedAt,
	}
}

func buildCascadeStatus(sprayID string, dispatches []dbsqlc.AlertDispatch) CascadeStatus {
	cs := CascadeStatus{
		SprayReportID: sprayID,
		PolledAt:      time.Now().UTC(),
	}
	cs.Summary.Total = len(dispatches)

	for _, d := range dispatches {
		pub := AlertDispatchPublic{
			ID:        d.ID.String(),
			ApiaryID:  d.ApiaryID.String(),
			DistanceM: d.DistanceM,
			PushState: string(d.PushState),
			CallState: string(d.CallState),
			SmsState:  string(d.SmsState),
		}
		if d.FinalStatus.Valid {
			s := string(d.FinalStatus.FinalStatus)
			pub.FinalStatus = &s
		}
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
	if cs.Summary.Total == 0 || resolved == cs.Summary.Total {
		cs.OverallStatus = "complete"
	} else {
		cs.OverallStatus = "in_progress"
	}
	return cs
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
