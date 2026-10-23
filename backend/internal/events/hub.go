// Package events is the in-process pub/sub that replaces Redis: stream
// envelopes and chat lifecycle events fan out to SSE subscribers by topic.
package events

import (
	"sync"
)

// Topic helpers keep naming in one place.
func UserStreams(userID string) string { return "streams:" + userID }
func UserChats(userID string) string   { return "chats:" + userID }
func Channel(channelID string) string  { return "channel:" + channelID }

type Subscription struct {
	C     <-chan []byte
	ch    chan []byte
	hub   *Hub
	topic string
	once  sync.Once
	// dropOnce guards Dropped: concurrent publishers can both find the same
	// subscriber full, and a second close would panic.
	dropOnce sync.Once
	// Closed when the hub dropped this subscriber for falling behind; the
	// client is expected to reconnect and replay from its cursor.
	Dropped chan struct{}
}

func (s *Subscription) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		delete(s.hub.subs[s.topic], s)
		if len(s.hub.subs[s.topic]) == 0 {
			delete(s.hub.subs, s.topic)
		}
		s.hub.mu.Unlock()
	})
}

type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[*Subscription]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[string]map[*Subscription]struct{}{}} }

func (h *Hub) Subscribe(topic string, buffer int) *Subscription {
	if buffer <= 0 {
		buffer = 1024
	}
	ch := make(chan []byte, buffer)
	s := &Subscription{C: ch, ch: ch, hub: h, topic: topic, Dropped: make(chan struct{})}
	h.mu.Lock()
	if h.subs[topic] == nil {
		h.subs[topic] = map[*Subscription]struct{}{}
	}
	h.subs[topic][s] = struct{}{}
	h.mu.Unlock()
	return s
}

// Publish never blocks: a subscriber whose buffer is full is dropped.
func (h *Hub) Publish(topic string, msg []byte) {
	h.mu.RLock()
	var slow []*Subscription
	for s := range h.subs[topic] {
		select {
		case s.ch <- msg:
		default:
			slow = append(slow, s)
		}
	}
	h.mu.RUnlock()
	for _, s := range slow {
		s.Close()
		s.dropOnce.Do(func() { close(s.Dropped) })
	}
}

func (h *Hub) HasSubscribers(topic string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[topic]) > 0
}
