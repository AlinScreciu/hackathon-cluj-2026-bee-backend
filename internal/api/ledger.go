package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/stdlib"
	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/domain"
	"github.com/radarul-albinelor/api/internal/middleware"
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

// rawAuditReport handles GET /api/v1/events/audit-report. Returns
// application/pdf with a human-readable copy of the entire ledger plus the
// chain-verification result. Registered as a raw chi route in router.go
// because Huma doesn't easily emit binary bodies.
func (h *Handlers) rawAuditReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user := middleware.UserFromContext(ctx)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != domain.RoleInspector {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	events, err := h.ledgerSvc.List(ctx, "", "", 10000, 0)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Reverse: List() returns newest first; the report reads chronologically.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}

	verify, err := h.ledgerSvc.Verify(ctx)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)

	users, err := q.ListAllUsers(ctx)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nameByID := make(map[string]string, len(users))
	for _, u := range users {
		nameByID[u.ID.String()] = u.FullName
	}

	apiaries, err := q.ListAllApiaries(ctx)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	apiaryNameByID := make(map[string]string, len(apiaries))
	for _, a := range apiaries {
		apiaryNameByID[a.ID.String()] = a.Name
	}

	pdfBytes, err := h.pdfSvc.GenerateAuditReport(
		events,
		nameByID,
		apiaryNameByID,
		verify.Valid,
		verify.LastHash,
		verify.CheckedAt,
		verify.Error,
	)
	if err != nil {
		http.Error(w, "pdf generation failed", http.StatusInternalServerError)
		return
	}

	filename := "registru-oficial-" + time.Now().UTC().Format("20060102-1504") + ".pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(pdfBytes)))
	_, _ = w.Write(pdfBytes)
}
