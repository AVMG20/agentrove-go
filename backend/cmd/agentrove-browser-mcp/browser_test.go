package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDevTools serves Chromium's DevTools HTTP endpoints and page
// websockets; pages in hung never answer.
type fakeDevTools struct {
	mu      sync.Mutex
	pages   []string
	hung    map[string]bool
	closed  []string
	stalled bool // /json/version doesn't answer
}

func (d *fakeDevTools) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case r.URL.Path == "/json/version":
		if d.stalled {
			d.mu.Unlock()
			<-r.Context().Done()
			d.mu.Lock()
			return
		}
		_, _ = w.Write([]byte(`{"Browser":"HeadlessChrome"}`))
	case r.URL.Path == "/json/list":
		var out []map[string]string
		for _, id := range d.pages {
			out = append(out, map[string]string{"id": id, "type": "page"})
		}
		out = append(out, map[string]string{"id": "SW", "type": "service_worker"})
		_ = json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(r.URL.Path, "/json/close/"):
		id := strings.TrimPrefix(r.URL.Path, "/json/close/")
		d.pages = slices.DeleteFunc(d.pages, func(p string) bool { return p == id })
		d.closed = append(d.closed, id)
		_, _ = w.Write([]byte("Target is closing"))
	case strings.HasPrefix(r.URL.Path, "/devtools/page/"):
		hung := d.hung[strings.TrimPrefix(r.URL.Path, "/devtools/page/")]
		conn, brw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		go serveWS(conn, brw.Reader, r.Header.Get("Sec-WebSocket-Key"), hung)
	default:
		http.NotFound(w, r)
	}
}

// serveWS answers each command with an event and then its result (none
// when hung).
func serveWS(conn net.Conn, br *bufio.Reader, key string, hung bool) {
	defer conn.Close()
	sum := sha1.Sum([]byte(key + wsGUID))
	_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"))
	c := &wsConn{conn: conn, br: br}
	for {
		msg, err := c.Read()
		if err != nil {
			return
		}
		if hung {
			continue
		}
		var m struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(msg, &m)
		writeServerFrame(conn, 0x9, []byte("ping"))
		writeServerFrame(conn, 0x1, []byte(`{"method":"Runtime.executionContextCreated","params":{}}`))
		res, _ := json.Marshal(map[string]any{"id": m.ID, "result": map[string]any{"echo": string(msg)}})
		writeServerFrame(conn, 0x1, res)
	}
}

// writeServerFrame writes an unmasked frame, as servers do.
func writeServerFrame(conn net.Conn, opcode byte, payload []byte) {
	hdr := []byte{0x80 | opcode}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, 126)
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(n))
	default:
		hdr = append(hdr, 127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	_, _ = conn.Write(append(hdr, payload...))
}

func newFakeDevTools(t *testing.T) (*fakeDevTools, *browserCtl, *[]string) {
	d := &fakeDevTools{hung: map[string]bool{}}
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	var stops []string
	b := &browserCtl{
		base:   srv.URL,
		client: &http.Client{Timeout: httpTimeout},
		stop:   func(pid string) { stops = append(stops, pid) },
		pid:    func() string { return "42" },
	}
	return d, b, &stops
}

func shortTimeouts(t *testing.T) {
	oldHTTP, oldProbe := httpTimeout, pageProbeTimeout
	httpTimeout, pageProbeTimeout = 200*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { httpTimeout, pageProbeTimeout = oldHTTP, oldProbe })
}

func TestHealClosesHungPages(t *testing.T) {
	shortTimeouts(t)
	d, b, stops := newFakeDevTools(t)
	d.pages = []string{"AAAA0001", "AAAA0002", "AAAA0003"}
	d.hung["AAAA0002"] = true
	b.heal()
	if !slices.Equal(d.closed, []string{"AAAA0002"}) || !slices.Equal(d.pages, []string{"AAAA0001", "AAAA0003"}) || len(*stops) != 0 {
		t.Fatalf("closed %v, left %v, stops %v", d.closed, d.pages, *stops)
	}
	if ids, err := b.listPages(); err != nil || !slices.Equal(ids, d.pages) {
		t.Fatalf("listPages = %v, %v", ids, err)
	}
}

func TestHealRestartsAnUnresponsiveBrowser(t *testing.T) {
	shortTimeouts(t)
	d, b, stops := newFakeDevTools(t)
	d.pages = []string{"AAAA0001"}
	d.stalled = true
	b.heal()
	if !slices.Equal(*stops, []string{"42"}) || len(d.closed) != 0 {
		t.Fatalf("stops %v, closed %v", *stops, d.closed)
	}
}

func TestHealLeavesAStoppedBrowserAlone(t *testing.T) {
	d, b, stops := newFakeDevTools(t)
	d.stalled = true
	b.pid = func() string { return "" }
	b.heal()
	if len(*stops) != 0 {
		t.Fatalf("stops %v", *stops)
	}
}

func TestClosePageOnlyTakesTargetIDs(t *testing.T) {
	d, b, _ := newFakeDevTools(t)
	b.closePage("../version")
	b.closePage("AAAA0001")
	if !slices.Equal(d.closed, []string{"AAAA0001"}) {
		t.Fatalf("closed %v", d.closed)
	}
}

// Messages of every length encoding go through, and pings are answered.
func TestWSRoundTrip(t *testing.T) {
	d, b, _ := newFakeDevTools(t)
	d.pages = []string{"AAAA0001"}
	c, err := dialWS(t.Context(), strings.Replace(b.base, "http://", "ws://", 1)+"/devtools/page/AAAA0001")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, n := range []int{10, 300, 70000} {
		cmd, _ := json.Marshal(map[string]any{"id": n, "method": "Runtime.evaluate", "params": map[string]string{"expression": strings.Repeat("x", n)}})
		if err := c.WriteText(cmd); err != nil {
			t.Fatal(err)
		}
		for {
			msg, err := c.Read()
			if err != nil {
				t.Fatal(err)
			}
			var r struct {
				ID     int `json:"id"`
				Result struct {
					Echo string `json:"echo"`
				} `json:"result"`
			}
			_ = json.Unmarshal(msg, &r)
			if r.ID == n {
				if r.Result.Echo != string(cmd) {
					t.Fatalf("echo of %d bytes differs", n)
				}
				break
			}
		}
	}
}
