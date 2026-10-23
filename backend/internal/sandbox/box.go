package sandbox

import (
	"context"
	"errors"
	"time"
)

// BoxInfo is the user-facing state of their Docker box.
type BoxInfo struct {
	Available      bool   `json:"available"` // Docker is reachable
	State          string `json:"state"`     // running | stopped | none
	Busy           bool   `json:"busy"`      // an agent or terminal holds it awake
	IdleForSeconds int64  `json:"idle_for_seconds"`
	IdleTTLSeconds int64  `json:"idle_ttl_seconds"`
	DiskBytes      *int64 `json:"disk_bytes"`
}

var ErrBoxBusy = errors.New("the box is busy: stop running chats and close terminals first")

func (m *Manager) docker() (*DockerProvider, bool) {
	p, ok := m.providers[KindDocker]
	if !ok {
		return nil, false
	}
	d, ok := p.(*DockerProvider)
	return d, ok
}

func (m *Manager) Box(ctx context.Context, userID string) BoxInfo {
	info := BoxInfo{State: "none", IdleTTLSeconds: int64(m.cfg.SandboxIdleTTL / time.Second)}
	d, ok := m.docker()
	if !ok {
		return info
	}
	info.Available = true
	id := BoxID(userID)
	exists, running, err := d.State(ctx, id)
	if err != nil || !exists {
		return info
	}
	info.State = "stopped"
	if running {
		info.State = "running"
	}
	m.mu.Lock()
	info.Busy = m.leases[id] > 0
	if last, ok := m.activity[id]; ok && running {
		info.IdleForSeconds = int64(time.Since(last) / time.Second)
	}
	m.mu.Unlock()
	if n, ok := d.DiskUsage(ctx, id); ok {
		info.DiskBytes = &n
	}
	return info
}

// StopBox stops the user's box now instead of waiting for the idle timeout.
// With reset it is removed too, so the next start gets a fresh system from
// the current image; home and projects are kept.
func (m *Manager) StopBox(ctx context.Context, userID string, reset bool) error {
	d, ok := m.docker()
	if !ok {
		return errors.New("docker is not available")
	}
	id := BoxID(userID)
	lock := m.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	m.mu.Lock()
	busy := m.leases[id] > 0
	m.mu.Unlock()
	if busy {
		return ErrBoxBusy
	}
	if m.OnStop != nil {
		m.OnStop(id)
	}
	if reset {
		return d.Delete(ctx, id)
	}
	return d.Stop(ctx, id)
}
