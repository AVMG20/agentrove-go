package profiles

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"agentrove/internal/httpx"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"
)

// Sign-in runs the provider CLI's own login in the user's box, in a PTY, and
// surfaces what the user needs (the sign-in URL, a device code, any prompt
// for a pasted code) to the Settings page:
//
//   - built-in Claude: `claude auth login` into the box's ~/claude.
//   - a Claude token profile: `claude setup-token`; the long-lived OAuth
//     token it prints is stored (encrypted) as the profile's token.
//   - Codex: `codex login --device-auth` into the profile's CODEX_HOME.
//   - Antigravity: `agy`, which signs in on first run, under the profile's
//     HOME; done once its token file appears.
const (
	LoginRunning   = "running"
	LoginSucceeded = "succeeded"
	LoginFailed    = "failed"
	LoginCancelled = "cancelled"

	loginTimeout = 15 * time.Minute
	loginKeep    = 10 * time.Minute // finished states stay readable this long
	outputTail   = 4000
	// Wide enough that the CLI never wraps the sign-in URL.
	loginCols = 1000
	loginRows = 40
)

// LoginState is what the Settings page polls.
type LoginState struct {
	ProfileID string `json:"profile_id"`
	Status    string `json:"status"`
	URL       string `json:"url"`
	Code      string `json:"code"`
	Output    string `json:"output"`
	Error     string `json:"error"`
}

type loginSession struct {
	userID string
	pty    sandbox.PTY
	cancel context.CancelFunc

	mu       sync.Mutex
	state    LoginState
	raw      strings.Builder
	finished time.Time
}

func (l *loginSession) snapshot() LoginState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

func (l *loginSession) finishedAt() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.finished
}

// finish settles the session once; later calls are ignored.
func (l *loginSession) finish(status, errText string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state.Status != LoginRunning {
		return false
	}
	l.state.Status, l.state.Error, l.finished = status, errText, time.Now()
	return true
}

// logins holds sign-ins by loginKey: the built-in profile's id is the same
// for every user, so the id alone would let one user's sign-in replace (and
// cancel) another's.
type logins struct {
	mu   sync.Mutex
	byID map[string]*loginSession
}

func loginKey(p *Profile) string { return p.UserID + ":" + p.ID }

func newLogins() *logins { return &logins{byID: map[string]*loginSession{}} }

// get returns p's sign-in, checking it belongs to p's user.
func (ls *logins) get(p *Profile) *loginSession {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	for k, l := range ls.byID {
		if l.snapshot().Status != LoginRunning && time.Since(l.finishedAt()) > loginKeep {
			delete(ls.byID, k)
		}
	}
	l := ls.byID[loginKey(p)]
	if l == nil || l.userID != p.UserID {
		return nil
	}
	return l
}

func (ls *logins) put(p *Profile, l *loginSession) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.byID[loginKey(p)] = l
}

func (ls *logins) cancel(p *Profile) {
	if l := ls.get(p); l != nil {
		l.finish(LoginCancelled, "")
		l.cancel()
	}
}

var (
	ansiRE       = regexp.MustCompile(`\x1b\[[0-9;?<>=]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][A-Za-z0-9]|\x1b[=>78DEHMc]`)
	urlRE        = regexp.MustCompile(`https://[^\s\x07\x1b"'<>` + "`" + `]+`)
	deviceCodeRE = regexp.MustCompile(`\b[A-Z0-9]{4,5}-[A-Z0-9]{4,6}\b`)
	// Followed by whitespace, so a token split across two reads isn't taken
	// half-printed.
	claudeTokenRE = regexp.MustCompile(`(sk-ant-oat[0-9]{2}-[A-Za-z0-9_-]{20,})\s`)
	secretRE      = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{8,}|sk-[A-Za-z0-9_-]{20,}`)
)

// loginCommand is the CLI invocation that signs p in.
func loginCommand(p *Profile) ([]string, error) {
	switch {
	case p.Builtin:
		return []string{"claude", "auth", "login"}, nil
	case p.Provider == models.ProviderClaude && p.AuthMode == AuthToken:
		return []string{"claude", "setup-token"}, nil
	case p.Provider == models.ProviderCodex && p.AuthMode == AuthLogin:
		return []string{"codex", "login", "--device-auth"}, nil
	case p.Provider == models.ProviderAntigravity && p.AuthMode == AuthLogin:
		return []string{"agy"}, nil
	}
	return nil, httpx.BadRequest("This profile signs in with a token; edit it to change the token")
}

// StartLogin starts (or restarts) the sign-in flow of a profile.
func (s *Service) StartLogin(ctx context.Context, p *Profile) (LoginState, error) {
	cmd, err := loginCommand(p)
	if err != nil {
		return LoginState{}, err
	}
	s.logins.cancel(p)
	sb, err := s.sandboxes.ForUser(p.UserID)
	if err != nil {
		return LoginState{}, httpx.Errorf(503, "%v", err)
	}
	launch := p.Launch()
	env := launch.Env
	if p.Builtin {
		env["CLAUDE_CONFIG_DIR"] = sandbox.ContainerClaudeDir
	}
	// The CLI fails to open a browser in the box and prints the URL instead.
	env["BROWSER"] = "true"
	script := `exec "$@"`
	if launch.Setup != "" {
		script = launch.Setup + ` && exec "$@"`
	}
	bin := cmd[0]
	full := append([]string{"bash", "-lc", `command -v "$1" >/dev/null 2>&1 || { echo "agentrove: $1 is not installed in this box" >&2; exit 127; }; ` + script, "login"}, cmd...)
	hadToken := s.signedIn(p)
	runCtx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	pty, release, err := sb.StartPTY(ctx, sandbox.ExecRequest{Cmd: full, Cwd: sandbox.ContainerHome, Env: env, Unset: launch.Unset}, loginRows, loginCols)
	if err != nil {
		cancel()
		return LoginState{}, httpx.Errorf(503, "Could not start the sign-in in your box: %v", err)
	}
	l := &loginSession{userID: p.UserID, pty: pty, cancel: cancel, state: LoginState{ProfileID: p.ID, Status: LoginRunning}}
	s.logins.put(p, l)

	go func() {
		<-runCtx.Done()
		if l.finish(LoginFailed, "Sign-in timed out") {
			slog.Info("profile sign-in timed out", "profile", p.ID)
		}
		_ = pty.Close()
	}()
	exited := make(chan error, 1)
	go func() {
		defer release()
		buf := make([]byte, 8192)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				s.loginOutput(p, l, buf[:n])
			}
			if err != nil {
				if err != io.EOF {
					slog.Debug("profile sign-in output", "profile", p.ID, "err", err)
				}
				break
			}
		}
		exited <- pty.Wait()
	}()
	go func() {
		defer cancel()
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case err := <-exited:
				s.loginExited(p, l, bin, err, hadToken)
				return
			case <-tick.C:
				// Antigravity's interactive CLI keeps running after it signs
				// in: its token file appearing is the success signal.
				if p.Provider == models.ProviderAntigravity && !hadToken && s.signedIn(p) {
					l.finish(LoginSucceeded, "")
					_ = pty.Close()
				}
			case <-runCtx.Done():
				return
			}
		}
	}()
	return l.snapshot(), nil
}

// loginOutput records a chunk of PTY output: the sign-in URL, a device code,
// and a token printed by `claude setup-token` (stored, never echoed back).
func (s *Service) loginOutput(p *Profile, l *loginSession, chunk []byte) {
	l.mu.Lock()
	l.raw.Write(chunk)
	raw := l.raw.String()
	if len(raw) > 256<<10 {
		raw = raw[len(raw)-128<<10:]
		l.raw.Reset()
		l.raw.WriteString(raw)
	}
	text := cleanTerminal(raw)
	if u := pickURL(urlRE.FindAllString(raw, -1)); u != "" {
		l.state.URL = u
	}
	if c := deviceCodeRE.FindString(text); c != "" && p.Provider == models.ProviderCodex {
		l.state.Code = c
	}
	token := ""
	if p.Provider == models.ProviderClaude && !p.Builtin {
		if m := claudeTokenRE.FindStringSubmatch(text); m != nil {
			token = m[1]
		}
	}
	tail := secretRE.ReplaceAllString(text, "sk-ant-…")
	if len(tail) > outputTail {
		tail = tail[len(tail)-outputTail:]
	}
	l.state.Output = strings.TrimSpace(tail)
	l.mu.Unlock()
	if token != "" && l.snapshot().Status == LoginRunning {
		if err := s.saveLoginToken(p, token); err != nil {
			l.finish(LoginFailed, "Could not save the token: "+err.Error())
		} else {
			l.finish(LoginSucceeded, "")
		}
		_ = l.pty.Close()
	}
}

func (s *Service) saveLoginToken(p *Profile, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cur, err := s.Get(ctx, p.UserID, p.ID)
	if err != nil {
		return err
	}
	cur.Token = token
	cur.AuthMode = AuthToken
	cur.TokenEnv = "" // an OAuth token: CLAUDE_CODE_OAUTH_TOKEN
	return s.save(ctx, cur)
}

func (s *Service) loginExited(p *Profile, l *loginSession, bin string, err error, hadToken bool) {
	out := l.snapshot().Output
	switch {
	case err == nil && (p.Builtin || p.Provider == models.ProviderCodex):
		l.finish(LoginSucceeded, "")
	case strings.Contains(out, "is not installed in this box"):
		s.NoteMissing(p.UserID, bin)
		l.finish(LoginFailed, "`"+bin+"` is not installed in your box")
	case p.Provider == models.ProviderAntigravity && !hadToken && s.signedIn(p):
		l.finish(LoginSucceeded, "")
	case err == nil:
		l.finish(LoginFailed, "The sign-in ended without credentials")
	default:
		l.finish(LoginFailed, "The sign-in did not complete")
	}
}

// LoginState returns the current or last sign-in of a profile.
func (s *Service) LoginState(p *Profile) (LoginState, bool) {
	l := s.logins.get(p)
	if l == nil {
		return LoginState{}, false
	}
	return l.snapshot(), true
}

// LoginInput types text (a pasted code, a menu choice) into the sign-in.
func (s *Service) LoginInput(p *Profile, text string) error {
	l := s.logins.get(p)
	if l == nil || l.snapshot().Status != LoginRunning {
		return httpx.NotFound("No sign-in is running for this profile")
	}
	if len(text) > 4096 || strings.ContainsAny(text, "\x03\x04\x1a") {
		return httpx.Unprocessable("Invalid input")
	}
	_, err := l.pty.Write([]byte(text + "\r"))
	return err
}

// CancelLogin stops a running sign-in.
func (s *Service) CancelLogin(p *Profile) {
	s.logins.cancel(p)
}

// cleanTerminal turns raw PTY output into plain text lines.
func cleanTerminal(raw string) string {
	s := ansiRE.ReplaceAllString(raw, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		// A carriage return redraws the line: keep what was drawn last.
		if j := strings.LastIndex(line, "\r"); j >= 0 {
			line = line[j+1:]
		}
		lines[i] = strings.TrimRight(strings.Map(func(r rune) rune {
			if r < 0x20 && r != '\t' {
				return -1
			}
			return r
		}, line), " ")
	}
	var out []string
	for _, line := range lines {
		if line == "" && len(out) > 0 && out[len(out)-1] == "" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// pickURL chooses the sign-in link among the URLs the CLI printed.
func pickURL(urls []string) string {
	best := ""
	for _, u := range urls {
		u = strings.TrimRight(u, ".,);]")
		low := strings.ToLower(u)
		if strings.Contains(low, "oauth") || strings.Contains(low, "authorize") || strings.Contains(low, "device") ||
			strings.Contains(low, "login") || strings.Contains(low, "accounts.google.com") {
			best = u
		} else if best == "" {
			best = u
		}
	}
	return best
}
