package artifact

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
)

// ContentPolicy sandboxes the agent-written page (no allow-same-origin) so it
// gets an opaque origin even when opened directly and can't use the app's
// storage or call the API as the viewer.
const ContentPolicy = "sandbox allow-scripts allow-forms allow-modals allow-popups " +
	"allow-popups-to-escape-sandbox allow-downloads"

type Module struct {
	a   *app.App
	svc *Service
}

func New(a *app.App) *Module { return &Module{a: a, svc: &Service{DB: a.DB}} }

func (m *Module) Service() *Service { return m.svc }

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix + "/artifacts"
	for _, base := range []string{p, p + "/{$}"} {
		mux.HandleFunc("GET "+base, m.withActor(m.list))
		mux.HandleFunc("POST "+base, m.withActor(m.create))
	}
	mux.HandleFunc("GET "+p+"/{id}", m.withActor(m.get))
	mux.HandleFunc("PATCH "+p+"/{id}", m.withActor(m.update))
	mux.HandleFunc("DELETE "+p+"/{id}", m.withActor(m.delete))
	mux.HandleFunc("GET "+p+"/{id}/view", httpx.Wrap(m.view))
	mux.HandleFunc("GET "+p+"/{id}/content", httpx.Wrap(m.content))
	mux.HandleFunc("GET "+p+"/{id}/versions", m.withActor(m.versions))
	mux.HandleFunc("POST "+p+"/{id}/errors", m.withActor(m.reportErrors))
	mux.HandleFunc("GET "+p+"/{id}/state", m.withActor(m.state))
	mux.HandleFunc("PUT "+p+"/{id}/state/{key}", m.withActor(m.setState))
	mux.HandleFunc("DELETE "+p+"/{id}/state/{key}", m.withActor(m.deleteState))
}

// actor is the caller: a user session token, or the artifacts-only agent
// token, which may publish and edit but not delete or change visibility.
// MCP tokens are refused here; agents reach artifacts through /mcp.
type actor struct {
	user        *models.User
	agentScoped bool
}

type actorKey struct{}

func (m *Module) resolveActor(r *http.Request) (*actor, error) {
	token := auth.TokenFromRequest(r)
	if u, err := m.a.Auth.Authenticate(r.Context(), token); err == nil {
		return &actor{user: u}, nil
	}
	if token != "" {
		if sub, err := m.a.Keys.VerifyToken(token, auth.AudienceArtifactAgent); err == nil {
			if u, err := m.a.Auth.GetUser(r.Context(), sub); err == nil && u.IsActive &&
				(m.a.Sandboxes == nil || m.a.Sandboxes.FromUserBox(r, u.ID)) {
				return &actor{user: u, agentScoped: true}, nil
			}
		}
	}
	return nil, httpx.Unauthorized("Could not validate credentials")
}

func (m *Module) withActor(h httpx.HandlerFunc) http.HandlerFunc {
	return httpx.Wrap(func(w http.ResponseWriter, r *http.Request) error {
		act, err := m.resolveActor(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			return err
		}
		return h(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, act)))
	})
}

func actorFrom(r *http.Request) *actor { return r.Context().Value(actorKey{}).(*actor) }

// optionalViewer: anonymous without an Authorization header; credentials that
// don't validate are a 401 so the app refreshes an expired session.
func (m *Module) optionalViewer(w http.ResponseWriter, r *http.Request) (*models.User, error) {
	if r.Header.Get("Authorization") == "" {
		return nil, nil
	}
	act, err := m.resolveActor(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", "Bearer")
		return nil, err
	}
	return act.user, nil
}

// MaxListLimit caps one page of GET /artifacts.
const MaxListLimit = 100

func (m *Module) list(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	o := ListOptions{Query: strings.TrimSpace(q.Get("q")), ChatID: q.Get("chat_id")}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return httpx.Unprocessable("limit must be a positive integer")
		}
		o.Limit = min(n, MaxListLimit)
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return httpx.Unprocessable("offset must be a non-negative integer")
		}
		o.Offset = n
	}
	items, err := m.svc.List(r.Context(), actorFrom(r).user.ID, o)
	if err != nil {
		return err
	}
	return httpx.OK(w, items)
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Title  string  `json:"title"`
		HTML   string  `json:"html"`
		ChatID *string `json:"chat_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	a, err := m.svc.Create(r.Context(), actorFrom(r).user.ID, body.Title, body.HTML, body.ChatID)
	if err != nil {
		return err
	}
	return httpx.Created(w, a.Detail())
}

func (m *Module) get(w http.ResponseWriter, r *http.Request) error {
	a, err := m.svc.Get(r.Context(), r.PathValue("id"), actorFrom(r).user.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, a.Detail())
}

func (m *Module) update(w http.ResponseWriter, r *http.Request) error {
	var raw map[string]json.RawMessage
	if err := httpx.Decode(r, &raw); err != nil {
		return err
	}
	var title, html *string
	var isPublic, pinned *bool
	for key, val := range raw {
		if string(val) == "null" {
			return httpx.Unprocessable(key + ": Field cannot be null")
		}
		var err error
		switch key {
		case "title":
			title = new(string)
			err = json.Unmarshal(val, title)
		case "html":
			html = new(string)
			err = json.Unmarshal(val, html)
		case "is_public":
			isPublic = new(bool)
			err = json.Unmarshal(val, isPublic)
		case "pinned":
			pinned = new(bool)
			err = json.Unmarshal(val, pinned)
		}
		if err != nil {
			return httpx.Unprocessable(fmt.Sprintf("%s: %v", key, err))
		}
	}
	act := actorFrom(r)
	if isPublic != nil && act.agentScoped {
		return httpx.Forbidden("Only the user can change the visibility of an artifact")
	}
	if pinned != nil {
		if act.agentScoped {
			return httpx.Forbidden("Only the user can pin an artifact")
		}
		if err := m.svc.SetPinned(r.Context(), r.PathValue("id"), act.user.ID, *pinned); err != nil {
			return err
		}
	}
	var a *Artifact
	var err error
	if title != nil || html != nil || isPublic != nil {
		a, err = m.svc.Update(r.Context(), r.PathValue("id"), act.user.ID, title, html, isPublic)
	} else {
		a, err = m.svc.Get(r.Context(), r.PathValue("id"), act.user.ID)
	}
	if err != nil {
		return err
	}
	return httpx.OK(w, a.Detail())
}

func (m *Module) delete(w http.ResponseWriter, r *http.Request) error {
	act := actorFrom(r)
	if act.agentScoped {
		return httpx.Forbidden("Only the user can delete an artifact")
	}
	if err := m.svc.Delete(r.Context(), r.PathValue("id"), act.user.ID); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (m *Module) view(w http.ResponseWriter, r *http.Request) error {
	viewer, err := m.optionalViewer(w, r)
	if err != nil {
		return err
	}
	viewerID := ""
	if viewer != nil {
		viewerID = viewer.ID
	}
	a, err := m.svc.Viewable(r.Context(), r.PathValue("id"), viewerID, false)
	if err != nil {
		return err
	}
	// The version busts the browser cache after edits; a private artifact's
	// iframe can't send the bearer header, so it carries a view token instead.
	contentPath := fmt.Sprintf("/artifacts/%s/content?v=%d", a.ID, a.Version)
	if !a.IsPublic {
		tok, err := m.a.Keys.IssueToken(a.ID, auth.AudienceArtifactView, m.a.Cfg.ArtifactViewTokenTTL)
		if err != nil {
			return err
		}
		contentPath += "&view_token=" + tok
	}
	return httpx.OK(w, struct {
		Artifact
		IsOwner     bool   `json:"is_owner"`
		ContentPath string `json:"content_path"`
	}{*a, viewer != nil && viewer.ID == a.UserID, contentPath})
}

func (m *Module) content(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	granted := false
	if vt := r.URL.Query().Get("view_token"); vt != "" {
		if sub, err := m.a.Keys.VerifyToken(vt, auth.AudienceArtifactView); err == nil && strings.EqualFold(sub, id) {
			granted = true
		}
	}
	viewer, err := m.optionalViewer(w, r)
	if err != nil {
		return err
	}
	viewerID := ""
	if viewer != nil {
		viewerID = viewer.ID
	}
	a, err := m.svc.Viewable(r.Context(), id, viewerID, granted)
	if err != nil {
		return err
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", ContentPolicy)
	// Keeps the view token in the URL from leaking to sites the page links to.
	h.Set("Referrer-Policy", "no-referrer")
	if a.IsPublic {
		h.Set("Cache-Control", "no-cache")
	} else {
		h.Set("Cache-Control", "private, no-store")
	}
	w.WriteHeader(http.StatusOK)
	_, err = w.Write([]byte(WithErrorReporter(a.HTML)))
	return err
}

func (m *Module) versions(w http.ResponseWriter, r *http.Request) error {
	list, err := m.svc.Versions(r.Context(), r.PathValue("id"), actorFrom(r).user.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, list)
}

func (m *Module) reportErrors(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Version  int      `json:"version"`
		Messages []string `json:"messages"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if len(body.Messages) > 20 {
		body.Messages = body.Messages[:20]
	}
	if err := m.svc.ReportErrors(r.Context(), r.PathValue("id"), actorFrom(r).user.ID, body.Version, body.Messages); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (m *Module) state(w http.ResponseWriter, r *http.Request) error {
	st, err := m.svc.State(r.Context(), r.PathValue("id"), actorFrom(r).user.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]any{"state": st})
}

func (m *Module) setState(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Value json.RawMessage `json:"value"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if err := m.svc.SetState(r.Context(), r.PathValue("id"), actorFrom(r).user.ID, r.PathValue("key"), body.Value); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (m *Module) deleteState(w http.ResponseWriter, r *http.Request) error {
	if err := m.svc.SetState(r.Context(), r.PathValue("id"), actorFrom(r).user.ID, r.PathValue("key"), nil); err != nil {
		return err
	}
	return httpx.NoContent(w)
}
