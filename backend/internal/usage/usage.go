// Package usage serves GET /api/v1/usage: the plan usage windows (5-hour,
// weekly, ...) of an account. Claude accounts read Anthropic's OAuth usage
// endpoint with the sign-in's or the OAuth token, or a custom endpoint's
// <base URL>/api/usage (proxies mirror the OAuth usage layout) unless the
// account names a usage URL. Codex accounts signed in with ChatGPT read
// ChatGPT's rate limits. Antigravity accounts signed in with Google read
// `agy -p /usage` in the box, while it runs. API keys have no usage to show.
package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
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
	"agentrove/internal/sandbox"
)

const (
	fetchTimeout = 10 * time.Second
	// Gateways rate-limit their usage endpoint and every open tab polls, so
	// one fetch per user is shared for a while; a manual refresh may cut it
	// short, not to zero.
	cacheTTL   = 60 * time.Second
	minRefresh = 10 * time.Second
)

// Window is one rate-limit window: how much of it is used, when it resets
// and how long it is (18000 for the 5-hour window).
type Window struct {
	UsedPercent   float64     `json:"used_percent"`
	ResetsAt      db.NullTime `json:"resets_at"`
	WindowSeconds int         `json:"window_seconds"`
}

// Device is one device sharing a proxied plan, as the bitsec proxy reports
// it: its share of this week's plan usage and its color slot (1-based).
type Device struct {
	Name        string  `json:"name"`
	Color       int     `json:"color"`
	UsedPercent float64 `json:"used_percent"`
	Blocked     bool    `json:"blocked"`
}

type Response struct {
	Status   string  `json:"status"`
	Provider *string `json:"provider"`
	// Windows are shortest first.
	Windows []Window `json:"windows"`
	// Devices is set when a proxy splits the plan between devices.
	Devices   []Device `json:"devices,omitempty"`
	Error     *string  `json:"error"`
	FetchedAt db.Time  `json:"fetched_at"`
}

const (
	officialHost     = "api.anthropic.com"
	officialEndpoint = "https://api.anthropic.com/api/oauth/usage"
	proxyUsagePath   = "/api/usage"
	oauthTokenPrefix = "sk-ant-oat"
	usageURLKey      = "CLAUDE_USAGE_URL"
	codexEndpoint    = "https://chatgpt.com/backend-api/wham/usage"

	agyProvider   = "antigravity"
	agyBoxStopped = "Usage shows while your box is running"

	sessionSeconds = 5 * 3600
	daySeconds     = 24 * 3600
	weekSeconds    = 7 * 24 * 3600
)

type Source struct {
	// Codex marks ChatGPT's rate-limit layout (else Anthropic's).
	Codex bool
	// Agy is read from `agy -p /usage` in the box instead of over HTTP.
	Agy      *profiles.Profile
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

func parseWindow(raw any, seconds int) *Window {
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
	return &Window{UsedPercent: math.Min(100, math.Max(0, used)), ResetsAt: parseReset(reset), WindowSeconds: seconds}
}

func okResponse(provider string, windows ...*Window) Response {
	out := []Window{}
	for _, w := range windows {
		if w != nil {
			out = append(out, *w)
		}
	}
	return Response{Status: "ok", Provider: &provider, Windows: out, FetchedAt: db.Now()}
}

// ParseBody reads Anthropic's OAuth usage layout (five_hour / seven_day).
func ParseBody(provider string, body any) Response {
	p := provider
	m, ok := body.(map[string]any)
	if !ok {
		return errorResponse(provider, "Usage response was not a JSON object")
	}
	session, weekly := parseWindow(m["five_hour"], sessionSeconds), parseWindow(m["seven_day"], weekSeconds)
	if session == nil && weekly == nil {
		return errorResponse(provider, "Usage response had no 5-hour or weekly window")
	}
	res := okResponse(p, session, weekly)
	res.Devices = parseDevices(m["devices"])
	return res
}

// parseDevices reads a proxy's per-device split (name, color,
// usedPercentOfPlan, blocked); entries without a name or number are skipped.
func parseDevices(raw any) []Device {
	list, _ := raw.([]any)
	var out []Device
	for _, item := range list {
		d, _ := item.(map[string]any)
		name, _ := d["name"].(string)
		used, ok := d["usedPercentOfPlan"].(float64)
		if strings.TrimSpace(name) == "" || !ok || math.IsNaN(used) || math.IsInf(used, 0) {
			continue
		}
		color, _ := d["color"].(float64)
		blocked, _ := d["blocked"].(bool)
		out = append(out, Device{Name: name, Color: int(color), UsedPercent: math.Min(100, math.Max(0, used)), Blocked: blocked})
	}
	return out
}

// ParseCodexBody reads ChatGPT's rate limits (rate_limit.primary_window and
// secondary_window, each with used_percent, limit_window_seconds, reset_at).
func ParseCodexBody(provider string, body any) Response {
	m, ok := body.(map[string]any)
	if !ok {
		return errorResponse(provider, "Usage response was not a JSON object")
	}
	limits, _ := m["rate_limit"].(map[string]any)
	var windows []*Window
	for _, k := range []string{"primary_window", "secondary_window"} {
		raw, ok := limits[k].(map[string]any)
		if !ok {
			continue
		}
		used, ok := raw["used_percent"].(float64)
		if !ok || math.IsNaN(used) || math.IsInf(used, 0) {
			continue
		}
		secs, _ := raw["limit_window_seconds"].(float64)
		windows = append(windows, &Window{UsedPercent: math.Min(100, math.Max(0, used)), ResetsAt: parseReset(raw["reset_at"]), WindowSeconds: int(secs)})
	}
	if len(windows) == 0 {
		return errorResponse(provider, "Usage response had no rate-limit window")
	}
	if len(windows) == 2 && windows[1].WindowSeconds < windows[0].WindowSeconds {
		windows[0], windows[1] = windows[1], windows[0]
	}
	return okResponse(provider, windows...)
}

// ParseAgyBody reads `agy -p /usage --output-format json`: command.data
// .groups, each a set of models sharing quota buckets with a window
// ("5h", "weekly", ...) and a remaining_fraction. The first group is the
// Gemini models agy runs on; the others (Claude, GPT) aren't shown.
func ParseAgyBody(body any) Response {
	m, _ := body.(map[string]any)
	cmd, _ := m["command"].(map[string]any)
	data, _ := cmd["data"].(map[string]any)
	groups, _ := data["groups"].([]any)
	var windows []*Window
	for _, g := range groups {
		group, _ := g.(map[string]any)
		buckets, _ := group["buckets"].([]any)
		for _, b := range buckets {
			bucket, _ := b.(map[string]any)
			left, ok := bucket["remaining_fraction"].(float64)
			if !ok || math.IsNaN(left) || math.IsInf(left, 0) {
				continue
			}
			window, _ := bucket["window"].(string)
			used := math.Min(100, math.Max(0, (1-left)*100))
			windows = append(windows, &Window{UsedPercent: used, ResetsAt: parseReset(bucket["reset_time"]), WindowSeconds: agyWindowSeconds(window)})
		}
		if len(windows) > 0 {
			break
		}
	}
	if len(windows) == 0 {
		return errorResponse(agyProvider, "agy reported no quota for this account")
	}
	sort.SliceStable(windows, func(i, j int) bool { return windows[i].WindowSeconds < windows[j].WindowSeconds })
	return okResponse(agyProvider, windows...)
}

// agyWindowSeconds reads agy's window names ("5h", "weekly", "daily").
func agyWindowSeconds(window string) int {
	w := strings.ToLower(window)
	switch {
	case strings.Contains(w, "week"):
		return weekSeconds
	case strings.Contains(w, "day") || strings.Contains(w, "daily"):
		return daySeconds
	case strings.Contains(w, "five") || strings.Contains(w, "session"):
		return sessionSeconds
	}
	if m := agyHoursRE.FindStringSubmatch(w); m != nil {
		h, _ := strconv.Atoi(m[1])
		return h * 3600
	}
	return 0
}

var agyHoursRE = regexp.MustCompile(`^(\d+)[\s_-]*h`)

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
	mux.HandleFunc("GET "+m.a.Cfg.APIPrefix+"/usage", m.a.Auth.Require(m.get))
}

// get reports the usage of an account: profile_id's, else the default one.
func (m *Module) get(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	refresh := false
	if b := httpx.QueryBool(r, "refresh"); b != nil {
		refresh = *b
	}
	var p *profiles.Profile
	var err error
	if id := r.URL.Query().Get("profile_id"); id != "" {
		if p, err = m.a.Profiles.Get(r.Context(), u.ID, id); err != nil {
			return err
		}
	} else if p, err = m.a.Profiles.Default(r.Context(), u.ID); err != nil {
		return httpx.OK(w, Response{Status: "unavailable", Windows: []Window{}, FetchedAt: db.Now()})
	}
	return httpx.OK(w, m.usage(r.Context(), u.ID, m.source(p), refresh))
}

// source picks where an account's usage comes from (nil: nowhere).
func (m *Module) source(p *profiles.Profile) *Source {
	switch {
	case p.AuthMode == profiles.AuthLogin && p.Provider == models.ProviderClaude:
		token, _ := m.a.Profiles.LoginToken(p)
		if token == "" {
			return nil
		}
		return &Source{Provider: officialHost, URL: officialEndpoint, Token: token, Headers: map[string]string{"anthropic-beta": "oauth-2025-04-20"}}
	case p.AuthMode == profiles.AuthLogin && p.Provider == models.ProviderCodex:
		token, account := m.a.Profiles.LoginToken(p)
		if token == "" {
			return nil
		}
		src := &Source{Codex: true, Provider: "chatgpt.com", URL: codexEndpoint, Token: token, Headers: map[string]string{}}
		if account != "" {
			src.Headers["ChatGPT-Account-Id"] = account
		}
		return src
	case p.AuthMode == profiles.AuthLogin && p.Provider == models.ProviderAntigravity:
		return &Source{Agy: p, Provider: agyProvider, URL: "agy:" + p.ID}
	case p.Provider == models.ProviderClaude:
		// A token or an endpoint: the URLs were chosen by the user, so they go
		// in as user vars (public addresses only).
		var custom []models.CustomEnvVar
		for k, v := range p.ApplyEnv(nil, nil) {
			custom = append(custom, models.CustomEnvVar{Key: k, Value: v})
		}
		return ResolveSource(map[string]string{}, custom)
	}
	return nil
}

func cacheKey(userID string, s *Source) string {
	// Token hashed so a rotated token never reuses the old account's numbers.
	sum := sha256.Sum256([]byte(s.Token))
	return userID + ":" + s.URL + ":" + hex.EncodeToString(sum[:])[:16]
}

func (m *Module) usage(ctx context.Context, userID string, src *Source, refresh bool) Response {
	if src == nil {
		return Response{Status: "unavailable", Windows: []Window{}, FetchedAt: db.Now()}
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
	var res Response
	if src.Agy != nil {
		res = m.fetchAgy(ctx, src.Agy)
		// A stopped box keeps showing what it last reported.
		if res.Error != nil && *res.Error == agyBoxStopped && ok && cached.res.Status == "ok" {
			return cached.res
		}
	} else {
		res = m.fetch(ctx, src)
	}
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

func (m *Module) fetchAgy(ctx context.Context, p *profiles.Profile) Response {
	out, err := m.a.Profiles.AntigravityUsage(ctx, p)
	if errors.Is(err, sandbox.ErrNotRunning) {
		return errorResponse(agyProvider, agyBoxStopped)
	}
	if err != nil {
		return errorResponse(agyProvider, "agy couldn't report usage")
	}
	var body any
	if err := json.Unmarshal(out, &body); err != nil {
		return errorResponse(agyProvider, "agy's usage report was not valid JSON")
	}
	return ParseAgyBody(body)
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
	if src.Codex {
		return ParseCodexBody(src.Provider, body)
	}
	return ParseBody(src.Provider, body)
}
