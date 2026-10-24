package browserview

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type viewerRun struct {
	f    *fakeBrowser
	sink *fakeSink
	in   chan clientMsg
	done chan error
}

func startViewer(t *testing.T, f *fakeBrowser) *viewerRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn := NewConn(f)
	r := &viewerRun{f: f, sink: &fakeSink{}, in: make(chan clientMsg, 16), done: make(chan error, 1)}
	v := newViewer(conn, r.sink, nil, nil)
	go func() { r.done <- v.run(ctx, r.in) }()
	t.Cleanup(func() {
		cancel()
		conn.Close()
		<-r.done
	})
	return r
}

func (r *viewerRun) activeSession(t *testing.T) string {
	t.Helper()
	r.f.waitCall(t, "Page.startScreencast", 0)
	return r.f.waitCall(t, "Page.startScreencast", r.f.count("Page.startScreencast")-1).SessionID
}

func TestViewerAttachesAndStreams(t *testing.T) {
	f := newFakeBrowser("about:blank", "http://localhost:3000/")
	r := startViewer(t, f)

	// The tab with something loaded is shown first.
	attach := f.waitCall(t, "Target.attachToTarget", 0)
	if params(attach)["targetId"] != "T2" || params(attach)["flatten"] != true {
		t.Fatalf("attach = %v", params(attach))
	}
	cast := f.waitCall(t, "Page.startScreencast", 0)
	if p := params(cast); p["format"] != "jpeg" || p["maxWidth"] != float64(defaultScreencastWidth) {
		t.Fatalf("screencast = %v", p)
	}
	tabs := r.sink.waitText(t, "tabs", func(m map[string]any) bool { return m["active"] == "T2" })
	if n := len(tabs["tabs"].([]any)); n != 2 {
		t.Fatalf("tabs = %v (service workers aren't tabs)", tabs)
	}
	r.sink.waitText(t, "nav", func(m map[string]any) bool { return m["canGoBack"] == true && m["canGoForward"] == true })

	sid := cast.SessionID
	f.frame(sid, 1)
	waitUntil(t, "frame 1", func() bool { return r.sink.frameCount() == 1 })
	b := r.sink.frames[0]
	n := binary.BigEndian.Uint32(b)
	if string(b[4+n:]) != "jpeg-1" {
		t.Fatalf("payload %q", b[4+n:])
	}
	ack := f.waitCall(t, "Page.screencastFrameAck", 0)
	if params(ack)["sessionId"] != 1.0 || ack.SessionID != sid {
		t.Fatalf("ack = %v", ack)
	}
}

func TestViewerFlowControl(t *testing.T) {
	f := newFakeBrowser("http://localhost:3000/")
	r := startViewer(t, f)
	sid := r.activeSession(t)

	// Chromium sends a frame per ack. With two frames unacked by the client,
	// it gets no ack for the second.
	f.frame(sid, 1)
	waitUntil(t, "first ack", func() bool { return f.count("Page.screencastFrameAck") == 1 })
	f.frame(sid, 2)
	waitUntil(t, "frame 2", func() bool { return r.sink.frameCount() == 2 })
	time.Sleep(3 * minFrameInterval)
	if n := f.count("Page.screencastFrameAck"); n != 1 {
		t.Fatalf("acked %d frames with the client two behind", n)
	}
	r.in <- clientMsg{Type: "ack", Seq: 1}
	waitUntil(t, "second ack", func() bool { return f.count("Page.screencastFrameAck") == 2 })
	if p := params(f.waitCall(t, "Page.screencastFrameAck", 1)); p["sessionId"] != 2.0 {
		t.Fatalf("ack = %v", p)
	}
	// A stale or future seq changes nothing.
	r.in <- clientMsg{Type: "ack", Seq: 99}
	f.frame(sid, 3)
	waitUntil(t, "frame 3", func() bool { return r.sink.frameCount() == 3 })
	time.Sleep(3 * minFrameInterval)
	if n := f.count("Page.screencastFrameAck"); n != 2 {
		t.Fatalf("acked %d frames", n)
	}
}

func TestViewerInput(t *testing.T) {
	f := newFakeBrowser("http://localhost:3000/")
	r := startViewer(t, f)
	sid := r.activeSession(t)

	r.in <- clientMsg{Type: "mouse", Event: "down", X: 100, Y: 50, Button: "left", Buttons: 1, ClickCount: 1}
	r.in <- clientMsg{Type: "mouse", Event: "up", X: 100, Y: 50, Button: "left", ClickCount: 1}
	r.in <- clientMsg{Type: "key", Event: "down", Key: "x", Code: "KeyX", KeyCode: 88}
	r.in <- clientMsg{Type: "text", Text: "pasted"}
	r.in <- clientMsg{Type: "wheel", X: 1, Y: 2, DeltaY: 300}

	down := f.waitCall(t, "Input.dispatchMouseEvent", 0)
	up := f.waitCall(t, "Input.dispatchMouseEvent", 1)
	if params(down)["type"] != "mousePressed" || params(up)["type"] != "mouseReleased" || down.SessionID != sid {
		t.Fatalf("mouse = %v %v", params(down), params(up))
	}
	if p := params(f.waitCall(t, "Input.dispatchKeyEvent", 0)); p["type"] != "keyDown" || p["text"] != "x" {
		t.Fatalf("key = %v", p)
	}
	if p := params(f.waitCall(t, "Input.insertText", 0)); p["text"] != "pasted" {
		t.Fatalf("text = %v", p)
	}
	if p := params(f.waitCall(t, "Input.dispatchMouseEvent", 2)); p["type"] != "mouseWheel" || p["deltaY"] != 300.0 {
		t.Fatalf("wheel = %v", p)
	}
}

func TestViewerNavigation(t *testing.T) {
	f := newFakeBrowser("http://localhost:3000/")
	r := startViewer(t, f)
	r.activeSession(t)

	r.in <- clientMsg{Type: "navigate", URL: "localhost:5173"}
	if p := params(f.waitCall(t, "Page.navigate", 0)); p["url"] != "http://localhost:5173" {
		t.Fatalf("navigate = %v", p)
	}
	r.in <- clientMsg{Type: "navigate", URL: "file:///etc/passwd"}
	r.sink.waitText(t, "error", nil)

	// History moves from an isolated world the page can't tamper with.
	evals := f.count("Runtime.evaluate")
	r.in <- clientMsg{Type: "back"}
	if p := params(f.waitCall(t, "Runtime.evaluate", evals)); p["expression"] != "history.back()" || p["contextId"] != 1.0 {
		t.Fatalf("back = %v", p)
	}
	r.in <- clientMsg{Type: "forward"}
	if p := params(f.waitCall(t, "Runtime.evaluate", evals+1)); p["expression"] != "history.forward()" {
		t.Fatalf("forward = %v", p)
	}
	r.in <- clientMsg{Type: "reload"}
	f.waitCall(t, "Page.reload", 0)

	r.in <- clientMsg{Type: "viewport", Width: 390, Height: 844}
	set := f.waitCall(t, "Browser.setWindowBounds", 0)
	if b := params(set)["bounds"].(map[string]any); b["width"] != 390.0 || params(set)["windowId"] != 7.0 {
		t.Fatalf("bounds = %v", params(set))
	}
}

func TestViewerFollowsTabs(t *testing.T) {
	f := newFakeBrowser("http://localhost:3000/")
	r := startViewer(t, f)
	r.activeSession(t)

	// The agent opens a tab: the viewer follows it.
	f.mu.Lock()
	f.targets = append(f.targets, targetInfo{TargetID: "T9", Type: "page", URL: "http://localhost:3000/new"})
	f.mu.Unlock()
	f.emit("", "Target.targetCreated", map[string]any{"targetInfo": targetInfo{TargetID: "T9", Type: "page", URL: "http://localhost:3000/new"}})
	r.sink.waitText(t, "tabs", func(m map[string]any) bool { return m["active"] == "T9" })
	old := f.waitCall(t, "Page.stopScreencast", 0)

	// Title changes reach the tab strip.
	f.emit("", "Target.targetInfoChanged", map[string]any{"targetInfo": targetInfo{TargetID: "T9", Type: "page", URL: "http://localhost:3000/new", Title: "New"}})
	r.sink.waitText(t, "tabs", func(m map[string]any) bool {
		tabs := m["tabs"].([]any)
		return len(tabs) == 2 && tabs[1].(map[string]any)["title"] == "New"
	})

	// Back to the first tab by hand.
	r.in <- clientMsg{Type: "tab.switch", ID: "T1"}
	r.sink.waitText(t, "tabs", func(m map[string]any) bool { return m["active"] == "T1" })
	if old.SessionID == "" {
		t.Fatal("stopped the screencast of no session")
	}
	f.waitCall(t, "Target.activateTarget", 0)

	// Closing the active tab shows the remaining one.
	r.in <- clientMsg{Type: "tab.close", ID: "T1"}
	r.sink.waitText(t, "tabs", func(m map[string]any) bool { return m["active"] == "T9" && len(m["tabs"].([]any)) == 1 })

	// The last one too: an empty tab list.
	r.in <- clientMsg{Type: "tab.close", ID: "T9"}
	r.sink.waitText(t, "tabs", func(m map[string]any) bool { return m["active"] == "" && len(m["tabs"].([]any)) == 0 })

	r.in <- clientMsg{Type: "tab.new"}
	r.sink.waitText(t, "tabs", func(m map[string]any) bool { return m["active"] != "" && len(m["tabs"].([]any)) == 1 })
}

func TestViewerOpensATabWhenNoneExist(t *testing.T) {
	f := newFakeBrowser()
	r := startViewer(t, f)
	if p := params(f.waitCall(t, "Target.createTarget", 0)); p["url"] != "about:blank" {
		t.Fatalf("create = %v", p)
	}
	r.sink.waitText(t, "tabs", func(m map[string]any) bool { return m["active"] != "" })
	f.waitCall(t, "Page.startScreencast", 0)
}

func TestViewerRetriesScreencast(t *testing.T) {
	f := newFakeBrowser("http://localhost:3000/")
	f.failScreencast = 2
	r := startViewer(t, f)
	f.waitCall(t, "Page.startScreencast", 2)
	sid := r.activeSession(t)
	f.frame(sid, 1)
	waitUntil(t, "frame", func() bool { return r.sink.frameCount() == 1 })
}

func TestViewerScreenSizeRestartsScreencast(t *testing.T) {
	f := newFakeBrowser("http://localhost:3000/")
	r := startViewer(t, f)
	r.activeSession(t)
	r.in <- clientMsg{Type: "screen", Width: 800, Height: 500}
	f.waitCall(t, "Page.stopScreencast", 0)
	if p := params(f.waitCall(t, "Page.startScreencast", 1)); p["maxWidth"] != 800.0 || p["maxHeight"] != 500.0 {
		t.Fatalf("screencast = %v", p)
	}
}

func TestViewerDialogBypassesStuckInput(t *testing.T) {
	f := newFakeBrowser("http://localhost:3000/")
	r := startViewer(t, f)
	sid := r.activeSession(t)
	// The click that opened an alert doesn't return until it's handled.
	f.mu.Lock()
	f.hold["Input.dispatchMouseEvent"] = true
	f.mu.Unlock()
	r.in <- clientMsg{Type: "mouse", Event: "up", X: 1, Y: 1, Button: "left"}
	f.emit(sid, "Page.javascriptDialogOpening", map[string]any{"type": "confirm", "message": "Sure?"})
	if m := r.sink.waitText(t, "dialog", nil); m["open"] != true || m["message"] != "Sure?" {
		t.Fatalf("dialog = %v", m)
	}
	r.in <- clientMsg{Type: "dialog", Accept: true}
	if p := params(f.waitCall(t, "Page.handleJavaScriptDialog", 0)); p["accept"] != true {
		t.Fatalf("handle = %v", p)
	}
}

func TestViewerResetAndBrowserGone(t *testing.T) {
	f := newFakeBrowser("http://localhost:3000/")
	r := startViewer(t, f)
	r.activeSession(t)
	r.in <- clientMsg{Type: "reset"}
	if err := <-r.done; !errors.Is(err, errReset) {
		t.Fatalf("err = %v", err)
	}
	r.done <- nil // for cleanup

	f2 := newFakeBrowser("http://localhost:3000/")
	r2 := startViewer(t, f2)
	r2.activeSession(t)
	_ = f2.Close()
	if err := <-r2.done; !errors.Is(err, errBrowserGone) {
		t.Fatalf("err = %v", err)
	}
	r2.done <- nil
}

func TestViewerClipsPageStrings(t *testing.T) {
	f := newFakeBrowser("http://localhost:3000/")
	r := startViewer(t, f)
	sid := r.activeSession(t)
	big := strings.Repeat("é", 1<<20)
	f.emit("", "Target.targetInfoChanged", map[string]any{"targetInfo": targetInfo{TargetID: "T1", Type: "page", URL: "http://x/?" + big, Title: big}})
	tabs := r.sink.waitText(t, "tabs", func(m map[string]any) bool {
		return strings.HasPrefix(m["tabs"].([]any)[0].(map[string]any)["url"].(string), "http://x/?")
	})
	tb := tabs["tabs"].([]any)[0].(map[string]any)
	if url, title := tb["url"].(string), tb["title"].(string); len(url) > maxTabField || len(title) > maxTabField || !utf8.ValidString(url) || !utf8.ValidString(title) {
		t.Fatalf("tab not clipped: url %d bytes, title %d bytes", len(url), len(title))
	}
	f.emit(sid, "Page.javascriptDialogOpening", map[string]any{"type": "prompt", "message": big, "defaultPrompt": big})
	d := r.sink.waitText(t, "dialog", nil)
	if len(d["message"].(string)) > maxDialogMessage || len(d["defaultPrompt"].(string)) > maxTabField {
		t.Fatalf("dialog not clipped: %d / %d bytes", len(d["message"].(string)), len(d["defaultPrompt"].(string)))
	}
}

func TestViewerCapsTabsItOpens(t *testing.T) {
	f := newFakeBrowser("http://localhost:3000/")
	r := startViewer(t, f)
	r.activeSession(t)
	for range maxViewerTabs + 5 {
		r.in <- clientMsg{Type: "tab.new"}
	}
	r.sink.waitText(t, "error", func(m map[string]any) bool { return m["message"] == "Too many tabs open" })
	time.Sleep(200 * time.Millisecond)
	if n := f.count("Target.createTarget"); n > maxViewerTabs {
		t.Fatalf("viewer opened %d tabs", n)
	}
}

// No reply the viewer asks for may grow with page content: a page can
// pushState any number of 2MB URLs, which made getNavigationHistory (and
// getTargets with many tabs) exceed cdpReadLimit and end the connection.
func TestViewerAvoidsUnboundedReplies(t *testing.T) {
	f := newFakeBrowser("http://localhost:3000/", "about:blank")
	r := startViewer(t, f)
	r.activeSession(t)
	r.sink.waitText(t, "nav", nil)
	// Titles still update by polling, without document.title events.
	f.mu.Lock()
	f.targets[0].Title = "Renamed"
	f.mu.Unlock()
	r.sink.waitText(t, "tabs", func(m map[string]any) bool {
		return m["tabs"].([]any)[0].(map[string]any)["title"] == "Renamed"
	})
	r.in <- clientMsg{Type: "back"}
	f.waitCall(t, "Runtime.evaluate", 1)
	for _, m := range []string{"Target.getTargets", "Page.getNavigationHistory", "Page.navigateToHistoryEntry"} {
		if n := f.count(m); n > 0 {
			t.Errorf("viewer called %s %d times", m, n)
		}
	}
}

func TestInitialPageAvoidsBrokenTabs(t *testing.T) {
	pages := []tab{{ID: "A", URL: "http://a/"}, {ID: "B", URL: "about:blank"}, {ID: "C", URL: "http://c/"}}
	if got := initialPage(pages, nil); got != "C" {
		t.Fatalf("got %s", got)
	}
	if got := initialPage(pages, map[string]bool{"C": true}); got != "A" {
		t.Fatalf("got %s", got)
	}
	if got := initialPage(pages, map[string]bool{"A": true, "C": true}); got != "B" {
		t.Fatalf("got %s", got)
	}
	if got := initialPage(pages, map[string]bool{"A": true, "B": true, "C": true}); got != "" {
		t.Fatalf("got %s", got)
	}
}
