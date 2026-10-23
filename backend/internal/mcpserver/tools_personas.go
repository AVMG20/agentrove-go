package mcpserver

import (
	"context"
	"encoding/json"
	"strings"

	"agentrove/internal/db"
	"agentrove/internal/models"
)

// --- personas (a list in user settings) ---

func (m *Module) personas(ctx context.Context, userID string) ([]models.Persona, error) {
	st, err := m.a.Store.GetUserSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !st.Personas.Valid || st.Personas.V == nil {
		return []models.Persona{}, nil
	}
	return st.Personas.V, nil
}

func (m *Module) savePersonas(ctx context.Context, userID string, list []models.Persona) error {
	_, err := m.a.DB.ExecContext(ctx, `UPDATE user_settings SET personas = ?, updated_at = ? WHERE user_id = ?`, db.J(list), db.Now(), userID)
	return err
}

func (m *Module) listPersonas(ctx context.Context, c *caller, _ json.RawMessage) (any, error) {
	list, err := m.personas(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, p := range list {
		out = append(out, map[string]any{"name": p.Name, "content": p.Content})
	}
	return map[string]any{"personas": out}, nil
}

type personaArgs struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

func (m *Module) getPersona(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a personaArgs
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	list, err := m.personas(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		if p.Name == a.Name {
			return map[string]any{"persona": map[string]any{"name": p.Name, "content": p.Content}}, nil
		}
	}
	return nil, errorf("No persona named %q exists.", a.Name)
}

func (m *Module) createPersona(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a personaArgs
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Name) == "" {
		return nil, errorf("name is required")
	}
	list, err := m.personas(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		if p.Name == a.Name {
			return nil, errorf("A persona named %q already exists.", a.Name)
		}
	}
	if err := m.savePersonas(ctx, c.userID, append(list, models.Persona{Name: a.Name, Content: a.Content, CreatedByAgent: true})); err != nil {
		return nil, err
	}
	return map[string]any{"persona": map[string]any{"name": a.Name}}, nil
}

func (m *Module) updatePersona(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a personaArgs
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	list, err := m.personas(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == a.Name {
			if !list[i].CreatedByAgent {
				return nil, errUserPersona(a.Name)
			}
			list[i].Content = a.Content
			if err := m.savePersonas(ctx, c.userID, list); err != nil {
				return nil, err
			}
			return map[string]any{"persona": map[string]any{"name": a.Name}}, nil
		}
	}
	return nil, errorf("No persona named %q exists.", a.Name)
}

func (m *Module) deletePersona(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a personaArgs
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	list, err := m.personas(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	kept := []models.Persona{}
	for _, p := range list {
		if p.Name != a.Name {
			kept = append(kept, p)
		} else if !p.CreatedByAgent {
			return nil, errUserPersona(a.Name)
		}
	}
	if len(kept) == len(list) {
		return nil, errorf("No persona named %q exists.", a.Name)
	}
	if err := m.savePersonas(ctx, c.userID, kept); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

func errUserPersona(name string) error {
	return errorf("Persona %q was created by the user; only the user can change or delete it (in Settings). "+
		"Create a new persona instead.", name)
}
