package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/artifact"
	"agentrove/internal/auth"
	"agentrove/internal/automation"
	"agentrove/internal/channel"
	"agentrove/internal/config"
	"agentrove/internal/db"
	"agentrove/internal/events"
	"agentrove/internal/store"
)

const userID = "11111111-1111-1111-1111-111111111111"

func newModule(t *testing.T) *Module {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	keys, _ := auth.NewKeys(strings.Repeat("k", 40))
	cfg := &config.Config{APIPrefix: "/api/v1", BaseURL: "http://api.test", FrontendURL: "http://app.test", MCPEnabled: true,
		ArtifactsEnabled: true, MCPTokenLifetime: time.Hour, StoragePath: t.TempDir()}
	a := &app.App{Cfg: cfg, DB: d, Keys: keys, Hub: events.NewHub(), Store: &store.Store{DB: d, Keys: keys}}
	a.Auth = &auth.Service{DB: d, Cfg: cfg, Keys: keys}
	now := db.Now()
	if _, err := d.Exec(`INSERT INTO users (id, email, username, hashed_password, is_active, created_at, updated_at) VALUES (?, 'a@b.c', 'a', 'x', 1, ?, ?)`, userID, now, now); err != nil {
		t.Fatal(err)
	}
	return New(a, channel.NewService(a), automation.NewService(a), &artifact.Service{DB: d})
}

func rpc(t *testing.T, m *Module, c *caller, body string) map[string]any {
	t.Helper()
	resp, ok := m.Handle(context.Background(), c, []byte(body))
	if !ok {
		return nil
	}
	b, _ := json.Marshal(resp)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return out
}

func TestDispatch(t *testing.T) {
	m := newModule(t)
	full := &caller{userID: userID, fullAccess: true}
	artifactsOnly := &caller{userID: userID}
	cases := []struct {
		name    string
		c       *caller
		body    string
		noReply bool
		check   func(t *testing.T, out map[string]any)
	}{
		{name: "initialize", c: full, body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude","version":"1"}}}`,
			check: func(t *testing.T, out map[string]any) {
				res := out["result"].(map[string]any)
				if res["protocolVersion"] != "2025-06-18" || res["serverInfo"].(map[string]any)["name"] != "agentrove" {
					t.Fatalf("init: %v", res)
				}
				if _, ok := res["capabilities"].(map[string]any)["tools"]; !ok {
					t.Fatal("no tools capability")
				}
			}},
		{name: "initialize unknown version falls back", c: full, body: `{"jsonrpc":"2.0","id":"a","method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
			check: func(t *testing.T, out map[string]any) {
				if out["id"] != "a" || out["result"].(map[string]any)["protocolVersion"] != ProtocolVersion {
					t.Fatalf("got %v", out)
				}
			}},
		{name: "initialized notification", c: full, body: `{"jsonrpc":"2.0","method":"notifications/initialized"}`, noReply: true},
		{name: "ping", c: full, body: `{"jsonrpc":"2.0","id":2,"method":"ping"}`,
			check: func(t *testing.T, out map[string]any) {
				if r, ok := out["result"].(map[string]any); !ok || len(r) != 0 {
					t.Fatalf("ping: %v", out)
				}
			}},
		{name: "unknown method", c: full, body: `{"jsonrpc":"2.0","id":3,"method":"resources/list"}`,
			check: func(t *testing.T, out map[string]any) {
				if out["error"].(map[string]any)["code"].(float64) != codeMethodNotFound {
					t.Fatalf("got %v", out)
				}
			}},
		{name: "parse error", c: full, body: `{not json`,
			check: func(t *testing.T, out map[string]any) {
				if out["error"].(map[string]any)["code"].(float64) != codeParse {
					t.Fatalf("got %v", out)
				}
			}},
		{name: "full tool list", c: full, body: `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`,
			check: func(t *testing.T, out map[string]any) {
				tools := out["result"].(map[string]any)["tools"].([]any)
				if len(tools) != len(m.tools) {
					t.Fatalf("want %d tools, got %d", len(m.tools), len(tools))
				}
				first := tools[0].(map[string]any)
				if first["inputSchema"].(map[string]any)["type"] != "object" || first["description"] == "" {
					t.Fatalf("bad tool: %v", first)
				}
			}},
		{name: "artifact-only tool list", c: artifactsOnly, body: `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`,
			check: func(t *testing.T, out map[string]any) {
				var names []string
				for _, x := range out["result"].(map[string]any)["tools"].([]any) {
					names = append(names, x.(map[string]any)["name"].(string))
				}
				if strings.Join(names, ",") != "create_artifact,update_artifact,list_artifacts,get_artifact" {
					t.Fatalf("got %v", names)
				}
			}},
		{name: "artifact-only cannot call full tools", c: artifactsOnly, body: `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"list_workspaces","arguments":{}}}`,
			check: func(t *testing.T, out map[string]any) {
				if out["error"].(map[string]any)["code"].(float64) != codeInvalidParams {
					t.Fatalf("got %v", out)
				}
			}},
		{name: "tool error is a result", c: full, body: `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_persona","arguments":{"name":"nope"}}}`,
			check: func(t *testing.T, out map[string]any) {
				res := out["result"].(map[string]any)
				if res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "No persona named") {
					t.Fatalf("got %v", res)
				}
			}},
		{name: "list models", c: full, body: `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"list_models"}}`,
			check: func(t *testing.T, out map[string]any) {
				sc := out["result"].(map[string]any)["structuredContent"].(map[string]any)
				models := sc["models"].([]any)
				if len(models) == 0 || models[0].(map[string]any)["model_id"] != "claude-opus-5-5" {
					t.Fatalf("models: %v", models)
				}
			}},
		{name: "batch with notification", c: full, body: `[{"jsonrpc":"2.0","id":9,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"}]`,
			check: func(t *testing.T, out map[string]any) {}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if strings.HasPrefix(c.body, "[") {
				resp, ok := m.Handle(context.Background(), c.c, []byte(c.body))
				list, isList := resp.([]rpcResponse)
				if !ok || !isList || len(list) != 1 || string(list[0].ID) != "9" {
					t.Fatalf("batch: %#v", resp)
				}
				return
			}
			out := rpc(t, m, c.c, c.body)
			if c.noReply {
				if out != nil {
					t.Fatalf("expected no reply, got %v", out)
				}
				return
			}
			if out == nil || out["jsonrpc"] != "2.0" {
				t.Fatalf("bad reply %v", out)
			}
			c.check(t, out)
		})
	}
}

func callTool(t *testing.T, m *Module, c *caller, name string, args any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	out := rpc(t, m, c, string(b))
	res := out["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("%s failed: %v", name, res["content"])
	}
	return res["structuredContent"].(map[string]any)
}

func TestArtifactAndPersonaTools(t *testing.T) {
	m := newModule(t)
	c := &caller{userID: userID, chatID: "33333333-3333-3333-3333-333333333333"}
	created := callTool(t, m, c, "create_artifact", map[string]any{"title": "Report", "html": "<!doctype html><p>hi</p>"})["artifact"].(map[string]any)
	id := created["id"].(string)
	if created["url"] != "http://app.test/artifacts/"+id || created["version"].(float64) != 1 || created["is_public"] != false {
		t.Fatalf("created: %v", created)
	}
	updated := callTool(t, m, c, "update_artifact", map[string]any{"artifact_id": id, "html": "<p>v2</p>"})["artifact"].(map[string]any)
	if updated["version"].(float64) != 2 {
		t.Fatalf("updated: %v", updated)
	}
	got := callTool(t, m, c, "get_artifact", map[string]any{"artifact_id": id})["artifact"].(map[string]any)
	if got["html"] != "<p>v2</p>" || got["title"] != "Report" {
		t.Fatalf("got: %v", got)
	}
	if list := callTool(t, m, c, "list_artifacts", map[string]any{})["artifacts"].([]any); len(list) != 1 {
		t.Fatalf("list: %v", list)
	}

	full := &caller{userID: userID, fullAccess: true}
	callTool(t, m, full, "create_persona", map[string]any{"name": "Critic", "content": "Be harsh"})
	callTool(t, m, full, "update_persona", map[string]any{"name": "Critic", "content": "Be fair"})
	p := callTool(t, m, full, "get_persona", map[string]any{"name": "Critic"})["persona"].(map[string]any)
	if p["content"] != "Be fair" {
		t.Fatalf("persona: %v", p)
	}
	callTool(t, m, full, "delete_persona", map[string]any{"name": "Critic"})
	if ps := callTool(t, m, full, "list_personas", map[string]any{})["personas"].([]any); len(ps) != 0 {
		t.Fatalf("personas left: %v", ps)
	}
	if cur := callTool(t, m, c, "create_artifact", map[string]any{"title": "x", "html": "y"}); cur == nil {
		t.Fatal("no result")
	}
	if got := callTool(t, m, &caller{userID: userID, fullAccess: true, chatID: "abc"}, "get_current_chat", map[string]any{}); got["chat_id"] != "abc" {
		t.Fatalf("current chat: %v", got)
	}
}

func TestHTTPAuth(t *testing.T) {
	m := newModule(t)
	mux := http.NewServeMux()
	m.Routes(mux)
	post := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	session, _ := m.a.Keys.IssueToken(userID, auth.AudienceArtifactView, time.Hour)
	if rec := post(session); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong audience: %d", rec.Code)
	}
	for _, tc := range []struct {
		full bool
		want int
	}{{true, len(m.tools)}, {false, 4}} {
		cfg, err := ServerConfig(m.a, userID, tc.full)
		if err != nil {
			t.Fatal(err)
		}
		if cfg["type"] != "http" || cfg["url"] != "http://api.test/api/v1/mcp" {
			t.Fatalf("config: %v", cfg)
		}
		tok := strings.TrimPrefix(cfg["headers"].(map[string]any)["Authorization"].(string), "Bearer ")
		rec := post(tok)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("status %d %s", rec.Code, rec.Body)
		}
		var out struct {
			Result struct {
				Tools []any `json:"tools"`
			} `json:"result"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if len(out.Result.Tools) != tc.want {
			t.Fatalf("full=%v: %d tools", tc.full, len(out.Result.Tools))
		}
	}
	// Notifications get 202 with no body.
	tok, _ := m.a.Keys.IssueToken(userID, auth.AudienceMCP, time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Fatalf("notification: %d %q", rec.Code, rec.Body)
	}
}
