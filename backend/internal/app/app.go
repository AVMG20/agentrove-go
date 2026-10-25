// Package app holds the shared dependencies every feature module receives.
package app

import (
	"context"

	"agentrove/internal/auth"
	"agentrove/internal/config"
	"agentrove/internal/db"
	"agentrove/internal/events"
	"agentrove/internal/profiles"
	"agentrove/internal/sandbox"
	"agentrove/internal/store"
)

type App struct {
	Cfg       *config.Config
	DB        *db.DB
	Auth      *auth.Service
	Keys      *auth.Keys
	Hub       *events.Hub
	Sandboxes *sandbox.Manager
	Store     *store.Store
	// Profiles are the provider accounts chats run on (nil in some tests:
	// everything then runs on the built-in Claude profile).
	Profiles *profiles.Service

	// Late-bound hooks so modules can call each other without import cycles.
	// Set by main after all modules are constructed.
	Hooks Hooks
}

// Hooks are cross-module entry points. A nil hook means the feature is absent.
type Hooks struct {
	// StartChatTurn starts an agent turn in a chat as if the user sent prompt.
	// Used by automations, channels and the MCP server.
	StartChatTurn func(req ChatTurnRequest) (ChatTurnResult, error)
	// CancelChatTurn interrupts a running turn.
	CancelChatTurn func(chatID string) error
	// ChatTurnActive reports whether a chat has a turn running.
	ChatTurnActive func(chatID string) bool
	// GenerateText runs a one-shot, tool-less Claude prompt with the user's
	// Claude login (commit messages, PR descriptions, titles).
	GenerateText func(ctx context.Context, userID, modelID, systemPrompt, prompt string) (string, error)
	// OnChatDeleted lets modules clean up per-chat state (processes, worktrees).
	OnChatDeleted []func(chatID string)
	// OnWorkspaceDeleted lets modules clean up per-workspace state.
	OnWorkspaceDeleted []func(workspaceID string)
}

// ChatTurnRequest mirrors the fields of POST /chat/chat.
type ChatTurnRequest struct {
	UserID              string
	ChatID              string
	Prompt              string
	ModelID             string
	PermissionMode      string
	ThinkingMode        *string
	Worktree            bool
	BaseBranch          *string
	SelectedPersonaName string
	Attachments         []AttachmentInput
	// MemberSink, when set, receives every stream event of the turn (channels).
	MemberSink func(kind string, payload map[string]any)
	// OnDone runs once the turn reaches a terminal state.
	OnDone func(status string, content string)
	// Unattended turns (automations, MCP workers) auto-approve and never ask.
	Unattended bool
}

type AttachmentInput struct {
	FileURL  string  `json:"file_url"`
	FilePath *string `json:"file_path"`
	FileType string  `json:"file_type"`
	Filename *string `json:"filename"`
}

type ChatTurnResult struct {
	ChatID       string  `json:"chat_id"`
	MessageID    string  `json:"message_id"`
	LastSeq      int64   `json:"last_seq"`
	CheckpointID *string `json:"checkpoint_id"`
	WorktreeCwd  *string `json:"worktree_cwd"`
}
