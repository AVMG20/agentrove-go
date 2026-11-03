package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The shared browser over its HTTP endpoints and a minimal CDP websocket
// client, for what playwright-mcp can't do itself: tell whether the browser
// is fit for a new connection. Playwright's connectOverCDP attaches to every
// page and waits for each to answer, so one hung page (a script in a busy
// loop, a JS dialog whose CDP session went away, input that never
// completes) makes every later playwright-mcp time out in initializeServer,
// for every agent in the box.

var (
	httpTimeout = 3 * time.Second
	// A page that can't evaluate `0` in this long is hung: it would hold up
	// connectOverCDP.
	pageProbeTimeout = 5 * time.Second
)

// targetIDPattern is what Chromium's target ids look like; only such ids
// are put into the code the proxy runs in playwright-mcp.
var targetIDPattern = regexp.MustCompile(`^[0-9A-Fa-f]{8,64}$`)

// browserCtl talks to the browser on base (http://127.0.0.1:<port>).
type browserCtl struct {
	base   string
	client *http.Client
	// stop stops the browser if it still is pid ("" when unknown).
	stop func(pid string)
	// pid of the running browser, "" if none or unknown.
	pid func() string
}

func newBrowserCtl(port string) *browserCtl {
	return &browserCtl{
		base:   "http://127.0.0.1:" + port,
		client: &http.Client{Timeout: httpTimeout},
		stop: func(pid string) {
			args := []string{"stop"}
			if pid != "" {
				args = append(args, pid)
			}
			if out, err := exec.Command("agentrove-browser", args...).CombinedOutput(); err != nil {
				log.Printf("agentrove-browser stop: %v: %s", err, strings.TrimSpace(string(out)))
			}
		},
		pid: func() string {
			out, err := exec.Command("agentrove-browser", "status").Output()
			if f := strings.Fields(string(out)); err == nil && len(f) == 2 && f[0] == "running" {
				return f[1]
			}
			return ""
		},
	}
}

func (b *browserCtl) getJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", path, resp.Status)
	}
	if v == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// pages lists the browser's page targets.
func (b *browserCtl) pages(ctx context.Context) ([]string, error) {
	var targets []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := b.getJSON(ctx, "/json/list", &targets); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, t := range targets {
		if t.Type == "page" {
			ids = append(ids, t.ID)
		}
	}
	return ids, nil
}

func (b *browserCtl) listPages() ([]string, error) { return b.pages(context.Background()) }

// closePage closes a page from the browser side, which works even when its
// renderer is hung.
func (b *browserCtl) closePage(id string) {
	if !targetIDPattern.MatchString(id) {
		return
	}
	_ = b.getJSON(context.Background(), "/json/close/"+id, nil)
}

// heal makes the browser fit for a new playwright-mcp: it closes pages that
// don't answer and stops a browser that doesn't answer at all (the next
// `agentrove-browser ensure` starts a fresh one). A browser that isn't
// running is left to ensure.
func (b *browserCtl) heal() {
	pid := b.pid()
	if pid == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()
	if err := b.getJSON(ctx, "/json/version", nil); err != nil {
		log.Printf("the browser isn't answering (%v); restarting it", err)
		b.stop(pid)
		return
	}
	ids, err := b.pages(ctx)
	if err != nil {
		log.Printf("the browser isn't answering (%v); restarting it", err)
		b.stop(pid)
		return
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		if !targetIDPattern.MatchString(id) {
			continue
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), pageProbeTimeout)
			defer cancel()
			if err := probePage(ctx, strings.Replace(b.base, "http://", "ws://", 1)+"/devtools/page/"+id); errors.Is(err, os.ErrDeadlineExceeded) {
				log.Printf("page %s isn't answering; closing it", id)
				b.closePage(id)
			}
		})
	}
	wg.Wait()
}

// probePage evaluates `0` in a page. It fails with os.ErrDeadlineExceeded
// when the page doesn't answer before ctx ends; other errors (the page went
// away meanwhile) aren't a hang.
func probePage(ctx context.Context, wsURL string) error {
	c, err := dialWS(ctx, wsURL)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.WriteText([]byte(`{"id":1,"method":"Runtime.evaluate","params":{"expression":"0"}}`)); err != nil {
		return err
	}
	for {
		msg, err := c.Read()
		if err != nil {
			return err
		}
		var r struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(msg, &r) == nil && r.ID == 1 {
			return nil
		}
	}
}

// wsConn is a websocket client connection: just enough of RFC 6455 for
// DevTools (no extensions; masked text frames out; ping answered).
type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
}

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// dialWS connects to a ws:// URL; ctx's deadline bounds the whole
// connection, reads included.
func dialWS(ctx context.Context, rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "ws" {
		return nil, fmt.Errorf("unsupported websocket URL %q", rawURL)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	key := base64.StdEncoding.EncodeToString(nonce)
	req := "GET " + u.RequestURI() + " HTTP/1.1\r\nHost: " + u.Host +
		"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: " + key +
		"\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket handshake: %s", resp.Status)
	}
	return &wsConn{conn: conn, br: br}, nil
}

func (c *wsConn) Close() error { return c.conn.Close() }

func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	hdr := []byte{0x80 | opcode}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, 0x80|126)
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(n))
	default:
		hdr = append(hdr, 0x80|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	hdr = append(hdr, mask[:]...)
	frame := make([]byte, len(hdr)+len(payload))
	copy(frame, hdr)
	for i, b := range payload {
		frame[len(hdr)+i] = b ^ mask[i%4]
	}
	_, err := c.conn.Write(frame)
	return err
}

func (c *wsConn) WriteText(payload []byte) error { return c.writeFrame(0x1, payload) }

// maxWSMessage caps what Read accepts; DevTools caps its own messages lower.
const maxWSMessage = 256 << 20

// Read returns the next data message.
func (c *wsConn) Read() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(c.br, h[:]); err != nil {
			return nil, err
		}
		fin, opcode := h[0]&0x80 != 0, h[0]&0x0F
		n := uint64(h[1] & 0x7F)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(c.br, b[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(c.br, b[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		var mask [4]byte
		masked := h[1]&0x80 != 0
		if masked {
			if _, err := io.ReadFull(c.br, mask[:]); err != nil {
				return nil, err
			}
		}
		if n > maxWSMessage || uint64(len(msg))+n > maxWSMessage {
			return nil, errors.New("websocket message too large")
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.br, payload); err != nil {
			return nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch opcode {
		case 0x8:
			return nil, io.EOF
		case 0x9:
			if err := c.writeFrame(0xA, payload); err != nil {
				return nil, err
			}
			continue
		case 0xA:
			continue
		}
		msg = append(msg, payload...)
		if fin {
			return msg, nil
		}
	}
}
