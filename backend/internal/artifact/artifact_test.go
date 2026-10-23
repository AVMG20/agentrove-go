package artifact

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/config"
	"agentrove/internal/db"
)

const owner = "11111111-1111-1111-1111-111111111111"

func setup(t *testing.T) (*Module, *http.ServeMux) {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	keys, _ := auth.NewKeys(strings.Repeat("k", 40))
	cfg := &config.Config{APIPrefix: "/api/v1", ArtifactViewTokenTTL: time.Minute}
	a := &app.App{Cfg: cfg, DB: d, Keys: keys, Auth: &auth.Service{DB: d, Cfg: cfg, Keys: keys}}
	now := db.Now()
	for i, id := range []string{owner, "22222222-2222-2222-2222-222222222222"} {
		if _, err := d.Exec(`INSERT INTO users (id, email, username, hashed_password, is_active, created_at, updated_at) VALUES (?, ?, ?, 'x', 1, ?, ?)`,
			id, string(rune('a'+i))+"@x.y", string(rune('a'+i)), now, now); err != nil {
			t.Fatal(err)
		}
	}
	m := New(a)
	mux := http.NewServeMux()
	m.Routes(mux)
	return m, mux
}

func do(mux *http.ServeMux, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestArtifactLifecycle(t *testing.T) {
	m, mux := setup(t)
	session, _ := m.a.Keys.IssueToken(owner, auth.AudienceSession, time.Hour)
	agent, _ := m.a.Keys.IssueToken(owner, auth.AudienceArtifactAgent, time.Hour)
	other, _ := m.a.Keys.IssueToken("22222222-2222-2222-2222-222222222222", auth.AudienceSession, time.Hour)

	rec := do(mux, "POST", "/api/v1/artifacts", agent, `{"title":"T","html":"<p>1</p>"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id := created["id"].(string)
	if created["html"] != "<p>1</p>" || created["version"].(float64) != 1 || created["chat_id"] != nil {
		t.Fatalf("created: %v", created)
	}

	cases := []struct {
		name, method, path, token, body string
		want                            int
	}{
		{"agent cannot publish", "PATCH", "/api/v1/artifacts/" + id, agent, `{"is_public":true}`, 403},
		{"agent cannot delete", "DELETE", "/api/v1/artifacts/" + id, agent, ``, 403},
		{"null rejected", "PATCH", "/api/v1/artifacts/" + id, session, `{"title":null}`, 422},
		{"other user 404", "GET", "/api/v1/artifacts/" + id, other, ``, 404},
		{"no auth 401", "GET", "/api/v1/artifacts", "", ``, 401},
		{"private view hidden from others", "GET", "/api/v1/artifacts/" + id + "/view", other, ``, 404},
		{"private content hidden anonymously", "GET", "/api/v1/artifacts/" + id + "/content", "", ``, 404},
		{"bad id 404", "GET", "/api/v1/artifacts/not-a-uuid", session, ``, 404},
	}
	for _, c := range cases {
		if rec := do(mux, c.method, c.path, c.token, c.body); rec.Code != c.want {
			t.Errorf("%s: got %d %s", c.name, rec.Code, rec.Body)
		}
	}

	// Content edits bump the version; visibility changes don't.
	rec = do(mux, "PATCH", "/api/v1/artifacts/"+id, agent, `{"html":"<p>2</p>"}`)
	var a map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &a)
	if a["version"].(float64) != 2 {
		t.Fatalf("after html edit: %v", a)
	}
	rec = do(mux, "PATCH", "/api/v1/artifacts/"+id, session, `{"title":"T"}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &a)
	if a["version"].(float64) != 2 {
		t.Fatalf("unchanged title must not bump: %v", a)
	}

	// Owner view carries a view token that unlocks the sandboxed content.
	rec = do(mux, "GET", "/api/v1/artifacts/"+id+"/view", session, ``)
	var view map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	path := view["content_path"].(string)
	if view["is_owner"] != true || !strings.Contains(path, "view_token=") || !strings.HasPrefix(path, "/artifacts/"+id+"/content?v=2") {
		t.Fatalf("view: %v", view)
	}
	rec = do(mux, "GET", "/api/v1"+path, "", ``)
	if rec.Code != 200 || rec.Body.String() != "<p>2</p>" {
		t.Fatalf("content: %d %s", rec.Code, rec.Body)
	}
	h := rec.Header()
	if !strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox allow-scripts") || h.Get("Referrer-Policy") != "no-referrer" ||
		h.Get("Cache-Control") != "private, no-store" || !strings.HasPrefix(h.Get("Content-Type"), "text/html") {
		t.Fatalf("headers: %v", h)
	}

	// Public: anyone can view, no token.
	do(mux, "PATCH", "/api/v1/artifacts/"+id, session, `{"is_public":true}`)
	rec = do(mux, "GET", "/api/v1/artifacts/"+id+"/view", "", ``)
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if rec.Code != 200 || view["is_owner"] != false || strings.Contains(view["content_path"].(string), "view_token") {
		t.Fatalf("public view: %d %v", rec.Code, view)
	}

	if rec := do(mux, "DELETE", "/api/v1/artifacts/"+id, session, ``); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	rec = do(mux, "GET", "/api/v1/artifacts/", session, ``)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("list after delete: %d %s", rec.Code, rec.Body)
	}
}
