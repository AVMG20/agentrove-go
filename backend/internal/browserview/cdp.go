// Package browserview serves the live viewer of a box's shared browser. The
// API never reaches a box over the network, so it speaks the Chrome DevTools
// Protocol to the box's Chromium through a docker exec stream (socat to the
// loopback debugging port) and gives the frontend a small protocol of its own
// (tabs, navigation, input, screencast frames) instead of raw CDP.
package browserview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Transport carries whole CDP messages (websocket text frames).
type Transport interface {
	ReadMessage(ctx context.Context) ([]byte, error)
	WriteMessage(ctx context.Context, msg []byte) error
	Close() error
}

// Event is a CDP event; SessionID is set for events of an attached target.
type Event struct {
	Method    string
	SessionID string
	Params    json.RawMessage
}

// CDPError is an error response to a call.
type CDPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *CDPError) Error() string { return fmt.Sprintf("cdp: %s (%d)", e.Message, e.Code) }

// ErrClosed is returned by calls on a closed connection.
var ErrClosed = errors.New("cdp: connection closed")

// maxQueuedEvents bounds the event backlog. Screencast frames are flow
// controlled by acks, so a backlog this large means a stuck consumer.
const maxQueuedEvents = 4096

// Conn is a CDP client on the browser endpoint, using flattened sessions
// (Target.attachToTarget with flatten) for pages.
type Conn struct {
	t Transport

	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan reply
	events  []Event
	err     error

	notify chan struct{} // signalled when events are queued
	done   chan struct{}
}

type reply struct {
	result json.RawMessage
	err    error
}

type wireMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *CDPError       `json:"error,omitempty"`
}

// NewConn starts reading from t; Close (or a read error) ends it.
func NewConn(t Transport) *Conn {
	c := &Conn{t: t, pending: map[int64]chan reply{}, notify: make(chan struct{}, 1), done: make(chan struct{})}
	go c.readLoop()
	return c
}

func (c *Conn) readLoop() {
	ctx := context.Background()
	for {
		raw, err := c.t.ReadMessage(ctx)
		if err != nil {
			c.fail(err)
			return
		}
		var m wireMessage
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if m.Method == "" {
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				if m.Error != nil {
					ch <- reply{err: m.Error}
				} else {
					ch <- reply{result: m.Result}
				}
			}
			continue
		}
		c.mu.Lock()
		if len(c.events) >= maxQueuedEvents {
			c.mu.Unlock()
			c.fail(errors.New("cdp: event backlog overflow"))
			return
		}
		c.events = append(c.events, Event{Method: m.Method, SessionID: m.SessionID, Params: m.Params})
		c.mu.Unlock()
		select {
		case c.notify <- struct{}{}:
		default:
		}
	}
}

func (c *Conn) fail(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	pending := c.pending
	c.pending = map[int64]chan reply{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- reply{err: ErrClosed}
	}
	_ = c.t.Close()
	close(c.done)
}

// Call sends method to the browser (sessionID "") or an attached target and
// waits for its result.
func (c *Conn) Call(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()
	msg, _ := json.Marshal(wireMessage{ID: id, Method: method, SessionID: sessionID, Params: raw})
	c.writeMu.Lock()
	err := c.t.WriteMessage(ctx, msg)
	c.writeMu.Unlock()
	if err != nil {
		c.forget(id)
		c.fail(err)
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	}
}

func (c *Conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Notify is signalled whenever events are waiting; drain them with Events.
func (c *Conn) Notify() <-chan struct{} { return c.notify }

// Events returns and clears the queued events, oldest first.
func (c *Conn) Events() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	ev := c.events
	c.events = nil
	return ev
}

// Done is closed when the connection ends.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the connection ended.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) Close() { c.fail(ErrClosed) }
