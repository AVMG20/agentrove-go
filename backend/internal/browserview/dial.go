package browserview

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// cdpReadLimit fits a full-size screencast frame (base64 JPEG) with room.
const cdpReadLimit = 32 << 20

// DialCDP does the websocket client handshake for the browser endpoint path
// (from /json/version, e.g. /devtools/browser/<id>) over stream, a byte
// stream already connected to the debugging port.
func DialCDP(ctx context.Context, stream io.ReadWriteCloser, path string) (Transport, error) {
	conn := &streamConn{rw: stream}
	used := false
	var mu sync.Mutex
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			mu.Lock()
			defer mu.Unlock()
			if used {
				return nil, io.ErrClosedPipe
			}
			used = true
			return conn, nil
		},
		DisableKeepAlives: true,
	}}
	// Chromium only accepts a loopback or IP Host header.
	ws, _, err := websocket.Dial(ctx, "ws://127.0.0.1:9222"+path, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	ws.SetReadLimit(cdpReadLimit)
	return &wsTransport{ws: ws}, nil
}

type wsTransport struct{ ws *websocket.Conn }

func (t *wsTransport) ReadMessage(ctx context.Context) ([]byte, error) {
	_, b, err := t.ws.Read(ctx)
	return b, err
}

func (t *wsTransport) WriteMessage(ctx context.Context, msg []byte) error {
	return t.ws.Write(ctx, websocket.MessageText, msg)
}

func (t *wsTransport) Close() error { return t.ws.CloseNow() }

// streamConn adapts an exec stream to net.Conn for the HTTP client. It has
// no deadlines; contexts bound every use instead.
type streamConn struct {
	rw   io.ReadWriteCloser
	once sync.Once
}

func (c *streamConn) Read(b []byte) (int, error)  { return c.rw.Read(b) }
func (c *streamConn) Write(b []byte) (int, error) { return c.rw.Write(b) }
func (c *streamConn) Close() error {
	var err error
	c.once.Do(func() { err = c.rw.Close() })
	return err
}
func (c *streamConn) LocalAddr() net.Addr              { return execAddr{} }
func (c *streamConn) RemoteAddr() net.Addr             { return execAddr{} }
func (c *streamConn) SetDeadline(time.Time) error      { return nil }
func (c *streamConn) SetReadDeadline(time.Time) error  { return nil }
func (c *streamConn) SetWriteDeadline(time.Time) error { return nil }

type execAddr struct{}

func (execAddr) Network() string { return "docker-exec" }
func (execAddr) String() string  { return "box:cdp" }
