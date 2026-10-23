// Package github serves /api/v1/github: repository search, pull requests,
// collaborators (via the user's PAT) and AI-written PR descriptions/commit
// messages.
package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/prompts"
)

const (
	apiBase = "https://api.github.com"
	// Repo search filters the user's own list: up to 1000 repos, reused for a
	// minute because search re-runs on every keystroke.
	accessibleMaxPages = 10
	accessibleTTL      = time.Minute
)

type Module struct {
	a      *app.App
	client *http.Client
	base   string

	mu    sync.Mutex
	repos map[string]cachedRepos
}

type cachedRepos struct {
	at    time.Time
	repos []map[string]any
}

func New(a *app.App) *Module {
	return &Module{a: a, client: &http.Client{Timeout: 15 * time.Second}, base: apiBase, repos: map[string]cachedRepos{}}
}

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix + "/github"
	req := m.a.Auth.Require
	mux.HandleFunc("GET "+p+"/repositories", req(m.listRepositories))
	mux.HandleFunc("GET "+p+"/pulls", req(m.listPulls))
	mux.HandleFunc("POST "+p+"/pulls", req(m.createPull))
	mux.HandleFunc("POST "+p+"/generate-pr-description", req(m.generatePRDescription))
	mux.HandleFunc("POST "+p+"/generate-commit-message", req(m.generateCommitMessage))
	mux.HandleFunc("GET "+p+"/collaborators", req(m.listCollaborators))
}

func (m *Module) token(r *http.Request) (string, error) {
	st, err := m.a.Store.GetUserSettings(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		return "", err
	}
	tok := db.Deref(st.GithubPersonalAccessToken)
	if tok == "" {
		return "", httpx.BadRequest("GitHub personal access token not configured")
	}
	return tok, nil
}

// ghError is a GitHub failure; 502 unless stated (GitHubException default).
func ghError(msg string, status int) error {
	if status == 0 {
		status = http.StatusBadGateway
	}
	return &httpx.Error{Status: status, Detail: msg}
}

type ghResponse struct {
	status int
	body   []byte
}

func (m *Module) do(ctx context.Context, token, method, path string, query url.Values, payload any) (ghResponse, error) {
	u := m.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return ghResponse{}, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return ghResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return ghResponse{}, ghError("GitHub API request failed", 0)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return ghResponse{}, ghError("GitHub API request failed", 0)
	}
	// A bad PAT must not look like our own 401, or the frontend logs out.
	if resp.StatusCode == http.StatusUnauthorized {
		return ghResponse{}, ghError("GitHub token is invalid or expired", http.StatusBadRequest)
	}
	return ghResponse{status: resp.StatusCode, body: b}, nil
}

func snippet(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func (m *Module) getJSON(ctx context.Context, token, path string, query url.Values, out any) error {
	res, err := m.do(ctx, token, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	if res.status != http.StatusOK {
		slog.Warn("GitHub API error", "status", res.status, "body", snippet(res.body))
		return ghError("GitHub API request failed", 0)
	}
	if err := json.Unmarshal(res.body, out); err != nil {
		return ghError("GitHub API request failed", 0)
	}
	return nil
}

// ---- repositories ----

type repo struct {
	Name            string  `json:"name"`
	FullName        string  `json:"full_name"`
	Description     *string `json:"description"`
	Language        *string `json:"language"`
	HTMLURL         string  `json:"html_url"`
	CloneURL        string  `json:"clone_url"`
	Private         bool    `json:"private"`
	PushedAt        *string `json:"pushed_at"`
	StargazersCount int     `json:"stargazers_count"`
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func strOrNil(m map[string]any, k string) *string {
	if s, ok := m[k].(string); ok {
		return &s
	}
	return nil
}

func num(m map[string]any, k string) int {
	f, _ := m[k].(float64)
	return int(f)
}

func toRepo(r map[string]any) repo {
	private, _ := r["private"].(bool)
	return repo{
		Name: str(r, "name"), FullName: str(r, "full_name"), Description: strOrNil(r, "description"),
		Language: strOrNil(r, "language"), HTMLURL: str(r, "html_url"), CloneURL: str(r, "clone_url"),
		Private: private, PushedAt: strOrNil(r, "pushed_at"), StargazersCount: num(r, "stargazers_count"),
	}
}

func (m *Module) userReposPage(ctx context.Context, token string, page, perPage int) ([]map[string]any, error) {
	q := url.Values{
		"sort": {"pushed"}, "direction": {"desc"}, "per_page": {strconv.Itoa(perPage)}, "page": {strconv.Itoa(page)},
		"affiliation": {"owner,collaborator,organization_member"},
	}
	var out []map[string]any
	if err := m.getJSON(ctx, token, "/user/repos", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *Module) accessibleRepos(ctx context.Context, token string) ([]map[string]any, error) {
	sum := sha256.Sum256([]byte("Bearer " + token))
	key := hex.EncodeToString(sum[:])
	m.mu.Lock()
	c, ok := m.repos[key]
	m.mu.Unlock()
	if ok && time.Since(c.at) < accessibleTTL {
		return c.repos, nil
	}
	var all []map[string]any
	for page := 1; page <= accessibleMaxPages; page++ {
		batch, err := m.userReposPage(ctx, token, page, 100)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	m.mu.Lock()
	for k, v := range m.repos {
		if time.Since(v.at) >= accessibleTTL {
			delete(m.repos, k)
		}
	}
	m.repos[key] = cachedRepos{at: time.Now(), repos: all}
	m.mu.Unlock()
	return all, nil
}

func queryInt(r *http.Request, key string, def, lo, hi int) (int, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || (hi > 0 && n > hi) {
		return 0, httpx.Unprocessable(fmt.Sprintf("Invalid %s", key))
	}
	return n, nil
}

func (m *Module) listRepositories(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query().Get("q")
	if len(q) > 256 {
		return httpx.Unprocessable("q must be at most 256 characters")
	}
	page, err := queryInt(r, "page", 1, 1, 0)
	if err != nil {
		return err
	}
	perPage, err := queryInt(r, "per_page", 20, 1, 100)
	if err != nil {
		return err
	}
	token, err := m.token(r)
	if err != nil {
		return err
	}
	terms := strings.Fields(strings.ToLower(q))
	if len(terms) > 0 {
		// Only repos the user can reach: /search/repositories spans all of GitHub.
		all, err := m.accessibleRepos(r.Context(), token)
		if err != nil {
			return err
		}
		var matches []map[string]any
		for _, rp := range all {
			hay := strings.ToLower(str(rp, "full_name") + " " + str(rp, "description"))
			ok := true
			for _, t := range terms {
				if !strings.Contains(hay, t) {
					ok = false
					break
				}
			}
			if ok {
				matches = append(matches, rp)
			}
		}
		start := (page - 1) * perPage
		items := []repo{}
		for i := start; i < len(matches) && i < start+perPage; i++ {
			items = append(items, toRepo(matches[i]))
		}
		return httpx.OK(w, map[string]any{"items": items, "has_more": len(matches) > start+perPage})
	}
	raw, err := m.userReposPage(r.Context(), token, page, perPage)
	if err != nil {
		return err
	}
	items := make([]repo, 0, len(raw))
	for _, rp := range raw {
		items = append(items, toRepo(rp))
	}
	return httpx.OK(w, map[string]any{"items": items, "has_more": len(raw) == perPage})
}

// ---- pull requests ----

func (m *Module) listPulls(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	owner, repoName := qs.Get("owner"), qs.Get("repo")
	if owner == "" || repoName == "" {
		return httpx.Unprocessable("owner and repo are required")
	}
	state := qs.Get("state")
	if state == "" {
		state = "open"
	}
	if state != "open" && state != "closed" && state != "all" {
		return httpx.Unprocessable("state must be one of 'open', 'closed', 'all'")
	}
	if _, ok := qs["head"]; ok && qs.Get("head") == "" {
		return httpx.Unprocessable("head must not be empty")
	}
	token, err := m.token(r)
	if err != nil {
		return err
	}
	q := url.Values{"state": {state}, "per_page": {"30"}, "sort": {"updated"}, "direction": {"desc"}}
	if head := qs.Get("head"); head != "" {
		q.Set("head", head)
	}
	var raw []map[string]any
	if err := m.getJSON(r.Context(), token, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repoName)+"/pulls", q, &raw); err != nil {
		return err
	}
	items := make([]map[string]any, 0, len(raw))
	for _, pr := range raw {
		head, _ := pr["head"].(map[string]any)
		base, _ := pr["base"].(map[string]any)
		user, _ := pr["user"].(map[string]any)
		headRepo := ""
		if hr, ok := head["repo"].(map[string]any); ok {
			headRepo = str(hr, "full_name")
		}
		draft, _ := pr["draft"].(bool)
		items = append(items, map[string]any{
			"number": num(pr, "number"), "title": str(pr, "title"), "body": strOrNil(pr, "body"),
			"state": str(pr, "state"), "html_url": str(pr, "html_url"),
			"head":            map[string]any{"ref": str(head, "ref"), "sha": str(head, "sha"), "repo": map[string]any{"full_name": headRepo}},
			"base":            map[string]any{"ref": str(base, "ref")},
			"user":            map[string]any{"login": str(user, "login"), "avatar_url": str(user, "avatar_url")},
			"draft":           draft,
			"review_comments": num(pr, "review_comments"),
			"merged":          pr["merged_at"] != nil,
		})
	}
	return httpx.OK(w, map[string]any{"items": items})
}

func (m *Module) createPull(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Owner     *string  `json:"owner"`
		Repo      *string  `json:"repo"`
		Title     *string  `json:"title"`
		Body      *string  `json:"body"`
		Head      *string  `json:"head"`
		Base      *string  `json:"base"`
		Reviewers []string `json:"reviewers"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Owner == nil || body.Repo == nil || body.Title == nil || body.Body == nil || body.Head == nil || body.Base == nil {
		return httpx.Unprocessable("owner, repo, title, body, head and base are required")
	}
	token, err := m.token(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	path := "/repos/" + url.PathEscape(*body.Owner) + "/" + url.PathEscape(*body.Repo) + "/pulls"
	res, err := m.do(ctx, token, http.MethodPost, path, nil, map[string]string{
		"title": *body.Title, "body": *body.Body, "head": *body.Head, "base": *body.Base,
	})
	if err != nil {
		return err
	}
	if res.status != http.StatusOK && res.status != http.StatusCreated {
		slog.Warn("GitHub create PR failed", "status", res.status, "body", snippet(res.body))
		detail := "Failed to create pull request"
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(res.body, &e) == nil && e.Message != "" {
			detail = e.Message
		}
		return ghError(detail, 0)
	}
	var pr map[string]any
	if err := json.Unmarshal(res.body, &pr); err != nil {
		return ghError("Failed to create pull request", 0)
	}
	number := num(pr, "number")
	var warning *string
	if len(body.Reviewers) > 0 {
		rr, err := m.do(ctx, token, http.MethodPost, fmt.Sprintf("%s/%d/requested_reviewers", path, number), nil, map[string]any{"reviewers": body.Reviewers})
		if err != nil || (rr.status != http.StatusOK && rr.status != http.StatusCreated) {
			msg := "Failed to assign reviewers"
			warning = &msg
			slog.Warn("GitHub assign reviewers failed", "pr", number, "status", rr.status, "body", snippet(rr.body))
		}
	}
	return httpx.OK(w, map[string]any{"number": number, "html_url": str(pr, "html_url"), "title": str(pr, "title"), "reviewer_warning": warning})
}

func (m *Module) listCollaborators(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	owner, repoName := qs.Get("owner"), qs.Get("repo")
	if owner == "" || repoName == "" {
		return httpx.Unprocessable("owner and repo are required")
	}
	token, err := m.token(r)
	if err != nil {
		return err
	}
	res, err := m.do(r.Context(), token, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repoName)+"/collaborators", url.Values{"per_page": {"50"}}, nil)
	if err != nil {
		return err
	}
	out := []map[string]string{}
	// 403/404 are expected without push access.
	if res.status == http.StatusForbidden || res.status == http.StatusNotFound {
		return httpx.OK(w, out)
	}
	if res.status != http.StatusOK {
		slog.Warn("GitHub collaborators failed", "status", res.status, "body", snippet(res.body))
		return ghError("Failed to load collaborators", 0)
	}
	var raw []map[string]any
	if err := json.Unmarshal(res.body, &raw); err != nil {
		return ghError("Failed to load collaborators", 0)
	}
	for _, c := range raw {
		out = append(out, map[string]string{"login": str(c, "login"), "avatar_url": str(c, "avatar_url")})
	}
	return httpx.OK(w, out)
}

// ---- AI text ----

func (m *Module) generate(r *http.Request, modelID, system, prompt string) (string, error) {
	gen := m.a.Hooks.GenerateText
	if gen == nil {
		return "", httpx.Errorf(http.StatusServiceUnavailable, "Text generation is not available")
	}
	out, err := gen(r.Context(), auth.UserFrom(r.Context()).ID, modelID, system, prompt)
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) {
			return "", he
		}
		return "", httpx.BadRequest(err.Error())
	}
	return strings.TrimSpace(out), nil
}

func validLen(s string, lo, hi int) bool {
	n := utf8.RuneCountInString(s)
	return n >= lo && n <= hi
}

func (m *Module) generatePRDescription(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Title   string `json:"title"`
		Diff    string `json:"diff"`
		ModelID string `json:"model_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if !validLen(body.Title, 0, 256) || !validLen(body.Diff, 1, 200_000) || !validLen(body.ModelID, 0, 128) {
		return httpx.Unprocessable("title ≤256, diff 1-200000 and model_id ≤128 characters")
	}
	out, err := m.generate(r, body.ModelID, prompts.GeneratePrDescriptionSystemPrompt, prompts.GeneratePrDescriptionTitlePrefix+body.Title+"\n\n"+body.Diff)
	if err != nil {
		return err
	}
	if out == "" {
		return httpx.BadRequest("AI returned an empty description")
	}
	return httpx.OK(w, map[string]string{"description": out})
}

func (m *Module) generateCommitMessage(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Diff    string `json:"diff"`
		ModelID string `json:"model_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if !validLen(body.Diff, 1, 200_000) || !validLen(body.ModelID, 0, 128) {
		return httpx.Unprocessable("diff 1-200000 and model_id ≤128 characters")
	}
	out, err := m.generate(r, body.ModelID, prompts.GenerateCommitMessageSystemPrompt, body.Diff)
	if err != nil {
		return err
	}
	if out == "" {
		return httpx.BadRequest("AI returned an empty commit message")
	}
	return httpx.OK(w, map[string]string{"message": out})
}
