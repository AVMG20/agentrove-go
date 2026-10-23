package artifact

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	if rec.Code != 200 || rec.Body.String() != WithErrorReporter("<p>2</p>") {
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

func TestWithErrorReporter(t *testing.T) {
	cases := map[string]string{
		"<!doctype html><html lang=en><head><title>x</title></head></html>": "<!doctype html><html lang=en><head>" + errorReporter + "<title>",
		"<!DOCTYPE html><html><body>x</body></html>":                        "<!DOCTYPE html><html>" + errorReporter + "<body>",
		"<!doctype html><p>x</p>":                                           "<!doctype html>" + errorReporter + "<p>",
		"<p>x</p>":                                                          errorReporter + "<p>",
	}
	for in, prefix := range cases {
		if got := WithErrorReporter(in); !strings.HasPrefix(got, prefix) {
			t.Errorf("%q: got %q", in, got)
		}
	}
	// <header> is not <head>.
	if got := WithErrorReporter("<!doctype html><header>h</header>"); !strings.HasPrefix(got, "<!doctype html>"+errorReporter+"<header>") {
		t.Errorf("header: %q", got)
	}
}

func TestApplyEdits(t *testing.T) {
	got, err := ApplyEdits("<p>a</p><p>b</p>", []Edit{{Old: "<p>a</p>", New: "<p>A</p>"}, {Old: "p>", New: "div>", ReplaceAll: true}})
	if err != nil || got != "<div>A</div><div>b</div>" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, edits := range [][]Edit{nil, {{Old: "", New: "x"}}, {{Old: "zzz", New: "x"}}, {{Old: "p>", New: "x"}}} {
		if _, err := ApplyEdits("<p>a</p>", edits); err == nil {
			t.Errorf("%v: want error", edits)
		}
	}
}

func TestHistoryErrorsAndState(t *testing.T) {
	m, mux := setup(t)
	ctx := t.Context()
	svc := m.svc
	a, err := svc.Create(ctx, owner, "T", "<p>one</p>", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Edit(ctx, a.ID, owner, []Edit{{Old: "one", New: "two"}}, nil); err != nil {
		t.Fatal(err)
	}
	restored, err := svc.Restore(ctx, a.ID, owner, 1)
	if err != nil || restored.Version != 3 || restored.HTML != "<p>one</p>" {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	if v2, err := svc.AtVersion(ctx, a.ID, owner, 2); err != nil || v2.HTML != "<p>two</p>" {
		t.Fatalf("v2: %+v %v", v2, err)
	}
	if vs, _ := svc.Versions(ctx, a.ID, owner); len(vs) != 3 || vs[0].Version != 3 {
		t.Fatalf("versions: %v", vs)
	}
	for i := 0; i < KeepVersions+2; i++ {
		html := "<p>" + strings.Repeat("x", i+1) + "</p>"
		if _, err := svc.Update(ctx, a.ID, owner, nil, &html, nil); err != nil {
			t.Fatal(err)
		}
	}
	if vs, _ := svc.Versions(ctx, a.ID, owner); len(vs) != KeepVersions {
		t.Fatalf("pruned versions: %d", len(vs))
	}
	if _, err := svc.Restore(ctx, a.ID, owner, 1); err == nil {
		t.Fatal("restoring a pruned version must fail")
	}

	cur, _ := svc.Get(ctx, a.ID, owner)
	session, _ := m.a.Keys.IssueToken(owner, auth.AudienceSession, time.Hour)
	other, _ := m.a.Keys.IssueToken("22222222-2222-2222-2222-222222222222", auth.AudienceSession, time.Hour)
	base := "/api/v1/artifacts/" + a.ID
	body := `{"version":` + strconv.Itoa(cur.Version) + `,"messages":["boom","boom"," "]}`
	if rec := do(mux, "POST", base+"/errors", session, body); rec.Code != 204 {
		t.Fatalf("report: %d %s", rec.Code, rec.Body)
	}
	do(mux, "POST", base+"/errors", session, `{"version":1,"messages":["stale"]}`)
	if rec := do(mux, "POST", base+"/errors", other, body); rec.Code != 404 {
		t.Fatalf("other user report: %d", rec.Code)
	}
	if errs, _ := svc.Errors(ctx, a.ID, cur.Version); len(errs) != 1 || errs[0].Message != "boom" {
		t.Fatalf("errors: %v", errs)
	}

	if rec := do(mux, "PUT", base+"/state/answers", session, `{"value":{"q1":"yes"}}`); rec.Code != 204 {
		t.Fatalf("set state: %d %s", rec.Code, rec.Body)
	}
	if rec := do(mux, "PUT", base+"/state/bad", session, `{"value":`+`"`+strings.Repeat("x", MaxStateValue)+`"}`); rec.Code != 422 {
		t.Fatalf("oversized state: %d", rec.Code)
	}
	if rec := do(mux, "GET", base+"/state", other, ``); rec.Code != 404 {
		t.Fatalf("other user state: %d", rec.Code)
	}
	rec := do(mux, "GET", base+"/state", session, ``)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"state":{"answers":{"q1":"yes"}}}` {
		t.Fatalf("state: %d %s", rec.Code, rec.Body)
	}
	do(mux, "DELETE", base+"/state/answers", session, ``)
	if st, _ := svc.State(ctx, a.ID, owner); len(st) != 0 {
		t.Fatalf("state after delete: %v", st)
	}
}

// replace_all of a short string by a long one multiplies the page; it must be
// refused before the result is built.
func TestApplyEditsRefusesAmplification(t *testing.T) {
	page := strings.Repeat("a", MaxHTMLLength)
	huge := strings.Repeat("b", 1<<20)
	if _, err := ApplyEdits(page, []Edit{{Old: "a", New: huge, ReplaceAll: true}}); err == nil {
		t.Fatal("amplifying edit accepted")
	}
	if got, err := ApplyEdits("<p>a</p>", []Edit{{Old: "a", New: "bb", ReplaceAll: true}}); err != nil || got != "<p>bb</p>" {
		t.Errorf("small edit = %q, %v", got, err)
	}
}
