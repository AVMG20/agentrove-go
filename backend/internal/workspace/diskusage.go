package workspace

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentrove/internal/models"
	"agentrove/internal/sandbox"
)

const (
	// Walking a repo with node_modules takes seconds and sizes barely move
	// minute to minute.
	usageCacheTTL   = 5 * time.Minute
	duTimeout       = time.Minute
	maxConcurrentDu = 3
)

type usageEntry struct {
	WorkspaceID  string `json:"workspace_id"`
	FilesBytes   *int64 `json:"files_bytes"`
	SandboxBytes *int64 `json:"sandbox_bytes"`
	TotalBytes   *int64 `json:"total_bytes"`
}

type cachedUsage struct {
	at    time.Time
	entry usageEntry
}

type diskUsage struct {
	mu    sync.Mutex
	cache map[string]cachedUsage
	slots chan struct{}
}

func newDiskUsage() *diskUsage {
	return &diskUsage{cache: map[string]cachedUsage{}, slots: make(chan struct{}, maxConcurrentDu)}
}

func (d *diskUsage) forget(id string) {
	d.mu.Lock()
	delete(d.cache, id)
	d.mu.Unlock()
}

func (d *diskUsage) get(ctx context.Context, mgr *sandbox.Manager, ws *models.Workspace, refresh bool) usageEntry {
	d.mu.Lock()
	c, ok := d.cache[ws.ID]
	d.mu.Unlock()
	if ok && !refresh && time.Since(c.at) < usageCacheTTL {
		return c.entry
	}
	select {
	case d.slots <- struct{}{}:
	case <-ctx.Done():
		return usageEntry{WorkspaceID: ws.ID}
	}
	entry := usageEntry{WorkspaceID: ws.ID, FilesBytes: directoryBytes(ctx, ws.WorkspacePath)}
	if ws.SandboxProvider == sandbox.KindDocker {
		if sb, err := mgr.For(ws); err == nil {
			if n, ok := sb.DiskUsage(ctx); ok {
				entry.SandboxBytes = &n
			}
		}
	}
	<-d.slots
	entry.TotalBytes = sumBytes(entry.FilesBytes, entry.SandboxBytes)
	d.mu.Lock()
	d.cache[ws.ID] = cachedUsage{at: time.Now(), entry: entry}
	d.mu.Unlock()
	return entry
}

func sumBytes(parts ...*int64) *int64 {
	var total int64
	found := false
	for _, p := range parts {
		if p != nil {
			total += *p
			found = true
		}
	}
	if !found {
		return nil
	}
	return &total
}

// directoryBytes runs `du -sk`; du still prints a total when some entries
// were unreadable, so its exit status is ignored.
func directoryBytes(ctx context.Context, path string) *int64 {
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
