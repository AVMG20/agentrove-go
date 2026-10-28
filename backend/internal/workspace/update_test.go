package workspace

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/db"
	"agentrove/internal/events"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"
	"agentrove/internal/store"
)

// A folder icon and color are set and reset independently of the name, and
// the user's other tabs are told to refetch.
func TestUpdateFolderAppearance(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	const userID = "11111111-1111-1111-1111-111111111111"
	now := db.Now()
	if _, err := d.Exec(`INSERT INTO users (id, email, username, hashed_password, is_active, created_at, updated_at) VALUES (?, 'a@b.c', 'a', 'x', 1, ?, ?)`, userID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO workspaces (id, name, user_id, sandbox_id, workspace_path, created_at, updated_at) VALUES ('w', 'proj', ?, 'sb', '/tmp', ?, ?)`, userID, now, now); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	sub := hub.Subscribe(events.UserChats(userID), 8)
	defer sub.Close()
	m := New(&app.App{DB: d, Hub: hub, Store: &store.Store{DB: d}, Sandboxes: &sandbox.Manager{}}, nil)
	patch := func(body string) (*models.Workspace, error) {
		req := httptest.NewRequest("PATCH", "/api/v1/workspaces/w", strings.NewReader(body))
		req.SetPathValue("workspace_id", "w")
		req = req.WithContext(auth.WithUser(req.Context(), &models.User{ID: userID}))
		rec := httptest.NewRecorder()
		if err := m.update(rec, req); err != nil {
			return nil, err
		}
		var ws models.Workspace
		return &ws, json.Unmarshal(rec.Body.Bytes(), &ws)
	}
	str := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}

	ws, err := patch(`{"icon": "rocket", "color": "blue"}`)
	if err != nil || str(ws.Icon) != "rocket" || str(ws.Color) != "blue" || ws.Name != "proj" {
		t.Fatalf("set = %+v, %v", ws, err)
	}
	select {
	case msg := <-sub.C:
		if string(msg) != `{"kind":"workspaces_updated"}` {
			t.Fatalf("event = %s", msg)
		}
	default:
		t.Fatal("no workspaces_updated event")
	}
	// Absent fields stay; null resets to the default.
	if ws, err = patch(`{"icon": null}`); err != nil || ws.Icon != nil || str(ws.Color) != "blue" {
		t.Fatalf("reset icon = %+v, %v", ws, err)
	}
	stored, err := (&store.Store{DB: d}).GetWorkspace(t.Context(), userID, "w")
	if err != nil || stored.Icon != nil || str(stored.Color) != "blue" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	for _, bad := range []string{`{"icon": "Rocket!"}`, `{"color": 3}`, `{"color": "` + strings.Repeat("a", 40) + `"}`} {
		if _, err := patch(bad); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}
