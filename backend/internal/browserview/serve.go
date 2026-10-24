package browserview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Queued control messages beyond this mean the client stopped reading.
var maxQueuedTextBytes = 1 << 20

// Close codes besides the terminal's 4001 (auth) and 4004 (not found).
const (
	CloseUnavailable websocket.StatusCode = 4006 // the box has no working browser
	CloseTooMany     websocket.StatusCode = 4008 // viewer limit reached
)

const (
	pingInterval  = 30 * time.Second
	writeTimeout  = 30 * time.Second
	textQueueSize = 256
	// Client messages are small; text input is capped at maxTextInput.
	clientReadSize = 256 << 10
	// A browser that keeps dying isn't retried forever.
	maxRestarts = 3
)

// Box is the browser side of a viewer: the box's launcher and a byte stream
// to its debugging port. The API implements it with docker exec; tests fake it.
type Box interface {
	// Ensure starts the browser if needed and returns its CDP browser
	// websocket path.
	Ensure(ctx context.Context) (string, error)
	// Dial opens a stream to the debugging port.
	Dial(ctx context.Context) (io.ReadWriteCloser, error)
	// Reset stops the browser and wipes its profile.
	Reset(ctx context.Context) error
	// Touch records user activity (keeps an idle box from stopping).
	Touch()
}

// Serve runs an authenticated viewer on c until the client leaves. It owns c
// and closes it.
func Serve(ctx context.Context, c *websocket.Conn, box Box) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.SetReadLimit(clientReadSize)
	w := newWriter(ctx, c, cancel)

	in := make(chan clientMsg, 256)
	go func() {
		defer close(in)
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				cancel()
				return
			}
			var m clientMsg
			if typ != websocket.MessageText || json.Unmarshal(data, &m) != nil || m.Type == "" {
				continue
			}
			select {
			case in <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		t := time.NewTicker(pingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				// NAT/LB keepalive, like the terminal; no pong expected.
				w.Text(map[string]any{"type": "ping"})
			}
		}
	}()

	restarts := 0
	// Tabs shown when a connection ended: the next session starts elsewhere.
	avoid := map[string]bool{}
	for {
		started := time.Now()
		tried, err := session(ctx, box, w, in, avoid)
		if time.Since(started) > time.Minute {
			restarts = 0
		}
		if w.overflowed() {
			// Not a clean end: the client should come back.
			_ = c.Close(websocket.StatusTryAgainLater, "Client too slow")
			return
		}
		if ctx.Err() != nil || err == nil {
			_ = c.Close(websocket.StatusNormalClosure, "")
			return
		}
		if errors.Is(err, errBrowserGone) && tried != "" {
			avoid[tried] = true
		}
		switch {
		case errors.Is(err, errReset):
			w.Text(map[string]any{"type": "status", "state": "restarting"})
			if err := box.Reset(ctx); err != nil {
				slog.Warn("reset box browser", "err", err)
			}
			restarts = 0
			continue
		case errors.Is(err, errBrowserGone) && restarts < maxRestarts:
			restarts++
			w.Text(map[string]any{"type": "status", "state": "restarting"})
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(restarts) * time.Second):
			}
			continue
		}
		slog.Info("browser viewer closed", "err", err)
		_ = c.Close(CloseUnavailable, closeReason(err))
		return
	}
}

// session connects to the browser once and runs a viewer on it. It returns
// the tab it last tried to show.
func session(ctx context.Context, box Box, w *writer, in <-chan clientMsg, avoid map[string]bool) (string, error) {
	w.Text(map[string]any{"type": "status", "state": "starting"})
	sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	path, err := box.Ensure(sctx)
	if err != nil {
		cancel()
		return "", err
	}
	stream, err := box.Dial(sctx)
	if err != nil {
		cancel()
		return "", err
	}
	t, err := DialCDP(sctx, stream, path)
	cancel()
	if err != nil {
		return "", err
	}
	cdp := NewConn(t)
	defer cdp.Close()
	v := newViewer(cdp, w, box.Touch, avoid)
	err = v.run(ctx, in)
	return v.attaching, err
}

// closeReason fits an error into a close frame (123 bytes max).
func closeReason(err error) string {
	msg := "Browser unavailable: " + strings.TrimSpace(err.Error())
	if len(msg) > 120 {
		// Drops a rune cut in half.
		msg = strings.ToValidUTF8(msg[:120], "")
	}
	return msg
}

// writer is the socket's only writer: control messages in order, and only
// the newest screencast frame (a stale one is useless).
type writer struct {
	c      *websocket.Conn
	cancel context.CancelFunc
	text   chan []byte

	mu       sync.Mutex
	queued   int // bytes in text
	overflow bool
	frame    []byte
	ready    chan struct{}
}

func newWriter(ctx context.Context, c *websocket.Conn, cancel context.CancelFunc) *writer {
	w := &writer{c: c, cancel: cancel, text: make(chan []byte, textQueueSize), ready: make(chan struct{}, 1)}
	go w.loop(ctx)
	return w
}

func (w *writer) Text(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	w.mu.Lock()
	over := w.queued+len(b) > maxQueuedTextBytes
	if !over {
		w.queued += len(b)
	}
	w.mu.Unlock()
	if over {
		w.stalled()
		return
	}
	select {
	case w.text <- b:
	default:
		w.stalled()
	}
}

// stalled drops a client that stopped reading.
func (w *writer) stalled() {
	w.mu.Lock()
	w.overflow = true
	w.mu.Unlock()
	w.cancel()
}

func (w *writer) overflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflow
}

func (w *writer) sent(b []byte) {
	w.mu.Lock()
	w.queued -= len(b)
	w.mu.Unlock()
}

func (w *writer) Frame(b []byte) {
	w.mu.Lock()
	w.frame = b
	w.mu.Unlock()
	select {
	case w.ready <- struct{}{}:
	default:
	}
}

func (w *writer) loop(ctx context.Context) {
	write := func(typ websocket.MessageType, b []byte) bool {
		wctx, cancel := context.WithTimeout(ctx, writeTimeout)
		defer cancel()
		if err := w.c.Write(wctx, typ, b); err != nil {
			w.cancel()
			return false
		}
		return true
	}
	for {
		// Control messages first: tabs and status beat pixels.
		select {
		case b := <-w.text:
			w.sent(b)
			if !write(websocket.MessageText, b) {
				return
			}
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return
		case b := <-w.text:
			w.sent(b)
			if !write(websocket.MessageText, b) {
				return
			}
		case <-w.ready:
			w.mu.Lock()
			b := w.frame
			w.frame = nil
			w.mu.Unlock()
			if b != nil && !write(websocket.MessageBinary, b) {
				return
			}
		}
	}
}
