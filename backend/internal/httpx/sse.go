package httpx

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// SSE writes server-sent events. Safe for concurrent Send calls.
type SSE struct {
	mu sync.Mutex
	w  http.ResponseWriter
	f  http.Flusher
}

func NewSSE(w http.ResponseWriter) (*SSE, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		// Middleware wrappers expose the underlying writer via Unwrap.
		rc := http.NewResponseController(w)
		if err := rc.Flush(); err != nil {
			return nil, fmt.Errorf("streaming unsupported")
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s := &SSE{w: w, f: f}
	s.flush()
	return s, nil
}

func (s *SSE) flush() {
	if s.f != nil {
		s.f.Flush()
		return
	}
	_ = http.NewResponseController(s.w).Flush()
}

// Send writes one event. id may be empty.
func (s *SSE) Send(event, id, data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	if id != "" {
		b.WriteString("id: ")
		b.WriteString(id)
		b.WriteByte('\n')
	}
	if event != "" {
		b.WriteString("event: ")
		b.WriteString(event)
		b.WriteByte('\n')
	}
	for _, line := range strings.Split(data, "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if _, err := s.w.Write([]byte(b.String())); err != nil {
		return err
	}
	s.flush()
	return nil
}
