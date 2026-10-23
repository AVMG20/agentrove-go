// Package workspace serves /api/v1/workspaces: creating a workspace (empty
// dir, git clone or existing local dir) provisions its sandbox; deleting it
// soft-deletes it with its chats and tears the sandbox down.
package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/db"
	"agentrove/internal/gitops"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"
	"agentrove/internal/skills"

	"github.com/google/uuid"
)

const workspacesDirName = "workspaces"

type BuiltinSlashCommand struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// TerminateFunc kills a sandbox's terminals before the sandbox goes away.
type TerminateFunc func(ctx context.Context, sandboxID string)

type Module struct {
	a         *app.App
	terminate TerminateFunc
	usage     *diskUsage
}

func New(a *app.App, terminate TerminateFunc) *Module {
	return &Module{a: a, terminate: terminate, usage: newDiskUsage()}
}

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix + "/workspaces"
	req := m.a.Auth.Require
	mux.HandleFunc("POST "+p, req(m.create))
	mux.HandleFunc("POST "+p+"/{$}", req(m.create))
	mux.HandleFunc("GET "+p, req(m.list))
	mux.HandleFunc("GET "+p+"/{$}", req(m.list))
	mux.HandleFunc("GET "+p+"/disk-usage", req(m.diskUsage))
	mux.HandleFunc("GET "+p+"/box", req(m.box))
	mux.HandleFunc("POST "+p+"/box/stop", req(m.stopBox))
	mux.HandleFunc("GET "+p+"/{workspace_id}", req(m.get))
	mux.HandleFunc("PATCH "+p+"/{workspace_id}", req(m.update))
	mux.HandleFunc("DELETE "+p+"/{workspace_id}", req(m.delete))
	mux.HandleFunc("GET "+p+"/{workspace_id}/resources", req(m.resources))
}

func (m *Module) baseDir() string { return filepath.Join(m.a.Cfg.StoragePath, workspacesDirName) }

type createRequest struct {
	Name            *string `json:"name"`
	SourceType      *string `json:"source_type"`
	WorkspacePath   *string `json:"workspace_path"`
	GitURL          *string `json:"git_url"`
	SandboxProvider string  `json:"sandbox_provider"`
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	var body createRequest
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Name == nil || *body.Name == "" || utf8.RuneCountInString(*body.Name) > 255 {
		return httpx.Unprocessable("name must be 1-255 characters")
	}
	source := "empty"
	if body.SourceType != nil {
		source = *body.SourceType
	}
	if source != "git" && source != "local" && source != "empty" {
		return httpx.Unprocessable("source_type must be one of 'git', 'local', 'empty'")
	}
	if (body.WorkspacePath != nil && len(*body.WorkspacePath) > 2048) || (body.GitURL != nil && len(*body.GitURL) > 2048) {
		return httpx.Unprocessable("workspace_path/git_url must be at most 2048 characters")
	}
	provider := body.SandboxProvider
	if provider != sandbox.KindDocker && provider != sandbox.KindHost {
		return httpx.Unprocessable("sandbox_provider must be 'docker' or 'host'")
	}
	if !m.a.Sandboxes.HasProvider(provider) {
		return httpx.BadRequest(fmt.Sprintf("Sandbox provider %q is not available", provider))
	}

	// Provisioning shouldn't be abandoned halfway when the client disconnects.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()

	userDir := filepath.Join(m.baseDir(), u.ID)
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		return err
	}
	var workspacePath string
	var sourceURL *string
	created := false
	switch source {
	case "git":
		if body.GitURL == nil || *body.GitURL == "" {
			return httpx.BadRequest("git_url is required for git workspace")
		}
		gitURL, err := NormalizeGitURL(*body.GitURL)
		if err != nil {
			return err
		}
		token := ""
		if st, err := m.a.Store.GetUserSettings(ctx, u.ID); err == nil {
			token = db.Deref(st.GithubPersonalAccessToken)
		}
		dir, err := cloneRepo(ctx, userDir, gitURL, token)
		if err != nil {
			return err
		}
		workspacePath, sourceURL, created = dir, &gitURL, true
	case "local":
		if body.WorkspacePath == nil || *body.WorkspacePath == "" {
			return httpx.BadRequest("workspace_path is required for local workspace")
		}
		// Never mount/chown arbitrary host dirs into Docker sandboxes.
		if provider != sandbox.KindHost {
			return httpx.BadRequest("Local workspaces require the host sandbox provider")
		}
		dir, err := resolveLocalDir(*body.WorkspacePath)
		if err != nil {
			return err
		}
		workspacePath = dir
	default:
		dir := filepath.Join(userDir, SafeDirName(*body.Name)+"-"+shortHex())
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		workspacePath, created = dir, true
	}
	cleanupDir := func() {
		if created {
			_ = os.RemoveAll(workspacePath)
		}
	}

	sandboxID := NewSandboxID()
	if err := m.a.Sandboxes.Create(ctx, provider, sandboxID, workspacePath, u.ID); err != nil {
		cleanupDir()
		slog.Error("create sandbox", "provider", provider, "err", err)
		return httpx.Errorf(http.StatusInternalServerError, "Failed to create sandbox: %v", err)
	}

	now := db.Now()
	ws := &models.Workspace{
		ID: uuid.NewString(), Name: *body.Name, UserID: u.ID, SandboxID: sandboxID, SandboxProvider: provider,
		WorkspacePath: workspacePath, SourceType: &source, SourceURL: sourceURL, CreatedAt: now, UpdatedAt: now,
	}
	_, err := m.a.DB.ExecContext(ctx, `INSERT INTO workspaces (id, name, user_id, sandbox_id, sandbox_provider, workspace_path,
		source_type, source_url, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ws.ID, ws.Name, ws.UserID, ws.SandboxID, ws.SandboxProvider, ws.WorkspacePath, ws.SourceType, ws.SourceURL, now, now)
	if err != nil {
		// Never leak the sandbox provisioned above.
		_ = m.a.Sandboxes.Delete(context.Background(), provider, sandboxID)
		cleanupDir()
		return err
	}
	return httpx.Created(w, ws)
}

func resolveLocalDir(raw string) (string, error) {
	p := raw
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	p, err := filepath.Abs(p)
	if err == nil {
		p, err = filepath.EvalSymlinks(p)
	}
	if err != nil {
		return "", httpx.BadRequest("workspace_path must be an existing directory")
	}
	fi, err := os.Stat(p)
	if err != nil || !fi.IsDir() {
		return "", httpx.BadRequest("workspace_path must be an existing directory")
	}
	return p, nil
}

func qualified(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = prefix + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// list annotates each workspace with its visible top-level chat count and
// latest chat activity, most recently active first.
func (m *Module) list(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	rows, err := m.a.DB.QueryContext(r.Context(), `SELECT `+qualified("w.", models.WorkspaceColumns)+`, COUNT(c.id), MAX(c.updated_at)
		FROM workspaces w
		LEFT JOIN chats c ON c.workspace_id = w.id AND c.deleted_at IS NULL AND c.channel_id IS NULL AND c.parent_chat_id IS NULL
		WHERE w.user_id = ? AND w.deleted_at IS NULL
		GROUP BY w.id
		ORDER BY MAX(c.updated_at) IS NULL, MAX(c.updated_at) DESC, w.updated_at DESC`, u.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []models.Workspace{}
	for rows.Next() {
		var ws models.Workspace
		if err := rows.Scan(append(ws.ScanFields(), &ws.ChatCount, &ws.LastChatAt)...); err != nil {
			return err
		}
		items = append(items, ws)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	total := len(items)
	return httpx.OK(w, models.NewPaginated(items, 1, max(total, 1), total))
}

func (m *Module) get(w http.ResponseWriter, r *http.Request) error {
	ws, err := m.a.Store.GetWorkspace(r.Context(), auth.UserFrom(r.Context()).ID, r.PathValue("workspace_id"))
	if err != nil {
		return err
	}
	return httpx.OK(w, ws)
}

func (m *Module) update(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name *string `json:"name"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Name != nil && (*body.Name == "" || utf8.RuneCountInString(*body.Name) > 255) {
		return httpx.Unprocessable("name must be 1-255 characters")
	}
	ws, err := m.a.Store.GetWorkspace(r.Context(), auth.UserFrom(r.Context()).ID, r.PathValue("workspace_id"))
	if err != nil {
		return err
	}
	if body.Name != nil {
		now := db.Now()
		if _, err := m.a.DB.ExecContext(r.Context(), `UPDATE workspaces SET name = ?, updated_at = ? WHERE id = ?`, *body.Name, now, ws.ID); err != nil {
			return err
		}
		ws.Name, ws.UpdatedAt = *body.Name, now
	}
	return httpx.OK(w, ws)
}

func (m *Module) resources(w http.ResponseWriter, r *http.Request) error {
	ws, err := m.a.Store.GetWorkspace(r.Context(), auth.UserFrom(r.Context()).ID, r.PathValue("workspace_id"))
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]any{
		"skills":                 skills.NewService(m.a.Cfg, ws).List(),
		"builtin_slash_commands": map[string][]BuiltinSlashCommand{skills.Source: builtinSlashCommands},
	})
}

// delete soft-deletes the workspace and its chats, runs cleanup hooks, then
// tears down terminals, worktrees, the sandbox and (for dirs we created under
// storage) the files in the background.
func (m *Module) delete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	ws, err := m.a.Store.GetWorkspace(ctx, auth.UserFrom(ctx).ID, r.PathValue("workspace_id"))
	if err != nil {
		return err
	}
	var chatIDs, ownedWorktrees []string
	err = m.a.DB.Tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id, worktree_cwd FROM chats WHERE workspace_id = ? AND deleted_at IS NULL`, ws.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			var cwd *string
			if err := rows.Scan(&id, &cwd); err != nil {
				rows.Close()
				return err
			}
			chatIDs = append(chatIDs, id)
			// Only the chat's own worktree: a sub-thread's inherited cwd
			// won't match its own id.
			if cwd != nil && *cwd == gitops.ChatWorktreePath(id) {
				ownedWorktrees = append(ownedWorktrees, *cwd)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		now := db.Now()
		stmts := []struct {
			q    string
			args []any
		}{
			{`UPDATE workspaces SET deleted_at = ?, updated_at = ? WHERE id = ?`, []any{now, now, ws.ID}},
			{`UPDATE messages SET deleted_at = ? WHERE deleted_at IS NULL AND chat_id IN (SELECT id FROM chats WHERE workspace_id = ? AND deleted_at IS NULL)`, []any{now, ws.ID}},
			{`UPDATE chats SET deleted_at = ? WHERE workspace_id = ? AND deleted_at IS NULL`, []any{now, ws.ID}},
			// Soft-delete never fires the FK cascade; automations would keep
			// firing into a deleted workspace.
			{`DELETE FROM automations WHERE workspace_id = ?`, []any{ws.ID}},
		}
		for _, s := range stmts {
			if _, err := tx.ExecContext(ctx, s.q, s.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	for _, id := range chatIDs {
		for _, fn := range m.a.Hooks.OnChatDeleted {
			runHook(func() { fn(id) })
		}
	}
	for _, fn := range m.a.Hooks.OnWorkspaceDeleted {
		runHook(func() { fn(ws.ID) })
	}
	go m.teardown(ws, ownedWorktrees)
	return httpx.NoContent(w)
}

func runHook(fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("workspace delete hook panicked", "err", rec)
		}
	}()
	fn()
}

// teardown order matters: terminals die while the sandbox still exists (tmux
// kill-session runs inside it), worktrees go next (a local dir outlives the
// sandbox), then the sandbox, then files we own.
func (m *Module) teardown(ws *models.Workspace, worktrees []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if m.terminate != nil {
		m.terminate(ctx, ws.SandboxID)
	}
	underStorage := m.ownsDir(ws.WorkspacePath)
	if !underStorage && len(worktrees) > 0 {
		if sb, err := m.a.Sandboxes.For(ws); err == nil {
			for _, wt := range worktrees {
				gitops.RemoveWorktree(ctx, sb, wt)
			}
		}
	}
	if err := m.a.Sandboxes.Delete(ctx, ws.SandboxProvider, ws.SandboxID); err != nil {
		slog.Warn("delete sandbox", "sandbox", ws.SandboxID, "err", err)
	}
	if underStorage {
		if err := os.RemoveAll(ws.WorkspacePath); err != nil {
			slog.Warn("remove workspace dir", "path", ws.WorkspacePath, "err", err)
		}
	}
	m.usage.forget(ws.ID)
}

// ownsDir reports whether p is strictly inside the storage dir (so it was
// created by us and may be deleted).
func (m *Module) ownsDir(p string) bool {
	base, err := filepath.Abs(m.a.Cfg.StoragePath)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(base, target)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (m *Module) diskUsage(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	refresh := false
	if b := httpx.QueryBool(r, "refresh"); b != nil {
		refresh = *b
	}
	rows, err := m.a.DB.QueryContext(r.Context(), `SELECT `+models.WorkspaceColumns+` FROM workspaces WHERE user_id = ? AND deleted_at IS NULL`, u.ID)
	if err != nil {
		return err
	}
	var list []*models.Workspace
	for rows.Next() {
		ws := &models.Workspace{}
		if err := rows.Scan(ws.ScanFields()...); err != nil {
			rows.Close()
			return err
		}
		list = append(list, ws)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	out := make([]usageEntry, len(list))
	var wg sync.WaitGroup
	for i, ws := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = m.usage.get(r.Context(), m.a.Sandboxes, ws, refresh)
		}()
	}
	wg.Wait()
	return httpx.OK(w, out)
}

func (m *Module) box(w http.ResponseWriter, r *http.Request) error {
	return httpx.OK(w, m.a.Sandboxes.Box(r.Context(), auth.UserFrom(r.Context()).ID))
}

// stopBox stops the user's box now; ?reset=true also discards its system
// (apt packages) so it restarts fresh from the current image.
func (m *Module) stopBox(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	err := m.a.Sandboxes.StopBox(r.Context(), u.ID, r.URL.Query().Get("reset") == "true")
	if errors.Is(err, sandbox.ErrBoxBusy) {
		return httpx.Conflict(err.Error())
	}
	if err != nil {
		return err
	}
	return httpx.OK(w, m.a.Sandboxes.Box(r.Context(), u.ID))
}
