package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/httpx"
)

type Module struct {
	a   *app.App
	svc *Service

	mu     sync.Mutex
	ctx    context.Context
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

func New(a *app.App) *Module {
	ctx, cancel := context.WithCancel(context.Background())
	return &Module{a: a, svc: NewService(a), ctx: ctx, cancel: cancel}
}

func (m *Module) Service() *Service { return m.svc }

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix + "/automations"
	for _, base := range []string{p, p + "/{$}"} {
		mux.HandleFunc("GET "+base, m.a.Auth.Require(m.list))
		mux.HandleFunc("POST "+base, m.a.Auth.Require(m.create))
	}
	mux.HandleFunc("PATCH "+p+"/{id}", m.a.Auth.Require(m.update))
	mux.HandleFunc("DELETE "+p+"/{id}", m.a.Auth.Require(m.delete))
	mux.HandleFunc("POST "+p+"/{id}/run", m.a.Auth.Require(m.run))
	// Called by GitHub: authenticated by the per-automation HMAC secret.
	mux.HandleFunc("POST "+p+"/{id}/github-webhook", httpx.Wrap(m.webhook))
}

// Start runs the dispatch loop (every minute, like the Python maintenance job).
func (m *Module) Start(ctx context.Context) {
	t := time.NewTicker(dispatchInterval)
	defer t.Stop()
	for {
		m.svc.RunDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Module) Stop(ctx context.Context) {
	m.cancel()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// background runs fn detached from the request, bounded by module shutdown.
func (m *Module) background(fn func(ctx context.Context)) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				slog.Error("automation background task panicked", "err", r)
			}
		}()
		fn(m.ctx)
	}()
}

// requestBaseURL is the address the user reached us on (GitHub can't follow
// redirects, so it beats a possibly-plain-http BASE_URL behind a TLS proxy).
func requestBaseURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host + "/"
}

// nonNullable fields reject an explicit JSON null on PATCH.
var nonNullable = map[string]bool{
	"name": true, "prompt": true, "model_id": true, "workspace_id": true, "cron_expression": true, "timezone": true,
	"permission_mode": true, "worktree": true, "selected_persona_name": true, "enabled": true, "trigger_type": true,
}

func decodeInput(r *http.Request, patch bool) (*Input, error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 10<<20))
	if err != nil {
		return nil, httpx.Unprocessable("Invalid body")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, httpx.Unprocessable("Invalid JSON body: " + err.Error())
	}
	in := &Input{Set: map[string]bool{}}
	for k, v := range raw {
		in.Set[k] = true
		if string(v) == "null" && nonNullable[k] {
			if patch {
				return nil, httpx.Unprocessable(k + ": Field cannot be null")
			}
			delete(in.Set, k)
		}
	}
	if err := json.Unmarshal(body, in); err != nil {
		return nil, httpx.Unprocessable(fmt.Sprintf("Invalid JSON body: %v", err))
	}
	return in, nil
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) error {
	out, err := m.svc.List(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, out)
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) error {
	in, err := decodeInput(r, false)
	if err != nil {
		return err
	}
	x, err := m.svc.Create(r.Context(), auth.UserFrom(r.Context()).ID, in, requestBaseURL(r))
	if err != nil {
		return err
	}
	return httpx.Created(w, x)
}

func (m *Module) update(w http.ResponseWriter, r *http.Request) error {
	in, err := decodeInput(r, true)
	if err != nil {
		return err
	}
	x, err := m.svc.Update(r.Context(), r.PathValue("id"), auth.UserFrom(r.Context()).ID, in, requestBaseURL(r))
	if err != nil {
		return err
	}
	return httpx.OK(w, x)
}

func (m *Module) delete(w http.ResponseWriter, r *http.Request) error {
	if err := m.svc.Delete(r.Context(), r.PathValue("id"), auth.UserFrom(r.Context()).ID); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (m *Module) run(w http.ResponseWriter, r *http.Request) error {
	chatID, err := m.svc.Run(r.Context(), r.PathValue("id"), auth.UserFrom(r.Context()).ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]string{"chat_id": chatID})
}

func (m *Module) webhook(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 25<<20))
	if err != nil {
		return httpx.BadRequest("Invalid body")
	}
	var payload struct {
		Action any `json:"action"`
	}
	_ = json.Unmarshal(body, &payload)
	action, _ := payload.Action.(string)
	id := r.PathValue("id")
	relevant, err := m.svc.HandleWebhook(r.Context(), id, body, r.Header.Get("X-Hub-Signature-256"), r.Header.Get("X-GitHub-Event"), action)
	if err != nil {
		return err
	}
	if relevant {
		m.background(func(ctx context.Context) { m.svc.CheckGitHubNow(ctx, id) })
	}
	return httpx.JSON(w, http.StatusAccepted, map[string]bool{"queued": relevant})
}
