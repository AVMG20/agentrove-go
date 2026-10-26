package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentrove/internal/agentstream"
	"agentrove/internal/antigravity"
	"agentrove/internal/codex"
	"agentrove/internal/db"
	"agentrove/internal/mcpserver"
	"agentrove/internal/models"
	"agentrove/internal/profiles"
	"agentrove/internal/sandbox"
)

// Chats run on a provider profile (internal/profiles). Claude profiles use
// the long-lived Claude session (sessions.go, turn.execute) with the
// profile's config dir and env; Codex and Antigravity run one CLI process
// per turn (executeCLI), resumed by the thread id the CLI reports.

// chatProfile resolves the profile a chat runs on.
func (m *Module) chatProfile(ctx context.Context, userID string, chat *models.Chat) (*profiles.Profile, error) {
	return m.a.Profiles.ForChat(ctx, userID, chat.StoredProfileID)
}

// newChatProfile picks the profile a new chat starts on: the requested one,
// else the user's default. A model id of another provider (a stream
// action's model) picks an account of that provider when there is one.
func (m *Module) newChatProfile(ctx context.Context, userID, profileID, modelID string) (*profiles.Profile, error) {
	if profileID != "" {
		return m.a.Profiles.Get(ctx, userID, profileID)
	}
	p, err := m.a.Profiles.Default(ctx, userID)
	if err != nil {
		return nil, err
	}
	if modelID != "" && !models.ValidModelFor(p.Provider, modelID) {
		if other := m.a.Profiles.DefaultFor(ctx, userID, models.ProviderOf(modelID)); other != nil {
			p = other
		}
	}
	return p, nil
}

func reqProvider(req *turnRequest) string {
	if req.profile == nil {
		return models.ProviderClaude
	}
	return req.profile.Provider
}

// otherProviderModel reports whether id is a Codex or Antigravity model and
// not a Claude one.
func otherProviderModel(id string) bool {
	if _, ok := models.LookupProviderModel(models.ProviderClaude, id); ok {
		return false
	}
	for _, p := range models.Providers {
		if p == models.ProviderClaude {
			continue
		}
		if _, ok := models.LookupProviderModel(p, id); ok {
			return true
		}
	}
	return false
}

// signInHint explains a credentials failure in terms of the chat's profile.
func signInHint(p *profiles.Profile) string {
	if p == nil {
		return "\n\nAdd an account under Settings → Providers and start a new thread on it."
	}
	return fmt.Sprintf("\n\nThe %s profile \"%s\" isn't signed in. Sign in or add a token under Settings → Providers.", profiles.ProviderName(p.Provider), p.Name)
}

// looksUnauthenticated matches the CLIs' "no credentials" failures.
func looksUnauthenticated(errText string) bool {
	low := strings.ToLower(errText)
	for _, s := range []string{"not logged in", "unauthorized", "401", "please log in", "please login", "codex login", "invalid api key", "authentication", "sign in"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// profileShell wraps a CLI command: fail clearly when the CLI is missing,
// prepare the profile's dirs, then exec it. Login shell so ~/.bashrc PATH
// additions (npm globals, native installs) resolve the binary.
func profileShell(setup string, bin string, args []string) []string {
	script := `command -v "$1" >/dev/null 2>&1 || { echo "agentrove: $1 is not installed in this box" >&2; exit 127; }; `
	if setup != "" {
		script += setup + ` && `
	}
	script += `exec "$@"`
	return append([]string{"bash", "-lc", script, "agent", bin}, args...)
}

// cliPrompt is the user's text for a CLI that takes plain text: attachments
// are copied into the workspace and referenced by path; images are also
// returned (as sandbox paths) for CLIs that attach them.
func (m *Module) cliPrompt(sb *sandbox.Sandbox, req *turnRequest, attachImages bool) (string, []string, error) {
	var notes, images []string
	for _, att := range req.attachments {
		rel := db.Deref(att.FilePath)
		if rel == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.a.Cfg.StoragePath, filepath.FromSlash(rel)))
		if err != nil {
			slog.Warn("attachment missing", "path", rel)
			continue
		}
		name := firstNonEmpty(db.Deref(att.Filename), filepath.Base(rel))
		dest, err := writeUpload(sb, db.Deref(req.chat.WorktreeCwd), filepath.Base(rel), data)
		if err != nil {
			return "", nil, fmt.Errorf("copy attachment into workspace: %w", err)
		}
		full := path.Join(sb.Root, dest)
		if attachImages && att.FileType == "image" {
			images = append(images, full)
			continue
		}
		notes = append(notes, "- "+name+" is available at "+full)
	}
	prompt := req.prompt
	if len(notes) > 0 {
		prompt += "\n\n<user_attachments>\nUser uploaded the following files. Read them from these sandbox paths when needed.\n" + strings.Join(notes, "\n") + "\n</user_attachments>"
	}
	return prompt, images, nil
}

// cliRun is one turn's process for a per-turn CLI.
type cliRun struct {
	cmd   []string
	stdin string
	// env is added to the turn's env (the agentrove MCP server).
	env map[string]string
	tr  agentstream.LineTranslator
}

func (t *turn) cliRun(sb *sandbox.Sandbox, resumeID string) (*cliRun, error) {
	p := t.req.profile
	launch := p.Launch()
	mcp, env := t.m.cliMCP(sb, t.req)
	rules := ""
	if mcp != nil {
		rules = mcpserver.Instructions
	}
	setup := launch.Setup
	if s := p.ChatSetup(rules); s != "" {
		setup = strings.TrimPrefix(setup+" && "+s, " && ")
	}
	switch p.Provider {
	case models.ProviderCodex:
		prompt, images, err := t.m.cliPrompt(sb, t.req, true)
		if err != nil {
			return nil, err
		}
		cfg := codex.Config{Model: t.req.modelID, Effort: t.req.effort, ResumeID: resumeID, Images: images, Instructions: strings.TrimSpace(boxPrompt)}
		if mcp != nil {
			cfg.Instructions += "\n\n" + rules
			cfg.MCP = []codex.MCPServer{{Name: agentroveServer, URL: mcp.URL, HeaderEnv: mcp.headerEnv}}
		}
		return &cliRun{cmd: profileShell(setup, "codex", cfg.Args()), stdin: prompt, env: env, tr: codex.NewTranslator()}, nil
	case models.ProviderAntigravity:
		prompt, _, err := t.m.cliPrompt(sb, t.req, false)
		if err != nil {
			return nil, err
		}
		args, err := antigravity.Config{Model: t.req.modelID, ResumeID: resumeID}.Args(prompt)
		if err != nil {
			return nil, err
		}
		return &cliRun{cmd: profileShell(setup, "agy", args), env: env, tr: antigravity.NewTranslator()}, nil
	}
	return nil, fmt.Errorf("unsupported provider %q", p.Provider)
}

// agentroveServer is the MCP server name of the app's own tools (artifacts,
// and with MCP enabled, orchestration); tool names are mcp__agentrove__*.
const agentroveServer = "agentrove"

// cliMCPServer is the agentrove MCP server for a Codex or Antigravity turn.
type cliMCPServer struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	// headerEnv names the env var carrying each header, for Codex.
	headerEnv map[string]string
}

// cliMCP returns the agentrove MCP server for a per-turn CLI and the env
// that carries it (nil when there is none): Codex reads each header from
// its own var, Antigravity's setup writes the whole server
// (profiles.AgentroveMCPEnv) into its config. It also installs the artifacts
// skill that ChatSetup copies into the CLI's config.
func (m *Module) cliMCP(sb *sandbox.Sandbox, req *turnRequest) (*cliMCPServer, map[string]string) {
	if m.MCPConfig == nil {
		return nil, nil
	}
	cfg, _ := m.MCPConfig(req.userID, req.chat.ID, sb.Kind)[agentroveServer].(map[string]any)
	url, _ := cfg["url"].(string)
	if url == "" {
		return nil, nil
	}
	m.installArtifactSkill(sb, req.userID)
	s := &cliMCPServer{URL: url, Headers: map[string]string{}, headerEnv: map[string]string{}}
	env := map[string]string{}
	headers, _ := cfg["headers"].(map[string]any)
	for h, v := range headers {
		val, ok := v.(string)
		if !ok {
			continue
		}
		name := "AGENTROVE_MCP_HEADER_" + strings.ToUpper(strings.NewReplacer("-", "_", " ", "_").Replace(h))
		s.Headers[h], s.headerEnv[h], env[name] = val, name, val
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, nil
	}
	env[profiles.AgentroveMCPEnv] = string(b)
	return s, env
}

// executeCLI runs a Codex or Antigravity turn: one process, its stdout
// translated line by line. An interrupt kills the process; the thread id it
// reported is kept, so the next turn resumes the conversation.
func (t *turn) executeCLI(sb *sandbox.Sandbox) (*agentstream.Result, error) {
	resumeID := db.Deref(t.req.chat.SessionID)
	res, sawOutput, err := t.runCLI(sb, resumeID)
	if resumeID != "" && !sawOutput && !t.cancelled.Load() && resumeLost(res, err) {
		// The thread is gone (box reset, CLI home wiped): start a new one
		// instead of failing every turn on the stale id.
		slog.Warn("agent thread missing; starting a new one", "chat", t.chatID, "provider", reqProvider(t.req), "thread", resumeID)
		_, _ = t.m.a.DB.ExecContext(context.Background(), `UPDATE chats SET session_id = NULL WHERE id = ?`, t.chatID)
		res, _, err = t.runCLI(sb, "")
	}
	return res, err
}

func resumeLost(res *agentstream.Result, err error) bool {
	text := ""
	if err != nil {
		text = err.Error()
	} else if res != nil && res.IsError {
		text = res.ErrorText
	}
	low := strings.ToLower(text)
	return strings.Contains(low, "no conversation") || strings.Contains(low, "conversation not found") ||
		strings.Contains(low, "thread not found") || strings.Contains(low, "session not found") ||
		strings.Contains(low, "no rollout found") || strings.Contains(low, "no such session")
}

func (t *turn) runCLI(sb *sandbox.Sandbox, resumeID string) (*agentstream.Result, bool, error) {
	run, err := t.cliRun(sb, resumeID)
	if err != nil {
		return nil, false, err
	}
	launch := t.req.profile.Launch()
	env := t.m.sessionEnv(t.req)
	for k, v := range launch.Env {
		env[k] = v
	}
	for _, k := range launch.Unset {
		if _, own := launch.Env[k]; !own {
			delete(env, k)
		}
	}
	for k, v := range run.env {
		env[k] = v
	}
	if t.cancelled.Load() {
		return nil, false, nil
	}
	proc, release, err := sb.Start(t.ctx, sandbox.ExecRequest{
		Cmd: run.cmd, Cwd: sb.Resolve(db.Deref(t.req.chat.WorktreeCwd)), Env: env, Unset: launch.Unset, UnsetGlobal: launch.UnsetGlobal,
	})
	if err != nil {
		return nil, false, fmt.Errorf("start %s: %w", profiles.Binary(t.req.profile.Provider), err)
	}
	defer release()
	go func() {
		stdin := proc.Stdin()
		if run.stdin != "" {
			_, _ = io.WriteString(stdin, run.stdin)
		}
		_ = stdin.Close()
	}()
	stderr := &tailBuffer{max: 8192}
	stderrDone := make(chan struct{})
	go func() { defer close(stderrDone); _, _ = io.Copy(stderr, proc.Stderr()) }()
	lines := make(chan []byte, 256)
	go func() {
		defer close(lines)
		agentstream.ReadLines(proc.Stdout(), func(b []byte) { lines <- append([]byte(nil), b...) })
	}()

	var result *agentstream.Result
	ctxDone := t.ctx.Done()
	killed := false
	for lines != nil {
		select {
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			evs, res := run.tr.HandleLine(line)
			for _, e := range evs {
				t.process(e)
			}
			if res != nil {
				result = res
			}
		case <-ctxDone:
			ctxDone = nil
			if !killed {
				killed = true
				go func() { _ = proc.Kill() }()
			}
		}
	}
	waitErr := proc.Wait()
	select {
	case <-stderrDone:
	case <-time.After(2 * time.Second):
	}
	for _, e := range run.tr.Finish() {
		t.process(e)
	}
	if sid := run.tr.SessionID(); sid != "" {
		t.m.persistSessionID(t.chatID, sid)
	}
	saw := run.tr.SawOutput()
	if t.cancelled.Load() {
		return nil, saw, nil
	}
	if result != nil {
		return result, saw, nil
	}
	bin := profiles.Binary(t.req.profile.Provider)
	tail := strings.TrimSpace(stderr.String())
	if strings.Contains(tail, "is not installed in this box") {
		if t.m.a.Profiles != nil {
			t.m.a.Profiles.NoteMissing(t.req.userID, bin)
		}
		return nil, saw, fmt.Errorf("The %s CLI (`%s`) is not installed in your box. Rebuild the sandbox image to get it.", profiles.ProviderName(t.req.profile.Provider), bin)
	}
	if tail != "" {
		return nil, saw, fmt.Errorf("%s exited: %s", bin, lastLines(tail, 6))
	}
	if waitErr != nil {
		return nil, saw, fmt.Errorf("%s exited: %v", bin, waitErr)
	}
	return nil, saw, fmt.Errorf("%s exited without finishing the turn", bin)
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
