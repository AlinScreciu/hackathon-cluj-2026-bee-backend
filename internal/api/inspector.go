package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerInspector(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "inspector-map-data",
		Method:      http.MethodGet,
		Path:        "/api/v1/inspector/map-data",
		Summary:     "Get map data for inspector",
		Tags:        []string{"inspector"},
	}, h.inspectorMapData)

	huma.Register(api, huma.Operation{
		OperationID: "inspector-list-farmers",
		Method:      http.MethodGet,
		Path:        "/api/v1/inspector/farmers",
		Summary:     "List all farmers",
		Tags:        []string{"inspector"},
	}, h.inspectorListFarmers)

	huma.Register(api, huma.Operation{
		OperationID: "inspector-get-farmer",
		Method:      http.MethodGet,
		Path:        "/api/v1/inspector/farmers/{id}",
		Summary:     "Get farmer details",
		Tags:        []string{"inspector"},
	}, h.inspectorGetFarmer)
}

func (h *Handlers) inspectorMapData(_ context.Context, _ *struct {
	BBox string `query:"bbox" required:"false"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) inspectorListFarmers(_ context.Context, _ *struct{}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) inspectorGetFarmer(_ context.Context, _ *struct {
	ID string `path:"id"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}
