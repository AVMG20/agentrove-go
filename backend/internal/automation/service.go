// Package automation runs prompts on a cron schedule or on new GitHub
// activity (/api/v1/automations), each run in a fresh unattended chat.
package automation

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"agentrove/internal/app"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"

	"github.com/google/uuid"
)

const (
	// A busy repo can't fan out into dozens of runs in one poll; the rest are
	// picked up on the next tick.
	maxGitHubEventsPerPoll = 10
	// "Run now" on a GitHub automation replays the latest matching event this old.
	githubTestLookback = 30 * 24 * time.Hour
	// With a webhook, the cron poll only catches deliveries missed while down.
	githubWebhookCatchup = time.Hour
	// Give GitHub's REST listings a moment to include the delivered item.
	githubWebhookSettle = 3 * time.Second
	dispatchInterval    = 60 * time.Second
)

type Automation struct {
	ID                  string            `json:"id"`
	UserID              string            `json:"user_id"`
	WorkspaceID         string            `json:"workspace_id"`
	Name                string            `json:"name"`
	Prompt              string            `json:"prompt"`
	ModelID             string            `json:"model_id"`
	CronExpression      string            `json:"cron_expression"`
	Timezone            string            `json:"timezone"`
	PermissionMode      string            `json:"permission_mode"`
	ThinkingMode        *string           `json:"thinking_mode"`
	Worktree            bool              `json:"worktree"`
	SelectedPersonaName string            `json:"selected_persona_name"`
	Enabled             bool              `json:"enabled"`
	NextRunAt           db.Time           `json:"next_run_at"`
	LastRunAt           db.NullTime       `json:"last_run_at"`
	TriggerType         string            `json:"trigger_type"`
	GithubRepo          *string           `json:"github_repo"`
	GithubEvents        db.JSON[[]string] `json:"github_events"`
	GithubCursor        db.NullTime       `json:"-"`
	GithubWebhookID     *int64            `json:"-"`
	GithubWebhookSecret *string           `json:"-"`
	GithubWebhookActive bool              `json:"github_webhook_active"`
	GithubWebhookError  *string           `json:"github_webhook_error"`
	CreatedAt           db.Time           `json:"created_at"`
	UpdatedAt           db.Time           `json:"updated_at"`
}

const columns = `id, user_id, workspace_id, name, prompt, model_id, cron_expression, timezone, permission_mode, thinking_mode,
	worktree, selected_persona_name, enabled, next_run_at, last_run_at, trigger_type, github_repo, github_events, github_cursor,
	github_webhook_id, github_webhook_secret, github_webhook_error, created_at, updated_at`

func scan(row interface{ Scan(...any) error }) (*Automation, error) {
	x := &Automation{}
	err := row.Scan(&x.ID, &x.UserID, &x.WorkspaceID, &x.Name, &x.Prompt, &x.ModelID, &x.CronExpression, &x.Timezone,
		&x.PermissionMode, &x.ThinkingMode, &x.Worktree, &x.SelectedPersonaName, &x.Enabled, &x.NextRunAt, &x.LastRunAt,
		&x.TriggerType, &x.GithubRepo, &x.GithubEvents, &x.GithubCursor, &x.GithubWebhookID, &x.GithubWebhookSecret,
		&x.GithubWebhookError, &x.CreatedAt, &x.UpdatedAt)
	x.GithubWebhookActive = x.GithubWebhookID != nil
	return x, err
}

func (x *Automation) events() []string {
	if !x.GithubEvents.Valid {
		return nil
	}
	return x.GithubEvents.V
}

// Input is the create body and, with Set, the merged update.
type Input struct {
	WorkspaceID         *string   `json:"workspace_id"`
	Name                *string   `json:"name"`
	Prompt              *string   `json:"prompt"`
	ModelID             *string   `json:"model_id"`
	CronExpression      *string   `json:"cron_expression"`
	Timezone            *string   `json:"timezone"`
	PermissionMode      *string   `json:"permission_mode"`
	ThinkingMode        *string   `json:"thinking_mode"`
	Worktree            *bool     `json:"worktree"`
	SelectedPersonaName *string   `json:"selected_persona_name"`
	Enabled             *bool     `json:"enabled"`
	TriggerType         *string   `json:"trigger_type"`
	GithubRepo          *string   `json:"github_repo"`
	GithubEvents        *[]string `json:"github_events"`
	// Set lists the JSON keys present in the body (PATCH semantics).
	Set map[string]bool `json:"-"`
}

type Service struct {
	a *app.App
	// githubFor is swappable in tests.
	githubFor func(ctx context.Context, userID string) *GitHubClient
}

func NewService(a *app.App) *Service {
	s := &Service{a: a}
	s.githubFor = s.defaultGitHub
	return s
}

func (s *Service) defaultGitHub(ctx context.Context, userID string) *GitHubClient {
	st, err := s.a.Store.GetUserSettings(ctx, userID)
	if err != nil || db.Deref(st.GithubPersonalAccessToken) == "" {
		return nil
	}
	return NewGitHubClient(*st.GithubPersonalAccessToken)
}

func notFound() *httpx.Error { return httpx.NotFound("Automation not found") }

func lenBetween(field, v string, min, max int) error {
	n := utf8.RuneCountInString(v)
	if n < min || n > max {
		return httpx.Unprocessable(fmt.Sprintf("%s must be between %d and %d characters", field, min, max))
	}
	return nil
}

// validate checks field-level rules for the fields present in in.
func validate(in *Input) error {
	checks := []struct {
		name     string
		v        *string
		min, max int
	}{
		{"name", in.Name, 1, 255}, {"prompt", in.Prompt, 1, 100000}, {"model_id", in.ModelID, 1, 255},
		{"cron_expression", in.CronExpression, 1, 128}, {"timezone", in.Timezone, 1, 64},
		{"thinking_mode", in.ThinkingMode, 0, 50}, {"selected_persona_name", in.SelectedPersonaName, 1, 100},
		{"github_repo", in.GithubRepo, 0, 255},
	}
	for _, c := range checks {
		if c.v != nil {
			if err := lenBetween(c.name, *c.v, c.min, c.max); err != nil {
				return err
			}
		}
	}
	if in.CronExpression != nil && !ValidCron(*in.CronExpression) {
		return httpx.Unprocessable("Invalid cron expression")
	}
	if in.Timezone != nil {
		if _, err := LoadZone(*in.Timezone); err != nil {
			return httpx.Unprocessable("Unknown timezone")
		}
	}
	if in.PermissionMode != nil && !models.ValidPermissionMode(*in.PermissionMode) {
		return httpx.Unprocessable("Invalid permission mode")
	}
	if in.TriggerType != nil && *in.TriggerType != "schedule" && *in.TriggerType != "github" {
		return httpx.Unprocessable("trigger_type must be 'schedule' or 'github'")
	}
	if in.GithubRepo != nil {
		repo, err := NormalizeGitHubRepo(*in.GithubRepo)
		if err != nil {
			return httpx.Unprocessable(err.Error())
		}
		in.GithubRepo = &repo
	}
	if in.GithubEvents != nil {
		if len(*in.GithubEvents) > 10 {
			return httpx.Unprocessable("github_events can have at most 10 items")
		}
		evs, err := NormalizeGitHubEvents(*in.GithubEvents)
		if err != nil {
			return httpx.Unprocessable(err.Error())
		}
		in.GithubEvents = &evs
	}
	if in.ModelID != nil {
		if _, ok := models.LookupModel(*in.ModelID); !ok {
			return httpx.BadRequest("Unknown model")
		}
	}
	return nil
}

func validateTrigger(triggerType string, repo *string, events []string) error {
	if triggerType == "github" && (repo == nil || *repo == "" || len(events) == 0) {
		return errors.New("GitHub triggers need a repository and at least one event")
	}
	return nil
}

func (s *Service) nextRunValidated(expr, tz string) (time.Time, error) {
	t, err := NextRun(expr, tz, time.Now())
	if err != nil {
		return time.Time{}, httpx.BadRequest("Cron expression has no future occurrence in its timezone")
	}
	return t, nil
}

func (s *Service) validateWorkspace(ctx context.Context, workspaceID, userID string) error {
	var n int
	if err := s.a.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM workspaces WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, workspaceID, userID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return httpx.NotFound("Workspace not found")
	}
	return nil
}

func (s *Service) getOwned(ctx context.Context, id, userID string) (*Automation, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, notFound()
	}
	x, err := scan(s.a.DB.QueryRowContext(ctx, `SELECT `+columns+` FROM automations WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	return x, err
}

func (s *Service) getByID(ctx context.Context, id string) (*Automation, error) {
	x, err := scan(s.a.DB.QueryRowContext(ctx, `SELECT `+columns+` FROM automations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	return x, err
}

func (s *Service) List(ctx context.Context, userID string) ([]*Automation, error) {
	rows, err := s.a.DB.QueryContext(ctx, `SELECT `+columns+` FROM automations WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Automation{}
	for rows.Next() {
		x, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func deref[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

func (s *Service) Create(ctx context.Context, userID string, in *Input, baseURL string) (*Automation, error) {
	for name, v := range map[string]*string{"workspace_id": in.WorkspaceID, "name": in.Name, "prompt": in.Prompt, "model_id": in.ModelID, "cron_expression": in.CronExpression} {
		if v == nil {
			return nil, httpx.Unprocessable(name + ": Field required")
		}
	}
	if err := validate(in); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	x := &Automation{
		ID: uuid.NewString(), UserID: userID, WorkspaceID: *in.WorkspaceID, Name: *in.Name, Prompt: *in.Prompt,
		ModelID: *in.ModelID, CronExpression: *in.CronExpression, Timezone: deref(in.Timezone, "UTC"),
		PermissionMode: deref(in.PermissionMode, models.FullAccessPermissionMode), ThinkingMode: in.ThinkingMode,
		Worktree: deref(in.Worktree, false), SelectedPersonaName: deref(in.SelectedPersonaName, models.DefaultPersonaName),
		Enabled: deref(in.Enabled, true), TriggerType: deref(in.TriggerType, "schedule"), GithubRepo: in.GithubRepo,
		CreatedAt: db.T(now), UpdatedAt: db.T(now),
	}
	if in.GithubEvents != nil {
		x.GithubEvents = db.J(*in.GithubEvents)
	}
	if err := validateTrigger(x.TriggerType, x.GithubRepo, x.events()); err != nil {
		return nil, httpx.Unprocessable(err.Error())
	}
	if _, err := uuid.Parse(x.WorkspaceID); err != nil {
		return nil, httpx.Unprocessable("workspace_id: Input should be a valid UUID")
	}
	if err := s.validateWorkspace(ctx, x.WorkspaceID, userID); err != nil {
		return nil, err
	}
	next, err := s.nextRunValidated(x.CronExpression, x.Timezone)
	if err != nil {
		return nil, err
	}
	x.NextRunAt = db.T(next)
	if x.TriggerType == "github" {
		x.GithubCursor = db.NT(now)
	}
	_, err = s.a.DB.ExecContext(ctx, `INSERT INTO automations (id, user_id, workspace_id, name, prompt, model_id, cron_expression, timezone,
		permission_mode, thinking_mode, worktree, selected_persona_name, enabled, next_run_at, trigger_type, github_repo, github_events,
		github_cursor, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		x.ID, x.UserID, x.WorkspaceID, x.Name, x.Prompt, x.ModelID, x.CronExpression, x.Timezone, x.PermissionMode, x.ThinkingMode,
		x.Worktree, x.SelectedPersonaName, x.Enabled, x.NextRunAt, x.TriggerType, x.GithubRepo, x.GithubEvents, x.GithubCursor,
		x.CreatedAt, x.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if x.TriggerType == "github" {
		return s.syncWebhook(ctx, x, nil, nil, baseURL)
	}
	return x, nil
}

func (s *Service) Update(ctx context.Context, id, userID string, in *Input, baseURL string) (*Automation, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	x, err := s.getOwned(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	oldRepo, oldHook := x.GithubRepo, x.GithubWebhookID
	if in.WorkspaceID != nil {
		if _, err := uuid.Parse(*in.WorkspaceID); err != nil {
			return nil, httpx.Unprocessable("workspace_id: Input should be a valid UUID")
		}
		if err := s.validateWorkspace(ctx, *in.WorkspaceID, userID); err != nil {
			return nil, err
		}
		x.WorkspaceID = *in.WorkspaceID
	}
	set := in.Set
	assign := func(key string, dst *string, v *string) {
		if set[key] && v != nil {
			*dst = *v
		}
	}
	assign("name", &x.Name, in.Name)
	assign("prompt", &x.Prompt, in.Prompt)
	assign("model_id", &x.ModelID, in.ModelID)
	assign("cron_expression", &x.CronExpression, in.CronExpression)
	assign("timezone", &x.Timezone, in.Timezone)
	assign("permission_mode", &x.PermissionMode, in.PermissionMode)
	assign("selected_persona_name", &x.SelectedPersonaName, in.SelectedPersonaName)
	assign("trigger_type", &x.TriggerType, in.TriggerType)
	if set["thinking_mode"] {
		x.ThinkingMode = in.ThinkingMode
	}
	if set["worktree"] && in.Worktree != nil {
		x.Worktree = *in.Worktree
	}
	if set["enabled"] && in.Enabled != nil {
		x.Enabled = *in.Enabled
	}
	if set["github_repo"] {
		x.GithubRepo = in.GithubRepo
	}
	if set["github_events"] {
		x.GithubEvents = db.JSON[[]string]{}
		if in.GithubEvents != nil {
			x.GithubEvents = db.J(*in.GithubEvents)
		}
	}
	if err := validateTrigger(x.TriggerType, x.GithubRepo, x.events()); err != nil {
		return nil, httpx.BadRequest(err.Error())
	}
	now := time.Now().UTC()
	if set["trigger_type"] || set["github_repo"] {
		// Restart watching from now; never backfill history.
		x.GithubCursor = db.NullTime{}
		if x.TriggerType == "github" {
			x.GithubCursor = db.NT(now)
		}
	}
	if set["cron_expression"] || set["timezone"] || set["enabled"] {
		next, err := s.nextRunValidated(x.CronExpression, x.Timezone)
		if err != nil {
			return nil, err
		}
		x.NextRunAt = db.T(next)
	}
	x.UpdatedAt = db.T(now)
	_, err = s.a.DB.ExecContext(ctx, `UPDATE automations SET workspace_id = ?, name = ?, prompt = ?, model_id = ?, cron_expression = ?,
		timezone = ?, permission_mode = ?, thinking_mode = ?, worktree = ?, selected_persona_name = ?, enabled = ?, next_run_at = ?,
		trigger_type = ?, github_repo = ?, github_events = ?, github_cursor = ?, updated_at = ? WHERE id = ?`,
		x.WorkspaceID, x.Name, x.Prompt, x.ModelID, x.CronExpression, x.Timezone, x.PermissionMode, x.ThinkingMode, x.Worktree,
		x.SelectedPersonaName, x.Enabled, x.NextRunAt, x.TriggerType, x.GithubRepo, x.GithubEvents, x.GithubCursor, x.UpdatedAt, x.ID)
	if err != nil {
		return nil, err
	}
	// Re-register when what the hook watches changed, or retry a failed setup
	// on any save (e.g. after the token gained the webhook scope).
	if set["trigger_type"] || set["github_repo"] || set["github_events"] || (x.TriggerType == "github" && oldHook == nil) {
		return s.syncWebhook(ctx, x, oldRepo, oldHook, baseURL)
	}
	return x, nil
}

func (s *Service) Delete(ctx context.Context, id, userID string) error {
	x, err := s.getOwned(ctx, id, userID)
	if err != nil {
		return err
	}
	if _, err := s.a.DB.ExecContext(ctx, `DELETE FROM automations WHERE id = ?`, id); err != nil {
		return err
	}
	if x.GithubRepo != nil && x.GithubWebhookID != nil {
		s.deleteWebhook(ctx, userID, *x.GithubRepo, *x.GithubWebhookID)
	}
	return nil
}

// deleteWebhook is best effort: a hook left behind only delivers to a 404.
func (s *Service) deleteWebhook(ctx context.Context, userID, repo string, hookID int64) {
	gh := s.githubFor(ctx, userID)
	if gh == nil {
		return
	}
	if err := gh.DeleteWebhook(ctx, repo, hookID); err != nil {
		slog.Warn("could not remove GitHub webhook", "hook", hookID, "repo", repo, "err", err)
	}
}

func isPublicURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host != "" && host != "localhost" && host != "127.0.0.1" && host != "::1" &&
		!strings.HasSuffix(host, ".local") && !strings.HasSuffix(host, ".localhost")
}

// webhookURL is where GitHub delivers; localhost never qualifies. The
// address the user reached us on wins over BASE_URL.
func (s *Service) webhookURL(id, requestBase string) string {
	for _, base := range []string{requestBase, s.a.Cfg.BaseURL} {
		if base != "" && isPublicURL(base) {
			return strings.TrimRight(base, "/") + s.a.Cfg.APIPrefix + "/automations/" + id + "/github-webhook"
		}
	}
	return ""
}

// syncWebhook never fails the save: without a webhook the cron poll still works.
func (s *Service) syncWebhook(ctx context.Context, x *Automation, oldRepo *string, oldHook *int64, baseURL string) (*Automation, error) {
	if oldRepo != nil && *oldRepo != "" && oldHook != nil {
		s.deleteWebhook(ctx, x.UserID, *oldRepo, *oldHook)
	}
	var hookID *int64
	var secret, errMsg *string
	hookURL := s.webhookURL(x.ID, baseURL)
	gh := s.githubFor(ctx, x.UserID)
	switch {
	case x.TriggerType != "github" || x.GithubRepo == nil || *x.GithubRepo == "":
	case hookURL == "":
		errMsg = db.S("No public URL for GitHub to deliver to; checking on a schedule")
	case gh == nil:
		errMsg = db.S("Add a GitHub token in Settings to use GitHub triggers")
	default:
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		plain := hex.EncodeToString(b)
		seen := map[string]bool{}
		var hookEvents []string
		for _, e := range x.events() {
			if we, ok := githubWebhookEvents[e]; ok && !seen[we[0]] {
				seen[we[0]] = true
				hookEvents = append(hookEvents, we[0])
			}
		}
		sort.Strings(hookEvents)
		id, err := gh.CreateWebhook(ctx, *x.GithubRepo, hookURL, plain, hookEvents)
		if err != nil {
			errMsg = db.S(truncate(err.Error(), 255))
		} else {
			enc, err := s.a.Keys.Encrypt(plain)
			if err != nil {
				return nil, err
			}
			hookID, secret = &id, &enc
		}
	}
	_, err := s.a.DB.ExecContext(ctx, `UPDATE automations SET github_webhook_id = ?, github_webhook_secret = ?, github_webhook_error = ?, updated_at = ? WHERE id = ?`,
		hookID, secret, errMsg, db.Now(), x.ID)
	if err != nil {
		return nil, err
	}
	return s.getByID(ctx, x.ID)
}

// HandleWebhook reports whether a delivery should trigger a check. Unknown
// ids and bad signatures look the same (no oracle for valid ids).
func (s *Service) HandleWebhook(ctx context.Context, id string, body []byte, signature, event, action string) (bool, error) {
	if _, err := uuid.Parse(id); err != nil {
		return false, httpx.NotFound("Webhook not found")
	}
	x, err := s.getByID(ctx, id)
	if err != nil || x.TriggerType != "github" || db.Deref(x.GithubWebhookSecret) == "" {
		return false, httpx.NotFound("Webhook not found")
	}
	secret, err := s.a.Keys.Decrypt(*x.GithubWebhookSecret)
	if err != nil || !VerifyGitHubSignature(secret, body, signature) {
		return false, httpx.NotFound("Webhook not found")
	}
	if !x.Enabled {
		return false, nil
	}
	for _, e := range x.events() {
		if we, ok := githubWebhookEvents[e]; ok && we[0] == event && we[1] == action {
			return true, nil
		}
	}
	return false, nil
}

// CheckGitHubNow runs the same cursor-guarded poll the schedule uses, so a
// delivery and a poll racing never fire an event twice.
func (s *Service) CheckGitHubNow(ctx context.Context, id string) {
	select {
	case <-time.After(githubWebhookSettle):
	case <-ctx.Done():
		return
	}
	x, err := s.getByID(ctx, id)
	if err != nil || !x.Enabled || x.TriggerType != "github" {
		return
	}
	s.dispatch(ctx, x)
}

// Run is a manual "run now": records last_run_at but leaves next_run_at so the
// scheduled slot still fires. GitHub automations replay the latest matching
// event (without moving the cursor) so the prompt can be tested.
func (s *Service) Run(ctx context.Context, id, userID string) (string, error) {
	x, err := s.getOwned(ctx, id, userID)
	if err != nil {
		return "", err
	}
	var ev *ActivityEvent
	if x.TriggerType == "github" {
		gh := s.githubFor(ctx, userID)
		if gh == nil {
			return "", httpx.BadRequest("Add a GitHub token in Settings to use GitHub triggers")
		}
		evs, err := gh.ListActivity(ctx, db.Deref(x.GithubRepo), x.events(), time.Now().Add(-githubTestLookback), "")
		if err != nil {
			return "", githubHTTPError(err)
		}
		if len(evs) == 0 {
			return "", httpx.NotFound("No matching GitHub activity in the last 30 days to test with")
		}
		ev = &evs[len(evs)-1]
	}
	if _, err := s.a.DB.ExecContext(ctx, `UPDATE automations SET last_run_at = ?, updated_at = ? WHERE id = ?`, db.Now(), db.Now(), x.ID); err != nil {
		return "", err
	}
	return s.fire(ctx, x, ev)
}

func githubHTTPError(err error) error {
	var ge *GitHubError
	if errors.As(err, &ge) {
		return &httpx.Error{Status: ge.Status, Detail: ge.Msg}
	}
	return err
}

// RunDue is the dispatch tick. Schedules advance (compare-and-swap) before
// firing so a crash mid-dispatch can't re-fire the same slot.
func (s *Service) RunDue(ctx context.Context) {
	now := time.Now().UTC()
	rows, err := s.a.DB.QueryContext(ctx, `SELECT `+columns+` FROM automations WHERE enabled = 1 AND next_run_at <= ?`, db.T(now))
	if err != nil {
		slog.Warn("automation dispatch query", "err", err)
		return
	}
	var due []*Automation
	for rows.Next() {
		x, err := scan(rows)
		if err != nil {
			rows.Close()
			slog.Warn("automation scan", "err", err)
			return
		}
		due = append(due, x)
	}
	rows.Close()
	var toFire []*Automation
	for _, x := range due {
		next, err := NextRun(x.CronExpression, x.Timezone, now)
		if err != nil {
			// Uncomputable schedule must not abort the batch: disable it. The
			// guard spares a row a concurrent PATCH just repaired.
			slog.Error("automation schedule is uncomputable; disabling", "automation", x.ID, "name", x.Name, "err", err)
			_, _ = s.a.DB.ExecContext(ctx, `UPDATE automations SET enabled = 0 WHERE id = ? AND next_run_at = ?`, x.ID, x.NextRunAt)
			continue
		}
		if x.GithubWebhookID != nil && next.Before(now.Add(githubWebhookCatchup)) {
			next = now.Add(githubWebhookCatchup)
		}
		res, err := s.a.DB.ExecContext(ctx, `UPDATE automations SET next_run_at = ?, last_run_at = ? WHERE id = ? AND next_run_at = ?`,
			db.T(next), db.T(now), x.ID, x.NextRunAt)
		if err != nil {
			slog.Warn("automation claim", "automation", x.ID, "err", err)
			continue
		}
		if n, _ := res.RowsAffected(); n == 1 {
			toFire = append(toFire, x)
		}
	}
	for _, x := range toFire {
		s.dispatch(ctx, x)
	}
}

func (s *Service) dispatch(ctx context.Context, x *Automation) {
	var err error
	if x.TriggerType == "github" {
		err = s.pollGitHub(ctx, x)
	} else {
		_, err = s.fire(ctx, x, nil)
	}
	if err == nil {
		return
	}
	if errors.Is(err, ErrWorkspaceNotFound) {
		// Soft-deleted workspace: the run can never succeed again. Guard
		// against a concurrent repair pointing it elsewhere.
		_, _ = s.a.DB.ExecContext(ctx, `UPDATE automations SET enabled = 0 WHERE id = ? AND workspace_id = ?`, x.ID, x.WorkspaceID)
		slog.Warn("automation disabled: workspace deleted", "automation", x.ID, "workspace", x.WorkspaceID)
	}
	slog.Error("automation failed to start", "automation", x.ID, "name", x.Name, "err", err)
}

// pollGitHub advances the cursor (CAS) before firing: a crash mid-fire loses
// those runs rather than re-firing them every poll after restart.
func (s *Service) pollGitHub(ctx context.Context, x *Automation) error {
	gh := s.githubFor(ctx, x.UserID)
	if gh == nil {
		slog.Warn("automation has no GitHub token configured; skipping poll", "automation", x.ID)
		return nil
	}
	if !x.GithubCursor.Valid {
		// Never watched yet: start from now instead of replaying history.
		_, err := s.a.DB.ExecContext(ctx, `UPDATE automations SET github_cursor = ? WHERE id = ?`, db.Now(), x.ID)
		return err
	}
	cursor := x.GithubCursor.Time
	login, err := gh.AuthenticatedLogin(ctx)
	if err != nil {
		return err
	}
	evs, err := gh.ListActivity(ctx, db.Deref(x.GithubRepo), x.events(), cursor.Time, login)
	if err != nil {
		return err
	}
	if len(evs) > maxGitHubEventsPerPoll {
		evs = evs[:maxGitHubEventsPerPoll]
	}
	if len(evs) == 0 {
		return nil
	}
	res, err := s.a.DB.ExecContext(ctx, `UPDATE automations SET github_cursor = ? WHERE id = ? AND github_cursor = ?`,
		db.T(evs[len(evs)-1].CreatedAt), x.ID, cursor)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil
	}
	for i := range evs {
		if _, err := s.fire(ctx, x, &evs[i]); err != nil {
			return err
		}
	}
	return nil
}

// EventPrompt appends the triggering GitHub item to the automation prompt.
func EventPrompt(prompt string, ev *ActivityEvent) string {
	body := strings.TrimSpace(ev.Body)
	if utf8.RuneCountInString(body) > 20000 {
		body = string([]rune(body)[:20000]) + "\n…(truncated)"
	}
	if body == "" {
		body = "(empty)"
	}
	lines := []string{prompt, "", "<github_event>", "type: " + ev.Event, "repository: " + ev.Repo}
	numRef := "<number>"
	if ev.Number != nil {
		lines = append(lines, fmt.Sprintf("number: #%d", *ev.Number))
		if *ev.Number != 0 {
			numRef = fmt.Sprint(*ev.Number)
		}
	}
	lines = append(lines,
		"title: "+ev.Title, "author: "+ev.Author, "url: "+ev.URL, "body:", body, "</github_event>", "",
		// GITHUB_TOKEN (the user's PAT) is in the agent env, so gh acts as them.
		"The gh CLI is authenticated as the user; use it to act on this item"+
			fmt.Sprintf(" (e.g. `gh issue comment %s --repo %s --body ...`, which also works on pull requests).", numRef, ev.Repo),
	)
	return strings.Join(lines, "\n")
}

// RunTitle keeps recurring runs distinguishable in the sidebar.
func RunTitle(x *Automation, ev *ActivityEvent, now time.Time) string {
	var suffix string
	if ev != nil {
		label := githubEventLabels[ev.Event]
		if label == "" {
			label = "Event"
		}
		if ev.Number != nil && *ev.Number != 0 {
			suffix = fmt.Sprintf("%s #%d", label, *ev.Number)
		} else {
			suffix = ev.Title
		}
	} else {
		loc, err := LoadZone(x.Timezone)
		if err != nil {
			loc = time.UTC
		}
		suffix = now.In(loc).Format("Jan 02, 15:04")
	}
	title := []rune(x.Name + " · " + suffix)
	if len(title) > 255 {
		title = title[:255]
	}
	return string(title)
}

func (s *Service) fire(ctx context.Context, x *Automation, ev *ActivityEvent) (string, error) {
	// Re-check the model first: a retired model would otherwise leave an
	// orphan empty chat every slot.
	if _, ok := models.LookupModel(x.ModelID); !ok {
		return "", httpx.BadRequest("Unknown model")
	}
	if s.a.Hooks.StartChatTurn == nil {
		return "", httpx.Errorf(503, "Chat runtime unavailable")
	}
	prompt := x.Prompt
	if ev != nil {
		prompt = EventPrompt(x.Prompt, ev)
	}
	chat, err := CreateChat(ctx, s.a, x.UserID, x.WorkspaceID, RunTitle(x, ev, time.Now()), nil)
	if err != nil {
		return "", err
	}
	_, err = s.a.Hooks.StartChatTurn(app.ChatTurnRequest{
		UserID: x.UserID, ChatID: chat.ID, Prompt: prompt, ModelID: x.ModelID, PermissionMode: x.PermissionMode,
		ThinkingMode: x.ThinkingMode, Worktree: x.Worktree, SelectedPersonaName: x.SelectedPersonaName, Unattended: true,
	})
	if err != nil {
		DeleteChat(context.WithoutCancel(ctx), s.a, chat.ID)
		return "", err
	}
	return chat.ID, nil
}
