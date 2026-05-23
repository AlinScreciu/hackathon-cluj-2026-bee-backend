package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerSprayReports(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "create-spray-report",
		Method:      http.MethodPost,
		Path:        "/api/v1/spray-reports",
		Summary:     "Create a spray report",
		Tags:        []string{"spray-reports"},
	}, h.createSprayReport)

	huma.Register(api, huma.Operation{
		OperationID: "list-spray-reports",
		Method:      http.MethodGet,
		Path:        "/api/v1/spray-reports",
		Summary:     "List spray reports",
		Tags:        []string{"spray-reports"},
	}, h.listSprayReports)

	huma.Register(api, huma.Operation{
		OperationID: "get-spray-report",
		Method:      http.MethodGet,
		Path:        "/api/v1/spray-reports/{id}",
		Summary:     "Get spray report by ID",
		Tags:        []string{"spray-reports"},
	}, h.getSprayReport)

	huma.Register(api, huma.Operation{
		OperationID: "get-cascade-status",
		Method:      http.MethodGet,
		Path:        "/api/v1/spray-reports/{id}/cascade-status",
		Summary:     "Poll cascade status",
		Tags:        []string{"spray-reports"},
	}, h.getCascadeStatus)

	huma.Register(api, huma.Operation{
		OperationID: "get-primarie-pdf",
		Method:      http.MethodGet,
		Path:        "/api/v1/spray-reports/{id}/primarie-pdf",
		Summary:     "Download primarie PDF",
		Tags:        []string{"spray-reports"},
	}, h.getPrimariePDF)

	huma.Register(api, huma.Operation{
		OperationID: "cancel-spray-report",
		Method:      http.MethodPost,
		Path:        "/api/v1/spray-reports/{id}/cancel",
		Summary:     "Cancel spray report",
		Tags:        []string{"spray-reports"},
	}, h.cancelSprayReport)

	huma.Register(api, huma.Operation{
		OperationID: "anf-export",
		Method:      http.MethodPost,
		Path:        "/api/v1/spray-reports/anf-export",
		Summary:     "ANF 3-year audit export",
		Tags:        []string{"spray-reports"},
	}, h.anfExport)
}

func (h *Handlers) createSprayReport(_ context.Context, _ *struct{ Body any }) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) listSprayReports(_ context.Context, _ *struct {
	Status string `query:"status" enum:"active,past" required:"false"`
	Limit  int    `query:"limit" required:"false"`
	Cursor string `query:"cursor" required:"false"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) getSprayReport(_ context.Context, _ *struct {
	ID string `path:"id"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) getCascadeStatus(_ context.Context, _ *struct {
	ID string `path:"id"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) getPrimariePDF(_ context.Context, _ *struct {
	ID string `path:"id"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) cancelSprayReport(_ context.Context, _ *struct {
	ID string `path:"id"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) anfExport(_ context.Context, _ *struct{ Body any }) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}
