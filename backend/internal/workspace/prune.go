package workspace

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentrove/internal/auth"
	"agentrove/internal/db"
	"agentrove/internal/gitops"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"
)

type keptProject struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type pruneResult struct {
	FreedBytes *int64          `json:"freed_bytes"`
	Removed    []string        `json:"removed"`
	Kept       []keptProject   `json:"kept"`
	Box        sandbox.BoxInfo `json:"box"`
}

// pruneBox frees the user's box: the system packages, the home's toolchains
// and caches, and the files of every git project that holds no unsaved
// work. Threads and projects stay; a pruned project is cloned again the
// next time it is used (restoreFiles).
func (m *Module) pruneBox(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	// Pruning shouldn't stop halfway when the client disconnects.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	if !m.a.Sandboxes.HasProvider(sandbox.KindDocker) {
		return httpx.Errorf(http.StatusServiceUnavailable, "Docker isn't available on the server")
	}
	before := m.a.Sandboxes.Box(ctx, u.ID, true)
	if before.Busy {
		return httpx.Conflict(sandbox.ErrBoxBusy.Error())
	}
	list, err := m.userWorkspaces(ctx, u.ID)
	if err != nil {
		return err
	}
	res := pruneResult{Removed: []string{}, Kept: []keptProject{}}
	var prunable []*models.Workspace
	for _, ws := range list {
		if db.Deref(ws.SourceType) == models.ScratchSource || !m.ownsDir(ws.WorkspacePath) {
			continue
		}
		if _, err := os.Stat(ws.WorkspacePath); err != nil {
			continue // already pruned
		}
		if reason := m.keepReason(ctx, ws); reason != "" {
			res.Kept = append(res.Kept, keptProject{Name: ws.Name, Reason: reason})
			continue
		}
		prunable = append(prunable, ws)
	}
	keepHome, err := m.profileHomeDirs(ctx, u.ID)
	if err != nil {
		return err
	}
	err = m.a.Sandboxes.PruneBox(ctx, u.ID, keepHome, func() error {
		var errs []error
		for _, ws := range prunable {
			if err := os.RemoveAll(ws.WorkspacePath); err != nil {
				errs = append(errs, err)
				continue
			}
			m.usage.forget(ws.ID)
			res.Removed = append(res.Removed, ws.Name)
		}
		return errors.Join(errs...)
	})
	if errors.Is(err, sandbox.ErrBoxBusy) {
		return httpx.Conflict(err.Error())
	}
	if err != nil {
		slog.Warn("prune box", "user", u.ID, "err", err)
	}
	res.Box = m.a.Sandboxes.Box(ctx, u.ID, true)
	if before.TotalBytes != nil && res.Box.TotalBytes != nil {
		freed := max(*before.TotalBytes-*res.Box.TotalBytes, 0)
		res.FreedBytes = &freed
	}
	if err != nil {
		return httpx.Errorf(http.StatusInternalServerError, "The box was only partly pruned: %v", err)
	}
	return httpx.OK(w, res)
}

// keepReason says why a project's files can't be pruned, or "" when they
// can be cloned again without losing anything.
func (m *Module) keepReason(ctx context.Context, ws *models.Workspace) string {
	if db.Deref(ws.SourceType) != "git" || db.Deref(ws.SourceURL) == "" {
		return "not cloned from git, so its files exist only here"
	}
	sb, err := m.a.Sandboxes.For(ws)
	if err != nil {
		return "could not check it for unsaved work"
	}
	repo, err := gitops.OpenHost(sb, "")
	if err != nil {
		return "could not check it for unsaved work"
	}
	checks := []struct {
		args   []string
		reason string
		lines  int // more output lines than this means unsaved work
	}{
		{[]string{"status", "--porcelain"}, "has uncommitted changes", 0},
		{[]string{"log", "--branches", "--not", "--remotes", "--oneline", "-1"}, "has commits that aren't pushed", 0},
		{[]string{"stash", "list"}, "has stashed changes", 0},
		// Worktree threads keep their own checkouts inside the project.
		{[]string{"worktree", "list", "--porcelain"}, "has worktree threads", 1},
	}
	for _, c := range checks {
		out, err := repo.Run(ctx, c.args...)
		if err != nil || out.Exit != 0 {
			return "could not check it for unsaved work"
		}
		n := 0
		for _, l := range strings.Split(out.Stdout, "\n") {
			if strings.HasPrefix(l, "worktree ") || (c.lines == 0 && strings.TrimSpace(l) != "") {
				n++
			}
		}
		if n > c.lines {
			return c.reason
		}
	}
	return ""
}

// restoreFiles brings back a pruned project: a fresh clone of its repo, or
// an empty dir for one that wasn't cloned.
func (m *Module) restoreFiles(ctx context.Context, ws *models.Workspace) error {
	dir := ws.WorkspacePath
	if !m.ownsDir(dir) || filepath.Dir(dir) != m.a.Cfg.UserWorkspacesDir(ws.UserID) {
		return errors.New("the project folder is missing")
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	if gitURL := db.Deref(ws.SourceURL); db.Deref(ws.SourceType) == "git" && gitURL != "" {
		token := ""
		if st, err := m.a.Store.GetUserSettings(ctx, ws.UserID); err == nil {
			token = db.Deref(st.GithubPersonalAccessToken)
		}
		slog.Info("restoring pruned project", "workspace", ws.ID)
		if err := cloneInto(ctx, dir, gitURL, token); err != nil {
			return err
		}
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return m.a.Sandboxes.Create(ctx, ws.SandboxProvider, ws.SandboxID, dir, ws.UserID)
}

// profileHomeDirs are the top-level home entries holding the user's agent
// profiles' logins, which a prune keeps.
func (m *Module) profileHomeDirs(ctx context.Context, userID string) ([]string, error) {
	rows, err := m.a.DB.QueryContext(ctx, `SELECT config_dir FROM agent_profiles WHERE user_id = ? AND config_dir != ''`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var dir string
		if err := rows.Scan(&dir); err != nil {
			return nil, err
		}
		if first, _, _ := strings.Cut(strings.TrimLeft(filepath.ToSlash(filepath.Clean("/"+dir)), "/"), "/"); first != "" {
			out = append(out, first)
		}
	}
	return out, rows.Err()
}

func (m *Module) userWorkspaces(ctx context.Context, userID string) ([]*models.Workspace, error) {
	rows, err := m.a.DB.QueryContext(ctx, `SELECT `+models.WorkspaceColumns+` FROM workspaces WHERE user_id = ? AND deleted_at IS NULL`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*models.Workspace
	for rows.Next() {
		ws := &models.Workspace{}
		if err := rows.Scan(ws.ScanFields()...); err != nil {
			return nil, err
		}
		list = append(list, ws)
	}
	return list, rows.Err()
}
