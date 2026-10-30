package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata" // business time zone works even in a minimal container with no tz database

	"github.com/Fredrick-911/hotspot-platform/internal/auth"
	"github.com/Fredrick-911/hotspot-platform/internal/config"
	"github.com/Fredrick-911/hotspot-platform/internal/database"
	"github.com/Fredrick-911/hotspot-platform/internal/httpapi"
	"github.com/Fredrick-911/hotspot-platform/internal/radius"
	"github.com/Fredrick-911/hotspot-platform/internal/session"
	"github.com/Fredrick-911/hotspot-platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server stopped with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// 1. Database.
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dbCancel()
	dbPool, err := database.Open(dbCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database setup failed: %w", err)
	}
	defer dbPool.Close()
	logger.Info("database connected successfully")

	// 2. Our own tables, applied once each (tracked in schema_migrations).
	if cfg.AutoMigrate {
		migCtx, migCancel := context.WithTimeout(context.Background(), 60*time.Second)
		err := database.Migrate(migCtx, dbPool, migrations.Files, logger)
		migCancel()
		if err != nil {
			return fmt.Errorf("migrations failed: %w", err)
		}
	}

	// 3. The first platform admin.
	bootstrapAdmin(context.Background(), logger, dbPool, cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 4. Background job: end passes whose time or data is up, even if the router missed it.
	reaper := &session.Reaper{
		DB:           dbPool,
		Disconnector: session.RadiusDisconnector{DB: dbPool, Client: radius.Client{}},
		Logger:       logger,
	}
	go func() {
		if err := reaper.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("reaper stopped", "err", err)
		}
	}()

	// 5. HTTP server.
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:           httpapi.NewRouter(dbPool, cfg),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second, // live-update streams lift this for themselves
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api listening", "addr", srv.Addr, "env", cfg.Env, "base_url", cfg.BaseURL,
			"payhero", cfg.PayHeroConfigured(), "dev_payments", cfg.DevPayments(), "key", cfg.Fingerprint())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// bootstrapAdmin creates the first platform admin from BOOTSTRAP_ADMIN_EMAIL
// and BOOTSTRAP_ADMIN_PASSWORD, but only while no admin exists. After that the
// two variables can be removed from .env.
func bootstrapAdmin(ctx context.Context, logger *slog.Logger, db *pgxpool.Pool, cfg config.Config) {
	exists, err := auth.AdminExists(ctx, db)
	if err != nil {
		logger.Error("could not check for an admin", "err", err)
		return
	}
	if exists {
		return
	}
	if cfg.BootstrapAdminEmail == "" || cfg.BootstrapAdminPassword == "" {
		logger.Warn("there is no platform admin yet: set BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD in .env and restart")
		return
	}
	if len(cfg.BootstrapAdminPassword) < 10 {
		logger.Error("BOOTSTRAP_ADMIN_PASSWORD must be at least 10 characters; no admin created")
		return
	}
	u, err := auth.Create(ctx, db, "", cfg.BootstrapAdminEmail, "Platform Admin", cfg.BootstrapAdminPassword, auth.RoleAdmin)
	if err != nil {
		logger.Error("could not create the first admin", "err", err)
		return
	}
	logger.Info("first platform admin created: sign in at /login, then remove BOOTSTRAP_ADMIN_PASSWORD from .env", "email", u.Email)
}
