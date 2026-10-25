package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/config"
	"agentrove/internal/db"
	"agentrove/internal/events"
	"agentrove/internal/httpx"
	"agentrove/internal/profiles"
	"agentrove/internal/sandbox"
	"agentrove/internal/server"
	"agentrove/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agentrove:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	setupLogging(cfg.LogLevel)

	database, err := db.Open(cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	keys, err := auth.NewKeys(cfg.SecretKey)
	if err != nil {
		return err
	}
	st := &store.Store{DB: database, Keys: keys}
	// Box network subnets: requests from them never count as a trusted proxy.
	boxSubnets := httpx.NewPrefixSet()
	sandboxes := sandbox.NewManager(cfg, boxSubnets)
	sandboxes.SetEnvSource(st.UserEnv)

	a := &app.App{
		Cfg:       cfg,
		DB:        database,
		Keys:      keys,
		Auth:      &auth.Service{DB: database, Cfg: cfg, Keys: keys, BoxSubnets: boxSubnets},
		Hub:       events.NewHub(),
		Sandboxes: sandboxes,
		Store:     st,
		Profiles:  profiles.NewService(database, keys, cfg, sandboxes),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// One-time: Claude env vars from before profiles become a profile.
	a.Profiles.MigrateEnvVars(ctx)

	handler, shutdown, err := server.Build(ctx, a)
	if err != nil {
		return err
	}

	go sandboxes.RunIdleReaper(ctx)
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			a.Auth.PurgeExpiredRefreshTokens(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("agentrove listening", "addr", srv.Addr, "storage", cfg.StoragePath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil {
			return err
		}
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	shutdown(shutdownCtx)
	return nil
}

func setupLogging(level string) {
	var l slog.Level
	switch strings.ToUpper(level) {
	case "DEBUG":
		l = slog.LevelDebug
	case "WARNING", "WARN":
		l = slog.LevelWarn
	case "ERROR":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: l})))
}
