// Package server wires every feature module onto one mux.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"agentrove/internal/app"
	"agentrove/internal/httpx"
	"agentrove/internal/web"
)

// Module is implemented by each feature package.
type Module interface {
	Routes(mux *http.ServeMux)
}

// Starter modules run background work (schedulers, recovery) until ctx ends.
type Starter interface {
	Start(ctx context.Context)
}

// Stopper modules release resources on shutdown.
type Stopper interface {
	Stop(ctx context.Context)
}

func Build(ctx context.Context, a *app.App) (http.Handler, func(context.Context), error) {
	mux := http.NewServeMux()
	p := a.Cfg.APIPrefix

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		httpx.OK(w, map[string]string{"status": "healthy"})
	})
	mux.HandleFunc("GET "+p+"/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.DB.PingContext(r.Context()); err != nil {
			httpx.JSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "checks": map[string]any{"database": map[string]any{"ok": false, "error": err.Error()}}})
			return
		}
		httpx.OK(w, map[string]any{"status": "ready", "checks": map[string]any{"database": map[string]any{"ok": true}}})
	})

	a.Auth.Routes(mux, p)
	dist := web.Dist()
	if a.Cfg.FrontendDir != "" {
		dist = os.DirFS(a.Cfg.FrontendDir)
	}
	if dist != nil {
		mux.Handle("GET /", Frontend(dist))
	} else {
		slog.Info("no frontend built in and FRONTEND_DIR unset; serving the API only")
	}

	modules, err := buildModules(a)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range modules {
		m.Routes(mux)
		if s, ok := m.(Starter); ok {
			go s.Start(ctx)
		}
	}

	handler := httpx.Chain(mux, httpx.Recover, httpx.Logger, httpx.SecurityHeaders, httpx.CORS(a.Cfg.AllowedOrigins))
	shutdown := func(ctx context.Context) {
		for i := len(modules) - 1; i >= 0; i-- {
			if s, ok := modules[i].(Stopper); ok {
				s.Stop(ctx)
			}
		}
	}
	return handler, shutdown, nil
}
