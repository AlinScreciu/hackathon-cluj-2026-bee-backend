package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/domain"
	"github.com/radarul-albinelor/api/internal/middleware"
)

// AlertDispatchResponse is the typed API response for a single alert dispatch.
type AlertDispatchResponse struct {
	ID          string    `json:"id"`
	SprayID     string    `json:"spray_report_id"`
	ApiaryID    string    `json:"apiary_id"`
	DistanceM   float64   `json:"distance_m"`
	Downwind    bool      `json:"downwind"`
	PushState   string    `json:"push_state"`
	CallState   string    `json:"call_state"`
	SmsState    string    `json:"sms_state"`
	FinalStatus *string   `json:"final_status"`
	LedgerHash  string    `json:"ledger_hash"`
	CreatedAt   time.Time `json:"created_at"`
}

// mapAlertDispatch converts a sqlc AlertDispatch row to the typed response struct.
func mapAlertDispatch(d dbsqlc.AlertDispatch) AlertDispatchResponse {
	var finalStatus *string
	if d.FinalStatus.Valid {
		s := string(d.FinalStatus.FinalStatus)
		finalStatus = &s
	}
	return AlertDispatchResponse{
		ID:          d.ID.String(),
		SprayID:     d.SprayReportID.String(),
		ApiaryID:    d.ApiaryID.String(),
		DistanceM:   d.DistanceM,
		Downwind:    d.Downwind,
		PushState:   string(d.PushState),
		CallState:   string(d.CallState),
		SmsState:    string(d.SmsState),
		FinalStatus: finalStatus,
		LedgerHash:  d.LedgerHash,
		CreatedAt:   d.CreatedAt,
	}
}

// ListAlertsOutput is the typed response for GET /api/v1/alerts.
type ListAlertsOutput struct {
	Body struct {
		Alerts []AlertDispatchResponse `json:"alerts"`
	}
}

// GetAlertOutput is the typed response for GET /api/v1/alerts/{id}.
type GetAlertOutput struct {
	Body struct {
		Alert AlertDispatchResponse `json:"alert"`
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
		LedgerHash string `json:"ledger_hash"`
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

	alerts := make([]AlertDispatchResponse, len(rows))
	for i, r := range rows {
		alerts[i] = mapAlertDispatch(r)
	}

	out := &ListAlertsOutput{}
	out.Body.Alerts = alerts
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

	out := &GetAlertOutput{}
	out.Body.Alert = mapAlertDispatch(dispatch)
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

	out := &ConfirmAlertOutput{}
	out.Body.LedgerHash = hash
	return out, nil
}
