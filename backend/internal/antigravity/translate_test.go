package antigravity

import (
	"bytes"
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
	tr := NewTranslator()
	_, res := replay(t, tr, []byte(`{"event":"init","conversation_id":"c1"}
{"event":"result","result":{"conversation_id":"c1","status":"ERROR","error_message":"quota exhausted"}}`))
	if res == nil || !res.IsError || res.ErrorText != "quota exhausted" {
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
