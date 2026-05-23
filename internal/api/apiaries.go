package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
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
		LastLedgerHash: row.LedgerHash,
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
		OperationID:   "create-apiary",
		Method:        http.MethodPost,
		Path:          "/api/v1/apiaries",
		Summary:       "Register a new apiary",
		Tags:          []string{"apiaries"},
		DefaultStatus: http.StatusCreated,
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

type CreateApiaryInput struct {
	Body struct {
		Name      string  `json:"name"`
		Type      string  `json:"type"`
		Lat       float64 `json:"lat"`
		Lng       float64 `json:"lng"`
		HiveCount int32   `json:"hive_count"`
		StartDate string  `json:"start_date"`
		EndDate   *string `json:"end_date,omitempty"`
		Notes     *string `json:"notes,omitempty"`
	}
}

type CreateApiaryOutput struct {
	Body struct {
		Apiary     ApiaryOutput `json:"apiary"`
		LedgerHash string       `json:"ledger_hash"`
	}
}

func (h *Handlers) createApiary(ctx context.Context, input *CreateApiaryInput) (*CreateApiaryOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}
	if user.Role != domain.RoleApicultor {
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis pentru rolul dumneavoastră")
	}

	ownerID, err := uuid.Parse(user.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă")
	}

	name := strings.TrimSpace(input.Body.Name)
	if name == "" {
		return nil, huma.NewError(http.StatusBadRequest, "Numele stupinei este obligatoriu")
	}
	if input.Body.Type != string(domain.ApiaryTypePermanent) && input.Body.Type != string(domain.ApiaryTypePastoral) {
		return nil, huma.NewError(http.StatusBadRequest, "Tipul stupinei trebuie să fie 'permanent' sau 'pastoral'")
	}
	if input.Body.Lat < -90 || input.Body.Lat > 90 {
		return nil, huma.NewError(http.StatusBadRequest, "Latitudine invalidă")
	}
	if input.Body.Lng < -180 || input.Body.Lng > 180 {
		return nil, huma.NewError(http.StatusBadRequest, "Longitudine invalidă")
	}
	if input.Body.HiveCount < 0 {
		return nil, huma.NewError(http.StatusBadRequest, "Numărul de stupi nu poate fi negativ")
	}

	startDate, err := time.Parse("2006-01-02", input.Body.StartDate)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "Data de început trebuie să fie în format YYYY-MM-DD")
	}

	var endDate sql.NullTime
	if input.Body.EndDate != nil && *input.Body.EndDate != "" {
		t, err := time.Parse("2006-01-02", *input.Body.EndDate)
		if err != nil {
			return nil, huma.NewError(http.StatusBadRequest, "Data de sfârșit trebuie să fie în format YYYY-MM-DD")
		}
		if !t.After(startDate) {
			return nil, huma.NewError(http.StatusBadRequest, "Data de sfârșit trebuie să fie după data de început")
		}
		endDate = sql.NullTime{Time: t, Valid: true}
	}
	if input.Body.Type == string(domain.ApiaryTypePastoral) && !endDate.Valid {
		return nil, huma.NewError(http.StatusBadRequest, "Stupinele pastorale necesită o dată de sfârșit")
	}

	var notes sql.NullString
	if input.Body.Notes != nil && *input.Body.Notes != "" {
		notes = sql.NullString{String: *input.Body.Notes, Valid: true}
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	defer tx.Rollback()

	q := dbsqlc.New(tx)

	apiaryID := uuid.New()
	created, err := q.CreateApiary(ctx, dbsqlc.CreateApiaryParams{
		ID:        apiaryID,
		OwnerID:   ownerID,
		Name:      name,
		Type:      dbsqlc.ApiaryType(input.Body.Type),
		Lat:       input.Body.Lat,
		Lng:       input.Body.Lng,
		HiveCount: input.Body.HiveCount,
		StartDate: startDate,
		EndDate:   endDate,
		Notes:     notes,
	})
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	actorID := user.ID
	ledgerHash, err := h.ledgerSvc.Append(ctx, tx, "apiary.registered", &actorID, map[string]any{
		"apiary_id":  apiaryID.String(),
		"owner_id":   ownerID.String(),
		"name":       name,
		"type":       input.Body.Type,
		"lat":        input.Body.Lat,
		"lng":        input.Body.Lng,
		"hive_count": input.Body.HiveCount,
		"start_date": input.Body.StartDate,
	})
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	if err := q.UpdateApiaryLedgerHash(ctx, dbsqlc.UpdateApiaryLedgerHashParams{
		ID:         apiaryID,
		LedgerHash: ledgerHash,
	}); err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	if err := tx.Commit(); err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	resp := mapApiary(created)
	resp.LastLedgerHash = ledgerHash

	out := &CreateApiaryOutput{}
	out.Body.Apiary = resp
	out.Body.LedgerHash = ledgerHash
	return out, nil
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

func (h *Handlers) updateApiary(ctx context.Context, input *struct {
	ID   string `path:"id"`
	Body struct {
		Name      *string `json:"name,omitempty"`
		HiveCount *int32  `json:"hive_count,omitempty"`
		Notes     *string `json:"notes,omitempty"`
		Type      *string `json:"type,omitempty"`
	}
}) (*struct {
	Body struct {
		Apiary     ApiaryOutput `json:"apiary"`
		LedgerHash string       `json:"ledger_hash"`
	}
}, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}
	if user.Role != domain.RoleApicultor {
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis")
	}

	apiaryID, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "ID invalid")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	defer tx.Rollback()

	q := dbsqlc.New(tx)

	current, err := q.GetApiary(ctx, apiaryID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.NewError(http.StatusNotFound, "Stupina nu a fost găsită")
	}
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	if current.OwnerID.String() != user.ID {
		return nil, huma.NewError(http.StatusForbidden, "Nu aveți acces la această stupină")
	}

	name := current.Name
	if input.Body.Name != nil {
		name = *input.Body.Name
	}
	hiveCount := current.HiveCount
	if input.Body.HiveCount != nil {
		hiveCount = *input.Body.HiveCount
	}
	notes := current.Notes
	if input.Body.Notes != nil {
		notes = sql.NullString{String: *input.Body.Notes, Valid: true}
	}
	apiaryType := current.Type
	if input.Body.Type != nil {
		apiaryType = dbsqlc.ApiaryType(*input.Body.Type)
	}

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
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	actorID := user.ID
	ledgerHash, err := h.ledgerSvc.Append(ctx, tx, "apiary.updated", &actorID, map[string]any{
		"apiary_id":  apiaryID.String(),
		"name":       name,
		"hive_count": hiveCount,
	})
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	if err := q.UpdateApiaryLedgerHash(ctx, dbsqlc.UpdateApiaryLedgerHashParams{
		ID:         apiaryID,
		LedgerHash: ledgerHash,
	}); err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	if err := tx.Commit(); err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	resp := mapApiary(updated)
	resp.LastLedgerHash = ledgerHash

	out := &struct {
		Body struct {
			Apiary     ApiaryOutput `json:"apiary"`
			LedgerHash string       `json:"ledger_hash"`
		}
	}{}
	out.Body.Apiary = resp
	out.Body.LedgerHash = ledgerHash
	return out, nil
}
