package server

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/chat"
	"agentrove/internal/mcpserver"
	"agentrove/internal/profileapi"
)

// chatModules: chat, streaming runtime, Claude driver, models, attachments.
func chatModules(a *app.App) ([]Module, error) {
	m := chat.New(a)
	m.MCPConfig = newMCPConfigs(a).forChat
	return []Module{m, profileapi.New(a)}, nil
}

// mcpConfigs hands each chat session the agentrove MCP server over HTTP.
// Configs are cached so the token (and thus the session fingerprint) stays
// the same across turns until half its lifetime is used. A renewed token
// changes the fingerprint, so the next turn restarts the CLI (with --resume)
// on it; every turn therefore starts with at least half a lifetime left.
type mcpConfigs struct {
	a     *app.App
	mu    sync.Mutex
	cache map[string]cachedMCP
}

type cachedMCP struct {
	servers map[string]any
	renewAt time.Time
}

func newMCPConfigs(a *app.App) *mcpConfigs {
	return &mcpConfigs{a: a, cache: map[string]cachedMCP{}}
}

func (c *mcpConfigs) forChat(userID, chatID, kind string) map[string]any {
	full := c.a.Cfg.MCPEnabled
	if !full && !c.a.Cfg.ArtifactsEnabled {
		return nil
	}
	key := fmt.Sprintf("%s|%s|%s|%t", userID, chatID, kind, full)
	c.mu.Lock()
	defer c.mu.Unlock()
	if hit, ok := c.cache[key]; ok && time.Now().Before(hit.renewAt) {
		return hit.servers
	}
	cfg, err := mcpserver.ServerConfigForChat(c.a, userID, chatID, full)
	if err != nil {
		slog.Warn("mcp config", "chat", chatID, "err", err)
		return nil
	}
	cfg["url"] = c.baseURL(kind) + c.a.Cfg.APIPrefix + "/mcp"
	servers := map[string]any{"agentrove": cfg}
	ttl := c.a.Cfg.MCPTokenLifetime
	c.cache[key] = cachedMCP{servers: servers, renewAt: time.Now().Add(ttl / 2)}
	return servers
}

// baseURL is where an agent in a box reaches this API: the sandbox network
// alias (AGENTROVE_INTERNAL_URL) or the public BASE_URL.
func (c *mcpConfigs) baseURL(kind string) string {
	if u := os.Getenv("AGENTROVE_INTERNAL_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return strings.TrimRight(c.a.Cfg.BaseURL, "/")
}
