// Package profileapi serves the provider profiles API (Settings → Providers
// and the composer's profile picker): /api/v1/profiles.
package profileapi

import (
	"context"
	"net/http"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/profiles"
)

type Module struct{ a *app.App }

func New(a *app.App) *Module { return &Module{a: a} }

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix + "/profiles"
	req := m.a.Auth.Require
	mux.HandleFunc("GET "+p, req(m.list))
	mux.HandleFunc("GET "+p+"/{$}", req(m.list))
	mux.HandleFunc("POST "+p, req(m.create))
	mux.HandleFunc("POST "+p+"/{$}", req(m.create))
	mux.HandleFunc("PATCH "+p+"/{profile_id}", req(m.update))
	mux.HandleFunc("DELETE "+p+"/{profile_id}", req(m.delete))
	mux.HandleFunc("POST "+p+"/{profile_id}/default", req(m.setDefault))
	mux.HandleFunc("POST "+p+"/{profile_id}/login", req(m.startLogin))
	mux.HandleFunc("GET "+p+"/{profile_id}/login", req(m.loginState))
	mux.HandleFunc("POST "+p+"/{profile_id}/login/input", req(m.loginInput))
	mux.HandleFunc("DELETE "+p+"/{profile_id}/login", req(m.cancelLogin))
}

// View is a profile as the client sees it: the token is never sent, only
// whether one is set and its last characters.
type View struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	Name      string `json:"name"`
	AuthMode  string `json:"auth_mode"`
	BaseURL   string `json:"base_url"`
	UsageURL  string `json:"usage_url"`
	HasToken  bool   `json:"has_token"`
	TokenHint string `json:"token_hint"`
	// TokenEnv is the variable a Claude token is passed in ("" = by mode).
	TokenEnv  string               `json:"token_env"`
	IsDefault bool                 `json:"is_default"`
	Login     *profiles.LoginState `json:"login"`
	CreatedAt db.Time              `json:"created_at"`
	profiles.Status
}

func tokenHint(tok string) string {
	if len(tok) < 12 {
		return ""
	}
	return "…" + tok[len(tok)-4:]
}

func (m *Module) view(ctx context.Context, p *profiles.Profile, defaultID string) View {
	v := View{ID: p.ID, Provider: p.Provider, Name: p.Name, AuthMode: p.AuthMode, BaseURL: p.BaseURL, UsageURL: p.UsageURL,
		HasToken: p.Token != "", TokenHint: tokenHint(p.Token), TokenEnv: p.TokenEnv,
		IsDefault: p.ID == defaultID, CreatedAt: p.CreatedAt, Status: m.a.Profiles.Status(ctx, p)}
	if st, ok := m.a.Profiles.LoginState(p); ok {
		v.Login = &st
	}
	return v
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	m.a.Profiles.ProbeCLIs(r.Context(), u.ID)
	list, err := m.a.Profiles.List(r.Context(), u.ID)
	if err != nil {
		return err
	}
	def := m.a.Profiles.DefaultID(r.Context(), u.ID)
	out := make([]View, 0, len(list))
	for _, p := range list {
		out = append(out, m.view(r.Context(), p, def))
	}
	return httpx.OK(w, out)
}

func (m *Module) owned(r *http.Request) (*profiles.Profile, error) {
	return m.a.Profiles.Get(r.Context(), auth.UserFrom(r.Context()).ID, r.PathValue("profile_id"))
}

func (m *Module) respond(w http.ResponseWriter, r *http.Request, p *profiles.Profile, status int) error {
	u := auth.UserFrom(r.Context())
	v := m.view(r.Context(), p, m.a.Profiles.DefaultID(r.Context(), u.ID))
	return httpx.JSON(w, status, v)
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) error {
	var in profiles.Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	p, err := m.a.Profiles.Create(r.Context(), auth.UserFrom(r.Context()).ID, in)
	if err != nil {
		return err
	}
	return m.respond(w, r, p, http.StatusCreated)
}

func (m *Module) update(w http.ResponseWriter, r *http.Request) error {
	var in profiles.Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	p, err := m.a.Profiles.Update(r.Context(), auth.UserFrom(r.Context()).ID, r.PathValue("profile_id"), in)
	if err != nil {
		return err
	}
	return m.respond(w, r, p, http.StatusOK)
}

func (m *Module) delete(w http.ResponseWriter, r *http.Request) error {
	if err := m.a.Profiles.Delete(r.Context(), auth.UserFrom(r.Context()).ID, r.PathValue("profile_id")); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (m *Module) setDefault(w http.ResponseWriter, r *http.Request) error {
	p, err := m.owned(r)
	if err != nil {
		return err
	}
	if err := m.a.Profiles.SetDefault(r.Context(), p.UserID, p.ID); err != nil {
		return err
	}
	return m.respond(w, r, p, http.StatusOK)
}

func (m *Module) startLogin(w http.ResponseWriter, r *http.Request) error {
	p, err := m.owned(r)
	if err != nil {
		return err
	}
	st, err := m.a.Profiles.StartLogin(r.Context(), p)
	if err != nil {
		return err
	}
	return httpx.OK(w, st)
}

func (m *Module) loginState(w http.ResponseWriter, r *http.Request) error {
	p, err := m.owned(r)
	if err != nil {
		return err
	}
	st, ok := m.a.Profiles.LoginState(p)
	if !ok {
		return httpx.NotFound("No sign-in for this profile")
	}
	return httpx.OK(w, st)
}

func (m *Module) loginInput(w http.ResponseWriter, r *http.Request) error {
	p, err := m.owned(r)
	if err != nil {
		return err
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if err := m.a.Profiles.LoginInput(p, body.Text); err != nil {
		return err
	}
	st, _ := m.a.Profiles.LoginState(p)
	return httpx.OK(w, st)
}

func (m *Module) cancelLogin(w http.ResponseWriter, r *http.Request) error {
	p, err := m.owned(r)
	if err != nil {
		return err
	}
	m.a.Profiles.CancelLogin(p)
	return httpx.NoContent(w)
}
