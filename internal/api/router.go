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
	"github.com/jackc/pgx/v5/stdlib"

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
	"github.com/radarul-albinelor/api/internal/storage"
	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
)

type Handlers struct {
	cfg           *config.Config
	pool          *pgxpool.Pool
	db            *dbsqlc.Queries
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
	storage       storage.Storage
	storageIsR2   bool
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
	//
	// Sliding renewal: when an active session's JWT is within 4h of expiry,
	// re-sign and set a fresh 24h cookie. Idle sessions still die at 24h; active
	// ones are kept alive. The new cookie matches the login Set-Cookie shape
	// (auth.go: HttpOnly, SameSite=Lax, Path=/, MaxAge=86400).
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if cookie, err := req.Cookie("ra_session"); err == nil {
				if claims, err := jwtSvc.Verify(cookie.Value); err == nil {
					user := &domain.User{ID: claims.UserID, Role: claims.Role, CNP: claims.CNP}
					req = req.WithContext(middleware.WithUser(req.Context(), user))

					if claims.ExpiresAt != nil && time.Until(claims.ExpiresAt.Time) < 4*time.Hour {
						if newToken, signErr := jwtSvc.Sign(user); signErr == nil {
							http.SetCookie(w, &http.Cookie{
								Name:     "ra_session",
								Value:    newToken,
								HttpOnly: true,
								Secure:   cfg.AppEnv == "production",
								SameSite: http.SameSiteLaxMode,
								Path:     "/",
								Domain:   cfg.CookieDomain,
								MaxAge:   86400,
							})
						}
					}
				}
			}
			next.ServeHTTP(w, req)
		})
	})

	var emailClient *email.EmailClient
	if cfg.ResendAPIKey != "" {
		emailClient = email.NewClient(cfg.ResendAPIKey, cfg.ResendFromEmail)
	} else {
		slog.Warn("RESEND_API_KEY not set — email delivery disabled, codes logged to terminal only")
	}
	authSvc := services.NewAuthService(pool, jwtSvc, emailClient, cfg)
	ledgerSvc := services.NewLedgerService(pool)

	var twilioClient *twilio.Client
	if cfg.TwilioAccountSID != "" && cfg.TwilioAuthToken != "" {
		twilioClient = twilio.NewClient(cfg.TwilioAccountSID, cfg.TwilioAuthToken, cfg.TwilioFromPhone)
	}

	// Shared object store: voice audio (`voice/` prefix) + damage photos
	// (`photos/` prefix). R2 in production, local disk in dev (when R2 vars
	// absent). Bucket stays private; all reads go through short-lived presigned
	// GETs and all writes via presigned PUTs.
	var audioStore storage.Storage
	storageIsR2 := false
	if cfg.R2Enabled() {
		r2, err := storage.NewR2(storage.R2Config{
			AccountID:       cfg.R2AccountID,
			AccessKeyID:     cfg.R2AccessKeyID,
			SecretAccessKey: cfg.R2SecretAccessKey,
			Bucket:          cfg.R2Bucket,
		})
		if err != nil {
			slog.Error("storage R2 init failed, falling back to local disk", "err", err)
			audioStore = storage.NewLocal("uploads", cfg.AppBaseURL, "/uploads")
		} else {
			slog.Info("storage R2 enabled (private bucket, presigned URLs)", "bucket", cfg.R2Bucket)
			audioStore = r2
			storageIsR2 = true
		}
	} else {
		slog.Info("storage R2 not configured, using local disk")
		audioStore = storage.NewLocal("uploads", cfg.AppBaseURL, "/uploads")
	}

	var elevenLabsClient *elevenlabs.Client
	if cfg.ElevenLabsAPIKey != "" {
		voiceID := cfg.ElevenLabsVoiceID
		if voiceID == "" {
			voiceID = "21m00Tcm4TlvDq8ikWAM"
		}
		elevenLabsClient = elevenlabs.NewClient(cfg.ElevenLabsAPIKey, voiceID, audioStore)
		if cfg.ElevenLabsAgentID != "" && cfg.ElevenLabsPhoneNumID != "" {
			elevenLabsClient.WithOutboundCall(cfg.ElevenLabsAgentID, cfg.ElevenLabsPhoneNumID)
		}
		// Prewarm the three fixed messages so live calls never pay TTS latency
		// for them. Cache hit (R2 Exists) costs ~50ms; cache miss costs ~1-2s
		// plus one ElevenLabs API call, once per voiceID per text forever.
		elevenLabsClient.Prewarm(context.Background(), []string{
			"Mulțumim! Confirmare înregistrată. La revedere.",
			"Nu am primit o confirmare validă. Vă rugăm verificați SMS-ul primit.",
			"Nu am primit o confirmare. Vă rugăm verificați SMS-ul primit.",
		})
	}

	var pushClient *webpush.Client
	if cfg.VAPIDPublicKey != "" && cfg.VAPIDPrivateKey != "" {
		pushClient = webpush.NewClient(cfg.VAPIDPublicKey, cfg.VAPIDPrivateKey)
	}

	pdfSvc := services.NewPDFService(cfg.AppBaseURL)

	// Use explicit interface variables: a nil *T passed directly to an interface parameter
	// produces a non-nil interface (Go nil-interface gotcha), which causes MakeCall/Send
	// to panic on the nil pointer. Assigning only when the concrete value is non-nil
	// keeps c.notifier / c.pusher / c.elevenLabs truly nil in the cascade nil-checks.
	var cascadeNotifier services.Notifier
	if twilioClient != nil {
		cascadeNotifier = twilioClient
	}
	var cascadePusher services.PushSender
	if pushClient != nil {
		cascadePusher = pushClient
	}
	var cascadeCaller services.ElevenLabsCaller
	if elevenLabsClient != nil && cfg.ElevenLabsAgentID != "" && cfg.ElevenLabsPhoneNumID != "" {
		cascadeCaller = elevenLabsClient
	}
	cascadeSvc := services.NewCascadeService(pool, ledgerSvc, cascadeNotifier, cascadePusher, cascadeCaller, cfg.AppBaseURL)
	geoAIClient := geoai.NewClient(cfg.GeoAIBaseURL)
	weatherClient := weather.NewCachedClient(10 * time.Minute)
	handlerDB := dbsqlc.New(stdlib.OpenDBFromPool(pool))

	h := &Handlers{
		cfg:           cfg,
		pool:          pool,
		db:            handlerDB,
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
		storage:       audioStore,
		storageIsR2:   storageIsR2,
	}

	humaAPI := humachi.New(r, huma.DefaultConfig("BeeLive", "1.0.0"))

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
	r.Post("/api/v1/spray-reports/anf-export", h.rawANFExport)

	// Dev-only raw upload route. In prod (R2 backend) clients PUT directly to
	// R2 via the presigned URL; this path is never used. Gated so a production
	// misconfig can't accidentally accept uploads through the API.
	if !storageIsR2 {
		r.Put("/api/v1/uploads/raw/*", h.rawUploadPut)
	}

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
