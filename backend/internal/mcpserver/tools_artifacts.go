package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"agentrove/internal/artifact"
)

// --- artifacts ---

func (m *Module) artifactSummary(x *artifact.Artifact) map[string]any {
	return map[string]any{
		"id": x.ID, "title": x.Title, "url": strings.TrimRight(m.a.Cfg.FrontendURL, "/") + "/artifacts/" + x.ID,
		"is_public": x.IsPublic, "version": x.Version, "updated_at": x.UpdatedAt,
	}
}

func (m *Module) createArtifact(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		Title string `json:"title"`
		HTML  string `json:"html"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	var chatID *string
	if c.chatID != "" {
		chatID = &c.chatID
	}
	x, err := m.artifacts.Create(ctx, c.userID, a.Title, a.HTML, chatID)
	if errors.Is(err, artifact.ErrChatNotFound) {
		// A chat id the user doesn't own: publish unlinked rather than fail.
		x, err = m.artifacts.Create(ctx, c.userID, a.Title, a.HTML, nil)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"artifact": m.artifactSummary(x)}, nil
}

func (m *Module) updateArtifact(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ArtifactID string  `json:"artifact_id"`
		HTML       string  `json:"html"`
		Title      *string `json:"title"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if a.Title != nil && *a.Title == "" {
		a.Title = nil
	}
	x, err := m.artifacts.Update(ctx, a.ArtifactID, c.userID, a.Title, &a.HTML, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"artifact": m.artifactSummary(x)}, nil
}

func (m *Module) editArtifact(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ArtifactID string          `json:"artifact_id"`
		Edits      []artifact.Edit `json:"edits"`
		Title      *string         `json:"title"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if a.Title != nil && *a.Title == "" {
		a.Title = nil
	}
	x, err := m.artifacts.Edit(ctx, a.ArtifactID, c.userID, a.Edits, a.Title)
	if err != nil {
		return nil, err
	}
	return map[string]any{"artifact": m.artifactSummary(x)}, nil
}

func (m *Module) restoreArtifact(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ArtifactID string `json:"artifact_id"`
		Version    int    `json:"version"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	x, err := m.artifacts.Restore(ctx, a.ArtifactID, c.userID, a.Version)
	if err != nil {
		return nil, err
	}
	return map[string]any{"artifact": m.artifactSummary(x)}, nil
}

func (m *Module) listArtifacts(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ThisChat bool `json:"this_chat"`
	}
	if len(args) > 0 {
		if err := decode(args, &a); err != nil {
			return nil, err
		}
	}
	list, err := m.artifacts.List(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, x := range list {
		if a.ThisChat && (x.ChatID == nil || *x.ChatID != c.chatID) {
			continue
		}
		out = append(out, m.artifactSummary(x))
	}
	return map[string]any{"artifacts": out}, nil
}

func (m *Module) getArtifact(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ArtifactID string `json:"artifact_id"`
		Version    *int   `json:"version"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	current, err := m.artifacts.Get(ctx, a.ArtifactID, c.userID)
	if err != nil {
		return nil, err
	}
	x := current
	if a.Version != nil && *a.Version != current.Version {
		if x, err = m.artifacts.AtVersion(ctx, a.ArtifactID, c.userID, *a.Version); err != nil {
			return nil, err
		}
	}
	versions, err := m.artifacts.Versions(ctx, a.ArtifactID, c.userID)
	if err != nil {
		return nil, err
	}
	pageErrors, err := m.artifacts.Errors(ctx, current.ID, current.Version)
	if err != nil {
		return nil, err
	}
	s := m.artifactSummary(x)
	s["html"] = x.HTML
	s["current_version"] = current.Version
	s["versions"] = versions
	s["page_errors"] = pageErrors
	return map[string]any{"artifact": s}, nil
}

func (m *Module) getArtifactState(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ArtifactID string `json:"artifact_id"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	st, err := m.artifacts.State(ctx, a.ArtifactID, c.userID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"state": st}, nil
}

func (m *Module) setArtifactState(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ArtifactID string          `json:"artifact_id"`
		Key        string          `json:"key"`
		Value      json.RawMessage `json:"value"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if err := m.artifacts.SetState(ctx, a.ArtifactID, c.userID, a.Key, a.Value); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
