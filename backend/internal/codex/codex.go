// Package codex runs OpenAI's Codex CLI for a turn: one `codex exec --json`
// process per turn (`codex exec resume <thread>` after the first), whose
// JSONL events are translated into the app's stream events.
//
// exec mode streams whole items (a finished message, a command and its
// output) rather than token deltas, and has no approval channel, so turns
// always run with approvals and Codex's own sandbox off: the box is the
// sandbox, as for Claude's bypassPermissions.
package codex

import (
	"encoding/json"
	"sort"
	"strings"
)

// Config is everything one turn's process needs.
type Config struct {
	Model    string
	Effort   string
	ResumeID string
	// Images are sandbox paths of images attached to the prompt.
	Images []string
	// Instructions are appended to Codex's own developer instructions.
	Instructions string
	// MCP are HTTP MCP servers added for the turn.
	MCP []MCPServer
}

// MCPServer is a streamable-HTTP MCP server. HeaderEnv maps each header to
// the env var holding its value, so tokens stay out of argv.
type MCPServer struct {
	Name      string
	URL       string
	HeaderEnv map[string]string
}

// Args returns the argv after the binary. The prompt is read from stdin
// ("-"), which must be closed after writing it.
func (c Config) Args() []string {
	var args []string
	if c.ResumeID != "" {
		args = append(args, "exec", "resume")
	} else {
		args = append(args, "exec")
	}
	// -i takes a comma-separated list; a flag must follow it so the list
	// can't swallow the positional arguments.
	if len(c.Images) > 0 {
		args = append(args, "-i", strings.Join(c.Images, ","))
	}
	args = append(args, "--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox")
	if c.Model != "" {
		args = append(args, "-m", c.Model)
	}
	if c.Effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+tomlString(c.Effort))
	}
	if c.Instructions != "" {
		args = append(args, "-c", "developer_instructions="+tomlString(c.Instructions))
	}
	for _, m := range c.MCP {
		key := "mcp_servers." + m.Name
		args = append(args, "-c", key+".url="+tomlString(m.URL))
		if len(m.HeaderEnv) > 0 {
			headers := make([]string, 0, len(m.HeaderEnv))
			for h := range m.HeaderEnv {
				headers = append(headers, h)
			}
			sort.Strings(headers)
			for i, h := range headers {
				headers[i] = tomlString(h) + "=" + tomlString(m.HeaderEnv[h])
			}
			args = append(args, "-c", key+".env_http_headers={"+strings.Join(headers, ",")+"}")
		}
	}
	if c.ResumeID != "" {
		args = append(args, c.ResumeID)
	}
	return append(args, "-")
}

// tomlString quotes s as a TOML basic string (-c values are parsed as TOML);
// JSON string escapes are a subset TOML accepts.
func tomlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
