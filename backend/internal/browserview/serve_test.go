package browserview

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// cdpOverStream serves a fakeBrowser as a CDP websocket endpoint on one end
// of a pipe, like Chromium behind socat.
func cdpOverStream(t *testing.T, f *fakeBrowser, path string) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	ln := &oneConnListener{c: server, done: make(chan struct{})}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path || r.Host != "127.0.0.1:9222" {
			http.Error(w, "bad endpoint", http.StatusNotFound)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ws.SetReadLimit(cdpReadLimit)
		ctx := r.Context()
		go func() {
			for {
				b, err := f.ReadMessage(ctx)
				if err != nil {
					_ = ws.CloseNow()
					return
				}
				if ws.Write(ctx, websocket.MessageText, b) != nil {
					return
				}
			}
		}()
		for {
			_, b, err := ws.Read(ctx)
			if err != nil {
				_ = f.Close()
				return
			}
			_ = f.WriteMessage(ctx, b)
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return client
}

type oneConnListener struct {
	mu   sync.Mutex
	c    net.Conn
	done chan struct{}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	c := l.c
	l.c = nil
	l.mu.Unlock()
	if c != nil {
		return c, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *oneConnListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *oneConnListener) Addr() net.Addr { return execAddr{} }

func TestDialCDPOverStream(t *testing.T) {
	f := newFakeBrowser("about:blank")
	stream := cdpOverStream(t, f, "/devtools/browser/abc")
	tr, err := DialCDP(context.Background(), stream, "/devtools/browser/abc")
	if err != nil {
		t.Fatal(err)
	}
	c := NewConn(tr)
	defer c.Close()
	raw, err := c.Call(context.Background(), "", "Target.getTargets", nil)
	if err != nil || !strings.Contains(string(raw), "about:blank") {
		t.Fatalf("getTargets = %s, %v", raw, err)
	}
	// Errors come back as CDPError.
	f.mu.Lock()
	f.failScreencast = 1
	f.mu.Unlock()
	_, err = c.Call(context.Background(), "S1", "Page.startScreencast", map[string]any{})
	var cdpErr *CDPError
	if !errors.As(err, &cdpErr) || cdpErr.Code != -32000 {
		t.Fatalf("err = %v", err)
	}
	// Events are queued for the consumer.
	f.emit("S1", "Page.loadEventFired", map[string]any{})
	select {
	case <-c.Notify():
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
	if ev := c.Events(); len(ev) != 1 || ev[0].Method != "Page.loadEventFired" || ev[0].SessionID != "S1" {
		t.Fatalf("events = %v", ev)
	}
	// The browser going away fails calls and closes Done.
	_ = f.Close()
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("connection still open")
	}
	if _, err := c.Call(context.Background(), "", "Target.getTargets", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
}

func TestDialCDPRejectsWrongPath(t *testing.T) {
	stream := cdpOverStream(t, newFakeBrowser(), "/devtools/browser/abc")
	if _, err := DialCDP(context.Background(), stream, "/devtools/browser/other"); err == nil {
		t.Fatal("handshake succeeded on a wrong path")
	}
}

// fakeBox hands out fresh fake browsers, like the launcher would.
type fakeBox struct {
	t         *testing.T
	mu        sync.Mutex
	browsers  []*fakeBrowser
	resets    int
	ensureErr error
	// pages and poison set up each new browser (default: one dev server tab).
	pages  []string
	poison string
}

func (b *fakeBox) Ensure(context.Context) (string, error) {
	if b.ensureErr != nil {
		return "", b.ensureErr
	}
	return "/devtools/browser/x", nil
}

func (b *fakeBox) Dial(context.Context) (io.ReadWriteCloser, error) {
	pages := b.pages
	if pages == nil {
		pages = []string{"http://localhost:3000/"}
	}
	f := newFakeBrowser(pages...)
	f.poison = b.poison
	b.mu.Lock()
	b.browsers = append(b.browsers, f)
	b.mu.Unlock()
	return cdpOverStream(b.t, f, "/devtools/browser/x"), nil
}

func (b *fakeBox) Reset(context.Context) error {
	b.mu.Lock()
	b.resets++
	b.mu.Unlock()
	return nil
}

func (b *fakeBox) Touch() {}

func (b *fakeBox) browser(t *testing.T, i int) *fakeBrowser {
	t.Helper()
	waitUntilFor(t, 10*time.Second, "browser dialed", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.browsers) > i
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.browsers[i]
}

func serveViewer(t *testing.T, box Box) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		Serve(r.Context(), c, box)
	}))
	t.Cleanup(srv.Close)
	c, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(cdpReadLimit)
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

// readUntil reads server messages until match accepts one.
func readUntil(t *testing.T, c *websocket.Conn, match func(typ websocket.MessageType, b []byte) bool) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		typ, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if match(typ, b) {
			return b
		}
	}
}

func textOf(typ string) func(websocket.MessageType, []byte) bool {
	return func(mt websocket.MessageType, b []byte) bool {
		var m map[string]any
		return mt == websocket.MessageText && json.Unmarshal(b, &m) == nil && m["type"] == typ
	}
}

func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func TestServeEndToEnd(t *testing.T) {
	box := &fakeBox{t: t}
	c := serveViewer(t, box)
	readUntil(t, c, textOf("tabs"))
	f := box.browser(t, 0)
	sid := f.waitCall(t, "Page.startScreencast", 0).SessionID

	f.frame(sid, 1)
	b := readUntil(t, c, func(mt websocket.MessageType, _ []byte) bool { return mt == websocket.MessageBinary })
	n := binary.BigEndian.Uint32(b)
	var h frameHeader
	if json.Unmarshal(b[4:4+n], &h) != nil || h.Seq != 1 || h.Width != 1280 || string(b[4+n:]) != "jpeg-1" {
		t.Fatalf("frame %q", b)
	}
	send(t, c, map[string]any{"type": "ack", "seq": h.Seq})
	send(t, c, map[string]any{"type": "mouse", "event": "down", "x": 3, "y": 4, "button": "left", "buttons": 1, "clickCount": 1})
	if p := params(f.waitCall(t, "Input.dispatchMouseEvent", 0)); p["x"] != 3.0 || p["type"] != "mousePressed" {
		t.Fatalf("mouse = %v", p)
	}
	// Non-text frames and junk are ignored.
	_ = c.Write(context.Background(), websocket.MessageBinary, []byte("x"))
	_ = c.Write(context.Background(), websocket.MessageText, []byte("{"))

	// Reset: the launcher wipes the browser and a new one is attached.
	send(t, c, map[string]any{"type": "reset"})
	readUntil(t, c, func(mt websocket.MessageType, b []byte) bool {
		return mt == websocket.MessageText && strings.Contains(string(b), `"restarting"`)
	})
	f2 := box.browser(t, 1)
	f2.waitCall(t, "Page.startScreencast", 0)
	box.mu.Lock()
	resets := box.resets
	box.mu.Unlock()
	if resets != 1 {
		t.Fatalf("resets = %d", resets)
	}

	// The browser dies: the viewer reconnects on its own.
	_ = f2.Close()
	box.browser(t, 2).waitCall(t, "Page.startScreencast", 0)
}

func TestServeUnavailableBox(t *testing.T) {
	box := &fakeBox{t: t, ensureErr: errors.New("this box image has no browser; rebuild the sandbox image")}
	c := serveViewer(t, box)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		_, _, err := c.Read(ctx)
		if err == nil {
			continue
		}
		if websocket.CloseStatus(err) != CloseUnavailable || !strings.Contains(err.Error(), "rebuild the sandbox image") {
			t.Fatalf("close = %v", err)
		}
		return
	}
}

// A client that stops reading is dropped once its queued control messages
// pass maxQueuedTextBytes, however few they are.
func TestWriterBoundsQueuedBytes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// No write loop: nothing is ever sent.
	w := &writer{cancel: cancel, text: make(chan []byte, textQueueSize), ready: make(chan struct{}, 1)}
	big := strings.Repeat("x", 200<<10)
	for i := 0; i < 20 && ctx.Err() == nil; i++ {
		w.Text(map[string]any{"type": "tabs", "url": big})
	}
	if ctx.Err() == nil || !w.overflowed() {
		t.Fatal("writer kept queueing for a client that doesn't read")
	}
	if w.queued > maxQueuedTextBytes {
		t.Fatalf("queued %d bytes", w.queued)
	}
}

// One hostile tab (here: one that floods the connection when attached) must
// not keep the viewer down: the next session shows another tab.
func TestServeRecoversFromPoisonedTab(t *testing.T) {
	box := &fakeBox{t: t, pages: []string{"http://evil/"}, poison: "http://evil/"}
	c := serveViewer(t, box)
	// The first session attaches to the hostile tab and loses its connection.
	box.browser(t, 0).waitCall(t, "Target.attachToTarget", 0)
	f := box.browser(t, 1)
	// The second avoids it and opens a fresh tab instead of re-attaching.
	if p := params(f.waitCall(t, "Target.createTarget", 0)); p["url"] != "about:blank" {
		t.Fatalf("create = %v", p)
	}
	att := f.waitCall(t, "Target.attachToTarget", 0)
	if params(att)["targetId"] == "T1" {
		t.Fatal("re-attached the hostile tab")
	}
	f.waitCall(t, "Page.startScreencast", 0)
	// And it stays up.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		_, _, err := c.Read(ctx)
		if err == nil {
			continue
		}
		if ctx.Err() == nil {
			t.Fatalf("viewer closed: %v", err)
		}
		break
	}
	box.mu.Lock()
	n := len(box.browsers)
	box.mu.Unlock()
	if n != 2 {
		t.Fatalf("%d sessions, want 2", n)
	}
}

// A client too slow to read is closed with a code the frontend retries on.
func TestServeClosesStalledClientRetryably(t *testing.T) {
	old := maxQueuedTextBytes
	maxQueuedTextBytes = 64 << 10
	t.Cleanup(func() { maxQueuedTextBytes = old })
	box := &fakeBox{t: t}
	c := serveViewer(t, box)
	readUntil(t, c, textOf("tabs"))
	f := box.browser(t, 0)
	// Flood tab updates (each clipped to 2KB) past the socket buffers without
	// reading.
	big := strings.Repeat("u", 4000)
	for i := range 6000 {
		f.emit("", "Target.targetInfoChanged", map[string]any{"targetInfo": targetInfo{TargetID: "T1", Type: "page", URL: fmt.Sprintf("http://x/%d%s", i, big)}})
	}
	time.Sleep(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for {
		_, _, err := c.Read(ctx)
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			t.Fatal("stalled client never dropped")
		}
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			t.Fatalf("stalled client closed as normal: %v", err)
		}
		return
	}
}
