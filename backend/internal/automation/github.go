package automation

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const githubAPI = "https://api.github.com"

// activityPageSize is more than one poll interval's worth of new items on
// any sane repo; the dispatcher caps runs per poll anyway.
const activityPageSize = 50

var githubRepoRE = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)

// GitHubEvents are the trigger events a GitHub automation can watch.
var GitHubEvents = []string{"pull_request_opened", "issue_opened", "issue_comment_created", "release_published"}

var githubEventLabels = map[string]string{
	"pull_request_opened":   "PR",
	"issue_opened":          "Issue",
	"issue_comment_created": "Comment",
	"release_published":     "Release",
}

// githubWebhookEvents maps a trigger to the (webhook event, action) GitHub delivers.
var githubWebhookEvents = map[string][2]string{
	"pull_request_opened":   {"pull_request", "opened"},
	"issue_opened":          {"issues", "opened"},
	"issue_comment_created": {"issue_comment", "created"},
	"release_published":     {"release", "published"},
}

func NormalizeGitHubRepo(v string) (string, error) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "https://github.com/")
	v = strings.TrimRight(v, "/")
	if !githubRepoRE.MatchString(v) {
		return "", fmt.Errorf("GitHub repository must look like owner/name")
	}
	return v, nil
}

// NormalizeGitHubEvents validates and dedupes while keeping the user's order.
func NormalizeGitHubEvents(events []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, e := range events {
		if _, ok := githubEventLabels[e]; !ok {
			return nil, fmt.Errorf("Unknown GitHub event: %s", e)
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out, nil
}

// VerifyGitHubSignature checks X-Hub-Signature-256 against the hook secret.
func VerifyGitHubSignature(secret string, body []byte, signature string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// ActivityEvent is one new item in a watched repo.
type ActivityEvent struct {
	Event     string
	Repo      string
	Number    *int
	Title     string
	URL       string
	Author    string
	Body      string
	CreatedAt time.Time
}

type GitHubError struct {
	Msg    string
	Status int
}

func (e *GitHubError) Error() string { return e.Msg }

type GitHubClient struct {
	token string
	http  *http.Client
	base  string
}

func NewGitHubClient(token string) *GitHubClient {
	return &GitHubClient{token: token, http: &http.Client{Timeout: 15 * time.Second}, base: githubAPI}
}

func (g *GitHubClient) do(ctx context.Context, method, path string, query url.Values, body any) (*http.Response, []byte, error) {
	u := g.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return nil, nil, &GitHubError{Msg: "GitHub API request failed: " + err.Error(), Status: 502}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// Not a 401: the frontend would treat it as its own session expiring.
		return nil, nil, &GitHubError{Msg: "GitHub token is invalid or expired", Status: 400}
	}
	return resp, data, nil
}

func (g *GitHubClient) AuthenticatedLogin(ctx context.Context) (string, error) {
	resp, data, err := g.do(ctx, http.MethodGet, "/user", nil, nil)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", &GitHubError{Msg: "GitHub API request failed", Status: 502}
	}
	var u struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(data, &u); err != nil {
		return "", err
	}
	return u.Login, nil
}

func (g *GitHubClient) getList(ctx context.Context, path string, q url.Values) ([]map[string]any, error) {
	resp, data, err := g.do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, &GitHubError{Msg: "Repository not found or the GitHub token can't access it", Status: 404}
	}
	if resp.StatusCode != http.StatusOK {
		slog.Warn("GitHub API error", "status", resp.StatusCode, "body", truncate(string(data), 200))
		return nil, &GitHubError{Msg: "GitHub API request failed", Status: 502}
	}
	var out []map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListActivity returns items created strictly after since, oldest first.
// excludeAuthor drops our own comments so an automation that comments can't
// trigger itself. Our own PRs and issues still count: PRs Claude opens with
// the user's token are exactly what a review-and-merge automation watches.
func (g *GitHubClient) ListActivity(ctx context.Context, repo string, events []string, since time.Time, excludeAuthor string) ([]ActivityEvent, error) {
	want := map[string]bool{}
	for _, e := range events {
		want[e] = true
	}
	sinceStr := since.UTC().Format(time.RFC3339)
	var found []ActivityEvent
	if want["pull_request_opened"] || want["issue_opened"] {
		items, err := g.getList(ctx, "/repos/"+repo+"/issues", url.Values{
			"state": {"all"}, "sort": {"created"}, "direction": {"desc"}, "since": {sinceStr}, "per_page": {strconv.Itoa(activityPageSize)},
		})
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			kind := "issue_opened"
			if _, ok := item["pull_request"]; ok {
				kind = "pull_request_opened"
			}
			if want[kind] {
				found = append(found, activity(kind, repo, item, intPtr(item["number"]), str(item["title"])))
			}
		}
	}
	if want["issue_comment_created"] {
		items, err := g.getList(ctx, "/repos/"+repo+"/issues/comments", url.Values{
			"sort": {"created"}, "direction": {"desc"}, "since": {sinceStr}, "per_page": {strconv.Itoa(activityPageSize)},
		})
		if err != nil {
			return nil, err
		}
		for _, c := range items {
			issueURL := str(c["issue_url"])
			n, _ := strconv.Atoi(issueURL[strings.LastIndex(issueURL, "/")+1:])
			found = append(found, activity("issue_comment_created", repo, c, &n, fmt.Sprintf("Comment on #%d", n)))
		}
	}
	if want["release_published"] {
		items, err := g.getList(ctx, "/repos/"+repo+"/releases", url.Values{"per_page": {"20"}})
		if err != nil {
			return nil, err
		}
		for _, rel := range items {
			if draft, _ := rel["draft"].(bool); draft || str(rel["published_at"]) == "" {
				continue
			}
			rel["created_at"] = rel["published_at"]
			title := str(rel["name"])
			if title == "" {
				title = str(rel["tag_name"])
			}
			found = append(found, activity("release_published", repo, rel, nil, title))
		}
	}
	out := found[:0]
	for _, e := range found {
		own := excludeAuthor != "" && e.Author == excludeAuthor && e.Event == "issue_comment_created"
		if e.CreatedAt.After(since) && !own {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func activity(kind, repo string, item map[string]any, number *int, title string) ActivityEvent {
	author := "unknown"
	for _, k := range []string{"user", "author"} {
		if m, ok := item[k].(map[string]any); ok {
			if l := str(m["login"]); l != "" {
				author = l
				break
			}
		}
	}
	created, _ := time.Parse(time.RFC3339, str(item["created_at"]))
	return ActivityEvent{
		Event: kind, Repo: repo, Number: number, Title: title, URL: str(item["html_url"]),
		Author: author, Body: str(item["body"]), CreatedAt: created.UTC(),
	}
}

// CreateWebhook needs admin:repo_hook (classic) or Webhooks: write (fine-grained).
func (g *GitHubClient) CreateWebhook(ctx context.Context, repo, hookURL, secret string, events []string) (int64, error) {
	resp, data, err := g.do(ctx, http.MethodPost, "/repos/"+repo+"/hooks", nil, map[string]any{
		"name": "web", "active": true, "events": events,
		"config": map[string]any{"url": hookURL, "content_type": "json", "secret": secret, "insecure_ssl": "0"},
	})
	if err != nil {
		return 0, err
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return 0, &GitHubError{Msg: "GitHub token can't manage webhooks on this repository", Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusCreated {
		var body struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(data, &body)
		var msgs []string
		for _, e := range body.Errors {
			msgs = append(msgs, e.Message)
		}
		msg := strings.Join(msgs, "; ")
		if strings.TrimSpace(strings.ReplaceAll(msg, ";", "")) == "" {
			msg = "GitHub refused to create the webhook"
		}
		slog.Warn("GitHub webhook creation failed", "status", resp.StatusCode, "body", truncate(string(data), 200))
		return 0, &GitHubError{Msg: msg, Status: resp.StatusCode}
	}
	var hook struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &hook); err != nil {
		return 0, err
	}
	return hook.ID, nil
}

func (g *GitHubClient) DeleteWebhook(ctx context.Context, repo string, hookID int64) error {
	resp, _, err := g.do(ctx, http.MethodDelete, fmt.Sprintf("/repos/%s/hooks/%d", repo, hookID), nil, nil)
	if err != nil {
		return err
	}
	// 404: already removed on GitHub's side.
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return &GitHubError{Msg: "Failed to remove the GitHub webhook", Status: resp.StatusCode}
	}
	return nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func intPtr(v any) *int {
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	n := int(f)
	return &n
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
