package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	"agentrove/internal/app"
	"agentrove/internal/db"
	"agentrove/internal/events"
	"agentrove/internal/httpx"
	"agentrove/internal/models"

	"github.com/google/uuid"
)

// ErrWorkspaceNotFound marks a run whose workspace is gone (soft-deleted).
var ErrWorkspaceNotFound = httpx.NotFound("Workspace not found")

// CreateChat inserts a visible chat the way POST /chat/chats does (used by
// automations and the MCP server). automationID marks a run of that automation.
// The chat runs on an account of modelID's provider (the default one when it
// is that provider's), else on the default account, whose provider's default
// model then stands in.
func CreateChat(ctx context.Context, a *app.App, userID, workspaceID, title, modelID string, automationID *string) (*models.Chat, error) {
	now := db.Now()
	id := uuid.NewString()
	var profileID, provider any
	if a.Profiles != nil {
		p := a.Profiles.DefaultFor(ctx, userID, models.ProviderOf(modelID))
		if p == nil {
			var err error
			if p, err = a.Profiles.Default(ctx, userID); err != nil {
				return nil, err
			}
		}
		profileID, provider = p.ID, p.Provider
	}
	err := a.DB.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM workspaces WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, workspaceID, userID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrWorkspaceNotFound
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO chats (id, title, user_id, workspace_id, automation_id, profile_id, provider, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, title, userID, workspaceID, automationID, profileID, provider, now, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	chat, err := a.Store.GetChatByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// Open browser sessions show chats created out-of-band without a refresh.
	PublishChatEvent(a, userID, map[string]any{"kind": "chat_created", "chat": chat})
	return chat, nil
}

// DeleteChat removes a chat that never started (no orphan empty chats).
func DeleteChat(ctx context.Context, a *app.App, chatID string) {
	for _, fn := range a.Hooks.OnChatDeleted {
		fn(chatID)
	}
	if _, err := a.DB.ExecContext(ctx, `DELETE FROM chats WHERE id = ?`, chatID); err != nil {
		slog.Warn("delete orphan chat", "chat", chatID, "err", err)
	}
}

// PublishChatEvent is best-effort: a missed event only delays a sidebar refetch.
func PublishChatEvent(a *app.App, userID string, payload map[string]any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	a.Hub.Publish(events.UserChats(userID), b)
}
