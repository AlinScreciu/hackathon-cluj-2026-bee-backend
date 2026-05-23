package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerLedger(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "list-events",
		Method:      http.MethodGet,
		Path:        "/api/v1/events",
		Summary:     "List ledger events",
		Tags:        []string{"ledger"},
	}, h.listEvents)

	huma.Register(api, huma.Operation{
		OperationID: "get-event-by-hash",
		Method:      http.MethodGet,
		Path:        "/api/v1/events/{hash}",
		Summary:     "Get ledger event by hash",
		Tags:        []string{"ledger"},
	}, h.getEventByHash)

	huma.Register(api, huma.Operation{
		OperationID: "verify-ledger",
		Method:      http.MethodGet,
		Path:        "/api/v1/events/verify",
		Summary:     "Verify ledger chain integrity",
		Tags:        []string{"ledger"},
	}, h.verifyLedger)
}

func (h *Handlers) listEvents(_ context.Context, _ *struct {
	Type   string `query:"type" required:"false"`
	Actor  string `query:"actor" required:"false"`
	Limit  int    `query:"limit" required:"false"`
	Cursor string `query:"cursor" required:"false"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) getEventByHash(_ context.Context, _ *struct {
	Hash string `path:"hash"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) verifyLedger(_ context.Context, _ *struct{}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}
