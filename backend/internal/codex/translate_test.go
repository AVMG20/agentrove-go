package codex

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	"agentrove/internal/agentstream"
)

func replay(t *testing.T, name string) (*Translator, []string, *agentstream.Result) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	tr := NewTranslator()
	var got []string
	var res *agentstream.Result
	for _, line := range bytes.Split(data, []byte("\n")) {
		evs, r := tr.HandleLine(line)
		for _, e := range evs {
			got = append(got, agentstream.Summarize(e))
		}
		if r != nil {
			res = r
		}
	}
	return tr, got, res
}

func TestTranslateTurn(t *testing.T) {
	tr, got, res := replay(t, "turn.jsonl")
	want := []string{
		`system session=01a1745f-55b6-7272-a1b2-59be65661587`,
		`thinking "**Reading the file**"`,
		`plan [Read sample.txt:in_progress] [Report:pending]`,
		"tool_started item_2 Bash \"`cat sample.txt`\" in=map[command:cat sample.txt]",
		"tool_completed item_2 Bash \"`cat sample.txt`\" in=map[command:cat sample.txt] result=\"sample\\n\"",
		"tool_started item_3 Bash \"`false`\" in=map[command:false]",
		"tool_failed item_3 Bash \"`false`\" in=map[command:false] result=\"boom\\n\" error=\"boom\\nExit code 1\"",
		`tool_started item_4 apply_patch "Edit /home/user/projects/demo/a.go" in=map[changes:[map[kind:update path:/home/user/projects/demo/a.go]]]`,
		`tool_completed item_4 apply_patch "Edit /home/user/projects/demo/a.go" in=map[changes:[map[kind:update path:/home/user/projects/demo/a.go]]] result="Applied"`,
		`tool_started item_5 mcp__docs__search "mcp__docs__search" in=map[q:x]`,
		`tool_completed item_5 mcp__docs__search "mcp__docs__search" in=map[q:x] result="hit"`,
		`tool_started item_6 WebSearch "go generics" in=map[query:go generics]`,
		`tool_completed item_6 WebSearch "go generics" in=map[query:go generics]`,
		`plan [Read sample.txt:completed] [Report:in_progress]`,
		`text "sample"`,
		`text "\n\ndone"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events:\n got %q\nwant %q", got, want)
	}
	if res == nil || res.IsError {
		t.Fatalf("result = %+v, want success", res)
	}
	if tr.SessionID() != "01a1745f-55b6-7272-a1b2-59be65661587" || !tr.SawOutput() {
		t.Fatalf("session %q saw %v", tr.SessionID(), tr.SawOutput())
	}
}

func TestTranslateFailedTurn(t *testing.T) {
	tr, got, res := replay(t, "failed.jsonl")
	if want := []string{`system session=01a1745e-e6a5-7301-a651-146bb24251e7`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events %q, want %q", got, want)
	}
	if res == nil || !res.IsError || res.ErrorText != "The 'gpt-6-sol' model is not supported when using Codex with a ChatGPT account." {
		t.Fatalf("result = %+v", res)
	}
	if tr.SawOutput() {
		t.Fatal("a failed turn with no output must not count as output")
	}
}

func TestFinishClosesOpenTools(t *testing.T) {
	tr := NewTranslator()
	tr.HandleLine([]byte(`{"type":"item.started","item":{"id":"c1","type":"command_execution","command":"sleep 100","status":"in_progress"}}`))
	evs := tr.Finish()
	if len(evs) != 1 || evs[0].Kind != "tool_completed" {
		t.Fatalf("finish = %v", evs)
	}
	if again := tr.Finish(); len(again) != 0 {
		t.Fatalf("second finish = %v", again)
	}
}

func TestArgs(t *testing.T) {
	got := Config{Model: "gpt-6-luna", Effort: "high", Images: []string{"/a.png", "/b.png"}, Instructions: `say "hi"`}.Args()
	want := []string{"exec", "-i", "/a.png,/b.png", "--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox",
		"-m", "gpt-6-luna", "-c", `model_reasoning_effort="high"`, "-c", `developer_instructions="say \"hi\""`, "-"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args\n got %q\nwant %q", got, want)
	}
	mcp := []MCPServer{{Name: "agentrove", URL: "https://x/mcp", HeaderEnv: map[string]string{"X-Chat": "CHAT", "Authorization": "AUTH"}}}
	resumed := Config{Model: "gpt-6-luna", ResumeID: "thread-1", MCP: mcp}.Args()
	want = []string{"exec", "resume", "--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "-m", "gpt-6-luna",
		"-c", `mcp_servers.agentrove.url="https://x/mcp"`, "-c", `mcp_servers.agentrove.env_http_headers={"Authorization"="AUTH","X-Chat"="CHAT"}`,
		"thread-1", "-"}
	if !reflect.DeepEqual(resumed, want) {
		t.Fatalf("resume args\n got %q\nwant %q", resumed, want)
	}
}

func TestUnwrapShell(t *testing.T) {
	for in, want := range map[string]string{
		"/bin/bash -lc 'echo '\\''hi'\\'''": "echo 'hi'",
		`/bin/zsh -lc "ls -la"`:             "ls -la",
		"ls":                                "ls",
	} {
		if got := unwrapShell(in); got != want {
			t.Errorf("unwrapShell(%q) = %q, want %q", in, got, want)
		}
	}
}
