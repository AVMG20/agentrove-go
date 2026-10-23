// Package mcpserver is the AgentRove MCP server over Streamable HTTP
// (stateless JSON-RPC 2.0 on POST /api/v1/mcp). It replaces the Python stdio
// orchestration server and the artifacts MCP: a token for audience
// auth.AudienceMCP gets every tool, an auth.AudienceArtifactAgent token only
// the artifact tools. Neither is accepted by auth.Service.Require, so a token
// leaked from a box can't drive the rest of the API. Tools call the Go
// services directly.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"agentrove/internal/app"
	"agentrove/internal/artifact"
	"agentrove/internal/auth"
	"agentrove/internal/automation"
	"agentrove/internal/channel"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
)

const (
	ProtocolVersion = "2025-06-18"
	// ChatIDHeader names the chat the calling session runs in.
	ChatIDHeader = "X-Agentrove-Chat-Id"
	serverName   = "agentrove"
	serverVer    = "1.0.0"
)

var supportedVersions = map[string]bool{"2025-06-18": true, "2025-03-26": true, "2024-11-05": true}

type Module struct {
	a           *app.App
	channels    *channel.Service
	automations *automation.Service
	artifacts   *artifact.Service
	tools       []*tool
}

// Instructions lands in Claude's system prompt, so it knows artifacts exist
// even before it loads the tool definitions.
const Instructions = "An artifact is an HTML page published with create_artifact: it gets a link the user can open " +
	"and share. When the user asks for an artifact, or for a page, report, dashboard, chart or diagram to view or share, " +
	"use the artifacts skill and publish an artifact instead of writing an .html file. Artifacts are private until " +
	"the user makes them public, so publishing needs no confirmation."

func New(a *app.App, ch *channel.Service, au *automation.Service, ar *artifact.Service) *Module {
	m := &Module{a: a, channels: ch, automations: au, artifacts: ar}
	m.tools = m.buildTools()
	return m
}

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix + "/mcp"
	for _, path := range []string{p, p + "/{$}"} {
		mux.HandleFunc("POST "+path, m.serve)
		// Stateless server: no server-initiated SSE stream, no sessions.
		mux.HandleFunc("GET "+path, methodNotAllowed)
		mux.HandleFunc("DELETE "+path, methodNotAllowed)
	}
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "POST")
	httpx.JSON(w, http.StatusMethodNotAllowed, map[string]any{"detail": "Method Not Allowed"})
}

// ServerConfig is the Claude --mcp-config entry for a user's session.
// fullAccess selects every tool; otherwise only the artifact tools.
func ServerConfig(a *app.App, userID string, fullAccess bool) (map[string]any, error) {
	return ServerConfigForChat(a, userID, "", fullAccess)
}

// ServerConfigForChat also tells the server which chat the session runs in
// (get_current_chat, artifact linking, self-stop guards).
func ServerConfigForChat(a *app.App, userID, chatID string, fullAccess bool) (map[string]any, error) {
	audience, ttl := auth.AudienceArtifactAgent, a.Cfg.MCPTokenLifetime
	if fullAccess {
		audience = auth.AudienceMCP
	}
	token, err := a.Keys.IssueChatToken(userID, audience, chatID, ttl)
	if err != nil {
		return nil, err
	}
	// Docker sandboxes may not reach BASE_URL (e.g. localhost on the host).
	base := os.Getenv("AGENTROVE_INTERNAL_URL")
	if base == "" {
		base = a.Cfg.BaseURL
	}
	headers := map[string]any{"Authorization": "Bearer " + token}
	if chatID != "" {
		headers[ChatIDHeader] = chatID
	}
	return map[string]any{
		"type":    "http",
		"url":     strings.TrimRight(base, "/") + a.Cfg.APIPrefix + "/mcp",
		"headers": headers,
	}, nil
}

// caller is the authenticated MCP client.
type caller struct {
	userID     string
	chatID     string
	fullAccess bool
}

func (m *Module) authenticate(r *http.Request) *caller {
	h := r.Header.Get("Authorization")
	scheme, token, _ := strings.Cut(h, " ")
	token = strings.TrimSpace(token)
	if !strings.EqualFold(scheme, "bearer") || token == "" {
		return nil
	}
	c := &caller{}
	if u, err := m.a.Auth.Authenticate(r.Context(), token); err == nil {
		// A session token already grants the whole API, so its header is
		// believed.
		c.userID, c.fullAccess = u.ID, true
		c.chatID = strings.TrimSpace(r.Header.Get(ChatIDHeader))
		return c
	}
	// Agent tokens carry their chat; the header is box-supplied and ignored.
	claims, err := m.a.Keys.VerifyClaims(token, auth.AudienceMCP)
	if err == nil {
		c.fullAccess = true
	} else if claims, err = m.a.Keys.VerifyClaims(token, auth.AudienceArtifactAgent); err != nil {
		return nil
	}
	u, err := m.a.Auth.GetUser(r.Context(), claims.Subject)
	if err != nil || !u.IsActive {
		return nil
	}
	c.userID, c.chatID = u.ID, claims.Chat
	return c
}

// JSON-RPC 2.0 wire types.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

func (m *Module) serve(w http.ResponseWriter, r *http.Request) {
	c := m.authenticate(r)
	if c == nil {
		w.Header().Set("WWW-Authenticate", "Bearer")
		httpx.JSON(w, http.StatusUnauthorized, map[string]any{"detail": "Could not validate credentials"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "Request body too large or unreadable"}})
		return
	}
	resp, ok := m.Handle(r.Context(), c, body)
	if !ok {
		// Only notifications/responses: acknowledged without a body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeRPC(w, resp)
}

func writeRPC(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// Handle dispatches one JSON-RPC message or batch. ok=false means there is
// nothing to send back (notifications only).
func (m *Module) Handle(ctx context.Context, c *caller, body []byte) (any, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			return rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "Parse error"}}, true
		}
		if len(batch) == 0 {
			return rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeInvalidRequest, "Empty batch"}}, true
		}
		var out []rpcResponse
		for _, raw := range batch {
			if resp, ok := m.handleOne(ctx, c, raw); ok {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	}
	resp, ok := m.handleOne(ctx, c, trimmed)
	return resp, ok
}

func (m *Module) handleOne(ctx context.Context, c *caller, raw json.RawMessage) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "Parse error"}}, true
	}
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	if req.Method == "" {
		// A client response to a server request (we never send any) or junk.
		if isNotification {
			return rpcResponse{}, false
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{codeInvalidRequest, "Invalid Request"}}, true
	}
	result, rerr := m.dispatch(ctx, c, req.Method, req.Params)
	if isNotification {
		return rpcResponse{}, false
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		if result == nil {
			result = map[string]any{}
		}
		resp.Result = result
	}
	return resp, true
}

func (m *Module) dispatch(ctx context.Context, c *caller, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		version := ProtocolVersion
		if supportedVersions[p.ProtocolVersion] {
			version = p.ProtocolVersion
		}
		res := map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVer},
		}
		if m.a.Cfg.ArtifactsEnabled {
			res["instructions"] = Instructions
		}
		return res, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := []map[string]any{}
		for _, t := range m.visibleTools(c) {
			list = append(list, map[string]any{"name": t.name, "description": t.description, "inputSchema": t.schema})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
			return nil, &rpcError{codeInvalidParams, "tools/call needs a tool name"}
		}
		t := m.findTool(c, p.Name)
		if t == nil {
			return nil, &rpcError{codeInvalidParams, "Unknown tool: " + p.Name}
		}
		args := p.Arguments
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage("{}")
		}
		return m.callTool(ctx, c, t, args), nil
	default:
		if strings.HasPrefix(method, "notifications/") {
			return nil, nil
		}
		return nil, &rpcError{codeMethodNotFound, "Method not found: " + method}
	}
}

func (m *Module) visibleTools(c *caller) []*tool {
	var out []*tool
	for _, t := range m.tools {
		if m.allowed(c, t) {
			out = append(out, t)
		}
	}
	return out
}

func (m *Module) allowed(c *caller, t *tool) bool {
	if t.artifact {
		return m.a.Cfg.ArtifactsEnabled
	}
	return c.fullAccess && m.a.Cfg.MCPEnabled
}

func (m *Module) findTool(c *caller, name string) *tool {
	for _, t := range m.tools {
		if t.name == name && m.allowed(c, t) {
			return t
		}
	}
	return nil
}

// toolError is a failure reported to the model (isError result), not a
// protocol error.
type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

func errorf(format string, args ...any) error { return &toolError{fmt.Sprintf(format, args...)} }

func (m *Module) callTool(ctx context.Context, c *caller, t *tool, args json.RawMessage) (result map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("mcp tool panicked", "tool", t.name, "err", r)
			result = errorResult("Internal error in " + t.name)
		}
	}()
	out, err := t.handler(ctx, c, args)
	if err != nil {
		return errorResult(errorMessage(err))
	}
	text, merr := json.Marshal(out)
	if merr != nil {
		return errorResult("Failed to encode result: " + merr.Error())
	}
	res := map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": false}
	if obj, ok := out.(map[string]any); ok {
		res["structuredContent"] = obj
	}
	return res
}

func errorResult(msg string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
}

func errorMessage(err error) string {
	var he *httpx.Error
	if errors.As(err, &he) {
		return fmt.Sprint(he.Detail)
	}
	var te *toolError
	if errors.As(err, &te) {
		return te.msg
	}
	slog.Warn("mcp tool failed", "err", err)
	return err.Error()
}

// resolveModel defaults to the first Claude model and rejects unknown ids.
func resolveModel(id *string) (string, error) {
	if id == nil || *id == "" {
		return models.ClaudeModels[0].ID, nil
	}
	if _, ok := models.LookupModel(*id); !ok {
		return "", errorf("Unknown model_id: %s", *id)
	}
	return *id, nil
}
