package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"

	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/domain"
	"github.com/radarul-albinelor/api/internal/middleware"
)

// ParcelOutput is the typed response struct for parcel data.
type ParcelOutput struct {
	ID              string  `json:"id"`
	OwnerID         string  `json:"owner_id"`
	Name            string  `json:"name"`
	CadastralNumber string  `json:"cadastral_number"`
	Lat             float64 `json:"lat"`
	Lng             float64 `json:"lng"`
	SurfaceHa       float64 `json:"surface_ha"`
	DefaultCrop     *string `json:"default_crop"`
	County          string  `json:"county"`
	Locality        string  `json:"locality"`
}

// mapParcel converts a sqlc Parcel row to a ParcelOutput.
func mapParcel(row dbsqlc.Parcel) ParcelOutput {
	var defaultCrop *string
	if row.DefaultCrop.Valid {
		s := row.DefaultCrop.String
		defaultCrop = &s
	}
	return ParcelOutput{
		ID:              row.ID.String(),
		OwnerID:         row.OwnerID.String(),
		Name:            row.Name,
		CadastralNumber: row.CadastralNumber,
		Lat:             row.Lat,
		Lng:             row.Lng,
		SurfaceHa:       row.SurfaceHa,
		DefaultCrop:     defaultCrop,
		County:          row.County,
		Locality:        row.Locality,
	}
}

func registerParcels(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "list-parcels",
		Method:      http.MethodGet,
		Path:        "/api/v1/parcels",
		Summary:     "List my parcels",
		Tags:        []string{"parcels"},
	}, h.listParcels)

	huma.Register(api, huma.Operation{
		OperationID: "get-parcel",
		Method:      http.MethodGet,
		Path:        "/api/v1/parcels/{id}",
		Summary:     "Get parcel by ID",
		Tags:        []string{"parcels"},
	}, h.getParcel)
}

func (h *Handlers) listParcels(ctx context.Context, _ *struct{}) (*struct {
	Body struct {
		Items []ParcelOutput `json:"items"`
	}
}, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}
	if user.Role != domain.RoleFermier {
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis pentru rolul dumneavoastră")
	}

	ownerID, err := uuid.Parse(user.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)
	rows, err := q.ListParcelsByOwner(ctx, ownerID)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	items := make([]ParcelOutput, len(rows))
	for i, r := range rows {
		items[i] = mapParcel(r)
	}

	out := &struct {
		Body struct {
			Items []ParcelOutput `json:"items"`
		}
	}{}
	out.Body.Items = items
	return out, nil
}

func (h *Handlers) getParcel(ctx context.Context, input *struct {
	ID string `path:"id"`
}) (*struct {
	Body struct {
		Parcel ParcelOutput `json:"parcel"`
	}
}, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}
	if user.Role != domain.RoleFermier {
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis pentru rolul dumneavoastră")
	}

	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "ID invalid")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)
	row, err := q.GetParcel(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.NewError(http.StatusNotFound, "Parcela nu a fost găsită")
	}
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	if row.OwnerID.String() != user.ID {
		return nil, huma.NewError(http.StatusNotFound, "Parcela nu a fost găsită")
	}

	out := &struct {
		Body struct {
			Parcel ParcelOutput `json:"parcel"`
		}
	}{}
	out.Body.Parcel = mapParcel(row)
	return out, nil
}
