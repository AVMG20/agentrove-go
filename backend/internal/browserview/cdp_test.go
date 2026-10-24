package browserview

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The CDP peer is untrusted: an unread backlog is bounded by bytes.
// smallBacklog shrinks the backlog limits so tests don't push 24MB.
func smallBacklog(t *testing.T) {
	soft, hard := softQueuedEventBytes, maxQueuedEventBytes
	softQueuedEventBytes, maxQueuedEventBytes = 256<<10, 768<<10
	t.Cleanup(func() { softQueuedEventBytes, maxQueuedEventBytes = soft, hard })
}

func TestConnEventBacklogIsBounded(t *testing.T) {
	smallBacklog(t)
	f := newFakeBrowser()
	c := NewConn(f)
	defer c.Close()
	big := strings.Repeat("a", 64<<10)
	// Title churn past the soft limit is dropped, not fatal.
	for range 40 {
		f.emit("", "Target.targetInfoChanged", map[string]any{"targetInfo": map[string]any{"url": big}})
	}
	if _, err := c.Call(context.Background(), "", "Target.getTargets", nil); err != nil {
		t.Fatalf("connection closed by title churn: %v", err)
	}
	c.mu.Lock()
	queued := c.queued
	c.mu.Unlock()
	if queued > softQueuedEventBytes+(128<<10) {
		t.Fatalf("queued %d bytes of droppable events", queued)
	}
	// Anything else past the hard limit closes it.
	for range 40 {
		f.emit("S1", "Page.somethingBig", map[string]any{"data": big})
	}
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("backlog grew without bound")
	}
	if c.queued > maxQueuedEventBytes {
		t.Fatalf("queued %d bytes", c.queued)
	}
}

func TestConnDrainResetsBacklog(t *testing.T) {
	smallBacklog(t)
	f := newFakeBrowser()
	c := NewConn(f)
	defer c.Close()
	big := strings.Repeat("a", 64<<10)
	for range 3 {
		for range 10 {
			f.emit("S1", "Page.event", map[string]any{"data": big})
		}
		deadline := time.Now().Add(2 * time.Second)
		n := 0
		for n < 10 && time.Now().Before(deadline) {
			n += len(c.Events())
			time.Sleep(5 * time.Millisecond)
		}
		if n != 10 {
			t.Fatalf("got %d events", n)
		}
	}
	select {
	case <-c.Done():
		t.Fatalf("closed although drained: %v", c.Err())
	default:
	}
	var res json.RawMessage
	res, err := c.Call(context.Background(), "", "Target.getTargets", nil)
	if err != nil || len(res) == 0 {
		t.Fatalf("call: %v", err)
	}
}
