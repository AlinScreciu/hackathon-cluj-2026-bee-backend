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

// ApiaryRiskOutput represents the current risk assessment for an apiary.
type ApiaryRiskOutput struct {
	NearestSprayKm  *float64 `json:"nearest_spray_km"`
	NearestSprayEta *string  `json:"nearest_spray_eta"`
	ActiveAlerts    int      `json:"active_alerts"`
}

// ApiaryOutput is the typed response struct for a single apiary.
type ApiaryOutput struct {
	ID             string           `json:"id"`
	OwnerID        string           `json:"owner_id"`
	Name           string           `json:"name"`
	Type           string           `json:"type"`
	Lat            float64          `json:"lat"`
	Lng            float64          `json:"lng"`
	HiveCount      int32            `json:"hive_count"`
	StartDate      string           `json:"start_date"`
	EndDate        *string          `json:"end_date"`
	Notes          *string          `json:"notes"`
	Status         string           `json:"status"`
	CurrentRisk    ApiaryRiskOutput `json:"current_risk"`
	CreatedAt      string           `json:"created_at"`
	LastLedgerHash string           `json:"last_ledger_hash"`
}

// mapApiary converts a sqlc Apiary row to the typed ApiaryOutput response.
func mapApiary(row dbsqlc.Apiary) ApiaryOutput {
	var endDate *string
	if row.EndDate.Valid {
		s := row.EndDate.Time.Format("2006-01-02")
		endDate = &s
	}

	var notes *string
	if row.Notes.Valid {
		notes = &row.Notes.String
	}

	return ApiaryOutput{
		ID:             row.ID.String(),
		OwnerID:        row.OwnerID.String(),
		Name:           row.Name,
		Type:           string(row.Type),
		Lat:            row.Lat,
		Lng:            row.Lng,
		HiveCount:      row.HiveCount,
		StartDate:      row.StartDate.Format("2006-01-02"),
		EndDate:        endDate,
		Notes:          notes,
		Status:         "safe",
		CurrentRisk:    ApiaryRiskOutput{ActiveAlerts: 0},
		CreatedAt:      row.CreatedAt.Format(time.RFC3339),
		LastLedgerHash: "",
	}
}

func registerApiaries(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "list-apiaries",
		Method:      http.MethodGet,
		Path:        "/api/v1/apiaries",
		Summary:     "List my apiaries",
		Tags:        []string{"apiaries"},
	}, h.listApiaries)

	huma.Register(api, huma.Operation{
		OperationID: "create-apiary",
		Method:      http.MethodPost,
		Path:        "/api/v1/apiaries",
		Summary:     "Register a new apiary",
		Tags:        []string{"apiaries"},
	}, h.createApiary)

	huma.Register(api, huma.Operation{
		OperationID: "get-apiary",
		Method:      http.MethodGet,
		Path:        "/api/v1/apiaries/{id}",
		Summary:     "Get apiary by ID",
		Tags:        []string{"apiaries"},
	}, h.getApiary)

	huma.Register(api, huma.Operation{
		OperationID: "update-apiary",
		Method:      http.MethodPatch,
		Path:        "/api/v1/apiaries/{id}",
		Summary:     "Update apiary",
		Tags:        []string{"apiaries"},
	}, h.updateApiary)
}

func (h *Handlers) listApiaries(ctx context.Context, _ *struct{}) (*struct {
	Body struct {
		Items []ApiaryOutput `json:"items"`
	}
}, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}
	if user.Role != domain.RoleApicultor {
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis pentru rolul dumneavoastră")
	}

	ownerID, err := uuid.Parse(user.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)
	rows, err := q.ListApiariesByOwner(ctx, ownerID)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	items := make([]ApiaryOutput, len(rows))
	for i, r := range rows {
		items[i] = mapApiary(r)
	}

	out := &struct {
		Body struct {
			Items []ApiaryOutput `json:"items"`
		}
	}{}
	out.Body.Items = items
	return out, nil
}

func (h *Handlers) createApiary(_ context.Context, _ *struct{ Body any }) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) getApiary(ctx context.Context, input *struct {
	ID string `path:"id"`
}) (*struct {
	Body struct {
		Apiary ApiaryOutput `json:"apiary"`
	}
}, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}
	if user.Role != domain.RoleApicultor {
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis pentru rolul dumneavoastră")
	}

	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "ID invalid")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)
	row, err := q.GetApiary(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.NewError(http.StatusNotFound, "Stupina nu a fost găsită")
	}
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	if row.OwnerID.String() != user.ID {
		return nil, huma.NewError(http.StatusNotFound, "Stupina nu a fost găsită")
	}

	out := &struct {
		Body struct {
			Apiary ApiaryOutput `json:"apiary"`
		}
	}{}
	out.Body.Apiary = mapApiary(row)
	return out, nil
}

func (h *Handlers) updateApiary(_ context.Context, _ *struct {
	ID   string `path:"id"`
	Body any
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}
