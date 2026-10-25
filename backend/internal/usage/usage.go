// Package usage serves GET /api/v1/usage/claude: the 5-hour / weekly usage
// windows of the Claude account or proxy the user's agents run through. The
// endpoint follows ANTHROPIC_BASE_URL: Anthropic's own OAuth usage endpoint
// when it is unset or api.anthropic.com, otherwise <base URL>/api/usage
// (proxies mirror the OAuth usage layout), unless CLAUDE_USAGE_URL names
// the endpoint.
package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
	"agentrove/internal/profiles"
)

const (
	fetchTimeout = 10 * time.Second
	// Gateways rate-limit their usage endpoint and every open tab polls, so
	// one fetch per user is shared for a while; a manual refresh may cut it
	// short, not to zero.
	cacheTTL   = 60 * time.Second
	minRefresh = 10 * time.Second
)

type Window struct {
	UsedPercent float64     `json:"used_percent"`
	ResetsAt    db.NullTime `json:"resets_at"`
}

type Response struct {
	Status    string  `json:"status"`
	Provider  *string `json:"provider"`
	Session   *Window `json:"session"`
	Weekly    *Window `json:"weekly"`
	Error     *string `json:"error"`
	FetchedAt db.Time `json:"fetched_at"`
}

const (
	officialHost     = "api.anthropic.com"
	officialEndpoint = "https://api.anthropic.com/api/oauth/usage"
	proxyUsagePath   = "/api/usage"
	oauthTokenPrefix = "sk-ant-oat"
	usageURLKey      = "CLAUDE_USAGE_URL"
)

type Source struct {
	Provider string
	URL      string
	Token    string
	Headers  map[string]string
	// PublicOnly is set when a user's own var picked the host: the server
	// must not be steerable at internal addresses.
	PublicOnly bool
}

// ResolveSource applies the precedence the agent sees (deployment env, then
// the user's vars) and picks the usage endpoint for ANTHROPIC_BASE_URL: the
// official endpoint when it is unset or Anthropic's, else <base URL>/api/usage
// (https only, since the token goes with it). CLAUDE_USAGE_URL replaces the
// endpoint for proxies that serve usage elsewhere; the token stays the one
// for the base URL.
func ResolveSource(globalEnv map[string]string, custom []models.CustomEnvVar) *Source {
	env := map[string]string{}
	for k, v := range globalEnv {
		env[k] = v
	}
	userBase, userUsage := false, false
	for _, ev := range custom {
		env[ev.Key] = ev.Value
		switch ev.Key {
		case "ANTHROPIC_BASE_URL":
			userBase = true
		case usageURLKey:
			userUsage = true
		}
	}
	var base *url.URL
	if raw := strings.TrimSpace(env["ANTHROPIC_BASE_URL"]); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return nil
		}
		base = u
	}
	src := &Source{Provider: officialHost}
	switch {
	case base == nil || strings.EqualFold(base.Hostname(), officialHost):
		// The official endpoint only takes the OAuth token the CLI logs in
		// with; an API key would just be rejected on every poll.
		src.URL = officialEndpoint
		src.Token = firstEnv(env, "CLAUDE_CODE_OAUTH_TOKEN")
		if src.Token == "" {
			if t := firstEnv(env, "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"); strings.HasPrefix(t, oauthTokenPrefix) {
				src.Token = t
			}
		}
		src.Headers = map[string]string{"anthropic-beta": "oauth-2025-04-20"}
	case base.Scheme == "https":
		src.Provider = strings.ToLower(base.Hostname())
		src.URL = "https://" + base.Host + strings.TrimRight(base.Path, "/") + proxyUsagePath
		src.Token = firstEnv(env, "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY")
		src.PublicOnly = userBase
	}
	if raw := strings.TrimSpace(env[usageURLKey]); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" {
			return nil
		}
		src.URL = u.String()
		src.PublicOnly = src.PublicOnly || userUsage
	}
	if src.URL == "" || src.Token == "" {
		return nil
	}
	return src
}

func firstEnv(env map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(env[k]); v != "" {
			return v
		}
	}
	return ""
}

// parseReset accepts epoch seconds/milliseconds (number or numeric string)
// or an ISO timestamp.
func parseReset(v any) db.NullTime {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || x <= 0 {
			return db.NullTime{}
		}
		secs := x
		if x > 10_000_000_000 {
			secs = x / 1000
		}
		whole, frac := math.Modf(secs)
		return db.NT(time.Unix(int64(whole), int64(frac*1e9)))
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return db.NullTime{}
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return parseReset(f)
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02"} {
			if t, err := time.Parse(layout, strings.Replace(s, "Z", "+00:00", 1)); err == nil {
				return db.NT(t)
			}
			if t, err := time.Parse(layout, s); err == nil {
				return db.NT(t)
			}
		}
	}
	return db.NullTime{}
}

func parseWindow(raw any) *Window {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	used, found := 0.0, false
	for _, k := range []string{"utilization", "used_percentage", "usedPercent"} {
		if f, ok := m[k].(float64); ok {
			used, found = f, true
			break
		}
	}
	if !found || math.IsNaN(used) || math.IsInf(used, 0) {
		return nil
	}
	reset, ok := m["resets_at"]
	if !ok {
		reset = m["resetsAt"]
	}
	return &Window{UsedPercent: math.Min(100, math.Max(0, used)), ResetsAt: parseReset(reset)}
}

// ParseBody reads Anthropic's OAuth usage layout (five_hour / seven_day).
func ParseBody(provider string, body any) Response {
	p := provider
	m, ok := body.(map[string]any)
	if !ok {
		return errorResponse(provider, "Usage response was not a JSON object")
	}
	session, weekly := parseWindow(m["five_hour"]), parseWindow(m["seven_day"])
	if session == nil && weekly == nil {
		return errorResponse(provider, "Usage response had no 5-hour or weekly window")
	}
	return Response{Status: "ok", Provider: &p, Session: session, Weekly: weekly, FetchedAt: db.Now()}
}

func errorResponse(provider, msg string) Response {
	return Response{Status: "error", Provider: &provider, Error: &msg, FetchedAt: db.Now()}
}

type cacheEntry struct {
	at  time.Time
	res Response
}

type Module struct {
	a      *app.App
	client *http.Client
	// publicClient refuses non-public addresses, for user-picked hosts.
	publicClient *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry
	locks map[string]*sync.Mutex
}

func New(a *app.App) *Module {
	return &Module{
		a:            a,
		client:       newClient(nil),
		publicClient: newClient(publicOnlyControl),
		cache:        map[string]cacheEntry{},
		locks:        map[string]*sync.Mutex{},
	}
}

func newClient(control func(network, address string, c syscall.RawConn) error) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: fetchTimeout, Control: control}).DialContext
	return &http.Client{Timeout: fetchTimeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// publicOnlyControl runs after DNS resolution, so a public name that
// resolves to an internal address is refused too.
func publicOnlyControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return fmt.Errorf("usage endpoint %s is not a public address", ip)
	}
	return nil
}

func (m *Module) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+m.a.Cfg.APIPrefix+"/usage/claude", m.a.Auth.Require(m.get))
}

func (m *Module) get(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	refresh := false
	if b := httpx.QueryBool(r, "refresh"); b != nil {
		refresh = *b
	}
	// The meter follows a profile: the chat's (profile_id), else the default
	// Claude one. A profile's endpoint and usage URL were chosen by the user,
	// so they go in as user vars (public addresses only).
	globalEnv := m.a.Cfg.AgentGlobalEnv()
	var custom []models.CustomEnvVar
	p := profiles.Builtin(u.ID)
	if m.a.Profiles != nil {
		if id := r.URL.Query().Get("profile_id"); id != "" {
			got, err := m.a.Profiles.Get(r.Context(), u.ID, id)
			if err != nil {
				return err
			}
			p = got
		} else {
			p = m.a.Profiles.DefaultClaude(r.Context(), u.ID)
		}
	}
	if p.Provider != models.ProviderClaude {
		return httpx.OK(w, Response{Status: "unavailable", FetchedAt: db.Now()})
	}
	if p.Builtin {
		if st, err := m.a.Store.GetUserSettings(r.Context(), u.ID); err == nil && st.CustomEnvVars.Valid {
			custom = st.CustomEnvVars.V
		}
	} else {
		globalEnv = map[string]string{}
		for k, v := range p.ApplyEnv(nil, nil) {
			custom = append(custom, models.CustomEnvVar{Key: k, Value: v})
		}
	}
	src := ResolveSource(globalEnv, custom)
	return httpx.OK(w, m.usage(r.Context(), u.ID, src, refresh))
}

func cacheKey(userID string, s *Source) string {
	// Token hashed so a rotated token never reuses the old account's numbers.
	sum := sha256.Sum256([]byte(s.Token))
	return userID + ":" + s.URL + ":" + hex.EncodeToString(sum[:])[:16]
}

func (m *Module) usage(ctx context.Context, userID string, src *Source, refresh bool) Response {
	if src == nil {
		return Response{Status: "unavailable", FetchedAt: db.Now()}
	}
	key := cacheKey(userID, src)
	m.mu.Lock()
	lock := m.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		m.locks[key] = lock
	}
	m.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()

	m.mu.Lock()
	cached, ok := m.cache[key]
	m.mu.Unlock()
	limit := cacheTTL
	if refresh {
		limit = minRefresh
	}
	if ok && time.Since(cached.at) < limit {
		return cached.res
	}
	res := m.fetch(ctx, src)
	m.mu.Lock()
	for k, e := range m.cache {
		if time.Since(e.at) >= cacheTTL && k != key {
			delete(m.cache, k)
		}
	}
	m.cache[key] = cacheEntry{at: time.Now(), res: res}
	m.mu.Unlock()
	return res
}

func (m *Module) fetch(ctx context.Context, src *Source) Response {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return errorResponse(src.Provider, "Usage endpoint unreachable")
	}
	req.Header.Set("Authorization", "Bearer "+src.Token)
	req.Header.Set("Accept", "application/json")
	for k, v := range src.Headers {
		req.Header.Set(k, v)
	}
	client := m.client
	if src.PublicOnly {
		client = m.publicClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return errorResponse(src.Provider, "Usage endpoint unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errorResponse(src.Provider, "Usage endpoint rejected the token")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errorResponse(src.Provider, fmt.Sprintf("Usage endpoint returned HTTP %d", resp.StatusCode))
	}
	var body any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return errorResponse(src.Provider, "Usage response was not valid JSON")
	}
	return ParseBody(src.Provider, body)
}
