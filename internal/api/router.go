package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/radarul-albinelor/api/internal/config"
	"github.com/radarul-albinelor/api/internal/middleware"
	"github.com/radarul-albinelor/api/internal/platform"
)

type Handlers struct {
	cfg  *config.Config
	pool *pgxpool.Pool
	jwt  *platform.JWTService
}

func NewRouter(cfg *config.Config, pool *pgxpool.Pool) http.Handler {
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Logger)
	r.Use(middleware.Recover)
	r.Use(middleware.CORS(cfg.AllowedOrigins))

	jwtSvc := platform.NewJWTService(cfg.JWTSecret)
	h := &Handlers{cfg: cfg, pool: pool, jwt: jwtSvc}

	humaAPI := humachi.New(r, huma.DefaultConfig("Radarul Albinelor", "1.0.0"))

	// Health check — no auth
	huma.Register(humaAPI, huma.Operation{
		OperationID: "healthz",
		Method:      http.MethodGet,
		Path:        "/api/v1/healthz",
		Summary:     "Health check",
	}, func(_ context.Context, _ *struct{}) (*HealthResponse, error) {
		return &HealthResponse{Body: struct{ Status string `json:"status"` }{"ok"}}, nil
	})

	// Auth routes — no session required
	r.Group(func(r chi.Router) {
		registerAuth(humaAPI, h)
	})

	// VAPID public key — no auth
	registerPushPublic(humaAPI, h)

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(jwtSvc))
		registerApiaries(humaAPI, h)
		registerParcels(humaAPI, h)
		registerSprayReports(humaAPI, h)
		registerAlerts(humaAPI, h)
		registerDamage(humaAPI, h)
		registerInspector(humaAPI, h)
		registerLedger(humaAPI, h)
		registerPushProtected(humaAPI, h)
		registerReference(humaAPI, h)
	})

	// Twilio webhooks — no session, Twilio signature validates internally
	r.Group(func(r chi.Router) {
		registerTwilioWebhooks(humaAPI, h)
	})

	writeOpenAPI(humaAPI)

	return r
}

func writeOpenAPI(api huma.API) {
	data, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		slog.Warn("failed to marshal OpenAPI spec", "err", err)
		return
	}
	if err := os.WriteFile("openapi.json", data, 0644); err != nil {
		slog.Warn("failed to write openapi.json", "err", err)
		return
	}
	slog.Info("openapi.json written")
}

type HealthResponse struct {
	Body struct {
		Status string `json:"status"`
	}
}
