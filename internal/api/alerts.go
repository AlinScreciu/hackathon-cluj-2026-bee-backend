package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/domain"
	"github.com/radarul-albinelor/api/internal/middleware"
)

// AlertView is the contract shape the FE renders for both list and detail
// alert views. It joins dispatch + spray + parcel + farmer + apiary + current
// wind into a single flat object so the FE never has to make follow-up calls.
type AlertView struct {
	AlertDispatchID  string        `json:"alert_dispatch_id"`
	SprayReportID    string        `json:"spray_report_id"`
	FarmerNameMasked string        `json:"farmer_name_masked"`
	ApiaryID         string        `json:"apiary_id"`
	ApiaryName       string        `json:"apiary_name"`
	Substance        string        `json:"substance"`
	Toxicity         string        `json:"toxicity"`
	ScheduledAt      time.Time     `json:"scheduled_at"`
	DistanceKm       float64       `json:"distance_km"`
	Downwind         bool          `json:"downwind"`
	SprayLat         float64       `json:"spray_lat"`
	SprayLng         float64       `json:"spray_lng"`
	WindDirectionDeg float64       `json:"wind_direction_deg"`
	Channels         ChannelStates `json:"channels"`
	FinalStatus      *string       `json:"final_status"`
	InAppAction      *string       `json:"in_app_action"`
	LedgerHash       string        `json:"ledger_hash"`
	CreatedAt        time.Time     `json:"created_at"`
}

// ListAlertsOutput is the typed response for GET /api/v1/alerts.
type ListAlertsOutput struct {
	Body struct {
		Items []AlertView `json:"items"`
	}
}

// GetAlertOutput is the typed response for GET /api/v1/alerts/{id}.
type GetAlertOutput struct {
	Body struct {
		Alert       AlertView           `json:"alert"`
		SprayReport SprayReportResponse `json:"spray_report"`
	}
}

// ConfirmAlertInput is the typed request for POST /api/v1/alerts/{id}/confirm.
type ConfirmAlertInput struct {
	ID   string `path:"id"`
	Body struct {
		Action string `json:"action" enum:"move_hives,seal_in_place"`
	}
}

// ConfirmAlertOutput is the typed response for POST /api/v1/alerts/{id}/confirm.
type ConfirmAlertOutput struct {
	Body struct {
		Alert      AlertView `json:"alert"`
		LedgerHash string    `json:"ledger_hash"`
	}
}

func registerAlerts(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "list-alerts",
		Method:      http.MethodGet,
		Path:        "/api/v1/alerts",
		Summary:     "List my alerts",
		Tags:        []string{"alerts"},
	}, h.listAlerts)

	huma.Register(api, huma.Operation{
		OperationID: "get-alert",
		Method:      http.MethodGet,
		Path:        "/api/v1/alerts/{id}",
		Summary:     "Get alert by ID",
		Tags:        []string{"alerts"},
	}, h.getAlert)

	huma.Register(api, huma.Operation{
		OperationID: "confirm-alert",
		Method:      http.MethodPost,
		Path:        "/api/v1/alerts/{id}/confirm",
		Summary:     "Confirm alert in-app",
		Tags:        []string{"alerts"},
	}, h.confirmAlert)
}

func (h *Handlers) listAlerts(ctx context.Context, input *struct {
	Status string `query:"status" enum:"active,all" required:"false"`
}) (*ListAlertsOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}
	if user.Role != domain.RoleApicultor {
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis pentru rolul dumneavoastră")
	}

	beekeeperID, err := uuid.Parse(user.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)

	var rows []dbsqlc.AlertDispatch
	if input.Status == "all" {
		rows, err = q.ListAllAlertsByBeekeeper(ctx, beekeeperID)
	} else {
		rows, err = q.ListActiveAlertsByBeekeeper(ctx, beekeeperID)
	}
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	views := make([]AlertView, len(rows))
	for i, r := range rows {
		v, _, err := h.buildAlertView(ctx, r)
		if err != nil {
			return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
		}
		views[i] = v
	}

	out := &ListAlertsOutput{}
	out.Body.Items = views
	return out, nil
}

func (h *Handlers) getAlert(ctx context.Context, input *struct {
	ID string `path:"id"`
}) (*GetAlertOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}

	dispatchID, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "ID invalid")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)

	dispatch, err := q.GetAlertDispatch(ctx, dispatchID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.NewError(http.StatusNotFound, "Alerta nu a fost găsită")
	}
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	// Inspectors may view any dispatch; beekeepers may only view their own.
	if user.Role != domain.RoleInspector && dispatch.BeekeeperID.String() != user.ID {
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis")
	}

	view, spray, err := h.buildAlertView(ctx, dispatch)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	parcel, err := q.GetParcel(ctx, spray.ParcelID)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	out := &GetAlertOutput{}
	out.Body.Alert = view
	out.Body.SprayReport = dbSprayToResponse(spray, parcel)
	return out, nil
}

func (h *Handlers) confirmAlert(ctx context.Context, input *ConfirmAlertInput) (*ConfirmAlertOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}

	dispatchID, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "ID invalid")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)

	dispatch, err := q.GetAlertDispatch(ctx, dispatchID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.NewError(http.StatusNotFound, "Alerta nu a fost găsită")
	}
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	if dispatch.BeekeeperID.String() != user.ID {
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis")
	}

	hash, err := h.cascade.HandleInAppConfirm(ctx, input.ID, input.Body.Action)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	// Re-fetch so the view reflects the just-written final_status / in_app_action.
	dispatch, err = q.GetAlertDispatch(ctx, dispatchID)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	view, _, err := h.buildAlertView(ctx, dispatch)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	out := &ConfirmAlertOutput{}
	out.Body.Alert = view
	out.Body.LedgerHash = hash
	return out, nil
}

// buildAlertView assembles the contract AlertView for a single dispatch by joining
// spray_report + parcel + farmer + apiary and fetching current wind. Returns the
// view plus the underlying spray row so the detail handler can also emit it as
// SprayReportResponse on the response body.
func (h *Handlers) buildAlertView(
	ctx context.Context,
	dispatch dbsqlc.AlertDispatch,
) (AlertView, dbsqlc.SprayReport, error) {
	q := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))

	spray, err := q.GetSprayReport(ctx, dispatch.SprayReportID)
	if err != nil {
		return AlertView{}, dbsqlc.SprayReport{}, err
	}
	parcel, err := q.GetParcel(ctx, spray.ParcelID)
	if err != nil {
		return AlertView{}, dbsqlc.SprayReport{}, err
	}
	farmer, err := q.GetUserByID(ctx, spray.FarmerID)
	if err != nil {
		return AlertView{}, dbsqlc.SprayReport{}, err
	}
	apiary, err := q.GetApiary(ctx, dispatch.ApiaryID)
	if err != nil {
		return AlertView{}, dbsqlc.SprayReport{}, err
	}

	// Weather is non-fatal — if it fails, surface 0.0 rather than break the view.
	windDeg := 0.0
	if w, werr := h.weatherClient.Get(ctx, parcel.Lat, parcel.Lng); werr == nil && w != nil {
		windDeg = w.WindDirectionDeg
	}

	view := AlertView{
		AlertDispatchID:  dispatch.ID.String(),
		SprayReportID:    dispatch.SprayReportID.String(),
		FarmerNameMasked: maskFarmerName(farmer.FullName),
		ApiaryID:         dispatch.ApiaryID.String(),
		ApiaryName:       apiary.Name,
		Substance:        spray.Substance,
		Toxicity:         spray.Toxicity,
		ScheduledAt:      spray.ScheduledAt,
		DistanceKm:       dispatch.DistanceM / 1000.0,
		Downwind:         dispatch.Downwind,
		SprayLat:         parcel.Lat,
		SprayLng:         parcel.Lng,
		WindDirectionDeg: windDeg,
		Channels:         channelsFromDispatch(dispatch),
		LedgerHash:       dispatch.LedgerHash,
		CreatedAt:        dispatch.CreatedAt,
	}
	if dispatch.FinalStatus.Valid {
		s := string(dispatch.FinalStatus.FinalStatus)
		view.FinalStatus = &s
	}
	if dispatch.InAppAction.Valid {
		s := string(dispatch.InAppAction.InAppAction)
		view.InAppAction = &s
	}
	return view, spray, nil
}

// channelsFromDispatch wraps the three flat state columns into the FE's nested shape.
func channelsFromDispatch(d dbsqlc.AlertDispatch) ChannelStates {
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
	return ChannelStates{
		Push: ChannelPushState{State: string(d.PushState), At: pushAt},
		Call: ChannelCallState{State: string(d.CallState), At: callAt, Attempts: int(d.CallAttempts)},
		Sms:  ChannelSmsState{State: string(d.SmsState), At: smsAt},
	}
}

// maskFarmerName turns "Maria Popescu" → "M. Popescu"; "Vasile Mureșan" → "V. Mureșan".
// Preserves UTF-8 (Romanian diacritics).
func maskFarmerName(fullName string) string {
	parts := strings.Fields(fullName)
	if len(parts) == 0 {
		return ""
	}
	initial := func(s string) string {
		r, _ := utf8.DecodeRuneInString(s)
		return string(unicode.ToUpper(r)) + "."
	}
	if len(parts) == 1 {
		return initial(parts[0])
	}
	return initial(parts[0]) + " " + parts[len(parts)-1]
}
