package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

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

func (h *Handlers) listParcels(_ context.Context, _ *struct{}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) getParcel(_ context.Context, _ *struct {
	ID string `path:"id"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}
