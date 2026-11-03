package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"time"
)

// Every agent in a box drives the same browser, which the user watches. So
// that two agents don't navigate, resize or emulate on each other's page,
// each client gets a tab of its own: playwright-mcp attached over CDP
// otherwise starts on the browser's first page, the same one for everybody.
// The proxy opens the tab with the first tool call (the viewer follows new
// tabs, so the user sees it), puts every new playwright-mcp back on it
// after an idle stop or a restart, opens a new one when it was closed under
// the agent, and closes the tabs it opened when the session ends. The
// browser profile (logins) stays shared: a browser context per agent would
// hide the user's logins from it.
//
// playwright-mcp has no flag for this and names tabs by index only, so the
// proxy finds its tab by CDP target id with small run_code snippets of its
// own, answered as JSON (`_meta.json`).

type tabState struct {
	// tab is the target id of the tab the client is on ("" before the
	// first call).
	tab string
	// owned are the tabs opened for this client, closed when it ends.
	owned map[string]bool
	// tabAction is a browser_tabs action the client ran whose effect on its
	// current tab isn't known yet.
	tabAction string
	// lostTab is set when the client's tab was gone and replaced; its next
	// tool result says so.
	lostTab bool
}

const lostTabNote = "Note: the tab you were using is gone (it stopped responding and was closed, the browser " +
	"restarted, or someone closed it), so this ran in a new blank tab; navigate again."

const currentTargetJS = `async (page) => {
  const s = await page.context().newCDPSession(page);
  try { return (await s.send('Target.getTargetInfo')).targetInfo.targetId; } finally { await s.detach().catch(() => {}); }
}`

// findTargetJS returns the playwright-mcp tab index of target id, or -1.
// The index is the page's position in context.pages(), which playwright-mcp
// tabs follow.
func findTargetJS(id string) string {
	return `async (page) => {
  const pages = page.context().pages();
  for (let i = 0; i < pages.length; i++) {
    const s = await page.context().newCDPSession(pages[i]).catch(() => null);
    if (!s) continue;
    try { if ((await s.send('Target.getTargetInfo')).targetInfo.targetId === ` + strconv.Quote(id) + `) return i; } finally { await s.detach().catch(() => {}); }
  }
  return -1;
}`
}

var errInternalTimeout = errors.New("timed out")

type internalWait struct {
	be backend
	ch chan []byte
}

// internalCall runs a tool on the backend for the proxy itself; the client
// never sees it. It returns the tool's result section (JSON mode).
func (p *proxy) internalCall(tool string, args map[string]any) (string, error) {
	p.mu.Lock()
	be := p.be
	if be == nil {
		p.mu.Unlock()
		return "", errors.New("playwright-mcp exited")
	}
	p.seq++
	id := fmt.Sprintf("agentrove-browser-mcp-%d", p.seq)
	key := strconv.Quote(id)
	w := &internalWait{be: be, ch: make(chan []byte, 1)}
	p.internal[key] = w
	p.lastUse = time.Now()
	p.mu.Unlock()
	args["_meta"] = map[string]any{"json": true}
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	drop := func() {
		p.mu.Lock()
		delete(p.internal, key)
		p.mu.Unlock()
	}
	if err := be.Send(append(req, '\n')); err != nil {
		drop()
		return "", err
	}
	timer := time.NewTimer(p.internalTimeout)
	defer timer.Stop()
	select {
	case line, ok := <-w.ch:
		if !ok {
			return "", errors.New("playwright-mcp exited")
		}
		return toolResult(line)
	case <-timer.C:
		drop()
		return "", fmt.Errorf("%s: %w", tool, errInternalTimeout)
	}
}

// toolResult extracts the result section of a JSON-mode tool response.
func toolResult(line []byte) (string, error) {
	var r struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &r); err != nil {
		return "", err
	}
	if r.Error != nil {
		return "", errors.New(r.Error.Message)
	}
	text := ""
	if len(r.Result.Content) > 0 {
		text = r.Result.Content[0].Text
	}
	var payload struct {
		Result string `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		// Errors raised before the tool runs (connecting) aren't JSON.
		if r.Result.IsError {
			return "", errors.New(text)
		}
		return "", fmt.Errorf("unexpected tool result %q", text)
	}
	if r.Result.IsError {
		return "", errors.New(payload.Error)
	}
	return payload.Result, nil
}

// runCode runs a snippet with browser_run_code_unsafe and decodes its value.
func (p *proxy) runCode(code string, v any) error {
	res, err := p.internalCall("browser_run_code_unsafe", map[string]any{"code": code})
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(res), v)
}

// currentTab is the target id of the backend's current tab.
func (p *proxy) currentTab() (string, error) {
	var id string
	if err := p.runCode(currentTargetJS, &id); err != nil {
		return "", err
	}
	if !targetIDPattern.MatchString(id) {
		return "", fmt.Errorf("unexpected target id %q", id)
	}
	return id, nil
}

// claimTab puts the backend on the client's tab: selects it in a fresh
// backend, or opens a new one when the client has none or it was closed.
func (p *proxy) claimTab(fresh bool) error {
	if p.listPages == nil {
		return nil
	}
	p.mu.Lock()
	tab := p.tab
	if fresh {
		// What the client's last browser_tabs did went with the old backend.
		p.tabAction = ""
	}
	p.mu.Unlock()
	if tab != "" {
		ids, err := p.listPages()
		switch {
		case err != nil && !fresh:
			// The port is open (browserUp) but the browser doesn't answer:
			// a new playwright-mcp's heal restarts it.
			return fmt.Errorf("list tabs: %w", err)
		case err == nil && !slices.Contains(ids, tab):
			tab = ""
			p.mu.Lock()
			p.lostTab = true
			p.mu.Unlock()
		}
	}
	if tab != "" && !fresh {
		return nil
	}
	if tab != "" {
		var i int
		if err := p.runCode(findTargetJS(tab), &i); err != nil {
			return err
		}
		if i >= 0 {
			_, err := p.internalCall("browser_tabs", map[string]any{"action": "select", "index": i})
			return err
		}
	}
	if _, err := p.internalCall("browser_tabs", map[string]any{"action": "new"}); err != nil {
		return err
	}
	id, err := p.currentTab()
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.tab = id
	p.owned[id] = true
	p.mu.Unlock()
	return nil
}

// keepTab runs before each tool call on a running backend: it learns where
// a browser_tabs call moved the client, and gives it a new tab if its own
// was closed under it (playwright-mcp would move to some other tab, maybe
// another agent's). An error means the backend should be replaced.
func (p *proxy) keepTab() error {
	if p.listPages == nil {
		return nil
	}
	p.mu.Lock()
	action := p.tabAction
	p.tabAction = ""
	p.mu.Unlock()
	if action != "" {
		id, err := p.currentTab()
		if err != nil {
			return err
		}
		p.mu.Lock()
		switch {
		case action == "new":
			p.owned[id] = true
			p.tab = id
		case action == "select" || p.owned[id]:
			p.tab = id
		default:
			// Closing its tab moved it onto one that isn't its own.
			p.tab = ""
		}
		p.mu.Unlock()
	}
	return p.claimTab(false)
}

// afterCall runs when a client tool call is answered and returns the
// answer to pass on.
func (p *proxy) afterCall(be backend, c *call, line []byte) []byte {
	if p.listPages == nil {
		return line
	}
	p.mu.Lock()
	lost := p.lostTab
	p.lostTab = false
	p.mu.Unlock()
	if lost {
		line = prependText(line, lostTabNote)
	}
	if isErrorResult(line) {
		return line
	}
	switch c.tool {
	case "browser_tabs":
		if c.tabAction != "list" {
			p.mu.Lock()
			p.tabAction = c.tabAction
			p.mu.Unlock()
		}
	case "browser_close":
		// Attached over CDP, playwright-mcp only disconnects and would
		// then start over on the browser's first page: close the
		// client's tab as asked, and start over on a new one.
		p.stopIf(be, false)
		p.mu.Lock()
		tab := p.tab
		p.tab = ""
		delete(p.owned, tab)
		p.mu.Unlock()
		if tab != "" && p.closePage != nil {
			p.closePage(tab)
		}
	}
	return line
}

// prependText adds a text part before a tool result's content.
func prependText(line []byte, text string) []byte {
	var m map[string]json.RawMessage
	var res map[string]json.RawMessage
	var content []json.RawMessage
	if json.Unmarshal(line, &m) != nil || json.Unmarshal(m["result"], &res) != nil || json.Unmarshal(res["content"], &content) != nil {
		return line
	}
	part, _ := json.Marshal(map[string]string{"type": "text", "text": text})
	res["content"], _ = json.Marshal(append([]json.RawMessage{part}, content...))
	m["result"], _ = json.Marshal(res)
	out, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return append(out, '\n')
}

func isErrorResult(line []byte) bool {
	var r struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	return json.Unmarshal(line, &r) != nil || r.Result.IsError || len(r.Error) > 0
}

// closeOwnedTabs closes the tabs opened for this client.
func (p *proxy) closeOwnedTabs() {
	p.mu.Lock()
	owned := p.owned
	p.owned = map[string]bool{}
	p.tab = ""
	p.mu.Unlock()
	if p.closePage == nil || len(owned) == 0 {
		return
	}
	ids, err := p.listPages()
	if err != nil {
		return
	}
	for id := range owned {
		if slices.Contains(ids, id) {
			log.Printf("closing tab %s", id)
			p.closePage(id)
		}
	}
}
