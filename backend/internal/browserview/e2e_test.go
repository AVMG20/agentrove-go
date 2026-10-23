package browserview

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestRealBox drives a real box's browser through the viewer protocol. It
// needs Docker and a running container of the sandbox image:
//
//	AGENTROVE_BROWSER_E2E_CONTAINER=<container> go test ./internal/browserview -run RealBox -v
//
// The API reaches the box the same way: launcher and socat over docker exec.
func TestRealBox(t *testing.T) {
	name := os.Getenv("AGENTROVE_BROWSER_E2E_CONTAINER")
	if name == "" {
		t.Skip("AGENTROVE_BROWSER_E2E_CONTAINER not set")
	}
	box := &dockerCLIBox{name: name}
	page := `<!doctype html><meta charset="utf-8"><title>start</title>
<style>body{margin:0} button,input{position:absolute;left:0;width:300px;height:100px}</style>
<button id="b" style="top:0" onclick="document.title='clicked'">Click</button>
<input id="i" style="top:100px" oninput="document.title='typed:'+this.value" onkeydown="if(event.key==='Enter')document.title='enter:'+this.value">
<button id="d" style="top:200px" onclick="document.title=confirm('Sure?')?'confirmed':'cancelled'">Dialog</button>
<div style="height:5000px"></div>
<script>onscroll=()=>{document.title='scrolled:'+scrollY}</script>`
	if out, err := box.exec(page, "sh", "-c", "mkdir -p /tmp/e2e-site && cat > /tmp/e2e-site/index.html && (cd /tmp/e2e-site && nohup python3 -m http.server 3999 >/dev/null 2>&1 &) && sleep 1"); err != nil {
		t.Fatalf("serve test page: %v %s", err, out)
	}

	c := serveViewer(t, box)
	var mu sync.Mutex
	var texts []map[string]any
	var frames []frameHeader
	go func() {
		for {
			typ, b, err := c.Read(context.Background())
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				n := binary.BigEndian.Uint32(b)
				var h frameHeader
				_ = json.Unmarshal(b[4:4+n], &h)
				if !bytes.HasPrefix(b[4+n:], []byte{0xff, 0xd8}) {
					t.Errorf("frame %d is not a JPEG", h.Seq)
				}
				mu.Lock()
				frames = append(frames, h)
				mu.Unlock()
				ack, _ := json.Marshal(map[string]any{"type": "ack", "seq": h.Seq})
				_ = c.Write(context.Background(), websocket.MessageText, ack)
				continue
			}
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			mu.Lock()
			texts = append(texts, m)
			mu.Unlock()
		}
	}()
	wait := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			mu.Lock()
			ok := cond()
			mu.Unlock()
			if ok {
				return
			}
			if time.Now().After(deadline) {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("timed out waiting for %s; last messages: %v", what, texts[max(0, len(texts)-5):])
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	activeTitle := func() string {
		for i := len(texts) - 1; i >= 0; i-- {
			if texts[i]["type"] == "tabs" {
				for _, tb := range texts[i]["tabs"].([]any) {
					if tb := tb.(map[string]any); tb["id"] == texts[i]["active"] {
						return tb["title"].(string)
					}
				}
				return ""
			}
		}
		return ""
	}
	titleIs := func(want string) func() bool { return func() bool { return activeTitle() == want } }
	sendMsg := func(v map[string]any) { send(t, c, v) }

	wait("tabs", func() bool { return activeTitle() != "" || len(texts) > 2 })
	start := time.Now()
	sendMsg(map[string]any{"type": "navigate", "url": "localhost:3999"})
	wait("page loaded", titleIs("start"))
	wait("frames", func() bool { return len(frames) > 0 })
	t.Logf("first frames after %v: %+v", time.Since(start), frames[len(frames)-1])

	click := func(x, y float64) {
		sendMsg(map[string]any{"type": "mouse", "event": "move", "x": x, "y": y})
		sendMsg(map[string]any{"type": "mouse", "event": "down", "x": x, "y": y, "button": "left", "buttons": 1, "clickCount": 1})
		sendMsg(map[string]any{"type": "mouse", "event": "up", "x": x, "y": y, "button": "left", "clickCount": 1})
	}
	click(150, 50)
	wait("click", titleIs("clicked"))

	click(150, 150)
	for _, k := range []struct{ key, code string }{{"h", "KeyH"}, {"i", "KeyI"}} {
		sendMsg(map[string]any{"type": "key", "event": "down", "key": k.key, "code": k.code, "keyCode": int(strings.ToUpper(k.key)[0])})
		sendMsg(map[string]any{"type": "key", "event": "up", "key": k.key, "code": k.code, "keyCode": int(strings.ToUpper(k.key)[0])})
	}
	wait("typing", titleIs("typed:hi"))
	sendMsg(map[string]any{"type": "text", "text": " 🌍"})
	wait("pasted text", titleIs("typed:hi 🌍"))
	sendMsg(map[string]any{"type": "key", "event": "down", "key": "Enter", "code": "Enter", "keyCode": 13})
	sendMsg(map[string]any{"type": "key", "event": "up", "key": "Enter", "code": "Enter", "keyCode": 13})
	wait("enter", titleIs("enter:hi 🌍"))

	click(150, 250)
	wait("dialog", func() bool {
		for _, m := range texts {
			if m["type"] == "dialog" && m["open"] == true && m["message"] == "Sure?" {
				return true
			}
		}
		return false
	})
	sendMsg(map[string]any{"type": "dialog", "accept": true})
	wait("confirmed", titleIs("confirmed"))

	sendMsg(map[string]any{"type": "wheel", "x": 400, "y": 400, "deltaY": 600})
	wait("scroll", func() bool { return strings.HasPrefix(activeTitle(), "scrolled:") })

	sendMsg(map[string]any{"type": "viewport", "width": 900, "height": 700})
	wait("resized frames", func() bool { return len(frames) > 0 && frames[len(frames)-1].Width == 900 })

	sendMsg(map[string]any{"type": "tab.new"})
	wait("new tab", func() bool {
		for i := len(texts) - 1; i >= 0; i-- {
			if texts[i]["type"] == "tabs" {
				return len(texts[i]["tabs"].([]any)) >= 2 && activeTitle() != "confirmed" && !strings.HasPrefix(activeTitle(), "scrolled")
			}
		}
		return false
	})
	sendMsg(map[string]any{"type": "reset"})
	wait("reset", func() bool {
		for _, m := range texts {
			if m["type"] == "status" && m["state"] == "restarting" {
				return true
			}
		}
		return false
	})
	n := len(frames)
	wait("frames after reset", func() bool { return len(frames) > n })
	t.Logf("%d frames in %v", len(frames), time.Since(start))
}

// dockerCLIBox reaches a container's browser with the docker CLI.
type dockerCLIBox struct{ name string }

func (b *dockerCLIBox) exec(stdin string, args ...string) (string, error) {
	cmd := exec.Command("docker", append([]string{"exec", "-i", b.name}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (b *dockerCLIBox) Ensure(context.Context) (string, error) {
	out, err := exec.Command("docker", "exec", b.name, "agentrove-browser", "ensure").Output()
	if err != nil {
		return "", fmt.Errorf("ensure: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (b *dockerCLIBox) Dial(context.Context) (io.ReadWriteCloser, error) {
	cmd := exec.Command("docker", "exec", "-i", b.name, "socat", "STDIO", fmt.Sprintf("TCP:127.0.0.1:%d", DebugPort))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &cliStream{cmd: cmd, w: stdin, r: stdout}, nil
}

func (b *dockerCLIBox) Reset(context.Context) error {
	return exec.Command("docker", "exec", b.name, "agentrove-browser", "reset").Run()
}

func (b *dockerCLIBox) Touch() {}

type cliStream struct {
	cmd *exec.Cmd
	w   io.WriteCloser
	r   io.Reader
}

func (s *cliStream) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *cliStream) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s *cliStream) Close() error {
	_ = s.w.Close()
	go func() { _ = s.cmd.Wait() }()
	return nil
}
