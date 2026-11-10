package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"agentrove/internal/models"
)

// ContextWindow is the model's context size; unknown ids get a conservative default.
func ContextWindow(id string) int {
	if m, ok := models.LookupModel(id); ok {
		return m.ContextWindow
	}
	return 200_000
}

// Effort validates a thinking mode; "" means the CLI default.
func Effort(mode *string) (string, bool) {
	if mode == nil || *mode == "" {
		return "", true
	}
	for _, e := range models.EffortLevels {
		if e == *mode {
			return e, true
		}
	}
	return "", false
}

// RecordModels feeds the model list of a CLI's initialize reply into the
// model picker. Bedrock and Vertex logins list provider-specific ids, so only
// first-party replies count.
func RecordModels(initReply json.RawMessage) {
	var r struct {
		Version string            `json:"claude_code_version"`
		Models  []models.CLIModel `json:"models"`
		Account struct {
			APIProvider string `json:"apiProvider"`
		} `json:"account"`
	}
	if json.Unmarshal(initReply, &r) != nil || r.Version == "" {
		return
	}
	if p := r.Account.APIProvider; p != "" && p != "firstParty" {
		return
	}
	models.ReportClaudeCLI(r.Version, r.Models)
}

// ProbeModels starts bin without any login and returns its initialize reply,
// which lists the models that CLI release offers.
func ProbeModels(ctx context.Context, bin string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	tmp, err := os.MkdirTemp("", "agentrove-probe-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	cmd := exec.CommandContext(ctx, bin, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose")
	cmd.Dir = tmp
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tmp, "CLAUDE_CONFIG_DIR=" + filepath.Join(tmp, "config"), "DISABLE_AUTOUPDATER=1"}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if _, err := fmt.Fprintln(stdin, `{"type":"control_request","request_id":"probe","request":{"subtype":"initialize","hooks":null}}`); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var m struct {
			Type     string `json:"type"`
			Response struct {
				Subtype  string          `json:"subtype"`
				Error    string          `json:"error"`
				Response json.RawMessage `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Type != "control_response" {
			continue
		}
		if m.Response.Subtype != "success" {
			return nil, fmt.Errorf("claude initialize: %s", m.Response.Error)
		}
		return m.Response.Response, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("claude exited before initializing")
}
