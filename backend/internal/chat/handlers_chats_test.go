package chat

import (
	"context"
	"testing"

	"agentrove/internal/app"
	"agentrove/internal/db"
	"agentrove/internal/sandbox"
	appstore "agentrove/internal/store"
)

// withApp gives a test module the DB-backed parts of an App; sandboxes have
// no provider, so nothing runs in a box.
func withApp(m *Module, d *db.DB) {
	m.a = &app.App{DB: d, Store: &appstore.Store{DB: d}, Sandboxes: &sandbox.Manager{}}
}

// Deleting a project's last chat must leave the project alone: a project
// with no chats is a normal state, and tearing it down here skipped the
// workspace module's own cleanup.
func TestDeletingLastChatKeepsProject(t *testing.T) {
	m, d := newTestModule(t)
	withApp(m, d)
	workspaceDeleted := false
	m.a.Hooks.OnWorkspaceDeleted = append(m.a.Hooks.OnWorkspaceDeleted, func(string) { workspaceDeleted = true })

	if err := m.DeleteChats(context.Background(), testUser, []string{testChat}); err != nil {
		t.Fatal(err)
	}
	var chatDeleted, wsDeleted bool
	if err := d.QueryRow(`SELECT deleted_at IS NOT NULL FROM chats WHERE id = ?`, testChat).Scan(&chatDeleted); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT deleted_at IS NOT NULL FROM workspaces WHERE id = ?`, testWS).Scan(&wsDeleted); err != nil {
		t.Fatal(err)
	}
	if !chatDeleted || wsDeleted || workspaceDeleted {
		t.Fatalf("chat deleted=%v, workspace deleted=%v (hook %v); want only the chat", chatDeleted, wsDeleted, workspaceDeleted)
	}
}
