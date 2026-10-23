package chat

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentrove/internal/events"
	"agentrove/internal/httpx"

	"github.com/google/uuid"
)

const pingInterval = 15 * time.Second

// chatEventsFeed is the per-session chat lifecycle feed (chat_created,
// title_updated, stream_started, background_tasks). No replay: the sidebar
// refetches on reconnect.
func (m *Module) chatEventsFeed(w http.ResponseWriter, r *http.Request) error {
	sub := m.a.Hub.Subscribe(events.UserChats(user(r).ID), 256)
	defer sub.Close()
	sse, err := httpx.NewSSE(w)
	if err != nil {
		return err
	}
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-sub.Dropped:
			return nil
		case <-ping.C:
			if sse.Send("ping", "", "") != nil {
				return nil
			}
		case msg := <-sub.C:
			if sse.Send("chat_event", "", string(msg)) != nil {
				return nil
			}
		}
	}
}

func parseCursors(raw string) (map[string]int64, error) {
	out := map[string]int64{}
	if raw == "" {
		return out, nil
	}
	var parsed map[string]json.Number
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil {
		return nil, httpx.Unprocessable("cursors must be a JSON object")
	}
	for k, v := range parsed {
		if _, err := uuid.Parse(k); err != nil {
			return nil, httpx.Unprocessable("cursor keys must be chat ids")
		}
		n, err := strconv.ParseInt(v.String(), 10, 64)
		if err != nil || n < 0 {
			return nil, httpx.Unprocessable("cursor seq must be a non-negative integer")
		}
		out[k] = n
	}
	return out, nil
}

// streamsFeed multiplexes every stream of the user onto one SSE connection.
// It subscribes first, replays each cursor chat's backlog from the DB, then
// forwards live envelopes, dropping duplicates by seq and backfilling gaps.
func (m *Module) streamsFeed(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	if len(r.URL.Query().Get("cursors")) > 8192 {
		return httpx.Unprocessable("cursors too long")
	}
	requested, err := parseCursors(r.URL.Query().Get("cursors"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	last := map[string]int64{}
	for id, seq := range requested {
		if _, err := m.a.Store.GetMemberChat(ctx, u.ID, id); err == nil {
			last[id] = seq
		}
	}
	sub := m.a.Hub.Subscribe(events.UserStreams(u.ID), 4096)
	defer sub.Close()
	sse, err := httpx.NewSSE(w)
	if err != nil {
		return err
	}
	for id, seq := range last {
		n, err := m.replay(ctx, sse, id, seq)
		if err != nil {
			return nil
		}
		last[id] = n
	}
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sub.Dropped:
			// Fell behind: end the feed so the client reconnects with cursors.
			return nil
		case <-ping.C:
			if sse.Send("ping", "", "") != nil {
				return nil
			}
		case raw := <-sub.C:
			var env struct {
				ChatID    string `json:"chatId"`
				ChannelID string `json:"channelId"`
				Seq       *int64 `json:"seq"`
			}
			if json.Unmarshal(raw, &env) != nil {
				continue
			}
			if env.ChannelID != "" {
				if sse.Send("stream", "", string(raw)) != nil {
					return nil
				}
				continue
			}
			if env.ChatID == "" || env.Seq == nil {
				continue
			}
			seq := *env.Seq
			if prev, ok := last[env.ChatID]; ok {
				if seq <= prev {
					continue
				}
				if seq > prev+1 {
					n, err := m.replay(ctx, sse, env.ChatID, prev)
					if err != nil {
						return nil
					}
					last[env.ChatID] = n
					if n >= seq {
						continue
					}
				}
			}
			if sse.Send("stream", strconv.FormatInt(seq, 10), string(raw)) != nil {
				return nil
			}
			last[env.ChatID] = seq
		}
	}
}

// replay sends persisted events after seq and returns the last seq sent.
func (m *Module) replay(ctx context.Context, sse *httpx.SSE, chatID string, after int64) (int64, error) {
	for {
		evs, err := m.st.chatEventsAfter(ctx, chatID, after, 5000)
		if err != nil {
			slog.Warn("stream replay", "chat", chatID, "err", err)
			return after, nil
		}
		if len(evs) == 0 {
			return after, nil
		}
		for _, e := range evs {
			data := envelope(e.ChatID, e.MessageID, e.StreamID, e.Seq, e.Kind, e.Payload.V)
			if err := sse.Send("stream", strconv.FormatInt(e.Seq, 10), string(data)); err != nil {
				return after, err
			}
			after = e.Seq
		}
		if len(evs) < 5000 {
			return after, nil
		}
	}
}
