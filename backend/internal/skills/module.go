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
	mux.HandleFunc("GET "+p+"/{source}/{skill_name}/files", m.a.Auth.Require(m.files))
	mux.HandleFunc("PUT "+p+"/{source}/{skill_name}", m.a.Auth.Require(m.update))
}

func (m *Module) service(r *http.Request) (*Service, error) {
	id := r.URL.Query().Get("workspace_id")
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
	s, err := m.service(r)
	if err != nil {
		return err
	}
	name := r.PathValue("skill_name")
	files, err := s.Files(r.PathValue("source"), name)
	if err != nil {
		return mapErr(err)
	}
	return httpx.OK(w, map[string]any{"name": name, "files": files})
}

func (m *Module) update(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Files []FileEntry `json:"files"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Files == nil {
		return httpx.Unprocessable("files is required")
	}
	for _, f := range body.Files {
		if len(f.Path) > 4096 || len(f.Content) > 10_000_000 {
			return httpx.Unprocessable("file path or content too long")
		}
	}
	s, err := m.service(r)
	if err != nil {
		return err
	}
	out, err := s.Update(r.PathValue("source"), r.PathValue("skill_name"), body.Files)
	if err != nil {
		return mapErr(err)
	}
	return httpx.OK(w, out)
}
