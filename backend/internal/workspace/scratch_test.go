package workspace

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/db"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"
	"agentrove/internal/store"

	"github.com/google/uuid"
)

// The scratch workspace is found again rather than duplicated, and stays out
// of the project list.
func TestScratchWorkspaceIsHiddenAndUnique(t *testing.T) {
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
	insert := func(id, source string) error {
		_, err := d.Exec(`INSERT INTO workspaces (id, name, user_id, sandbox_id, workspace_path, source_type, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, id, userID, "sb-"+id, "/w/"+id, source, now, now)
		return err
	}
	if err := insert("project", "empty"); err != nil {
		t.Fatal(err)
	}
	if err := insert("scratch", models.ScratchSource); err != nil {
		t.Fatal(err)
	}
	if err := insert("scratch2", models.ScratchSource); err == nil {
		t.Fatal("a second scratch workspace was accepted")
	}

	m := New(&app.App{DB: d, Sandboxes: &sandbox.Manager{}}, nil)
	ws, err := m.ensureScratch(context.Background(), userID)
	if err != nil || ws.ID != "scratch" {
		t.Fatalf("ensureScratch = %v, %v; want the existing one", ws, err)
	}

	req := httptest.NewRequest("GET", "/api/v1/workspaces", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &models.User{ID: userID}))
	rec := httptest.NewRecorder()
	if err := m.list(rec, req); err != nil {
		t.Fatal(err)
	}
	var out models.Paginated[models.Workspace]
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "project" {
		t.Fatalf("list = %+v; want only the project", out.Items)
	}

	s := &store.Store{DB: d}
	for wsID, want := range map[string]bool{"project": false, "scratch": true} {
		chatID := uuid.NewString()
		if _, err := d.Exec(`INSERT INTO chats (id, title, user_id, workspace_id, created_at, updated_at) VALUES (?, 't', ?, ?, ?, ?)`, chatID, userID, wsID, now, now); err != nil {
			t.Fatal(err)
		}
		c, err := s.GetChat(context.Background(), userID, chatID)
		if err != nil || c.NoProject != want {
			t.Fatalf("chat in %s: no_project = %v (%v), want %v", wsID, c != nil && c.NoProject, err, want)
		}
	}
}
