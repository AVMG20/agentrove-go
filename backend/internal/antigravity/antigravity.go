// Package antigravity runs Google's Antigravity CLI (`agy`) for a turn: one
// `agy -p <prompt> --output-format stream-json` process per turn, resumed
// with `--conversation <id>` after the first, whose NDJSON step updates are
// translated into the app's stream events.
//
// Print mode can't ask for approval, so turns skip permission prompts: the
// box is the sandbox, as for Claude's bypassPermissions.
package antigravity

import "fmt"

// MaxPromptBytes keeps the prompt (passed as one argv string) under Linux's
// per-argument limit (MAX_ARG_STRLEN, 128KiB).
const MaxPromptBytes = 120 << 10

// Config is everything one turn's process needs.
type Config struct {
	Model    string
	ResumeID string
}

// Args returns the argv after the binary.
func (c Config) Args(prompt string) ([]string, error) {
	if len(prompt) > MaxPromptBytes {
		return nil, fmt.Errorf("the message is too long for Antigravity (%d KB max)", MaxPromptBytes>>10)
	}
	args := []string{"-p", prompt, "--output-format", "stream-json", "--dangerously-skip-permissions"}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	if c.ResumeID != "" {
		args = append(args, "--conversation", c.ResumeID)
	}
	return args, nil
}
