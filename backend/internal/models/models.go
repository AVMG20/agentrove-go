// Package models holds the persisted entities shared across services plus the
// common JSON response shapes. Field JSON names match the frontend contract.
package models

import (
	"agentrove/internal/db"
)

type User struct {
	ID                       string      `json:"id"`
	Email                    string      `json:"email"`
	Username                 string      `json:"username"`
	HashedPassword           string      `json:"-"`
	IsActive                 bool        `json:"is_active"`
	IsSuperuser              bool        `json:"is_superuser"`
	IsVerified               bool        `json:"is_verified"`
	VerificationToken        *string     `json:"-"`
	VerificationTokenExpires db.NullTime `json:"-"`
	ResetToken               *string     `json:"-"`
	ResetTokenExpires        db.NullTime `json:"-"`
	CreatedAt                db.Time     `json:"created_at"`
	UpdatedAt                db.Time     `json:"updated_at"`
}

const UserColumns = `id, email, username, hashed_password, is_active, is_superuser, is_verified,
	verification_token, verification_token_expires, reset_token, reset_token_expires, created_at, updated_at`

func (u *User) ScanFields() []any {
	return []any{&u.ID, &u.Email, &u.Username, &u.HashedPassword, &u.IsActive, &u.IsSuperuser, &u.IsVerified,
		&u.VerificationToken, &u.VerificationTokenExpires, &u.ResetToken, &u.ResetTokenExpires, &u.CreatedAt, &u.UpdatedAt}
}

type CustomEnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Persona struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type StreamAction struct {
	Label          string `json:"label"`
	Enabled        bool   `json:"enabled"`
	ModelID        string `json:"model_id"`
	Command        string `json:"command"`
	PersonaName    string `json:"persona_name"`
	PermissionMode string `json:"permission_mode"`
	ThinkingMode   string `json:"thinking_mode"`
}

type UserSettings struct {
	ID                        string                  `json:"id"`
	UserID                    string                  `json:"user_id"`
	GithubPersonalAccessToken *string                 `json:"github_personal_access_token"`
	CustomInstructions        *string                 `json:"custom_instructions"`
	CustomEnvVars             db.JSON[[]CustomEnvVar] `json:"custom_env_vars"`
	Personas                  db.JSON[[]Persona]      `json:"personas"`
	StreamActions             db.JSON[[]StreamAction] `json:"stream_actions"`
	NotificationsEnabled      bool                    `json:"notifications_enabled"`
	TitleModelID              string                  `json:"title_model_id"`
	CreatedAt                 db.Time                 `json:"created_at"`
	UpdatedAt                 db.Time                 `json:"updated_at"`
}

type Workspace struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	UserID          string      `json:"user_id"`
	SandboxID       string      `json:"sandbox_id"`
	SandboxProvider string      `json:"sandbox_provider"`
	WorkspacePath   string      `json:"workspace_path"`
	SourceType      *string     `json:"source_type"`
	SourceURL       *string     `json:"source_url"`
	DeletedAt       db.NullTime `json:"-"`
	CreatedAt       db.Time     `json:"created_at"`
	UpdatedAt       db.Time     `json:"updated_at"`
	// Computed for list responses.
	ChatCount  int         `json:"chat_count"`
	LastChatAt db.NullTime `json:"last_chat_at"`
}

const WorkspaceColumns = `id, name, user_id, sandbox_id, sandbox_provider, workspace_path, source_type, source_url, deleted_at, created_at, updated_at`

func (w *Workspace) ScanFields() []any {
	return []any{&w.ID, &w.Name, &w.UserID, &w.SandboxID, &w.SandboxProvider, &w.WorkspacePath, &w.SourceType, &w.SourceURL, &w.DeletedAt, &w.CreatedAt, &w.UpdatedAt}
}

type Chat struct {
	ID                string      `json:"id"`
	Title             string      `json:"title"`
	UserID            string      `json:"user_id"`
	WorkspaceID       string      `json:"workspace_id"`
	ChannelID         *string     `json:"channel_id"`
	SessionID         *string     `json:"-"`
	WorktreeCwd       *string     `json:"worktree_cwd"`
	ContextTokenUsage int         `json:"context_token_usage"`
	ContextWindow     int         `json:"-"`
	LastEventSeq      int64       `json:"-"`
	DeletedAt         db.NullTime `json:"-"`
	PinnedAt          db.NullTime `json:"pinned_at"`
	LastViewedAt      db.NullTime `json:"-"`
	ParentChatID      *string     `json:"parent_chat_id"`
	LastModelID       *string     `json:"last_model_id"`
	LastThinkingMode  *string     `json:"last_thinking_mode"`
	LastPersonaName   *string     `json:"last_persona_name"`
	CreatedAt         db.Time     `json:"created_at"`
	UpdatedAt         db.Time     `json:"updated_at"`

	// Joined/computed.
	SandboxID        string  `json:"sandbox_id"`
	SubThreadCount   int     `json:"sub_thread_count"`
	SessionAgentKind *string `json:"session_agent_kind"`
	Unread           bool    `json:"unread"`
}

// ChatColumns selects chat columns joined with workspaces as w (for sandbox_id).
const ChatColumns = `c.id, c.title, c.user_id, c.workspace_id, c.channel_id, c.session_id, c.worktree_cwd,
	c.context_token_usage, c.context_window, c.last_event_seq, c.deleted_at, c.pinned_at, c.last_viewed_at,
	c.parent_chat_id, c.last_model_id, c.last_thinking_mode, c.last_persona_name, c.created_at, c.updated_at, w.sandbox_id`

func (c *Chat) ScanFields() []any {
	return []any{&c.ID, &c.Title, &c.UserID, &c.WorkspaceID, &c.ChannelID, &c.SessionID, &c.WorktreeCwd,
		&c.ContextTokenUsage, &c.ContextWindow, &c.LastEventSeq, &c.DeletedAt, &c.PinnedAt, &c.LastViewedAt,
		&c.ParentChatID, &c.LastModelID, &c.LastThinkingMode, &c.LastPersonaName, &c.CreatedAt, &c.UpdatedAt, &c.SandboxID}
}

// Finish fills computed fields after a scan.
func (c *Chat) Finish() {
	c.Unread = !c.LastViewedAt.Valid || c.UpdatedAt.After(c.LastViewedAt.Time.Time)
	if c.SessionID != nil {
		kind := "claude"
		c.SessionAgentKind = &kind
	}
}

type Paginated[T any] struct {
	Items   []T `json:"items"`
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
	Total   int `json:"total"`
	Pages   int `json:"pages"`
}

func NewPaginated[T any](items []T, page, perPage, total int) Paginated[T] {
	if items == nil {
		items = []T{}
	}
	pages := 0
	if perPage > 0 {
		pages = (total + perPage - 1) / perPage
	}
	return Paginated[T]{Items: items, Page: page, PerPage: perPage, Total: total, Pages: pages}
}

type CursorPaginated[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

// Attachment is the persisted shape of an uploaded file (message, queue, channel).
type Attachment struct {
	FileURL  string  `json:"file_url"`
	FilePath *string `json:"file_path"`
	FileType string  `json:"file_type"`
	Filename *string `json:"filename"`
}
