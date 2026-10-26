package workspace

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"agentrove/internal/auth"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"

	"github.com/google/uuid"
)

// Threads without a project still need a workspace (chats.workspace_id is
// required), so they share one hidden scratch workspace per user: a plain
// dir (not a git repo) next to the projects, made on first use.
const (
	scratchDirName = ".scratch"
	scratchName    = "No project"
)

func (m *Module) scratch(w http.ResponseWriter, r *http.Request) error {
	ws, err := m.ensureScratch(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, ws)
}

func (m *Module) ensureScratch(ctx context.Context, userID string) (*models.Workspace, error) {
	ws, err := m.findScratch(ctx, userID)
	if !errors.Is(err, sql.ErrNoRows) {
		return ws, err
	}
	provider := sandbox.KindDocker
	if !m.a.Sandboxes.HasProvider(provider) {
		return nil, httpx.Errorf(http.StatusServiceUnavailable, "Docker isn't available on the server, so threads can't run")
	}
	dir := filepath.Join(m.baseDir(), userID, scratchDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	sandboxID := NewSandboxID()
	if err := m.a.Sandboxes.Create(ctx, provider, sandboxID, dir, userID); err != nil {
		return nil, httpx.Errorf(http.StatusInternalServerError, "Failed to create sandbox: %v", err)
	}
	// A concurrent first use may win the insert; the unique index keeps one.
	now := db.Now()
	_, err = m.a.DB.ExecContext(ctx, `INSERT OR IGNORE INTO workspaces (id, name, user_id, sandbox_id, sandbox_provider, workspace_path,
		source_type, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), scratchName, userID, sandboxID, provider, dir, models.ScratchSource, now, now)
	if err != nil {
		return nil, err
	}
	return m.findScratch(ctx, userID)
}

func (m *Module) findScratch(ctx context.Context, userID string) (*models.Workspace, error) {
	ws := &models.Workspace{}
	err := m.a.DB.QueryRowContext(ctx, `SELECT `+models.WorkspaceColumns+` FROM workspaces
		WHERE user_id = ? AND source_type = ? AND deleted_at IS NULL`, userID, models.ScratchSource).Scan(ws.ScanFields()...)
	if err != nil {
		return nil, err
	}
	return ws, nil
}
