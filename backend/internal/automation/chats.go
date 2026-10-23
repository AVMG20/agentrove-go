package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
// automations and the MCP server). With parentChatID it becomes a sub-thread:
// it inherits the parent's workspace and worktree, and the parent must be a
// top-level chat. automationID marks a run of that automation.
func CreateChat(ctx context.Context, a *app.App, userID, workspaceID, title string, parentChatID, automationID *string) (*models.Chat, error) {
	now := db.Now()
	id := uuid.NewString()
	err := a.DB.Tx(ctx, func(tx *sql.Tx) error {
		var worktreeCwd *string
		if parentChatID != nil {
			var parentWorkspace string
			var parentParent *string
			var updatedAt db.Time
			var lastViewed db.NullTime
			err := tx.QueryRowContext(ctx, `SELECT workspace_id, parent_chat_id, worktree_cwd, updated_at, last_viewed_at FROM chats
				WHERE id = ? AND user_id = ? AND deleted_at IS NULL AND channel_id IS NULL`, *parentChatID, userID).
				Scan(&parentWorkspace, &parentParent, &worktreeCwd, &updatedAt, &lastViewed)
			if errors.Is(err, sql.ErrNoRows) {
				return httpx.NotFound("Parent chat not found")
			}
			if err != nil {
				return err
			}
			if parentParent != nil {
				return httpx.BadRequest("Cannot create a sub-thread of a sub-thread")
			}
			workspaceID = parentWorkspace
			// Ordering-only bump; carry read state so a read parent stays read.
			wasRead := lastViewed.Valid && !updatedAt.After(lastViewed.Time.Time)
			if wasRead {
				_, err = tx.ExecContext(ctx, `UPDATE chats SET updated_at = ?, last_viewed_at = ? WHERE id = ?`, now, now, *parentChatID)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE chats SET updated_at = ? WHERE id = ?`, now, *parentChatID)
			}
			if err != nil {
				return err
			}
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM workspaces WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, workspaceID, userID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrWorkspaceNotFound
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO chats (id, title, user_id, workspace_id, parent_chat_id, worktree_cwd, automation_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, title, userID, workspaceID, parentChatID, worktreeCwd, automationID, now, now)
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
