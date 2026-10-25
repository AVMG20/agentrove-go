package skills

import (
	"errors"
	"net/http"
	"strings"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/httpx"
)

type Module struct{ a *app.App }

func New(a *app.App) *Module { return &Module{a: a} }

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix + "/skills"
	mux.HandleFunc("GET "+p, m.a.Auth.Require(m.list))
	mux.HandleFunc("GET "+p+"/{$}", m.a.Auth.Require(m.list))
	mux.HandleFunc("POST "+p, m.a.Auth.Require(m.create))
	mux.HandleFunc("POST "+p+"/{$}", m.a.Auth.Require(m.create))
	mux.HandleFunc("GET "+p+"/{source}/{skill_name}/files", m.a.Auth.Require(m.files))
	mux.HandleFunc("PUT "+p+"/{source}/{skill_name}", m.a.Auth.Require(m.update))
	mux.HandleFunc("DELETE "+p+"/{source}/{skill_name}", m.a.Auth.Require(m.delete))
}

// scope is the ?scope= filter: "" (every root, workspace required), project,
// user (global skills; no workspace needed) or plugin.
func scope(r *http.Request) (string, error) {
	switch sc := r.URL.Query().Get("scope"); sc {
	case "", ScopeProject, ScopeUser, ScopePlugin:
		return sc, nil
	default:
		return "", httpx.Unprocessable("scope must be project, user or plugin")
	}
}

func (m *Module) service(r *http.Request) (*Service, error) {
	id := r.URL.Query().Get("workspace_id")
	if id == "" && r.URL.Query().Get("scope") == ScopeUser {
		return NewUserService(m.a.Cfg, auth.UserFrom(r.Context()).ID), nil
	}
	if id == "" {
		return nil, httpx.Unprocessable("workspace_id is required")
	}
	ws, err := m.a.Store.GetWorkspace(r.Context(), auth.UserFrom(r.Context()).ID, id)
	if err != nil {
		return nil, err
	}
	return NewService(m.a.Cfg, ws), nil
}

func mapErr(err error) error {
	msg := err.Error()
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound(strings.TrimPrefix(msg, ErrNotFound.Error()+": "))
	case errors.Is(err, ErrInvalid):
		return httpx.BadRequest(strings.TrimPrefix(msg, ErrInvalid.Error()+": "))
	case errors.Is(err, ErrConflict):
		return httpx.Conflict(strings.TrimPrefix(msg, ErrConflict.Error()+": "))
	}
	return err
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) error {
	s, err := m.service(r)
	if err != nil {
		return err
	}
	return httpx.OK(w, s.List())
}

func (m *Module) files(w http.ResponseWriter, r *http.Request) error {
	sc, err := scope(r)
	if err != nil {
		return err
	}
	s, err := m.service(r)
	if err != nil {
		return err
	}
	name := r.PathValue("skill_name")
	files, err := s.Files(r.PathValue("source"), name, sc)
	if err != nil {
		return mapErr(err)
	}
	return httpx.OK(w, map[string]any{"name": name, "files": files})
}

func decodeFiles(r *http.Request, body any, files func() []FileEntry) error {
	if err := httpx.Decode(r, body); err != nil {
		return err
	}
	if files() == nil {
		return httpx.Unprocessable("files is required")
	}
	for _, f := range files() {
		if len(f.Path) > 4096 || len(f.Content) > 10_000_000 {
			return httpx.Unprocessable("file path or content too long")
		}
	}
	return nil
}

func (m *Module) update(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Files []FileEntry `json:"files"`
	}
	if err := decodeFiles(r, &body, func() []FileEntry { return body.Files }); err != nil {
		return err
	}
	sc, err := scope(r)
	if err != nil {
		return err
	}
	s, err := m.service(r)
	if err != nil {
		return err
	}
	out, err := s.Update(r.PathValue("source"), r.PathValue("skill_name"), sc, body.Files)
	if err != nil {
		return mapErr(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Source string      `json:"source"`
		Name   string      `json:"name"`
		Scope  string      `json:"scope"`
		Files  []FileEntry `json:"files"`
	}
	if err := decodeFiles(r, &body, func() []FileEntry { return body.Files }); err != nil {
		return err
	}
	if body.Source == "" {
		body.Source = Source
	}
	s, err := m.service(r)
	if err != nil {
		return err
	}
	out, err := s.Create(body.Source, body.Name, body.Scope, body.Files)
	if err != nil {
		return mapErr(err)
	}
	return httpx.Created(w, out)
}

func (m *Module) delete(w http.ResponseWriter, r *http.Request) error {
	sc, err := scope(r)
	if err != nil {
		return err
	}
	s, err := m.service(r)
	if err != nil {
		return err
	}
	if err := s.Delete(r.PathValue("source"), r.PathValue("skill_name"), sc); err != nil {
		return mapErr(err)
	}
	return httpx.NoContent(w)
}
