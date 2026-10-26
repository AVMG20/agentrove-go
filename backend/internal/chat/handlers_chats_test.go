package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"agentrove/internal/app"
	"agentrove/internal/db"
	"agentrove/internal/models"
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

// A chat created with worktree_chat_id joins that chat's workspace and
// worktree, and deleting it leaves the shared worktree to its owner.
func TestCreateChatInAnotherChatsWorktree(t *testing.T) {
	m, d := newTestModule(t)
	withApp(m, d)
	addProfile(t, withProfiles(t, m), models.ProviderClaude, "Work")
	ctx := context.Background()
	wt := ".worktrees/33333333"
	if _, err := d.Exec(`UPDATE chats SET worktree_cwd = ? WHERE id = ?`, wt, testChat); err != nil {
		t.Fatal(err)
	}
	src := testChat
	c, err := m.CreateChat(ctx, testUser, "Review", "", &src, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.WorkspaceID != testWS || c.WorktreeCwd == nil || *c.WorktreeCwd != wt {
		t.Fatalf("workspace=%q worktree=%v; want %q in %q", c.WorkspaceID, c.WorktreeCwd, testWS, wt)
	}
	if err := m.DeleteChats(ctx, testUser, []string{c.ID}); err != nil {
		t.Fatal(err)
	}
	var srcDeleted bool
	if err := d.QueryRow(`SELECT deleted_at IS NOT NULL FROM chats WHERE id = ?`, testChat).Scan(&srcDeleted); err != nil {
		t.Fatal(err)
	}
	if srcDeleted {
		t.Fatal("deleting the new chat deleted the chat whose worktree it used")
	}

	missing := "44444444-0000-0000-0000-000000000000"
	if _, err := m.CreateChat(ctx, testUser, "x", "", &missing, "", ""); err == nil || !strings.Contains(err.Error(), "Chat not found") {
		t.Fatalf("unknown worktree_chat_id: err = %v", err)
	}
}

func addCheckpoint(t *testing.T, d *db.DB, i int) {
	t.Helper()
	msg := fmt.Sprintf("m%02d", i)
	at := db.T(time.Unix(int64(1000+i), 0))
	if _, err := d.Exec(`INSERT INTO messages (id, chat_id, role, created_at, updated_at) VALUES (?, ?, 'assistant', ?, ?)`, msg, testChat, at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO chat_checkpoints (id, chat_id, assistant_message_id, base_head, pre_run_diff, created_at, updated_at)
		VALUES (?, ?, ?, 'h', 'diff', ?, ?)`, "cp"+msg, testChat, msg, at, at); err != nil {
		t.Fatal(err)
	}
}

func checkpointIDs(t *testing.T, d *db.DB) []string {
	t.Helper()
	rows, err := d.Query(`SELECT id FROM chat_checkpoints ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// Each checkpoint can hold a whole dirty tree: only the latest turns keep
// one, and deleting the chat drops them all.
func TestCheckpointsArePruned(t *testing.T) {
	m, d := newTestModule(t)
	withApp(m, d)
	for i := range keepCheckpoints + 3 {
		addCheckpoint(t, d, i)
	}
	m.pruneCheckpoints(context.Background(), testChat)
	ids := checkpointIDs(t, d)
	if len(ids) != keepCheckpoints || ids[0] != "cpm03" {
		t.Fatalf("kept %v", ids)
	}
	if err := m.DeleteChats(context.Background(), testUser, []string{testChat}); err != nil {
		t.Fatal(err)
	}
	if ids := checkpointIDs(t, d); len(ids) != 0 {
		t.Fatalf("checkpoints left after delete: %v", ids)
	}
}

func TestPlaceholderTitle(t *testing.T) {
	prompt := "  Fix the   login\nbug on the settings page please"
	for title, want := range map[string]bool{
		"Fix the login bug on the":     true, // client: prompt start, whitespace collapsed
		"New Chat":                     true,
		"":                             true,
		"Nightly sync · Jan 02, 15:04": false, // automation run
		"Reviewer":                     false, // deliberate title (e.g. a review chat)
		"Login fix":                    false, // explicit MCP title or a rename
	} {
		if got := isPlaceholderTitle(title, prompt); got != want {
			t.Errorf("isPlaceholderTitle(%q) = %v, want %v", title, got, want)
		}
	}
	long := strings.Repeat("é", 300)
	if got := clipTitle(long); !utf8.ValidString(got) || utf8.RuneCountInString(got) != 255 {
		t.Errorf("clipTitle split or miscounted: %d runes", utf8.RuneCountInString(got))
	}
}

// A rename during the first turn must survive the generated title.
func TestGeneratedTitleKeepsRename(t *testing.T) {
	m, d := newTestModule(t)
	withApp(m, d)
	ctx := context.Background()
	if _, err := d.Exec(`UPDATE chats SET title = 'Renamed' WHERE id = ?`, testChat); err != nil {
		t.Fatal(err)
	}
	if m.replaceTitle(ctx, testChat, "c", "Generated") {
		t.Fatal("replaced a renamed title")
	}
	if !m.replaceTitle(ctx, testChat, "Renamed", "Generated") {
		t.Fatal("did not replace the title it started with")
	}
}
