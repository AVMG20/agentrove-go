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
