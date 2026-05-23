package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/radarul-albinelor/api/internal/domain"
	"github.com/radarul-albinelor/api/internal/services"
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

func (h *Handlers) listEvents(ctx context.Context, input *struct {
	Type   string `query:"type" required:"false"`
	Actor  string `query:"actor" required:"false"`
	Limit  int    `query:"limit" required:"false"`
	Offset int    `query:"offset" required:"false"`
}) (*struct {
	Body struct {
		Events []domain.LedgerEvent `json:"events"`
	}
}, error) {
	events, err := h.ledgerSvc.List(ctx, input.Type, input.Actor, input.Limit, input.Offset)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	out := &struct {
		Body struct {
			Events []domain.LedgerEvent `json:"events"`
		}
	}{}
	out.Body.Events = events
	return out, nil
}

func (h *Handlers) getEventByHash(ctx context.Context, input *struct {
	Hash string `path:"hash"`
}) (*struct {
	Body struct {
		Event *domain.LedgerEvent   `json:"event"`
		Chain *services.LedgerChain `json:"chain"`
	}
}, error) {
	event, chain, err := h.ledgerSvc.GetByHash(ctx, input.Hash)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	if event == nil {
		return nil, huma.NewError(http.StatusNotFound, "Evenimentul nu a fost găsit")
	}
	out := &struct {
		Body struct {
			Event *domain.LedgerEvent   `json:"event"`
			Chain *services.LedgerChain `json:"chain"`
		}
	}{}
	out.Body.Event = event
	out.Body.Chain = chain
	return out, nil
}

func (h *Handlers) verifyLedger(ctx context.Context, _ *struct{}) (*struct {
	Body *services.VerifyResult
}, error) {
	result, err := h.ledgerSvc.Verify(ctx)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	return &struct {
		Body *services.VerifyResult
	}{Body: result}, nil
}
