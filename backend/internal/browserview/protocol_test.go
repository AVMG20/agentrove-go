package browserview

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"
)

func TestMouseParams(t *testing.T) {
	p, err := mouseParams(clientMsg{Event: "down", X: 10.5, Y: 20, Button: "left", Buttons: 1, ClickCount: 2, Modifiers: 8})
	if err != nil {
		t.Fatal(err)
	}
	if p["type"] != "mousePressed" || p["x"] != 10.5 || p["button"] != "left" || p["clickCount"] != 2 || p["modifiers"] != 8 || p["buttons"] != 1 {
		t.Fatalf("params = %v", p)
	}
	p, _ = mouseParams(clientMsg{Event: "move", X: math.NaN(), Y: 1e9})
	if p["type"] != "mouseMoved" || p["button"] != "none" || p["x"] != 0.0 || p["y"] != 100000.0 {
		t.Fatalf("params = %v", p)
	}
	if _, ok := p["clickCount"]; ok {
		t.Fatal("move with clickCount")
	}
	for _, bad := range []clientMsg{{Event: "click"}, {Event: "down", Button: "thumb"}} {
		if _, err := mouseParams(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestWheelParams(t *testing.T) {
	p := wheelParams(clientMsg{X: 5, Y: 6, DeltaY: 120, DeltaX: -1e9})
	if p["type"] != "mouseWheel" || p["deltaY"] != 120.0 || p["deltaX"] != -10000.0 {
		t.Fatalf("params = %v", p)
	}
}

func TestKeyParams(t *testing.T) {
	cases := []struct {
		msg      clientMsg
		typ      string
		wantText string
	}{
		{clientMsg{Event: "down", Key: "a", Code: "KeyA", KeyCode: 65}, "keyDown", "a"},
		{clientMsg{Event: "down", Key: "A", Code: "KeyA", KeyCode: 65, Modifiers: 8}, "keyDown", "A"},
		{clientMsg{Event: "down", Key: "é", Code: "KeyE", KeyCode: 69}, "keyDown", "é"},
		{clientMsg{Event: "down", Key: "Enter", Code: "Enter", KeyCode: 13}, "keyDown", "\r"},
		// Shortcuts and named keys don't type.
		{clientMsg{Event: "down", Key: "a", Code: "KeyA", KeyCode: 65, Modifiers: modCtrl}, "rawKeyDown", ""},
		{clientMsg{Event: "down", Key: "c", Code: "KeyC", KeyCode: 67, Modifiers: modMeta}, "rawKeyDown", ""},
		{clientMsg{Event: "down", Key: "Backspace", Code: "Backspace", KeyCode: 8}, "rawKeyDown", ""},
		{clientMsg{Event: "down", Key: "ArrowDown", Code: "ArrowDown", KeyCode: 40}, "rawKeyDown", ""},
		{clientMsg{Event: "down", Key: "Tab", Code: "Tab", KeyCode: 9}, "rawKeyDown", ""},
		{clientMsg{Event: "up", Key: "a", Code: "KeyA", KeyCode: 65}, "keyUp", ""},
	}
	for _, c := range cases {
		p, err := keyParams(c.msg)
		if err != nil {
			t.Fatalf("%+v: %v", c.msg, err)
		}
		text, _ := p["text"].(string)
		if p["type"] != c.typ || text != c.wantText {
			t.Errorf("%q mods %d %s: type %v text %q", c.msg.Key, c.msg.Modifiers, c.msg.Event, p["type"], text)
		}
		if p["windowsVirtualKeyCode"] != c.msg.KeyCode || p["code"] != c.msg.Code {
			t.Errorf("%q: %v", c.msg.Key, p)
		}
	}
	for _, bad := range []clientMsg{{Event: "down"}, {Event: "press", Key: "a"}} {
		if _, err := keyParams(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestNavigationURL(t *testing.T) {
	ok := map[string]string{
		"localhost:3000":            "http://localhost:3000",
		"localhost:5173/app?x=1":    "http://localhost:5173/app?x=1",
		"127.0.0.1:8080":            "http://127.0.0.1:8080",
		"[::1]:8000/":               "http://[::1]:8000/",
		"web.localhost":             "http://web.localhost",
		"devbox":                    "http://devbox",
		"example.com":               "https://example.com",
		"example.com:8443/path":     "https://example.com:8443/path",
		"  https://example.com/a  ": "https://example.com/a",
		"http://localhost:3000/":    "http://localhost:3000/",
		"about:blank":               "about:blank",
	}
	for in, want := range ok {
		got, err := navigationURL(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "javascript:alert(1)", "file:///etc/passwd", "chrome://settings", "data:text/html,x", "http://"} {
		if got, err := navigationURL(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestClampViewportAndScreencastSize(t *testing.T) {
	if w, h, ok := clampViewport(100, 99999); !ok || w != 320 || h != 2160 {
		t.Fatalf("clampViewport = %d %d %v", w, h, ok)
	}
	if _, _, ok := clampViewport(0, 600); ok {
		t.Fatal("accepted a zero width")
	}
	if w, h := screencastSize(0, 0); w != defaultScreencastWidth || h != defaultScreencastHeight {
		t.Fatalf("default = %d %d", w, h)
	}
	if w, h := screencastSize(5000, 50); w != maxScreencastSide || h != 120 {
		t.Fatalf("clamped = %d %d", w, h)
	}
}

func TestEncodeFrame(t *testing.T) {
	b := encodeFrame(frameHeader{Seq: 3, Width: 1280, Height: 800}, []byte("JPEG"))
	n := binary.BigEndian.Uint32(b)
	var h frameHeader
	if err := json.Unmarshal(b[4:4+n], &h); err != nil || h.Seq != 3 || h.Width != 1280 {
		t.Fatalf("header %s: %v", b[4:4+n], err)
	}
	if string(b[4+n:]) != "JPEG" {
		t.Fatalf("payload %q", b[4+n:])
	}
}
