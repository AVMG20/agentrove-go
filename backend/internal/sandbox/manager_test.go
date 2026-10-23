package sandbox

import (
	"context"
	"sync"
	"testing"
	"time"

	"agentrove/internal/config"
	"agentrove/internal/models"
)

// fakeProvider records lifecycle calls; Exec succeeds only while running.
type fakeProvider struct {
	mu      sync.Mutex
	running map[string]bool
	starts  int
	stops   int
}

func (f *fakeProvider) Kind() string                                    { return KindDocker }
func (f *fakeProvider) Create(context.Context, CreateOptions) error     { return nil }
func (f *fakeProvider) Delete(_ context.Context, id string) error       { return nil }
func (f *fakeProvider) DiskUsage(context.Context, string) (int64, bool) { return 0, false }
func (f *fakeProvider) EnsureRunning(_ context.Context, id string, _ CreateOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.running[id] {
		f.running[id] = true
		f.starts++
	}
	return nil
}
func (f *fakeProvider) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running[id] = false
	f.stops++
	return nil
}
func (f *fakeProvider) Running(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for id, r := range f.running {
		if r {
			out = append(out, id)
		}
	}
	return out, nil
}
func (f *fakeProvider) Exec(_ context.Context, id string, _ ExecRequest) (ExecResult, error) {
	return ExecResult{}, nil
}
func (f *fakeProvider) Start(context.Context, string, ExecRequest) (Process, error) { return nil, nil }
func (f *fakeProvider) StartPTY(context.Context, string, ExecRequest, int, int) (PTY, error) {
	return nil, nil
}

func newTestManager(ttl time.Duration) (*Manager, *fakeProvider) {
	cfg := &config.Config{SandboxIdleTTL: ttl}
	fp := &fakeProvider{running: map[string]bool{}}
	m := &Manager{cfg: cfg, providers: map[string]Provider{KindDocker: fp}, activity: map[string]time.Time{},
		leases: map[string]int{}, starting: map[string]*sync.Mutex{}, userEnv: map[string]map[string]string{}}
	return m, fp
}

func TestIdleReaperStopsAndWakes(t *testing.T) {
	m, fp := newTestManager(50 * time.Millisecond)
	var stopped []string
	m.OnStop = func(id string) { stopped = append(stopped, id) }
	sb, err := m.For(&models.Workspace{SandboxID: "s1", SandboxProvider: KindDocker, WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := sb.Exec(ctx, ExecRequest{Cmd: []string{"true"}}); err != nil {
		t.Fatal(err)
	}

	// Recently active: kept.
	m.reapOnce(ctx, fp)
	if fp.stops != 0 {
		t.Fatalf("stopped an active sandbox")
	}

	// Leased: kept even after the TTL.
	release := sb.Acquire()
	time.Sleep(80 * time.Millisecond)
	m.reapOnce(ctx, fp)
	if fp.stops != 0 {
		t.Fatalf("stopped a leased sandbox")
	}

	// Released and idle past the TTL: stopped, OnStop ran first.
	release()
	time.Sleep(80 * time.Millisecond)
	m.reapOnce(ctx, fp)
	if fp.stops != 1 || len(stopped) != 1 || stopped[0] != "s1" {
		t.Fatalf("idle sandbox not stopped: stops=%d onStop=%v", fp.stops, stopped)
	}

	// Next use wakes it transparently.
	if _, err := sb.Exec(ctx, ExecRequest{Cmd: []string{"true"}}); err != nil {
		t.Fatal(err)
	}
	if fp.starts != 2 || !fp.running["s1"] {
		t.Fatalf("sandbox not restarted: starts=%d", fp.starts)
	}
}

func TestIdleReaperGivesUnknownSandboxesFullTTL(t *testing.T) {
	m, fp := newTestManager(time.Hour)
	fp.running["orphan"] = true
	m.reapOnce(context.Background(), fp)
	if fp.stops != 0 {
		t.Fatal("stopped a sandbox on first sight")
	}
}
