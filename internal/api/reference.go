package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerReference(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "list-substances",
		Method:      http.MethodGet,
		Path:        "/api/v1/reference/substances",
		Summary:     "List pesticide substances",
		Tags:        []string{"reference"},
	}, h.listSubstances)

	huma.Register(api, huma.Operation{
		OperationID: "get-weather",
		Method:      http.MethodGet,
		Path:        "/api/v1/reference/weather",
		Summary:     "Get current weather at location",
		Tags:        []string{"reference"},
	}, h.getWeather)
}

func (h *Handlers) listSubstances(_ context.Context, _ *struct{}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) getWeather(_ context.Context, _ *struct {
	Lat float64 `query:"lat"`
	Lng float64 `query:"lng"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}
