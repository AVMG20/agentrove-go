package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"agentrove/internal/config"
	"agentrove/internal/models"
)

// EnvSource supplies per-user environment (custom env vars, GitHub token).
type EnvSource func(ctx context.Context, userID string) map[string]string

type Manager struct {
	cfg       *config.Config
	providers map[string]Provider
	envSource EnvSource

	mu       sync.Mutex
	activity map[string]time.Time // sandboxID -> last activity
	leases   map[string]int       // sandboxID -> active leases
	starting map[string]*sync.Mutex
	userEnv  map[string]map[string]string

	// OnStop runs before an idle sandbox is stopped (e.g. close agent processes).
	OnStop func(sandboxID string)
	// Working reports an agent turn running in the sandbox. Idle agent
	// processes and terminals hold leases too, but don't make it "busy".
	Working func(sandboxID string) bool
}

func NewManager(cfg *config.Config) *Manager {
	m := &Manager{
		cfg:       cfg,
		providers: map[string]Provider{},
		activity:  map[string]time.Time{},
		leases:    map[string]int{},
		starting:  map[string]*sync.Mutex{},
		userEnv:   map[string]map[string]string{},
	}
	if cfg.HostSandboxEnabled {
		m.providers[KindHost] = NewHostProvider(cfg)
	}
	if d, err := NewDockerProvider(cfg); err != nil {
		slog.Warn("docker sandbox provider unavailable", "err", err)
	} else {
		m.providers[KindDocker] = d
	}
	return m
}

func (m *Manager) SetEnvSource(fn EnvSource) { m.envSource = fn }

// InvalidateUserEnv drops the cached env after a settings change.
func (m *Manager) InvalidateUserEnv(userID string) {
	m.mu.Lock()
	delete(m.userEnv, userID)
	m.mu.Unlock()
}

func (m *Manager) Provider(kind string) (Provider, error) {
	p, ok := m.providers[kind]
	if !ok {
		return nil, fmt.Errorf("sandbox provider %q is not available", kind)
	}
	return p, nil
}

func (m *Manager) HasProvider(kind string) bool {
	_, ok := m.providers[kind]
	return ok
}

// For returns the sandbox handle of a workspace.
func (m *Manager) For(ws *models.Workspace) (*Sandbox, error) {
	p, err := m.Provider(ws.SandboxProvider)
	if err != nil {
		return nil, err
	}
	s := &Sandbox{ID: ws.SandboxID, Kind: ws.SandboxProvider, UserID: ws.UserID, WorkspacePath: ws.WorkspacePath, provider: p, mgr: m}
	if ws.SandboxProvider == KindDocker {
		// Every Docker workspace of a user lives in the user's one box.
		s.ID = BoxID(ws.UserID)
		s.Root = path.Join(ContainerProjects, filepath.Base(ws.WorkspacePath))
		s.Home = ContainerHome
	} else {
		s.Root = ws.WorkspacePath
		s.Home = m.hostHome(ws.UserID)
	}
	return s, nil
}

// Create prepares a workspace whose files are at workspacePath. A Docker
// workspace is a folder in the user's box, which starts lazily on first use.
func (m *Manager) Create(ctx context.Context, kind, sandboxID, workspacePath, userID string) error {
	p, err := m.Provider(kind)
	if err != nil {
		return err
	}
	if kind == KindDocker {
		if filepath.Dir(workspacePath) != m.cfg.UserWorkspacesDir(userID) {
			return fmt.Errorf("docker workspaces must live in %s", m.cfg.UserWorkspacesDir(userID))
		}
		return chownTree(workspacePath)
	}
	if err := p.Create(ctx, CreateOptions{SandboxID: sandboxID, WorkspacePath: workspacePath, UserID: userID}); err != nil {
		return err
	}
	m.Touch(sandboxID)
	return nil
}

// Delete releases a workspace's sandbox. The user's box is shared and stays;
// this only removes a container left from the per-workspace layout.
func (m *Manager) Delete(ctx context.Context, kind, sandboxID string) error {
	p, err := m.Provider(kind)
	if err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.activity, sandboxID)
	delete(m.leases, sandboxID)
	m.mu.Unlock()
	return p.Delete(ctx, sandboxID)
}

func (m *Manager) Touch(sandboxID string) {
	m.mu.Lock()
	m.activity[sandboxID] = time.Now()
	m.mu.Unlock()
}

// Acquire marks the sandbox busy until the returned func is called, which
// also counts as activity.
func (m *Manager) Acquire(sandboxID string) func() { return m.acquire(sandboxID, true) }

// acquire with touch=false leaves the idle clock alone on release: an agent
// process closing after sitting idle isn't use of the sandbox.
func (m *Manager) acquire(sandboxID string, touch bool) func() {
	m.mu.Lock()
	m.leases[sandboxID]++
	m.activity[sandboxID] = time.Now()
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			if m.leases[sandboxID] > 0 {
				m.leases[sandboxID]--
			}
			if m.leases[sandboxID] == 0 {
				delete(m.leases, sandboxID)
			}
			if touch {
				m.activity[sandboxID] = time.Now()
			}
			m.mu.Unlock()
		})
	}
}

// lockFor serialises starting and idle-stopping one sandbox.
func (m *Manager) lockFor(sandboxID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock := m.starting[sandboxID]
	if lock == nil {
		lock = &sync.Mutex{}
		m.starting[sandboxID] = lock
	}
	return lock
}

func (m *Manager) ensureRunning(ctx context.Context, s *Sandbox) error {
	lock := m.lockFor(s.ID)
	lock.Lock()
	defer lock.Unlock()
	if err := s.provider.EnsureRunning(ctx, s.ID, s.createOpts()); err != nil {
		return err
	}
	m.Touch(s.ID)
	return nil
}

// RunIdleReaper stops Docker sandboxes with no leases and no activity for
// SandboxIdleTTL. They restart transparently on the next Exec/Start.
func (m *Manager) RunIdleReaper(ctx context.Context) {
	if m.cfg.SandboxIdleTTL <= 0 {
		return
	}
	p, ok := m.providers[KindDocker]
	if !ok {
		return
	}
	if d, ok := p.(*DockerProvider); ok {
		d.RemoveLegacy(ctx)
	}
	// Containers already running at startup get a full TTL from now.
	if ids, err := p.Running(ctx); err == nil {
		for _, id := range ids {
			m.Touch(id)
		}
	}
	interval := time.Minute
	if m.cfg.SandboxIdleTTL < 5*time.Minute {
		interval = m.cfg.SandboxIdleTTL / 4
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reapOnce(ctx, p)
		}
	}
}

func (m *Manager) reapOnce(ctx context.Context, p Provider) {
	ids, err := p.Running(ctx)
	if err != nil {
		slog.Warn("idle reaper: list sandboxes", "err", err)
		return
	}
	for _, id := range ids {
		if m.idle(id) {
			m.stopIdle(ctx, p, id)
		}
	}
}

// idle reports whether a sandbox has no leases and no recent activity. A
// sandbox seen for the first time starts its idle clock now.
func (m *Manager) idle(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	last, seen := m.activity[id]
	if !seen {
		m.activity[id] = time.Now()
		return false
	}
	return m.leases[id] == 0 && time.Since(last) >= m.cfg.SandboxIdleTTL
}

// stopIdle stops the sandbox under its start lock, re-checking idleness so a
// concurrent wake-up (which touches activity under the same lock) wins.
func (m *Manager) stopIdle(ctx context.Context, p Provider, id string) {
	lock := m.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	if !m.idle(id) {
		return
	}
	slog.Info("stopping idle sandbox", "sandbox", id)
	if m.OnStop != nil {
		m.OnStop(id)
	}
	stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := p.Stop(stopCtx, id); err != nil {
		slog.Warn("stop idle sandbox", "sandbox", id, "err", err)
	}
}

func (m *Manager) mergeEnv(s *Sandbox, extra map[string]string) map[string]string {
	env := map[string]string{}
	for k, v := range m.cfg.AgentGlobalEnv() {
		env[k] = v
	}
	if s.UserID != "" && m.envSource != nil {
		m.mu.Lock()
		cached, ok := m.userEnv[s.UserID]
		m.mu.Unlock()
		if !ok {
			cached = m.envSource(context.Background(), s.UserID)
			m.mu.Lock()
			m.userEnv[s.UserID] = cached
			m.mu.Unlock()
		}
		for k, v := range cached {
			env[k] = v
		}
	}
	if env["GITHUB_TOKEN"] != "" && env["GIT_ASKPASS"] == "" {
		env["GIT_ASKPASS"] = s.GitAskpassPath()
	}
	env["TERM"] = TerminalType
	if s.Kind == KindHost {
		env["HOME"] = s.Home
		// Same per-user Claude login dir Docker sandboxes mount, so one login
		// covers both sandbox kinds.
		env["CLAUDE_CONFIG_DIR"] = filepath.Join(s.Home, "claude")
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func (m *Manager) hostHome(userID string) string {
	home := m.cfg.AgentHomeDir(userID)
	_ = SeedHome(home)
	return home
}

// defaultGitConfig gives commits an author out of the box; the agent or user
// can change it with `git config --global` (it lives in the persistent home).
const defaultGitConfig = `[user]
	name = Claude
	email = noreply@anthropic.com
[init]
	defaultBranch = main
[push]
	autoSetupRemote = true
[pull]
	rebase = false
`

// SeedHome creates a user's persistent home with a default git identity.
func SeedHome(home string) error {
	if err := os.MkdirAll(filepath.Join(home, "claude"), 0o755); err != nil {
		return err
	}
	gc := filepath.Join(home, ".gitconfig")
	if _, err := os.Stat(gc); os.IsNotExist(err) {
		return os.WriteFile(gc, []byte(defaultGitConfig), 0o644)
	}
	return nil
}

func (m *Manager) hostAskpassPath() string {
	p := filepath.Join(m.cfg.HostSandboxBaseDir, "git-askpass.sh")
	if _, err := os.Stat(p); err != nil {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("#!/bin/sh\necho \"$GITHUB_TOKEN\"\n"), 0o700)
	}
	return p
}
