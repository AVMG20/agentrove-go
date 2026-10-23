// Command agentrove-browser-mcp is the browser MCP server agents get in a
// Docker box: a lazy stdio proxy in front of Playwright MCP. The CLI starts
// every MCP server of a session when the session starts, and an idle
// playwright-mcp holds ~130MB, so this answers initialize and tools/list from
// a cache recorded at image build and only starts the shared browser
// (`agentrove-browser ensure`) and playwright-mcp on the first other request.
// playwright-mcp is stopped again after AGENTROVE_BROWSER_MCP_IDLE_SECONDS
// without requests, which drops its CDP connection so the browser's own idle
// watchdog can stop Chromium too.
//
// It only uses the standard library: it is built into the box image
// (sandbox/docker/Dockerfile), not into the API.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const defaultCachePath = "/usr/local/share/agentrove-browser/mcp-cache.json"

func main() {
	writeCache := flag.String("write-cache", "", "record playwright-mcp's initialize and tools/list results to this file and exit")
	cachePath := flag.String("cache", defaultCachePath, "cache file written by -write-cache")
	flag.Parse()
	log.SetPrefix("agentrove-browser-mcp: ")
	log.SetFlags(0)

	if *writeCache != "" {
		if err := recordCache(*writeCache); err != nil {
			log.Fatal(err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	c, err := loadCache(*cachePath)
	if err != nil {
		// Still works, just without the laziness.
		log.Printf("no tool cache (%v); starting playwright-mcp eagerly", err)
	}
	p := newProxy(c, os.Stdout, func() (backend, error) { return startPlaywright(true) })
	p.browserUp = cdpUp
	p.idle = time.Duration(envInt("AGENTROVE_BROWSER_MCP_IDLE_SECONDS", 300)) * time.Second
	p.prune = func() { pruneDir(filesDir, maxKeptFiles, maxKeptBytes) }
	go p.prune()
	p.run(ctx, os.Stdin)
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n >= 0 {
		return n
	}
	return def
}

func cdpPort() string {
	if p := os.Getenv("AGENTROVE_BROWSER_PORT"); p != "" {
		return p
	}
	return "9222"
}

// playwrightArgs are the flags for every playwright-mcp this starts, and for
// the one that records the cache, so the cached tool list matches.
func playwrightArgs() []string {
	return []string{
		// Attach to the box's shared browser instead of launching one.
		"--cdp-endpoint", "http://127.0.0.1:" + cdpPort(),
		// Coordinate mouse tools: clicking outside elements, wheel scrolling.
		"--caps", "vision",
		// Not the default (.playwright-mcp in the project), which would show
		// up in the user's git status.
		"--output-dir", filesDir,
	}
}

func cdpUp() bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+cdpPort(), time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// procBackend is a playwright-mcp child process.
type procBackend struct {
	cmd    *exec.Cmd
	sendMu sync.Mutex
	stdin  io.WriteCloser
	lines  chan []byte
}

func startPlaywright(ensureBrowser bool) (backend, error) {
	if ensureBrowser {
		var stderr strings.Builder
		cmd := exec.Command("agentrove-browser", "ensure")
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("agentrove-browser ensure: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	cmd := exec.Command("playwright-mcp", playwrightArgs()...)
	cmd.Stderr = os.Stderr
	// Without client roots, playwright-mcp's workspace is its cwd. Only at
	// run time: the image build records the cache as root, and a root-owned
	// dir would lock the box user out of it.
	if ensureBrowser {
		if err := os.MkdirAll(filesDir, 0o755); err == nil {
			cmd.Dir = filesDir
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start playwright-mcp: %w", err)
	}
	b := &procBackend{cmd: cmd, stdin: stdin, lines: make(chan []byte, 16)}
	go func() {
		r := bufio.NewReaderSize(stdout, 64<<10)
		for {
			line, err := r.ReadBytes('\n')
			if len(strings.TrimSpace(string(line))) > 0 {
				b.lines <- line
			}
			if err != nil {
				break
			}
		}
		_ = cmd.Wait()
		close(b.lines)
	}()
	return b, nil
}

func (b *procBackend) Send(line []byte) error {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	_, err := b.stdin.Write(line)
	return err
}

func (b *procBackend) Lines() <-chan []byte { return b.lines }

func (b *procBackend) Stop() {
	_ = b.stdin.Close()
	p := b.cmd.Process
	go func() {
		time.Sleep(3 * time.Second)
		_ = p.Kill()
	}()
}

func loadCache(path string) (*cache, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c cache
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if len(c.Initialize) == 0 || len(c.Tools) == 0 {
		return nil, errors.New("cache is incomplete")
	}
	return &c, nil
}

// recordCache runs playwright-mcp (no browser needed) and stores what the
// proxy answers before it starts one.
func recordCache(path string) error {
	b, err := startPlaywright(false)
	if err != nil {
		return err
	}
	defer b.Stop()
	call := func(id, method string, params any) (json.RawMessage, error) {
		line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if err := b.Send(append(line, '\n')); err != nil {
			return nil, err
		}
		return awaitResponse(b.Lines(), id, 60*time.Second, nil)
	}
	// An unknown version makes the server answer with the latest it supports.
	init, err := call("init", "initialize", map[string]any{
		"protocolVersion": "9999-12-31",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "agentrove-browser-mcp", "version": "1"},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	_ = b.Send([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))
	tools, err := call("tools", "tools/list", map[string]any{})
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	out, _ := json.Marshal(cache{Initialize: init, Tools: tools})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// filesDir keeps the newest files up to these limits: every navigation
// writes a snapshot there, and all sessions in the box share it.
const (
	maxKeptFiles = 300
	maxKeptBytes = 200 << 20
)

// pruneDir deletes the oldest regular files under dir beyond the newest
// maxFiles or maxBytes. Errors are ignored: another session may be pruning.
func pruneDir(dir string, maxFiles int, maxBytes int64) {
	type file struct {
		path string
		size int64
		mod  time.Time
	}
	var files []file
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files = append(files, file{path, info.Size(), info.ModTime()})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	var total int64
	for i, f := range files {
		total += f.size
		if i >= maxFiles || total > maxBytes {
			_ = os.Remove(f.path)
		}
	}
}
