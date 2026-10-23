package events

import (
	"sync"
	"testing"
)

// Concurrent publishers racing to drop the same full subscriber must not
// double-close Dropped.
func TestPublishConcurrentDrop(t *testing.T) {
	for range 50 {
		h := NewHub()
		s := h.Subscribe("t", 1)
		h.Publish("t", []byte("fill"))
		var wg sync.WaitGroup
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				h.Publish("t", []byte("x"))
			}()
		}
		wg.Wait()
		select {
		case <-s.Dropped:
		default:
			t.Fatal("full subscriber was not dropped")
		}
		if h.HasSubscribers("t") {
			t.Fatal("dropped subscriber still registered")
		}
	}
}
