package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/radarul-albinelor/api/internal/config"
	"github.com/radarul-albinelor/api/internal/domain"
	"github.com/radarul-albinelor/api/internal/external/email"
	"github.com/radarul-albinelor/api/internal/external/elevenlabs"
	"github.com/radarul-albinelor/api/internal/external/geoai"
	"github.com/radarul-albinelor/api/internal/external/twilio"
	"github.com/radarul-albinelor/api/internal/external/weather"
	"github.com/radarul-albinelor/api/internal/external/webpush"
	"github.com/radarul-albinelor/api/internal/middleware"
	"github.com/radarul-albinelor/api/internal/platform"
	"github.com/radarul-albinelor/api/internal/services"
)

type Handlers struct {
	cfg           *config.Config
	pool          *pgxpool.Pool
	jwt           *platform.JWTService
	authSvc       *services.AuthService
	ledgerSvc     *services.LedgerService
	cascade       *services.CascadeService
	geoAI         geoai.Client
	weatherClient *weather.CachedClient
	emailClient   *email.EmailClient
	twilioClient  *twilio.Client
	elevenLabs    *elevenlabs.Client
	pushClient    *webpush.Client
	pdfSvc        *services.PDFService
}

func NewRouter(cfg *config.Config, pool *pgxpool.Pool) (http.Handler, func()) {
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Logger)
	r.Use(middleware.Recover)
	r.Use(middleware.CORS(cfg.AllowedOrigins))

	jwtSvc := platform.NewJWTService(cfg.JWTSecret)

	// Session middleware: reads ra_session cookie and injects user into context when
	// valid. Runs on all routes (Huma and raw chi). Individual handlers enforce auth
	// by calling middleware.UserFromContext and returning 401 when nil.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if cookie, err := req.Cookie("ra_session"); err == nil {
				if claims, err := jwtSvc.Verify(cookie.Value); err == nil {
					user := &domain.User{ID: claims.UserID, Role: claims.Role, CNP: claims.CNP}
					req = req.WithContext(middleware.WithUser(req.Context(), user))
				}
			}
			next.ServeHTTP(w, req)
		})
	})

	emailClient := email.NewClient("smtp.resend.com", 465, "apikey", cfg.ResendAPIKey, cfg.ResendFromEmail)
	authSvc := services.NewAuthService(pool, jwtSvc, emailClient, cfg)
	ledgerSvc := services.NewLedgerService(pool)

	var twilioClient *twilio.Client
	if cfg.TwilioAccountSID != "" && cfg.TwilioAuthToken != "" {
		twilioClient = twilio.NewClient(cfg.TwilioAccountSID, cfg.TwilioAuthToken, cfg.TwilioFromPhone)
	}

	var elevenLabsClient *elevenlabs.Client
	if cfg.ElevenLabsAPIKey != "" {
		voiceID := cfg.ElevenLabsVoiceID
		if voiceID == "" {
			voiceID = "21m00Tcm4TlvDq8ikWAM"
		}
		elevenLabsClient = elevenlabs.NewClient(cfg.ElevenLabsAPIKey, voiceID)
	}

	var pushClient *webpush.Client
	if cfg.VAPIDPublicKey != "" && cfg.VAPIDPrivateKey != "" {
		pushClient = webpush.NewClient(cfg.VAPIDPublicKey, cfg.VAPIDPrivateKey)
	}

	pdfSvc := services.NewPDFService(cfg.AppBaseURL)
	cascadeSvc := services.NewCascadeService(pool, ledgerSvc, twilioClient, pushClient, cfg.AppBaseURL)
	geoAIClient := geoai.NewClient(cfg.GeoAIBaseURL)
	weatherClient := weather.NewCachedClient(10 * time.Minute)

	h := &Handlers{
		cfg:           cfg,
		pool:          pool,
		jwt:           jwtSvc,
		authSvc:       authSvc,
		ledgerSvc:     ledgerSvc,
		cascade:       cascadeSvc,
		geoAI:         geoAIClient,
		weatherClient: weatherClient,
		emailClient:   emailClient,
		twilioClient:  twilioClient,
		elevenLabs:    elevenLabsClient,
		pushClient:    pushClient,
		pdfSvc:        pdfSvc,
	}

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

	registerAuth(humaAPI, h)
	registerPushPublic(humaAPI, h)
	registerApiaries(humaAPI, h)
	registerParcels(humaAPI, h)
	registerSprayReports(humaAPI, h)
	registerAlerts(humaAPI, h)
	registerDamage(humaAPI, h)
	registerInspector(humaAPI, h)
	registerLedger(humaAPI, h)
	registerPushProtected(humaAPI, h)
	registerReference(humaAPI, h)

	writeOpenAPI(humaAPI)

	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir("uploads"))))
	r.Get("/api/v1/spray-reports/{id}/primarie-pdf", h.getPrimariePDF)

	// Twilio webhooks as raw chi routes — Twilio sends form-encoded bodies and
	// expects XML responses, so they bypass Huma.
	r.Post("/api/v1/webhooks/twilio/voice/gather", h.rawVoiceGather)
	r.Post("/api/v1/webhooks/twilio/voice/status", h.rawVoiceStatus)
	r.Post("/api/v1/webhooks/twilio/sms/inbound", h.rawSMSInbound)
	r.Post("/api/v1/webhooks/twilio/sms/status", h.rawSMSStatus)

	return r, cascadeSvc.Shutdown
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
