// Command piyo-api runs the Piyo HTTP API.
//
// Configuration comes from environment variables (see .env.example); a few
// flags override them for convenience: -port, -dev, -sqlite, -store. In
// DEV_MODE the server uses an in-memory store seeded with demo data; otherwise
// STORE picks the store (default postgres). FCM_PROJECT_ID enables push.
// SIGINT/SIGTERM trigger a graceful shutdown with a 10 s drain.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"piyo-backend/internal/config"
	"piyo-backend/internal/httpapi"
	"piyo-backend/internal/push"
	"piyo-backend/internal/ratelimit"
	"piyo-backend/internal/seed"
	"piyo-backend/internal/store"
	"piyo-backend/internal/store/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "piyo-api:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	flag.IntVar(&cfg.Port, "port", cfg.Port, "listen port (env PORT)")
	flag.BoolVar(&cfg.DevMode, "dev", cfg.DevMode, "development mode: in-memory store + seed data (env DEV_MODE)")
	flag.StringVar(&cfg.SQLitePath, "sqlite", cfg.SQLitePath, "SQLite file used when STORE=sqlite (env SQLITE_PATH)")
	flag.StringVar(&cfg.Store, "store", cfg.Store, "memory | sqlite | postgres (env STORE)")
	flag.StringVar(&cfg.ReleasesDir, "releases", cfg.ReleasesDir, "dataset releases directory (env RELEASES_DIR)")
	flag.Parse()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openStore(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("store close", "err", err)
		}
	}()

	limiter := ratelimit.New(cfg.IPRatePerMinute)
	defer limiter.Stop()

	sender, err := push.New(cfg.FCMProjectID, cfg.FCMCredentialsFile)
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}
	logger.Info("push", "enabled", sender.Enabled())
	if sender.Enabled() {
		go purgePushTokens(ctx, st, logger)
	}

	handler := httpapi.NewHandlerWithPush(cfg, st, limiter, logger, sender)
	srv := &http.Server{
		Addr:              net.JoinHostPort("", strconv.Itoa(cfg.Port)),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", srv.Addr, "mode", cfg.Mode())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	logger.Info("stopped")
	return nil
}

// newLogger uses a human-friendly text handler in dev and JSON in prod.
func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if cfg.DevMode {
		opts.Level = slog.LevelDebug
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

// openStore picks the store (STORE env) and seeds the memory store in dev.
func openStore(ctx context.Context, cfg config.Config, logger *slog.Logger) (store.Store, error) {
	switch cfg.Store {
	case config.StorePostgres:
		st, err := postgres.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("postgres: %w", err)
		}
		logger.Info("store: postgres", "url", redactDSN(cfg.DatabaseURL))
		return st, nil
	case config.StoreSQLite:
		st, err := store.OpenSQLite(ctx, cfg.SQLitePath)
		if err != nil {
			return nil, err
		}
		logger.Info("store: sqlite", "path", cfg.SQLitePath)
		return st, nil
	}
	st := store.NewMemory()
	if !cfg.DevMode {
		logger.Warn("store: in-memory store in prod mode; nothing is persisted")
		return st, nil
	}
	hash := func(installID string) string { return httpapi.HashInstallID(cfg.HashSalt, installID) }
	if err := seed.Seed(ctx, st, hash, time.Now()); err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}
	logger.Info("store: in-memory (seeded)", "blocked_demo_install_id", "blocked-demo-device")
	return st, nil
}

// purgePushTokens deletes tokens not refreshed within PushTokenMaxAge, at
// start and then daily (Cloud Run instances are short-lived, so start counts).
func purgePushTokens(ctx context.Context, st store.Store, logger *slog.Logger) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		n, err := st.DeletePushTokensBefore(ctx, time.Now().Add(-httpapi.PushTokenMaxAge))
		if err != nil && ctx.Err() == nil {
			logger.Warn("push: purge stale tokens", "err", err)
		} else if n > 0 {
			logger.Info("push: purged stale tokens", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// redactDSN hides the password of a postgres:// URL for logs.
func redactDSN(dsn string) string {
	if i := strings.Index(dsn, "://"); i >= 0 {
		if at := strings.LastIndex(dsn, "@"); at > i {
			if colon := strings.Index(dsn[i+3:at], ":"); colon >= 0 {
				return dsn[:i+3+colon+1] + "***" + dsn[at:]
			}
		}
	}
	return dsn
}
