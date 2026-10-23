package channel

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"unicode/utf8"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/httpx"
)

type Module struct {
	a   *app.App
	svc *Service
}

func New(a *app.App) *Module { return &Module{a: a, svc: NewService(a)} }

func (m *Module) Service() *Service { return m.svc }

// Start recovers channels left mid-turn by a restart.
func (m *Module) Start(ctx context.Context) {
	if err := m.svc.Recover(ctx); err != nil {
		slog.Error("channel recovery failed", "err", err)
	}
}

func (m *Module) Stop(ctx context.Context) { m.svc.Shutdown(ctx) }

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix + "/channels"
	req := m.a.Auth.Require
	for _, base := range []string{p, p + "/{$}"} {
		mux.HandleFunc("POST "+base, req(m.create))
		mux.HandleFunc("GET "+base, req(m.list))
	}
	mux.HandleFunc("GET "+p+"/{id}", req(m.get))
	mux.HandleFunc("PATCH "+p+"/{id}", req(m.rename))
	mux.HandleFunc("DELETE "+p+"/{id}", req(m.delete))
	mux.HandleFunc("GET "+p+"/{id}/messages", req(m.messages))
	mux.HandleFunc("POST "+p+"/{id}/messages", req(m.post))
	mux.HandleFunc("GET "+p+"/{id}/messages/{message_id}/activity", req(m.activity))
	mux.HandleFunc("POST "+p+"/{id}/queue", req(m.queueAdd))
	mux.HandleFunc("GET "+p+"/{id}/queue", req(m.queueList))
	mux.HandleFunc("PATCH "+p+"/{id}/queue/{message_id}", req(m.queueUpdate))
	mux.HandleFunc("DELETE "+p+"/{id}/queue/{message_id}", req(m.queueDelete))
	mux.HandleFunc("POST "+p+"/{id}/queue/{message_id}/send-now", req(m.queueSendNow))
	mux.HandleFunc("POST "+p+"/{id}/stop", req(m.stop))
	mux.HandleFunc("GET "+p+"/{id}/permissions", req(m.permissions))
	mux.HandleFunc("GET "+p+"/{id}/member-activity", req(m.memberActivity))
}

func (m *Module) channel(r *http.Request) (*Channel, error) {
	return m.svc.Get(r.Context(), r.PathValue("id"), auth.UserFrom(r.Context()).ID)
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) error {
	var in CreateInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ch, err := m.svc.Create(r.Context(), auth.UserFrom(r.Context()).ID, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, m.svc.Read(ch))
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) error {
	chans, err := m.svc.List(r.Context(), auth.UserFrom(r.Context()).ID, r.URL.Query().Get("workspace_id"))
	if err != nil {
		return err
	}
	out := make([]Read, 0, len(chans))
	for _, ch := range chans {
		out = append(out, m.svc.Read(ch))
	}
	return httpx.OK(w, out)
}

func (m *Module) get(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	return httpx.OK(w, m.svc.Read(ch))
}

func (m *Module) rename(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	ch, err = m.svc.Rename(r.Context(), ch, body.Name)
	if err != nil {
		return err
	}
	return httpx.OK(w, m.svc.Read(ch))
}

func (m *Module) delete(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	if err := m.svc.Delete(context.WithoutCancel(r.Context()), ch); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (m *Module) messages(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	var after int64
	if v := r.URL.Query().Get("after_seq"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return httpx.Unprocessable("after_seq must be an integer >= 0")
		}
		after = n
	}
	msgs, err := m.svc.Messages(r.Context(), ch.ID, after)
	if err != nil {
		return err
	}
	return httpx.OK(w, msgs)
}

func (m *Module) activity(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	out, err := m.svc.Activity(r.Context(), ch, r.PathValue("message_id"))
	if err != nil {
		return err
	}
	return httpx.OK(w, out)
}

// formMessage reads the multipart content + attached_files and stores files.
func (m *Module) formMessage(r *http.Request, ch *Channel) (string, []savedFile, error) {
	if err := httpx.ParseForm(r, 32<<20); err != nil {
		return "", nil, err
	}
	content := r.FormValue("content")
	if n := utf8.RuneCountInString(content); n < 1 || n > 100000 {
		return "", nil, httpx.Unprocessable("content must be between 1 and 100000 characters")
	}
	var files []savedFile
	if r.MultipartForm != nil {
		var err error
		files, err = m.svc.saveFiles(r.Context(), ch, r.MultipartForm.File["attached_files"])
		if err != nil {
			return "", nil, err
		}
	}
	return content, files, nil
}

func (m *Module) post(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	content, files, err := m.formMessage(r, ch)
	if err != nil {
		return err
	}
	msg, err := m.svc.Post(context.WithoutCancel(r.Context()), ch, content, files)
	if err != nil {
		return err
	}
	return httpx.Created(w, msg)
}

func (m *Module) queueAdd(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	content, files, err := m.formMessage(r, ch)
	if err != nil {
		return err
	}
	q, err := m.svc.queueAdd(r.Context(), ch.ID, content, files)
	if err != nil {
		return err
	}
	m.svc.DispatchQueued(context.WithoutCancel(r.Context()), ch)
	return httpx.Created(w, map[string]any{"id": q.ID, "queued_at": q.QueuedAt})
}

func (m *Module) queueList(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	items, err := m.svc.queueList(r.Context(), ch.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, items)
}

func (m *Module) queueUpdate(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if n := utf8.RuneCountInString(body.Content); n < 1 || n > 100000 {
		return httpx.Unprocessable("content must be between 1 and 100000 characters")
	}
	q, err := m.svc.queueUpdate(r.Context(), ch.ID, r.PathValue("message_id"), body.Content)
	if err != nil {
		return err
	}
	if q == nil {
		return httpx.NotFound("Queued message not found")
	}
	return httpx.OK(w, q)
}

func (m *Module) queueDelete(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	q, err := m.svc.queueDelete(r.Context(), ch.ID, r.PathValue("message_id"))
	if err != nil {
		return err
	}
	if q == nil {
		return httpx.NotFound("Queued message not found")
	}
	return httpx.NoContent(w)
}

func (m *Module) queueSendNow(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	if _, err := m.svc.SendQueuedNow(context.WithoutCancel(r.Context()), ch, r.PathValue("message_id")); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (m *Module) stop(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	m.svc.Stop(context.WithoutCancel(r.Context()), ch)
	return httpx.NoContent(w)
}

func (m *Module) permissions(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	return httpx.OK(w, m.svc.Permissions(ch))
}

func (m *Module) memberActivity(w http.ResponseWriter, r *http.Request) error {
	ch, err := m.channel(r)
	if err != nil {
		return err
	}
	return httpx.OK(w, m.svc.MemberActivity(ch))
}
