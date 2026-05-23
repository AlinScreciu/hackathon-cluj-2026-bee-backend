package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

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

func (h *Handlers) listAlerts(_ context.Context, _ *struct {
	Status string `query:"status" enum:"active,all" required:"false"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) getAlert(_ context.Context, _ *struct {
	ID string `path:"id"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) confirmAlert(_ context.Context, _ *struct {
	ID   string `path:"id"`
	Body struct {
		Action string `json:"action" enum:"move_hives,seal_in_place"`
	}
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}
