package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

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

func (h *Handlers) listApiaries(_ context.Context, _ *struct{}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) createApiary(_ context.Context, _ *struct{ Body any }) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) getApiary(_ context.Context, _ *struct {
	ID string `path:"id"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) updateApiary(_ context.Context, _ *struct {
	ID   string `path:"id"`
	Body any
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}
