// Package store has the cross-cutting lookups (ownership-checked workspace and
// chat fetches, user settings) that most handlers need.
package store

import (
	"context"
	"database/sql"
	"errors"

	"agentrove/internal/auth"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"

	"github.com/google/uuid"
)

type Store struct {
	DB   *db.DB
	Keys *auth.Keys
}

const DefaultTitleModelID = "haiku"

func (s *Store) GetWorkspace(ctx context.Context, userID, id string) (*models.Workspace, error) {
	w := &models.Workspace{}
	err := s.DB.QueryRowContext(ctx, `SELECT `+models.WorkspaceColumns+` FROM workspaces WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, id, userID).Scan(w.ScanFields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("Workspace not found")
	}
	return w, err
}

// GetWorkspaceBySandbox resolves the sandbox routes (/sandbox/{sandbox_id}/...).
func (s *Store) GetWorkspaceBySandbox(ctx context.Context, userID, sandboxID string) (*models.Workspace, error) {
	w := &models.Workspace{}
	err := s.DB.QueryRowContext(ctx, `SELECT `+models.WorkspaceColumns+` FROM workspaces WHERE sandbox_id = ? AND user_id = ? AND deleted_at IS NULL`, sandboxID, userID).Scan(w.ScanFields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("Sandbox not found")
	}
	return w, err
}

func (s *Store) GetWorkspaceByID(ctx context.Context, id string) (*models.Workspace, error) {
	w := &models.Workspace{}
	err := s.DB.QueryRowContext(ctx, `SELECT `+models.WorkspaceColumns+` FROM workspaces WHERE id = ?`, id).Scan(w.ScanFields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("Workspace not found")
	}
	return w, err
}

// GetChat returns a visible (non-channel) chat owned by userID.
func (s *Store) GetChat(ctx context.Context, userID, id string) (*models.Chat, error) {
	return s.getChat(ctx, userID, id, false)
}

// GetMemberChat also returns channel member chats (their terminal/editor views).
func (s *Store) GetMemberChat(ctx context.Context, userID, id string) (*models.Chat, error) {
	return s.getChat(ctx, userID, id, true)
}

func (s *Store) getChat(ctx context.Context, userID, id string, includeChannel bool) (*models.Chat, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, httpx.NotFound("Chat not found")
	}
	q := `SELECT ` + models.ChatColumns + ` FROM chats c JOIN workspaces w ON w.id = c.workspace_id
		WHERE c.id = ? AND c.user_id = ? AND c.deleted_at IS NULL`
	if !includeChannel {
		q += ` AND c.channel_id IS NULL`
	}
	c := &models.Chat{}
	err := s.DB.QueryRowContext(ctx, q, id, userID).Scan(c.ScanFields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("Chat not found")
	}
	if err != nil {
		return nil, err
	}
	c.Finish()
	return c, nil
}

// GetChatByID loads a chat without an ownership check (internal callers).
func (s *Store) GetChatByID(ctx context.Context, id string) (*models.Chat, error) {
	c := &models.Chat{}
	err := s.DB.QueryRowContext(ctx, `SELECT `+models.ChatColumns+` FROM chats c JOIN workspaces w ON w.id = c.workspace_id WHERE c.id = ?`, id).Scan(c.ScanFields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("Chat not found")
	}
	if err != nil {
		return nil, err
	}
	c.Finish()
	return c, nil
}

// GetUserSettings returns the user's settings, creating the row on first use.
// The GitHub token comes back decrypted.
func (s *Store) GetUserSettings(ctx context.Context, userID string) (*models.UserSettings, error) {
	st := &models.UserSettings{}
	row := s.DB.QueryRowContext(ctx, `SELECT id, user_id, github_personal_access_token, custom_instructions, custom_env_vars, personas,
		stream_actions, notifications_enabled, title_model_id, created_at, updated_at FROM user_settings WHERE user_id = ?`, userID)
	err := row.Scan(&st.ID, &st.UserID, &st.GithubPersonalAccessToken, &st.CustomInstructions, &st.CustomEnvVars, &st.Personas,
		&st.StreamActions, &st.NotificationsEnabled, &st.TitleModelID, &st.CreatedAt, &st.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		now := db.Now()
		st = &models.UserSettings{ID: uuid.NewString(), UserID: userID, NotificationsEnabled: true, TitleModelID: DefaultTitleModelID, CreatedAt: now, UpdatedAt: now}
		_, err = s.DB.ExecContext(ctx, `INSERT INTO user_settings (id, user_id, notifications_enabled, title_model_id, created_at, updated_at)
			VALUES (?, ?, 1, ?, ?, ?) ON CONFLICT(user_id) DO NOTHING`, st.ID, userID, st.TitleModelID, now, now)
		if err != nil {
			return nil, err
		}
		return s.GetUserSettings(ctx, userID)
	}
	if err != nil {
		return nil, err
	}
	if st.GithubPersonalAccessToken != nil {
		plain, err := s.Keys.Decrypt(*st.GithubPersonalAccessToken)
		if err == nil {
			st.GithubPersonalAccessToken = &plain
		}
	}
	return st, nil
}

// UserEnv is the env every sandbox command/agent of the user gets: custom
// env vars plus the GitHub token for git/gh.
func (s *Store) UserEnv(ctx context.Context, userID string) map[string]string {
	env := map[string]string{}
	st, err := s.GetUserSettings(ctx, userID)
	if err != nil {
		return env
	}
	if st.CustomEnvVars.Valid {
		for _, ev := range st.CustomEnvVars.V {
			if ev.Key != "" {
				env[ev.Key] = ev.Value
			}
		}
	}
	if tok := db.Deref(st.GithubPersonalAccessToken); tok != "" {
		env["GITHUB_TOKEN"] = tok
		env["GH_TOKEN"] = tok
	}
	return env
}
