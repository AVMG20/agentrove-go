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
	"agentrove/internal/httpx"
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

// NewManager sets up the providers; boxSubnets is filled with the box
// networks' subnets (see DockerProvider.LoadBoxSubnets).
func NewManager(cfg *config.Config, boxSubnets *httpx.PrefixSet) *Manager {
	m := &Manager{
		cfg:       cfg,
		providers: map[string]Provider{},
		activity:  map[string]time.Time{},
		leases:    map[string]int{},
		starting:  map[string]*sync.Mutex{},
		userEnv:   map[string]map[string]string{},
	}
	if d, err := NewDockerProvider(cfg, boxSubnets); err != nil {
		slog.Warn("docker sandbox provider unavailable", "err", err)
	} else {
		m.providers[KindDocker] = d
		// Before serving, so a box can't pass as a proxy in the first seconds.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		d.LoadBoxSubnets(ctx)
		cancel()
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
	if ws.SandboxProvider != KindDocker {
		return nil, fmt.Errorf("this project ran on the server itself, which is no longer supported; add it again as a project in your box")
	}
	// Every project of a user lives in the user's one box.
	s := &Sandbox{ID: BoxID(ws.UserID), Kind: KindDocker, UserID: ws.UserID, WorkspacePath: ws.WorkspacePath, provider: p, mgr: m,
		Root: path.Join(ContainerProjects, filepath.Base(ws.WorkspacePath)), Home: ContainerHome}
	return s, nil
}

// Create prepares a project whose files are at workspacePath: a folder in
// the user's box, which starts lazily on first use.
func (m *Manager) Create(ctx context.Context, kind, sandboxID, workspacePath, userID string) error {
	if _, err := m.Provider(kind); err != nil {
		return err
	}
	if filepath.Dir(workspacePath) != m.cfg.UserWorkspacesDir(userID) {
		return fmt.Errorf("projects must live in %s", m.cfg.UserWorkspacesDir(userID))
	}
	return chownTree(workspacePath)
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
// It first rejoins the per-user box networks, which must happen even with
// the reaper off.
func (m *Manager) RunIdleReaper(ctx context.Context) {
	p, ok := m.providers[KindDocker]
	if !ok {
		return
	}
	d, isDocker := p.(*DockerProvider)
	if isDocker {
		d.ReconnectNetworks(ctx)
	}
	if m.cfg.SandboxIdleTTL <= 0 {
		return
	}
	if isDocker {
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
	for k, v := range extra {
		env[k] = v
	}
	return env
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
