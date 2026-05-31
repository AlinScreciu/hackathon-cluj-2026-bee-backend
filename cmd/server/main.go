package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/radarul-albinelor/api/internal/api"
	"github.com/radarul-albinelor/api/internal/config"
	"github.com/radarul-albinelor/api/internal/services"
)

const version = "0.1.0"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	var handler slog.Handler
	if cfg.AppEnv == "production" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(handler))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	for _, dir := range []string{"uploads/pdfs", "uploads/voice", "uploads/photos"} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			slog.Error("failed to create upload dir", "dir", dir, "err", err)
			os.Exit(1)
		}
	}

	pool, err := pgxpool.New(ctx, cfg.DBConnStr)
	if err != nil {
		slog.Error("failed to create db pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		slog.Error("db ping failed", "err", err)
		os.Exit(1)
	}
	slog.Info("database connected")

	if len(os.Args) > 1 && os.Args[1] == "--seed" {
		if err := services.Seed(ctx, pool); err != nil {
			slog.Error("seed failed", "err", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if len(os.Args) > 1 && os.Args[1] == "--seed-more" {
		if err := services.Seed(ctx, pool); err != nil {
			slog.Error("seed failed", "err", err)
			os.Exit(1)
		}
		if err := services.SeedMore(ctx, pool); err != nil {
			slog.Error("seed-more failed", "err", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if len(os.Args) > 1 && os.Args[1] == "--demo-reset" {
		// Ensure users/apiaries/parcels exist (idempotent), then wipe + reseed.
		if err := services.Seed(ctx, pool); err != nil {
			slog.Error("seed failed", "err", err)
			os.Exit(1)
		}
		if err := services.SeedMore(ctx, pool); err != nil {
			slog.Error("seed-more failed", "err", err)
			os.Exit(1)
		}
		if err := services.SeedDemoReset(ctx, pool); err != nil {
			slog.Error("demo-reset failed", "err", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if len(os.Args) > 1 && os.Args[1] == "--demo-tamper" {
		if err := services.SeedDemoTamper(ctx, pool); err != nil {
			slog.Error("demo-tamper failed", "err", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	router, cascadeShutdown := api.NewRouter(cfg, pool)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	slog.Info("beelive-api starting",
		"version", version,
		"port", cfg.Port,
		"env", cfg.AppEnv,
		"app_base_url", cfg.AppBaseURL,
		"cookie_domain", cfg.CookieDomain,
		"geo_ai_base_url", cfg.GeoAIBaseURL,
		"allowed_origins", cfg.AllowedOrigins,
	)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down...")

	cascadeShutdown()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("shutdown complete")
}
