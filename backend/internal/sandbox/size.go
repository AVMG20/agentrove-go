package sandbox

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// Walking a home with toolchains takes seconds and sizes barely move
	// minute to minute.
	sizeCacheTTL = 5 * time.Minute
	duTimeout    = time.Minute
)

// DirBytes runs `du -sk`; du still prints a total when some entries were
// unreadable, so its exit status is ignored. nil when it failed or timed out.
func DirBytes(ctx context.Context, path string) *int64 {
	ctx, cancel := context.WithTimeout(ctx, duTimeout)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "du", "-sk", path).Output()
	if ctx.Err() != nil {
		return nil
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return nil
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return nil
	}
	n := kb * 1024
	return &n
}

type boxSizes struct {
	at       time.Time
	home     *int64
	projects *int64
}

// sizeCache holds each user's home and projects sizes; computing one is
// serialized per user so a burst of box polls runs du once.
type sizeCache struct {
	mu      sync.Mutex
	entries map[string]boxSizes
	locks   map[string]*sync.Mutex
}

func (c *sizeCache) lock(userID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.locks == nil {
		c.locks = map[string]*sync.Mutex{}
		c.entries = map[string]boxSizes{}
	}
	l := c.locks[userID]
	if l == nil {
		l = &sync.Mutex{}
		c.locks[userID] = l
	}
	return l
}

func (c *sizeCache) forget(userID string) {
	c.mu.Lock()
	delete(c.entries, userID)
	c.mu.Unlock()
}

func (m *Manager) userSizes(ctx context.Context, userID string, refresh bool) boxSizes {
	l := m.sizes.lock(userID)
	l.Lock()
	defer l.Unlock()
	m.sizes.mu.Lock()
	e, ok := m.sizes.entries[userID]
	m.sizes.mu.Unlock()
	if ok && !refresh && time.Since(e.at) < sizeCacheTTL {
		return e
	}
	e = boxSizes{at: time.Now(), home: DirBytes(ctx, m.cfg.AgentHomeDir(userID)), projects: DirBytes(ctx, m.cfg.UserWorkspacesDir(userID))}
	m.sizes.mu.Lock()
	m.sizes.entries[userID] = e
	m.sizes.mu.Unlock()
	return e
}

// filesMissing reports a project dir that is gone (pruned, or removed by
// hand), so its files have to be restored before use.
func filesMissing(dir string) bool {
	_, err := os.Stat(dir)
	return errors.Is(err, fs.ErrNotExist)
}

// homeKeep are the home entries a prune leaves: agent logins, sessions and
// settings, git identity and SSH keys, CLI config and shell setup. Everything
// else (toolchains, package caches, global installs) is reinstallable.
var homeKeep = map[string]bool{
	"claude": true, ".claude": true, ".claude.json": true, ".agentrove": true, ".codex": true, ".gemini": true,
	".gitconfig": true, ".git-credentials": true, ".ssh": true, ".gnupg": true, ".netrc": true, ".config": true,
	".bashrc": true, ".bash_profile": true, ".profile": true, ".zshrc": true, ".bash_history": true,
	// The mount point of the projects dir.
	"projects": true,
}

// pruneHome removes the home entries outside homeKeep and keep.
func pruneHome(home string, keep []string) error {
	entries, err := os.ReadDir(home)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	extra := map[string]bool{}
	for _, k := range keep {
		extra[k] = true
	}
	var errs []error
	for _, e := range entries {
		if homeKeep[e.Name()] || extra[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(home, e.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
