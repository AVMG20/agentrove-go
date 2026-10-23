package mcpserver

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"agentrove/internal/channel"
	"agentrove/internal/db"
	"agentrove/internal/models"
)

// --- channels ---

func channelSummary(ch *channel.Channel) map[string]any {
	members := []map[string]any{}
	for _, mb := range ch.Members {
		members = append(members, map[string]any{
			"id": mb.ID, "chat_id": mb.ChatID, "display_name": mb.DisplayName, "model_id": mb.ModelID,
			"persona": mb.Persona, "thinking_mode": mb.ThinkingMode,
		})
	}
	return map[string]any{
		"id": ch.ID, "name": ch.Name, "workspace_id": ch.WorkspaceID, "worktree": ch.Worktree, "branch": ch.Branch,
		"created_at": ch.CreatedAt, "updated_at": ch.UpdatedAt, "members": members,
	}
}

func (m *Module) listChannels(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		WorkspaceID *string `json:"workspace_id"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	chans, err := m.channels.List(ctx, c.userID, db.Deref(a.WorkspaceID))
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, ch := range chans {
		out = append(out, channelSummary(ch))
	}
	return map[string]any{"channels": out}, nil
}

func (m *Module) createChannel(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		Name    string `json:"name"`
		Members []struct {
			ModelID      string  `json:"model_id"`
			DisplayName  *string `json:"display_name"`
			Persona      *string `json:"persona"`
			ThinkingMode *string `json:"thinking_mode"`
		} `json:"members"`
		WorkspaceID *string `json:"workspace_id"`
		Worktree    bool    `json:"worktree"`
		Branch      *string `json:"branch"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if len(a.Members) == 0 {
		return nil, errorf("members must have at least one item")
	}
	ws, err := m.resolveWorkspace(ctx, c.userID, a.WorkspaceID)
	if err != nil {
		return nil, err
	}
	in := channel.CreateInput{WorkspaceID: ws, Name: a.Name, Worktree: a.Worktree, Branch: a.Branch}
	for _, mb := range a.Members {
		model, err := resolveModel(&mb.ModelID)
		if err != nil {
			return nil, err
		}
		if err := validEffort(mb.ThinkingMode); err != nil {
			return nil, err
		}
		mode := models.FullAccessPermissionMode
		in.Members = append(in.Members, channel.MemberInput{
			ModelID: model, DisplayName: mb.DisplayName, PermissionMode: &mode, Persona: mb.Persona, ThinkingMode: mb.ThinkingMode,
		})
	}
	ch, err := m.channels.Create(ctx, c.userID, in)
	if err != nil {
		return nil, err
	}
	return map[string]any{"channel": channelSummary(ch)}, nil
}

func (m *Module) postChannelMessage(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ChannelID string `json:"channel_id"`
		Content   string `json:"content"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if n := utf8.RuneCountInString(a.Content); n < 1 || n > 100000 {
		return nil, errorf("content must be between 1 and 100000 characters")
	}
	ch, err := m.channels.Get(ctx, a.ChannelID, c.userID)
	if err != nil {
		return nil, err
	}
	msg, err := m.channels.Post(context.WithoutCancel(ctx), ch, a.Content, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"message_id": msg.ID, "channel_id": msg.ChannelID, "seq": msg.Seq, "status": msg.Status}, nil
}

func (m *Module) getChannelMessages(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ChannelID string `json:"channel_id"`
		AfterSeq  int64  `json:"after_seq"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	ch, err := m.channels.Get(ctx, a.ChannelID, c.userID)
	if err != nil {
		return nil, err
	}
	msgs, err := m.channels.Messages(ctx, ch.ID, max(a.AfterSeq, 0))
	if err != nil {
		return nil, err
	}
	authors := map[string]string{}
	for _, mb := range ch.Members {
		authors[mb.ID] = mb.DisplayName
	}
	out := []map[string]any{}
	for _, msg := range msgs {
		author := "user"
		if msg.MemberID != nil {
			author = authors[*msg.MemberID]
		}
		out = append(out, map[string]any{
			"id": msg.ID, "seq": msg.Seq, "author": author, "member_id": msg.MemberID, "content": msg.Content,
			"status": msg.Status, "created_at": msg.CreatedAt,
		})
	}
	return map[string]any{"messages": out}, nil
}

func (m *Module) stopChannel(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ChannelID string `json:"channel_id"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	ch, err := m.channels.Get(ctx, a.ChannelID, c.userID)
	if err != nil {
		return nil, err
	}
	for _, mb := range ch.Members {
		if c.chatID != "" && mb.ChatID == c.chatID {
			return nil, errorf("Cannot stop a channel this chat is a member of from within its own turn.")
		}
	}
	m.channels.Stop(context.WithoutCancel(ctx), ch)
	return map[string]any{"stopped": true}, nil
}
