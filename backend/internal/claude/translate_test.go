package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func loadFixture(t *testing.T, name string) []Message {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []Message
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		m, ok, err := parseLine(sc.Bytes())
		if err != nil {
			t.Fatalf("parse %q: %v", sc.Text(), err)
		}
		if ok {
			out = append(out, m)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// summarize reduces an event to the fields the frontend keys on.
func summarize(e Event) string {
	switch e.Kind {
	case "assistant_text":
		return fmt.Sprintf("text %q", e.Payload["text"])
	case "assistant_thinking":
		return fmt.Sprintf("thinking %q", e.Payload["thinking"])
	case "usage":
		d := e.Payload["data"].(map[string]any)
		return fmt.Sprintf("usage %v/%v", d["input_tokens"], d["context_window"])
	case "system":
		d := e.Payload["data"].(map[string]any)
		return fmt.Sprintf("system session=%v", d["session_id"])
	case "plan":
		entries := e.Payload["data"].(map[string]any)["entries"].([]map[string]any)
		s := "plan"
		for _, en := range entries {
			s += fmt.Sprintf(" [%v:%v]", en["content"], en["status"])
		}
		return s
	case "tool_started", "tool_completed", "tool_failed":
		tool := e.Payload["tool"].(map[string]any)
		s := fmt.Sprintf("%s %v %v %q", e.Kind, tool["id"], tool["status"], tool["title"])
		if tool["input"] != nil {
			s += " +input"
		}
		if r, ok := tool["result"]; ok {
			s += fmt.Sprintf(" result=%q", r)
		}
		if er, ok := tool["error"]; ok {
			s += fmt.Sprintf(" error=%q", er)
		}
		return s
	}
	return e.Kind
}

func TestTranslatorReplaysTurn(t *testing.T) {
	tr := NewTranslator()
	tr.SetContextWindow(200000)
	var got []string
	var res *Result
	for _, m := range loadFixture(t, "turn.jsonl") {
		evs, r := tr.Handle(m)
		for _, e := range evs {
			got = append(got, summarize(e))
		}
		if r != nil {
			res = r
		}
	}
	want := []string{
		"system session=sess-1",
		"usage 120/200000",
		`thinking "Need deps first."`,
		`text "Installing"`,
		`text " now."`,
		`tool_started toolu_bash started "Bash"`,
		`tool_started toolu_bash started "Install dependencies" +input`,
		`tool_completed toolu_bash completed "Install dependencies" +input result="added 10 packages"`,
		"plan [Install deps:completed] [Run tests:in_progress]",
		`tool_started toolu_read started "Read /home/user/workspace/missing.txt" +input`,
		"usage 170/200000",
		`tool_failed toolu_read failed "Read /home/user/workspace/missing.txt" +input error="File does not exist."`,
		`text "Done."`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events:\n got  %q\n want %q", got, want)
	}
	if res == nil {
		t.Fatal("no result")
	}
	if res.IsError || res.Subtype != "success" || res.TotalCostUSD != 0.0123 || res.ContextWindow != 200000 {
		t.Fatalf("result = %+v", *res)
	}
	if !tr.SawOutput() {
		t.Fatal("SawOutput = false")
	}
}

func TestTranslatorClosesOpenToolsOnErrorResult(t *testing.T) {
	tr := NewTranslator()
	msgs := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"sleep 100"}}]}}`,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["Request was aborted."]}`,
	}
	var got []string
	var res *Result
	for _, line := range msgs {
		m, ok, err := parseLine([]byte(line))
		if !ok || err != nil {
			t.Fatalf("parse: %v", err)
		}
		evs, r := tr.Handle(m)
		for _, e := range evs {
			got = append(got, summarize(e))
		}
		res = r
	}
	want := []string{
		"tool_started toolu_1 started \"`sleep 100`\" +input",
		"tool_completed toolu_1 completed \"`sleep 100`\" +input",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events:\n got  %q\n want %q", got, want)
	}
	if res == nil || !res.IsError || res.ErrorText != "Request was aborted." {
		t.Fatalf("result = %+v", res)
	}
}

func TestParseLineSkipsNoise(t *testing.T) {
	for _, line := range []string{"", "   \n", "npm WARN deprecated\n"} {
		if _, ok, err := parseLine([]byte(line)); ok || err != nil {
			t.Fatalf("%q: ok=%v err=%v", line, ok, err)
		}
	}
	if _, ok, err := parseLine([]byte("{broken")); ok || err == nil {
		t.Fatalf("broken json: ok=%v err=%v", ok, err)
	}
	m, ok, _ := parseLine([]byte(`  {"type":"result","subtype":"success"}` + "\n"))
	if !ok || m.Type != "result" || string(m.Raw) != `{"type":"result","subtype":"success"}` {
		t.Fatalf("got %+v", m)
	}
}

// ToolsRunning tracks main-thread tool calls only, until their results.
func TestToolsRunning(t *testing.T) {
	tr := NewTranslator()
	parent := "toolu_agent"
	tr.toolStarted(parent, "Agent", nil, nil)
	tr.toolStarted("toolu_sub", "Bash", nil, &parent)
	if !tr.ToolsRunning() {
		t.Fatal("main-thread tool not running")
	}
	tr.user(Message{Message: json.RawMessage(`{"content":[{"type":"tool_result","tool_use_id":"toolu_agent","content":"Async agent launched"}]}`)})
	if tr.ToolsRunning() {
		t.Fatal("a subagent's tool counted as the turn's")
	}
}
