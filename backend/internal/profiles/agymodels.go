package profiles

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentrove/internal/models"
	"agentrove/internal/sandbox"
)

// agyModelsTTL is how long an account's `agy models` list is reused.
const agyModelsTTL = 10 * time.Minute

// agyModelCache holds each Antigravity account's last `agy models` list. What
// an account may use depends on how it signs in (a Gemini API key reaches
// models a Google sign-in doesn't), so it is kept per account.
type agyModelCache struct {
	mu      sync.Mutex
	entries map[string]agyModelEntry // profile id
}

type agyModelEntry struct {
	list    []models.ModelInfo
	fetched time.Time
	busy    bool
}

// AntigravityModels lists the models the user's signed-in Antigravity
// accounts offer (the union, in `agy models` order), asking agy in the box
// for any account whose list is missing or stale while the box is up. ok is
// false when no account has reported yet.
func (s *Service) AntigravityModels(ctx context.Context, userID string) (list []models.ModelInfo, ok bool) {
	ps, err := s.List(ctx, userID)
	if err != nil {
		return nil, false
	}
	seen := map[string]bool{}
	for _, p := range ps {
		if p.Provider != models.ProviderAntigravity || s.Status(ctx, p).State != StatusReady {
			continue
		}
		ms, got := s.agyModels(ctx, p)
		if !got {
			continue
		}
		ok = true
		for _, m := range ms {
			if !seen[m.ID] {
				seen[m.ID] = true
				list = append(list, m)
			}
		}
	}
	return list, ok
}

// agyModels is p's list: read from disk after a restart, fetched first
// when there is none, and refreshed in the background when stale.
func (s *Service) agyModels(ctx context.Context, p *Profile) ([]models.ModelInfo, bool) {
	c := s.agyModelCache
	c.mu.Lock()
	e, have := c.entries[p.ID]
	if !have {
		e.list, e.fetched = s.loadAgyModels(p)
	}
	cached := len(e.list) > 0
	refresh := !e.busy && (!cached || time.Since(e.fetched) > agyModelsTTL)
	e.busy = e.busy || refresh
	c.entries[p.ID] = e
	c.mu.Unlock()
	if refresh {
		if cached {
			go s.fetchAgyModels(context.WithoutCancel(ctx), p)
		} else {
			s.fetchAgyModels(ctx, p)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e = c.entries[p.ID]
	return e.list, len(e.list) > 0
}

// fetchAgyModels runs `agy models` for p in the user's box if it is up and
// saves the list. A failure keeps the last good list.
func (s *Service) fetchAgyModels(ctx context.Context, p *Profile) {
	ms, err := s.runAgyModels(ctx, p)
	ok := err == nil && len(ms) > 0
	if ok {
		s.saveAgyModels(p, ms)
	}
	c := s.agyModelCache
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[p.ID]
	e.busy = false
	if ok {
		e.list = ms
	}
	if ok || len(e.list) > 0 {
		// A failed refresh of a good list retries after the TTL.
		e.fetched = time.Now()
	}
	c.entries[p.ID] = e
}

// forgetAgyModels drops p's list, so it is fetched again when next needed.
func (s *Service) forgetAgyModels(p *Profile) {
	c := s.agyModelCache
	c.mu.Lock()
	delete(c.entries, p.ID)
	c.mu.Unlock()
	_ = os.Remove(s.agyModelsFile(p))
}

// agyModelsFile keeps an account's last list across restarts, so a chat on
// a model only that account lists keeps it while the box is stopped.
func (s *Service) agyModelsFile(p *Profile) string {
	return filepath.Join(s.cfg.StoragePath, "agy-models", p.ID+".json")
}

func (s *Service) loadAgyModels(p *Profile) ([]models.ModelInfo, time.Time) {
	f := s.agyModelsFile(p)
	data, err := os.ReadFile(f)
	if err != nil {
		return nil, time.Time{}
	}
	var ms []models.ModelInfo
	if json.Unmarshal(data, &ms) != nil {
		return nil, time.Time{}
	}
	var mod time.Time
	if fi, err := os.Stat(f); err == nil {
		mod = fi.ModTime()
	}
	return ms, mod
}

func (s *Service) saveAgyModels(p *Profile, ms []models.ModelInfo) {
	f := s.agyModelsFile(p)
	data, err := json.Marshal(ms)
	if err != nil || os.MkdirAll(filepath.Dir(f), 0o700) != nil {
		return
	}
	tmp := f + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, f)
	}
}

func (s *Service) runAgyModels(ctx context.Context, p *Profile) ([]models.ModelInfo, error) {
	if s.sandboxes == nil {
		return nil, sandbox.ErrNotRunning
	}
	sb, err := s.sandboxes.ForUser(p.UserID)
	if err != nil {
		return nil, err
	}
	launch := p.Launch()
	script := "exec agy models"
	if launch.Setup != "" {
		script = launch.Setup + " && " + script
	}
	res, err := sb.ExecIfRunning(ctx, sandbox.ExecRequest{Cmd: []string{"bash", "-lc", script}, Cwd: sandbox.ContainerHome,
		Env: launch.Env, Unset: launch.Unset, UnsetGlobal: launch.UnsetGlobal, Timeout: 20 * time.Second})
	if err != nil {
		return nil, err
	}
	return ParseAgyModels(res.Stdout), nil
}

// ParseAgyModels reads `agy models` output: one "<id>\t<name>" line per
// model after a "Fetching available models..." line.
func ParseAgyModels(out string) []models.ModelInfo {
	var list []models.ModelInfo
	for _, line := range strings.Split(out, "\n") {
		id, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		id, name = strings.TrimSpace(id), strings.TrimSpace(name)
		if !ok || name == "" || !models.ValidModelFor(models.ProviderAntigravity, id) {
			continue
		}
		list = append(list, models.AntigravityModel(id, name))
	}
	return list
}
