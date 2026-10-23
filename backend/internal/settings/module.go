// Package settings serves GET/PATCH /api/v1/settings/.
package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"unicode/utf8"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
)

const defaultPersonaName = "Default"

type Module struct{ a *app.App }

// AutoCompactWindows are the context sizes (tokens) a user can have Claude compact at.
var AutoCompactWindows = []int{100000, 250000, 500000, 750000, 1000000}

func New(a *app.App) *Module { return &Module{a: a} }

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix + "/settings"
	for _, path := range []string{p, p + "/{$}"} {
		mux.HandleFunc("GET "+path, m.a.Auth.Require(m.get))
		mux.HandleFunc("PATCH "+path, m.a.Auth.Require(m.patch))
	}
}

func (m *Module) get(w http.ResponseWriter, r *http.Request) error {
	st, err := m.a.Store.GetUserSettings(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		return err
	}
	st.BrowserAvailable = m.a.Cfg.BrowserEnabled
	return httpx.OK(w, st)
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

// decodeList mirrors the Python validator: null stays null, a string means
// an empty list, a list is validated element-wise.
func decodeList[T any](key string, raw json.RawMessage, validate func(*T) error) (db.JSON[[]T], error) {
	if isNull(raw) {
		return db.JSON[[]T]{}, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return db.J([]T{}), nil
	}
	var items []T
	if err := json.Unmarshal(raw, &items); err != nil {
		return db.JSON[[]T]{}, httpx.Unprocessable(fmt.Sprintf("%s: expected list or null", key))
	}
	if items == nil {
		items = []T{}
	}
	for i := range items {
		if validate != nil {
			if err := validate(&items[i]); err != nil {
				return db.JSON[[]T]{}, httpx.Unprocessable(fmt.Sprintf("%s[%d]: %s", key, i, err.Error()))
			}
		}
	}
	return db.J(items), nil
}

func lenBetween(s string, lo, hi int) bool {
	n := utf8.RuneCountInString(s)
	return n >= lo && n <= hi
}

func validateStreamAction(raw map[string]json.RawMessage, a *models.StreamAction) error {
	if _, ok := raw["enabled"]; !ok {
		a.Enabled = true
	}
	if _, ok := raw["persona_name"]; !ok {
		a.PersonaName = defaultPersonaName
	}
	if _, ok := raw["permission_mode"]; !ok {
		a.PermissionMode = "bypassPermissions"
	}
	if _, ok := raw["thinking_mode"]; !ok {
		a.ThinkingMode = "high"
	}
	switch {
	case !lenBetween(a.Label, 1, 60):
		return fmt.Errorf("label must be 1-60 characters")
	case !lenBetween(a.ModelID, 1, 200):
		return fmt.Errorf("model_id must be 1-200 characters")
	case !lenBetween(a.Command, 1, 4000):
		return fmt.Errorf("command must be 1-4000 characters")
	case !lenBetween(a.PersonaName, 0, 100):
		return fmt.Errorf("persona_name must be at most 100 characters")
	case !lenBetween(a.PermissionMode, 0, 50):
		return fmt.Errorf("permission_mode must be at most 50 characters")
	case !lenBetween(a.ThinkingMode, 0, 50):
		return fmt.Errorf("thinking_mode must be at most 50 characters")
	}
	return nil
}

func decodeStreamActions(raw json.RawMessage) (db.JSON[[]models.StreamAction], error) {
	out, err := decodeList[models.StreamAction]("stream_actions", raw, nil)
	if err != nil || !out.Valid || len(out.V) == 0 {
		return out, err
	}
	// Defaults depend on which keys were present, so re-read the raw objects.
	var objs []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &objs); err != nil {
		return out, httpx.Unprocessable("stream_actions: expected a list of objects")
	}
	for i := range out.V {
		if err := validateStreamAction(objs[i], &out.V[i]); err != nil {
			return out, httpx.Unprocessable(fmt.Sprintf("stream_actions[%d]: %s", i, err.Error()))
		}
	}
	return out, nil
}

// patch applies only the fields present in the body (exclude_unset).
func (m *Module) patch(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	var body map[string]json.RawMessage
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	sets := []string{}
	args := []any{}
	set := func(col string, v any) {
		sets = append(sets, col+" = ?")
		args = append(args, v)
	}
	if raw, ok := body["github_personal_access_token"]; ok {
		var tok *string
		if err := json.Unmarshal(raw, &tok); err != nil {
			return httpx.Unprocessable("github_personal_access_token must be a string or null")
		}
		if tok == nil || *tok == "" {
			set("github_personal_access_token", nil)
		} else {
			enc, err := m.a.Keys.Encrypt(*tok)
			if err != nil {
				return err
			}
			set("github_personal_access_token", enc)
		}
	}
	if raw, ok := body["custom_instructions"]; ok {
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			return httpx.Unprocessable("custom_instructions must be a string or null")
		}
		set("custom_instructions", v)
	}
	if raw, ok := body["custom_env_vars"]; ok {
		v, err := decodeList[models.CustomEnvVar]("custom_env_vars", raw, nil)
		if err != nil {
			return err
		}
		set("custom_env_vars", v)
	}
	if raw, ok := body["personas"]; ok {
		v, err := decodeList[models.Persona]("personas", raw, nil)
		if err != nil {
			return err
		}
		set("personas", v)
	}
	if raw, ok := body["stream_actions"]; ok {
		v, err := decodeStreamActions(raw)
		if err != nil {
			return err
		}
		set("stream_actions", v)
	}
	if raw, ok := body["notifications_enabled"]; ok {
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return httpx.Unprocessable("notifications_enabled must be a boolean")
		}
		set("notifications_enabled", v)
	}
	if raw, ok := body["title_model_id"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil || !lenBetween(v, 1, 128) {
			return httpx.Unprocessable("title_model_id must be a Claude model id or alias (1-128 characters)")
		}
		set("title_model_id", v)
	}
	if raw, ok := body["auto_compact_window"]; ok {
		var v int
		if err := json.Unmarshal(raw, &v); err != nil || !slices.Contains(AutoCompactWindows, v) {
			return httpx.Unprocessable("auto_compact_window must be one of 100000, 250000, 500000, 750000, 1000000")
		}
		set("auto_compact_window", v)
	}
	if raw, ok := body["browser_tools_enabled"]; ok {
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return httpx.Unprocessable("browser_tools_enabled must be a boolean")
		}
		set("browser_tools_enabled", v)
	}

	// Creates the row on first use.
	if _, err := m.a.Store.GetUserSettings(r.Context(), u.ID); err != nil {
		return err
	}
	if len(sets) > 0 {
		set("updated_at", db.Now())
		q := "UPDATE user_settings SET "
		for i, s := range sets {
			if i > 0 {
				q += ", "
			}
			q += s
		}
		args = append(args, u.ID)
		if _, err := m.a.DB.ExecContext(r.Context(), q+" WHERE user_id = ?", args...); err != nil {
			return err
		}
	}
	m.a.Sandboxes.InvalidateUserEnv(u.ID)
	return m.get(w, r)
}
