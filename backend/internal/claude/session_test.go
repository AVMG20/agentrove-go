package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProc is a CLI process whose exit and output the test controls.
type fakeProc struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	stderrR *io.PipeReader
	stderrW *io.PipeWriter
	exited  chan struct{}
	// Control requests the fake has seen, and an optional reply per subtype.
	mu       sync.Mutex
	controls []map[string]any
	replies  map[string]string
}

func newFakeProc(t *testing.T) *fakeProc {
	p := &fakeProc{exited: make(chan struct{})}
	p.stdinR, p.stdinW = io.Pipe()
	p.stdoutR, p.stdoutW = io.Pipe()
	p.stderrR, p.stderrW = io.Pipe()
	// Answer the initialize handshake like the CLI does.
	go func() {
		sc := bufio.NewScanner(p.stdinR)
		for sc.Scan() {
			var req struct {
				Type      string         `json:"type"`
				RequestID string         `json:"request_id"`
				Request   map[string]any `json:"request"`
			}
			if json.Unmarshal(sc.Bytes(), &req) == nil && req.Type == "control_request" {
				p.mu.Lock()
				p.controls = append(p.controls, req.Request)
				reply := p.replies[fmt.Sprint(req.Request["subtype"])]
				p.mu.Unlock()
				if reply == "" {
					reply = "{}"
				}
				p.line(`{"type":"control_response","response":{"subtype":"success","request_id":"` + req.RequestID + `","response":` + reply + `}}`)
			}
		}
	}()
	t.Cleanup(func() {
		p.stdoutW.Close()
		p.stderrW.Close()
		p.stdinR.Close()
	})
	return p
}

func (p *fakeProc) line(s string) { _, _ = p.stdoutW.Write([]byte(s + "\n")) }

func (p *fakeProc) Stdin() io.WriteCloser { return p.stdinW }
func (p *fakeProc) Stdout() io.Reader     { return p.stdoutR }
func (p *fakeProc) Stderr() io.Reader     { return p.stderrR }
func (p *fakeProc) Wait() error           { <-p.exited; return nil }
func (p *fakeProc) Kill() error           { return nil }

type sinkRecorder struct {
	mu   sync.Mutex
	msgs []Message
}

func (r *sinkRecorder) add(m Message) { r.mu.Lock(); r.msgs = append(r.msgs, m); r.mu.Unlock() }

func (r *sinkRecorder) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.msgs))
	for i, m := range r.msgs {
		out[i] = m.Type
	}
	return out
}

func startFake(t *testing.T, p *fakeProc) (*Session, *sinkRecorder) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := start(ctx, p, func() {}, "sb", "chat", Config{})
	if err != nil {
		t.Fatal(err)
	}
	rec := &sinkRecorder{}
	s.Attach(rec.add, nil)
	return s, rec
}

// The process can be reaped before its last stdout lines are read; Done must
// still come after them, or the turn fails without its result.
func TestSessionDoneWaitsForFinalOutput(t *testing.T) {
	p := newFakeProc(t)
	s, rec := startFake(t, p)

	close(p.exited)
	time.Sleep(50 * time.Millisecond) // the exit is observed first
	p.line(`{"type":"assistant","message":{"content":[]}}`)
	p.line(`{"type":"result","subtype":"success","is_error":false}`)
	p.stdoutW.Close()
	p.stderrW.Close()

	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done never fired")
	}
	if got := rec.types(); len(got) != 2 || got[1] != "result" {
		t.Fatalf("messages delivered before Done = %v, want [assistant result]", got)
	}
	if !s.Closed() {
		t.Fatal("session not closed after exit")
	}
}

// Output that never closes (a grandchild holding stdout) must not hold the
// exit notification forever.
func TestSessionDoneBoundedWhenOutputStaysOpen(t *testing.T) {
	prev := outputDrainTimeout
	outputDrainTimeout = 100 * time.Millisecond
	t.Cleanup(func() { outputDrainTimeout = prev })

	p := newFakeProc(t)
	s, _ := startFake(t, p)
	close(p.exited)
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done blocked on open stdout")
	}
}

func TestOneShotEnvDropsHostSecretsAndUnsafeUserVars(t *testing.T) {
	host := []string{"PATH=/usr/bin", "SECRET_KEY=s3cret", "MAIL_PASSWORD=x", "HTTPS_PROXY=http://p:3128"}
	extra := map[string]string{"ANTHROPIC_API_KEY": "k", "LD_PRELOAD": "/x.so", "NODE_OPTIONS": "--require /x.js", "PATH": "/evil"}
	got := map[string]string{}
	for _, kv := range oneShotEnv(host, extra) {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	want := map[string]string{"PATH": "/usr/bin", "HTTPS_PROXY": "http://p:3128", "ANTHROPIC_API_KEY": "k"}
	if len(got) != len(want) {
		t.Fatalf("env = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("env[%s] = %q, want %q (env %v)", k, got[k], v, got)
		}
	}
}

// A stop with steered messages pending asks the CLI to drop them and reports
// which it dropped, so they can go back to the user's queue.
func TestInterruptCancelQueuedReportsDropped(t *testing.T) {
	p := newFakeProc(t)
	p.mu.Lock()
	p.replies = map[string]string{"interrupt": `{"still_queued":[],"cancelled":["u1"]}`}
	p.mu.Unlock()
	s, _ := startFake(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dropped, err := s.InterruptCancelQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || dropped[0] != "u1" {
		t.Fatalf("dropped = %v, want [u1]", dropped)
	}
	p.mu.Lock()
	last := p.controls[len(p.controls)-1]
	p.mu.Unlock()
	if last["subtype"] != "interrupt" || last["cancel_queued"] != true {
		t.Fatalf("control request = %v, want interrupt with cancel_queued", last)
	}
}
