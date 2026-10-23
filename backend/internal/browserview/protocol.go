package browserview

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Viewer protocol. After the auth frame, the client sends JSON text frames
// (clientMsg); the server sends JSON text frames (status, tabs, nav,
// loading, dialog, error, ping) and binary screencast frames: a 4-byte
// big-endian header length, a JSON frameHeader, then the JPEG. The client
// acks each frame by seq once drawn; at most maxInflight frames are unacked.

type clientMsg struct {
	Type string `json:"type"`

	// ack
	Seq int64 `json:"seq"`

	// mouse, wheel, key: x/y are CSS pixels in the page viewport.
	Event      string  `json:"event"` // mouse: down|up|move; key: down|up
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Button     string  `json:"button"`
	Buttons    int     `json:"buttons"`
	ClickCount int     `json:"clickCount"`
	DeltaX     float64 `json:"deltaX"`
	DeltaY     float64 `json:"deltaY"`
	Modifiers  int     `json:"modifiers"` // Alt=1 Ctrl=2 Meta=4 Shift=8 (CDP's)
	Key        string  `json:"key"`
	Code       string  `json:"code"`
	KeyCode    int     `json:"keyCode"`
	Location   int     `json:"location"`
	Repeat     bool    `json:"repeat"`

	// text, navigate, tab.new
	Text string `json:"text"`
	URL  string `json:"url"`

	// tab.switch, tab.close
	ID string `json:"id"`

	// viewport (CSS pixels) and screen (device pixels the client draws in)
	Width  int `json:"width"`
	Height int `json:"height"`

	// dialog
	Accept     bool   `json:"accept"`
	PromptText string `json:"promptText"`
}

const (
	modAlt  = 1
	modCtrl = 2
	modMeta = 4

	maxTextInput = 64 << 10
)

var errBadInput = errors.New("invalid input")

var mouseTypes = map[string]string{"down": "mousePressed", "up": "mouseReleased", "move": "mouseMoved"}
var mouseButtons = map[string]bool{"none": true, "left": true, "middle": true, "right": true, "back": true, "forward": true}

// clampCoord keeps pointer coordinates in a sane range (CDP rejects NaN and
// a client may send anything).
func clampCoord(v float64) float64 {
	if v != v { // NaN
		return 0
	}
	return max(-10000, min(v, 100000))
}

// mouseParams translates a mouse message to Input.dispatchMouseEvent.
func mouseParams(m clientMsg) (map[string]any, error) {
	typ, ok := mouseTypes[m.Event]
	if !ok {
		return nil, errBadInput
	}
	button := m.Button
	if button == "" {
		button = "none"
	}
	if !mouseButtons[button] {
		return nil, errBadInput
	}
	p := map[string]any{
		"type":      typ,
		"x":         clampCoord(m.X),
		"y":         clampCoord(m.Y),
		"button":    button,
		"buttons":   m.Buttons & 31,
		"modifiers": m.Modifiers & 15,
	}
	if typ != "mouseMoved" {
		p["clickCount"] = max(1, min(m.ClickCount, 3))
	}
	return p, nil
}

// wheelParams translates a wheel message (deltas in CSS pixels).
func wheelParams(m clientMsg) map[string]any {
	clampDelta := func(v float64) float64 {
		if v != v {
			return 0
		}
		return max(-10000, min(v, 10000))
	}
	return map[string]any{
		"type":      "mouseWheel",
		"x":         clampCoord(m.X),
		"y":         clampCoord(m.Y),
		"deltaX":    clampDelta(m.DeltaX),
		"deltaY":    clampDelta(m.DeltaY),
		"modifiers": m.Modifiers & 15,
	}
}

// keyText is the text a key inserts: printable keys without Ctrl/Meta (those
// are shortcuts), and Enter. Like Puppeteer, keys with text are sent as
// keyDown and the rest as rawKeyDown, so shortcuts and navigation keys don't
// type anything.
func keyText(key string, modifiers int) string {
	if key == "Enter" {
		return "\r"
	}
	if modifiers&(modCtrl|modMeta) != 0 {
		return ""
	}
	if utf8.RuneCountInString(key) == 1 {
		return key
	}
	return ""
}

// keyParams translates a key message to Input.dispatchKeyEvent.
func keyParams(m clientMsg) (map[string]any, error) {
	if m.Key == "" || len(m.Key) > 64 || len(m.Code) > 64 {
		return nil, errBadInput
	}
	p := map[string]any{
		"key":                   m.Key,
		"code":                  m.Code,
		"windowsVirtualKeyCode": max(0, min(m.KeyCode, 255)),
		"nativeVirtualKeyCode":  max(0, min(m.KeyCode, 255)),
		"modifiers":             m.Modifiers & 15,
		"location":              max(0, min(m.Location, 3)),
		"autoRepeat":            m.Repeat,
	}
	switch m.Event {
	case "down":
		if text := keyText(m.Key, m.Modifiers); text != "" {
			p["type"] = "keyDown"
			p["text"] = text
			p["unmodifiedText"] = text
		} else {
			p["type"] = "rawKeyDown"
		}
	case "up":
		p["type"] = "keyUp"
	default:
		return nil, errBadInput
	}
	return p, nil
}

// navigationURL turns what the user typed in the URL bar into a URL. Bare
// hosts get http:// when local (the usual dev server) and https:// otherwise.
// Only web URLs and about:blank are allowed.
func navigationURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 8192 {
		return "", errBadInput
	}
	if s == "about:blank" {
		return s, nil
	}
	if !strings.Contains(s, "://") {
		host := strings.ToLower(s)
		if i := strings.IndexAny(host, "/?#"); i >= 0 {
			host = host[:i]
		}
		h := host
		if i := strings.LastIndex(h, ":"); i >= 0 && !strings.HasSuffix(h, "]") {
			h = h[:i]
		}
		if h == "localhost" || strings.HasSuffix(h, ".localhost") || h == "0.0.0.0" || strings.HasPrefix(h, "127.") || h == "[::1]" || !strings.Contains(h, ".") {
			s = "http://" + s
		} else {
			s = "https://" + s
		}
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errBadInput
	}
	return u.String(), nil
}

// clampViewport bounds a requested window size (CSS pixels).
func clampViewport(w, h int) (int, int, bool) {
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return max(320, min(w, 3840)), max(240, min(h, 2160)), true
}

// screencastSize is the maxWidth/maxHeight to ask for, from the client's
// drawing size in device pixels.
func screencastSize(w, h int) (int, int) {
	if w <= 0 || h <= 0 {
		return defaultScreencastWidth, defaultScreencastHeight
	}
	return max(160, min(w, maxScreencastSide)), max(120, min(h, maxScreencastSide))
}

// frameHeader describes a screencast frame: the page viewport it shows (CSS
// pixels), for mapping pointer positions, and the seq to ack.
type frameHeader struct {
	Seq    int64   `json:"seq"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func encodeFrame(h frameHeader, jpeg []byte) []byte {
	head, _ := json.Marshal(h)
	out := make([]byte, 4+len(head)+len(jpeg))
	binary.BigEndian.PutUint32(out, uint32(len(head)))
	copy(out[4:], head)
	copy(out[4+len(head):], jpeg)
	return out
}

// tab is a page target as the client sees it.
type tab struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}
