package browserview

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"time"
)

const (
	// DebugPort is the box browser's CDP port (agentrove-browser).
	DebugPort = 9222

	// Frames the client may hold unacked; more would only queue up behind a
	// slow link.
	maxInflight = 2
	// Frame rate cap: Chromium sends the next frame only after an ack.
	minFrameInterval = time.Second / 15
	jpegQuality      = 70

	defaultScreencastWidth  = 1280
	defaultScreencastHeight = 800
	maxScreencastSide       = 1920

	callTimeout       = 10 * time.Second
	navigationTimeout = 60 * time.Second
	// Page.startScreencast fails while a navigation commits; retry briefly.
	screencastRetry    = 250 * time.Millisecond
	screencastAttempts = 40
	titlePollInterval  = time.Second
)

var (
	errReset       = errors.New("browser reset requested")
	errBrowserGone = errors.New("browser connection closed")
)

// sink is where the viewer sends client messages; it never blocks.
type sink interface {
	Text(v any)
	// Frame replaces any frame not yet written: a stale frame is useless.
	Frame(b []byte)
}

// viewer drives one client's view of the browser over its own CDP
// connection. All state is owned by run's goroutine; input and navigation
// calls go through an ordered worker so a page stuck in a dialog can't stall
// the event loop.
type viewer struct {
	cdp   *Conn
	out   sink
	touch func()

	pages         []tab
	active        string // target id
	session       string // flattened session of active
	screencasting bool
	castAttempts  int
	screenW       int
	screenH       int

	seq        int64 // last frame sent to the client
	acked      int64 // last frame the client drew
	pendingAck *int  // Chromium's frame session id, ack deferred
	lastAck    time.Time

	ackTimer  *time.Timer
	castTimer *time.Timer
	work      chan func()
	results   chan func()
}

func newViewer(cdp *Conn, out sink, touch func()) *viewer {
	if touch == nil {
		touch = func() {}
	}
	v := &viewer{cdp: cdp, out: out, touch: touch, work: make(chan func(), 256), results: make(chan func(), 64)}
	v.ackTimer = time.NewTimer(time.Hour)
	v.ackTimer.Stop()
	v.castTimer = time.NewTimer(time.Hour)
	v.castTimer.Stop()
	return v
}

func (v *viewer) call(ctx context.Context, timeout time.Duration, session, method string, params any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return v.cdp.Call(ctx, session, method, params)
}

// enqueue runs f on the ordered worker; when it's backed up (a hung page)
// the input is dropped rather than blocking the loop.
func (v *viewer) enqueue(f func()) {
	select {
	case v.work <- f:
	default:
	}
}

// post hands a result back to the loop goroutine.
func (v *viewer) post(f func()) {
	select {
	case v.results <- f:
	default:
	}
}

// run serves until the client leaves (nil), the browser goes away
// (errBrowserGone) or the client asks for a reset (errReset).
func (v *viewer) run(ctx context.Context, in <-chan clientMsg) error {
	defer v.ackTimer.Stop()
	defer v.castTimer.Stop()
	go func() {
		for f := range v.work {
			f()
		}
	}()
	defer close(v.work)

	if err := v.start(ctx); err != nil {
		return err
	}
	poll := time.NewTicker(titlePollInterval)
	defer poll.Stop()
	for {
		select {
		case <-poll.C:
			v.refreshTitles(ctx)
		case <-ctx.Done():
			return ctx.Err()
		case <-v.cdp.Done():
			return errBrowserGone
		case <-v.cdp.Notify():
			for _, ev := range v.cdp.Events() {
				v.onEvent(ctx, ev)
			}
		case m, ok := <-in:
			if !ok {
				return nil
			}
			if err := v.onClient(ctx, m); err != nil {
				return err
			}
		case <-v.ackTimer.C:
			v.maybeAck(ctx)
		case <-v.castTimer.C:
			v.startScreencast(ctx)
		case f := <-v.results:
			f()
		}
	}
}

func (v *viewer) start(ctx context.Context) error {
	raw, err := v.call(ctx, callTimeout, "", "Target.getTargets", nil)
	if err != nil {
		return errBrowserGone
	}
	var res struct {
		TargetInfos []targetInfo `json:"targetInfos"`
	}
	_ = json.Unmarshal(raw, &res)
	for _, t := range res.TargetInfos {
		v.addPage(t)
	}
	// Created, changed and destroyed targets arrive as events from here on.
	if _, err := v.call(ctx, callTimeout, "", "Target.setDiscoverTargets", map[string]any{"discover": true}); err != nil {
		return errBrowserGone
	}
	v.out.Text(map[string]any{"type": "status", "state": "ready"})
	if id := initialPage(v.pages); id != "" {
		v.attach(ctx, id)
		return nil
	}
	// Nothing open (the agent closed every tab): give the viewer one, which
	// targetCreated then attaches to.
	if _, err := v.call(ctx, callTimeout, "", "Target.createTarget", map[string]any{"url": "about:blank"}); err != nil {
		return errBrowserGone
	}
	v.sendTabs()
	return nil
}

// initialPage picks the tab to show first: the last one with something
// loaded (agents open new tabs for new work), else the last one.
func initialPage(pages []tab) string {
	for i := len(pages) - 1; i >= 0; i-- {
		if pages[i].URL != "about:blank" && pages[i].URL != "" {
			return pages[i].ID
		}
	}
	if len(pages) > 0 {
		return pages[len(pages)-1].ID
	}
	return ""
}

type targetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

func (v *viewer) pageIndex(id string) int {
	return slices.IndexFunc(v.pages, func(t tab) bool { return t.ID == id })
}

// addPage records a page target and reports whether it is new.
func (v *viewer) addPage(t targetInfo) bool {
	if t.Type != "page" || t.TargetID == "" || v.pageIndex(t.TargetID) >= 0 {
		return false
	}
	v.pages = append(v.pages, tab{ID: t.TargetID, Title: t.Title, URL: t.URL})
	return true
}

func (v *viewer) sendTabs() {
	tabs := v.pages
	if tabs == nil {
		tabs = []tab{}
	}
	v.out.Text(map[string]any{"type": "tabs", "tabs": tabs, "active": v.active})
}

func (v *viewer) attach(ctx context.Context, id string) {
	v.detach()
	raw, err := v.call(ctx, callTimeout, "", "Target.attachToTarget", map[string]any{"targetId": id, "flatten": true})
	if err != nil {
		slog.Debug("browser viewer attach", "target", id, "err", err)
		v.sendTabs()
		return
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(raw, &res)
	v.active, v.session = id, res.SessionID
	v.castAttempts = 0
	_, _ = v.call(ctx, callTimeout, v.session, "Page.enable", nil)
	v.startScreencast(ctx)
	v.refreshNav(ctx)
	v.sendTabs()
}

// detach stops watching the active page (in the background: the page may be
// gone already).
func (v *viewer) detach() {
	sid := v.session
	v.session, v.screencasting, v.pendingAck = "", false, nil
	v.castTimer.Stop()
	if sid == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		_, _ = v.cdp.Call(ctx, sid, "Page.stopScreencast", nil)
		_, _ = v.cdp.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": sid})
	}()
}

func (v *viewer) startScreencast(ctx context.Context) {
	if v.session == "" || v.screencasting {
		return
	}
	w, h := screencastSize(v.screenW, v.screenH)
	_, err := v.call(ctx, callTimeout, v.session, "Page.startScreencast", map[string]any{
		"format": "jpeg", "quality": jpegQuality, "maxWidth": w, "maxHeight": h, "everyNthFrame": 1,
	})
	if err == nil {
		v.screencasting = true
		return
	}
	if v.castAttempts++; v.castAttempts < screencastAttempts {
		v.castTimer.Reset(screencastRetry)
		return
	}
	slog.Debug("browser viewer screencast", "target", v.active, "err", err)
	v.out.Text(map[string]any{"type": "error", "message": "Could not show this page"})
}

func (v *viewer) restartScreencast(ctx context.Context) {
	if v.session == "" {
		return
	}
	if v.screencasting {
		_, _ = v.call(ctx, callTimeout, v.session, "Page.stopScreencast", nil)
		v.screencasting, v.pendingAck = false, nil
	}
	v.castAttempts = 0
	v.startScreencast(ctx)
}

// maybeAck lets Chromium send the next frame once the client has room and
// the frame interval has passed.
func (v *viewer) maybeAck(ctx context.Context) {
	if v.pendingAck == nil || v.seq-v.acked >= maxInflight {
		return
	}
	if wait := minFrameInterval - time.Since(v.lastAck); wait > 0 {
		v.ackTimer.Reset(wait)
		return
	}
	frameSession, sid := *v.pendingAck, v.session
	v.pendingAck = nil
	v.lastAck = time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), callTimeout)
		defer cancel()
		_, _ = v.cdp.Call(ctx, sid, "Page.screencastFrameAck", map[string]any{"sessionId": frameSession})
	}()
}

func (v *viewer) refreshNav(ctx context.Context) {
	sid := v.session
	if sid == "" {
		return
	}
	go func() {
		raw, err := v.call(context.WithoutCancel(ctx), callTimeout, sid, "Page.getNavigationHistory", nil)
		if err != nil {
			return
		}
		var h navHistory
		_ = json.Unmarshal(raw, &h)
		v.post(func() {
			if v.session == sid {
				v.out.Text(map[string]any{"type": "nav", "canGoBack": h.CurrentIndex > 0, "canGoForward": h.CurrentIndex < len(h.Entries)-1})
			}
		})
	}()
}

// refreshTitles re-reads every tab's title and URL. Headless Chromium sends
// targetInfoChanged for navigations, but not for document.title changes, and
// mid-navigation it reports the host as the title, so the tab strip polls.
func (v *viewer) refreshTitles(ctx context.Context) {
	go func() {
		raw, err := v.call(context.WithoutCancel(ctx), callTimeout, "", "Target.getTargets", nil)
		if err != nil {
			return
		}
		var res struct {
			TargetInfos []targetInfo `json:"targetInfos"`
		}
		_ = json.Unmarshal(raw, &res)
		v.post(func() {
			changed := false
			for _, t := range res.TargetInfos {
				changed = v.updatePage(t) || changed
			}
			if changed {
				v.sendTabs()
			}
		})
	}()
}

// updatePage applies a target's title and URL to its tab and reports
// whether that changed it.
func (v *viewer) updatePage(t targetInfo) bool {
	i := v.pageIndex(t.TargetID)
	if i < 0 || (v.pages[i].Title == t.Title && v.pages[i].URL == t.URL) {
		return false
	}
	v.pages[i].Title, v.pages[i].URL = t.Title, t.URL
	return true
}

type navHistory struct {
	CurrentIndex int `json:"currentIndex"`
	Entries      []struct {
		ID int `json:"id"`
	} `json:"entries"`
}

func (v *viewer) onEvent(ctx context.Context, ev Event) {
	switch ev.Method {
	case "Target.targetCreated":
		var p struct {
			TargetInfo targetInfo `json:"targetInfo"`
		}
		_ = json.Unmarshal(ev.Params, &p)
		if v.addPage(p.TargetInfo) {
			// Follow new tabs: the agent opened one, or the viewer did.
			v.attach(ctx, p.TargetInfo.TargetID)
		}
	case "Target.targetInfoChanged":
		var p struct {
			TargetInfo targetInfo `json:"targetInfo"`
		}
		_ = json.Unmarshal(ev.Params, &p)
		if v.updatePage(p.TargetInfo) || v.addPage(p.TargetInfo) {
			v.sendTabs()
		}
	case "Target.targetDestroyed":
		var p struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(ev.Params, &p)
		i := v.pageIndex(p.TargetID)
		if i < 0 {
			return
		}
		v.pages = slices.Delete(v.pages, i, i+1)
		if p.TargetID != v.active {
			v.sendTabs()
			return
		}
		v.detach()
		v.active = ""
		if len(v.pages) > 0 {
			v.attach(ctx, v.pages[min(i, len(v.pages)-1)].ID)
			return
		}
		v.sendTabs()
	case "Target.detachedFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(ev.Params, &p)
		if p.SessionID != "" && p.SessionID == v.session {
			// The page went away under us (crash or close); targetDestroyed
			// handles a close, so only reattach when it still exists.
			v.session, v.screencasting, v.pendingAck = "", false, nil
			if id := v.active; v.pageIndex(id) >= 0 {
				v.attach(ctx, id)
			}
		}
	}
	if ev.SessionID == "" || ev.SessionID != v.session {
		return
	}
	switch ev.Method {
	case "Page.screencastFrame":
		v.onFrame(ctx, ev.Params)
	case "Page.frameNavigated":
		var p struct {
			Frame struct {
				ParentID string `json:"parentId"`
			} `json:"frame"`
		}
		_ = json.Unmarshal(ev.Params, &p)
		if p.Frame.ParentID == "" {
			v.refreshNav(ctx)
			if !v.screencasting {
				v.castAttempts = 0
				v.startScreencast(ctx)
			}
		}
	case "Page.navigatedWithinDocument":
		v.refreshNav(ctx)
	case "Page.frameStartedLoading", "Page.frameStoppedLoading":
		var p struct {
			FrameID string `json:"frameId"`
		}
		_ = json.Unmarshal(ev.Params, &p)
		// A page's main frame has the target's id.
		if p.FrameID == v.active {
			loading := ev.Method == "Page.frameStartedLoading"
			v.out.Text(map[string]any{"type": "loading", "loading": loading})
			if !loading {
				v.refreshTitles(ctx)
			}
		}
	case "Page.javascriptDialogOpening":
		var p struct {
			Type          string `json:"type"`
			Message       string `json:"message"`
			DefaultPrompt string `json:"defaultPrompt"`
		}
		_ = json.Unmarshal(ev.Params, &p)
		v.out.Text(map[string]any{"type": "dialog", "open": true, "dialogType": p.Type, "message": p.Message, "defaultPrompt": p.DefaultPrompt})
	case "Page.javascriptDialogClosed":
		v.out.Text(map[string]any{"type": "dialog", "open": false})
	}
}

func (v *viewer) onFrame(ctx context.Context, params json.RawMessage) {
	var p struct {
		Data      string `json:"data"`
		SessionID int    `json:"sessionId"`
		Metadata  struct {
			DeviceWidth  float64 `json:"deviceWidth"`
			DeviceHeight float64 `json:"deviceHeight"`
		} `json:"metadata"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	sid := p.SessionID
	v.pendingAck = &sid
	if jpeg, err := base64.StdEncoding.DecodeString(p.Data); err == nil {
		v.seq++
		v.out.Frame(encodeFrame(frameHeader{Seq: v.seq, Width: p.Metadata.DeviceWidth, Height: p.Metadata.DeviceHeight}, jpeg))
	}
	v.maybeAck(ctx)
}

func (v *viewer) inputCall(method string, params map[string]any) {
	sid := v.session
	if sid == "" {
		return
	}
	v.touch()
	v.enqueue(func() {
		_, _ = v.call(context.Background(), callTimeout, sid, method, params)
	})
}

// navCall runs a navigation command and reports its failure to the client.
func (v *viewer) navCall(method string, params map[string]any) {
	sid := v.session
	if sid == "" {
		return
	}
	v.touch()
	v.enqueue(func() {
		raw, err := v.call(context.Background(), navigationTimeout, sid, method, params)
		var res struct {
			ErrorText string `json:"errorText"`
		}
		_ = json.Unmarshal(raw, &res)
		msg := res.ErrorText
		if err != nil && !errors.Is(err, ErrClosed) {
			msg = err.Error()
		}
		if msg != "" {
			v.post(func() { v.out.Text(map[string]any{"type": "error", "message": "Navigation failed: " + msg}) })
		}
	})
}

func (v *viewer) historyStep(delta int) {
	sid := v.session
	if sid == "" {
		return
	}
	v.touch()
	v.enqueue(func() {
		raw, err := v.call(context.Background(), callTimeout, sid, "Page.getNavigationHistory", nil)
		if err != nil {
			return
		}
		var h navHistory
		_ = json.Unmarshal(raw, &h)
		i := h.CurrentIndex + delta
		if i < 0 || i >= len(h.Entries) {
			return
		}
		_, _ = v.call(context.Background(), navigationTimeout, sid, "Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[i].ID})
	})
}

// browserCall runs a browser-level command off the loop.
func (v *viewer) browserCall(method string, params map[string]any) {
	v.touch()
	go func() {
		_, err := v.call(context.Background(), callTimeout, "", method, params)
		if err != nil && !errors.Is(err, ErrClosed) {
			slog.Debug("browser viewer", "method", method, "err", err)
		}
	}()
}

func (v *viewer) onClient(ctx context.Context, m clientMsg) error {
	switch m.Type {
	case "ack":
		if m.Seq > v.acked && m.Seq <= v.seq {
			v.acked = m.Seq
		}
		v.maybeAck(ctx)
	case "mouse":
		if p, err := mouseParams(m); err == nil {
			v.inputCall("Input.dispatchMouseEvent", p)
		}
	case "wheel":
		v.inputCall("Input.dispatchMouseEvent", wheelParams(m))
	case "key":
		if p, err := keyParams(m); err == nil {
			v.inputCall("Input.dispatchKeyEvent", p)
		}
	case "text":
		if m.Text != "" && len(m.Text) <= maxTextInput {
			v.inputCall("Input.insertText", map[string]any{"text": m.Text})
		}
	case "navigate":
		u, err := navigationURL(m.URL)
		if err != nil {
			v.out.Text(map[string]any{"type": "error", "message": "Enter an http(s) URL"})
			return nil
		}
		v.navCall("Page.navigate", map[string]any{"url": u})
	case "back":
		v.historyStep(-1)
	case "forward":
		v.historyStep(1)
	case "reload":
		v.navCall("Page.reload", map[string]any{})
	case "stop":
		v.inputCall("Page.stopLoading", map[string]any{})
	case "tab.new":
		u := "about:blank"
		if m.URL != "" {
			if nu, err := navigationURL(m.URL); err == nil {
				u = nu
			}
		}
		v.browserCall("Target.createTarget", map[string]any{"url": u})
	case "tab.switch":
		if m.ID != v.active && v.pageIndex(m.ID) >= 0 {
			v.touch()
			v.attach(ctx, m.ID)
			v.browserCall("Target.activateTarget", map[string]any{"targetId": m.ID})
		}
	case "tab.close":
		if v.pageIndex(m.ID) >= 0 {
			v.browserCall("Target.closeTarget", map[string]any{"targetId": m.ID})
		}
	case "viewport":
		w, h, ok := clampViewport(m.Width, m.Height)
		if !ok || v.active == "" {
			return nil
		}
		target := v.active
		v.touch()
		v.enqueue(func() { v.resizeWindow(target, w, h) })
	case "screen":
		if m.Width <= 0 || m.Height <= 0 || (m.Width == v.screenW && m.Height == v.screenH) {
			return nil
		}
		ow, oh := screencastSize(v.screenW, v.screenH)
		v.screenW, v.screenH = m.Width, m.Height
		if nw, nh := screencastSize(m.Width, m.Height); nw != ow || nh != oh {
			v.restartScreencast(ctx)
		}
	case "dialog":
		// Not through the worker: it may be stuck on the very input that
		// opened the dialog.
		sid := v.session
		if sid == "" {
			return nil
		}
		v.touch()
		go func() {
			_, _ = v.call(context.Background(), callTimeout, sid, "Page.handleJavaScriptDialog", map[string]any{"accept": m.Accept, "promptText": m.PromptText})
		}()
	case "reset":
		return errReset
	}
	return nil
}

// resizeWindow sets the page's window size. In headless Chromium the window
// is the viewport, and unlike a device-metrics override it doesn't end with
// this viewer's session: the agent sees the same size.
func (v *viewer) resizeWindow(target string, w, h int) {
	ctx := context.Background()
	raw, err := v.call(ctx, callTimeout, "", "Browser.getWindowForTarget", map[string]any{"targetId": target})
	if err != nil {
		return
	}
	var res struct {
		WindowID int `json:"windowId"`
	}
	_ = json.Unmarshal(raw, &res)
	_, _ = v.call(ctx, callTimeout, "", "Browser.setWindowBounds", map[string]any{
		"windowId": res.WindowID, "bounds": map[string]any{"width": w, "height": h, "windowState": "normal"},
	})
}
