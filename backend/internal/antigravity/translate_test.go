package antigravity

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"agentrove/internal/agentstream"
)

func replay(t *testing.T, tr *Translator, data []byte) ([]string, *agentstream.Result) {
	t.Helper()
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
	return got, res
}

func TestTranslateTurn(t *testing.T) {
	data, err := os.ReadFile("testdata/turn.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	tr := NewTranslator()
	got, res := replay(t, tr, data)
	want := []string{
		`system session=585876e0-4aa4-477e-9cd0-783d20e7a22a`,
		`usage 6839/<nil>`,
		`tool_started agy-step-2 Read "Read /home/user/projects/demo/sample.txt" in=map[file_path:/home/user/projects/demo/sample.txt]`,
		`tool_completed agy-step-2 Read "Read /home/user/projects/demo/sample.txt" in=map[file_path:/home/user/projects/demo/sample.txt] result="1 lines, 7 bytes"`,
		`text "The content of sample.txt"`,
		`text " is: sample"`,
		`text "\n"`,
		`usage 8801/<nil>`,
		"tool_started agy-step-4 Bash \"`go test ./...`\" in=map[command:go test ./...]",
		"tool_failed agy-step-4 Bash \"`go test ./...`\" in=map[command:go test ./...] result=\"FAIL\" error=\"exit status 1\"",
		`tool_started agy-step-5 list_dir "list dir" in=map[DirectoryPath:/home/user/projects/demo]`,
		`text "Done."`,
		// list_dir never finished: closed out when the result arrives.
		`tool_completed agy-step-5 list_dir "list dir" in=map[DirectoryPath:/home/user/projects/demo]`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events:\n got %q\nwant %q", got, want)
	}
	if res == nil || res.IsError {
		t.Fatalf("result = %+v, want success", res)
	}
	if tr.SessionID() != "585876e0-4aa4-477e-9cd0-783d20e7a22a" || !tr.SawOutput() {
		t.Fatalf("session %q saw %v", tr.SessionID(), tr.SawOutput())
	}
}

func TestSeparateResponsesGetAParagraphBreak(t *testing.T) {
	tr := NewTranslator()
	got, _ := replay(t, tr, []byte(`{"event":"step_update","step_update":{"step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"one"}}
{"event":"step_update","step_update":{"step_index":2,"state":"DONE","step_type":"agent_response","text_delta":"two"}}
{"event":"step_update","step_update":{"step_index":3,"state":"ACTIVE","step_type":"thinking","text_delta":"hmm"}}`))
	want := []string{`text "one"`, `text "\n\ntwo"`, `thinking "hmm"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events %q, want %q", got, want)
	}
}

func TestFailedResult(t *testing.T) {
	// agy reports the failure in result.error.
	data, err := os.ReadFile("testdata/auth_failed.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	tr := NewTranslator()
	got, res := replay(t, tr, data)
	if res == nil || !res.IsError || res.ErrorText != "authentication failed or timed out" || res.Subtype != "error" {
		t.Fatalf("result = %+v", res)
	}
	if want := []string{"system session=6d1f0c2a-4c1e-4f7e-9a51-3b2f6f0d8e11"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events %q, want %q", got, want)
	}
	// An object error and the older error_message field read the same way.
	tr = NewTranslator()
	_, res = replay(t, tr, []byte(`{"event":"result","result":{"status":"ERROR","error":{"message":"quota exhausted"}}}`))
	if res == nil || res.ErrorText != "quota exhausted" {
		t.Fatalf("result = %+v", res)
	}
	tr = NewTranslator()
	_, res = replay(t, tr, []byte(`{"event":"result","result":{"status":"ERROR","error_message":"quota exhausted"}}`))
	if res == nil || res.ErrorText != "quota exhausted" {
		t.Fatalf("result = %+v", res)
	}
	tr = NewTranslator()
	_, res = replay(t, tr, []byte(`{"event":"result","result":{"status":"CANCELLED"}}`))
	if res == nil || !res.IsError || !strings.Contains(res.ErrorText, "cancelled") {
		t.Fatalf("result = %+v", res)
	}
}

func TestArgs(t *testing.T) {
	got, err := Config{Model: "gemini-3.8-flash-high", ResumeID: "c1"}.Args("hi")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-p", "hi", "--output-format", "stream-json", "--dangerously-skip-permissions", "--model", "gemini-3.8-flash-high", "--conversation", "c1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args %q, want %q", got, want)
	}
	if _, err := (Config{}).Args(strings.Repeat("x", MaxPromptBytes+1)); err == nil {
		t.Fatal("an oversized prompt must be refused")
	}
}

// liveTurn replays a captured agy 1.4.7 stream (sanitized) and collects the
// text, Bash cards and usage it turns into.
type liveTurn struct {
	text     string
	response string
	session  []string
	usage    []any
	bash     []map[string]any // tool_completed / tool_failed payloads
	started  int
	result   *agentstream.Result
}

func replayLive(t *testing.T, name string) (*Translator, liveTurn) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	tr := NewTranslator()
	var lt liveTurn
	for _, line := range bytes.Split(data, []byte("\n")) {
		var raw map[string]any
		if json.Unmarshal(line, &raw) == nil {
			if r, ok := raw["result"].(map[string]any); ok {
				lt.response, _ = r["response"].(string)
			}
		}
		evs, r := tr.HandleLine(line)
		for _, e := range evs {
			p := e.Payload
			switch e.Kind {
			case "assistant_text":
				lt.text += p["text"].(string)
			case "system":
				lt.session = append(lt.session, p["data"].(map[string]any)["session_id"].(string))
			case "usage":
				lt.usage = append(lt.usage, p["data"].(map[string]any)["input_tokens"])
			case "tool_started":
				lt.started++
			case "tool_completed", "tool_failed":
				lt.bash = append(lt.bash, p["tool"].(map[string]any))
			}
		}
		if r != nil {
			lt.result = r
		}
	}
	return tr, lt
}

func TestLiveReplyTextMatchesResponse(t *testing.T) {
	tr, lt := replayLive(t, "live_reply.jsonl")
	// The DONE step's own text_delta ("\n") is kept, and the result's
	// response is not emitted again on top of the deltas.
	if lt.text != "hello\n" || lt.text != lt.response {
		t.Fatalf("text %q, response %q", lt.text, lt.response)
	}
	const id = "3594d304-5212-4591-ab60-72a063b04d10"
	if !reflect.DeepEqual(lt.session, []string{id}) || tr.SessionID() != id {
		t.Fatalf("session events %q, SessionID %q", lt.session, tr.SessionID())
	}
	if !reflect.DeepEqual(lt.usage, []any{6830}) {
		t.Fatalf("usage %v", lt.usage)
	}
	if lt.result == nil || lt.result.IsError || lt.started != 0 {
		t.Fatalf("result %+v, tools %d", lt.result, lt.started)
	}
}

func TestLiveToolsBecomeBashCards(t *testing.T) {
	tr, lt := replayLive(t, "live_tools.jsonl")
	if lt.text == "" || lt.text != lt.response {
		t.Fatalf("text %q\nresponse %q", lt.text, lt.response)
	}
	if !strings.HasSuffix(lt.text, "There are **6** files in total.\n") {
		t.Fatalf("final DONE delta dropped: %q", lt.text)
	}
	const id = "424ae29e-44dc-4960-8898-8f46951d1e11"
	if !reflect.DeepEqual(lt.session, []string{id}) || tr.SessionID() != id {
		t.Fatalf("session events %q, SessionID %q", lt.session, tr.SessionID())
	}
	if !reflect.DeepEqual(lt.usage, []any{6843, 7653, 7949}) {
		t.Fatalf("usage %v", lt.usage)
	}
	if lt.started != 2 || len(lt.bash) != 2 {
		t.Fatalf("started %d, finished %d", lt.started, len(lt.bash))
	}
	want := []struct{ command, first, last string }{
		{"ls -la", "total 12\n", "turn3.jsonl\n"},
		{"find . -maxdepth 1 -type f", "./turn3.jsonl\n", "./turn3.err\n"},
	}
	for i, w := range want {
		card := lt.bash[i]
		in := card["input"].(map[string]any)
		out, _ := card["result"].(string)
		if card["name"] != "Bash" || card["status"] != "completed" || in["command"] != w.command {
			t.Fatalf("card %d = %v", i, card)
		}
		// The exit-code header is dropped and the PTY's CRLF normalized.
		if !strings.HasPrefix(out, w.first) || !strings.HasSuffix(out, w.last) || strings.Contains(out, "\r") {
			t.Fatalf("card %d output %q", i, out)
		}
	}
	if lt.result == nil || lt.result.IsError {
		t.Fatalf("result %+v", lt.result)
	}
}

func TestCommandOutput(t *testing.T) {
	cases := []struct {
		in   string
		code int
		out  string
		ok   bool
	}{
		{"Command exited with code 0.\n\nCommand output:\na\r\nb\r\n", 0, "a\nb\n", true},
		{"Command exited with code 2.\n\nCommand output:\nboom\r\n", 2, "boom\n", true},
		{"Command exited with code 0.\n", 0, "", true},
		{"FAIL", 0, "FAIL", false},
	}
	for _, c := range cases {
		code, out, ok := commandOutput(c.in)
		if code != c.code || out != c.out || ok != c.ok {
			t.Errorf("commandOutput(%q) = %d %q %v", c.in, code, out, ok)
		}
	}
}

func TestFailedCommandFailsTheCard(t *testing.T) {
	tr := NewTranslator()
	lines := []string{
		`{"event":"step_update","step_update":{"step_index":1,"state":"ACTIVE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"false"}}}}`,
		`{"event":"step_update","step_update":{"step_index":1,"state":"DONE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"false"},"output":"Command exited with code 1.\n\nCommand output:\nnope\r\n"}}}`,
	}
	got, _ := replay(t, tr, []byte(strings.Join(lines, "\n")))
	last := got[len(got)-1]
	if last != "tool_failed agy-step-1 Bash \"`false`\" in=map[command:false] result=\"nope\\n\" error=\"Exit code 1\"" {
		t.Fatalf("got %q", got)
	}
}

func TestMapToolAgentroveMCP(t *testing.T) {
	if got := mapTool("x", "mcp_agentrove_create_artifact", nil).Name; got != "mcp__agentrove__create_artifact" {
		t.Fatalf("name = %q", got)
	}
	if got := mapTool("x", "mcp_github_list_issues", nil).Name; got != "mcp_github_list_issues" {
		t.Fatalf("other server renamed: %q", got)
	}
}
