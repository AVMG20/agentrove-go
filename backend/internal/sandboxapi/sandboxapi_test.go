package sandboxapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentrove/internal/sandbox"
)

func TestParseLsFiles(t *testing.T) {
	got := ParseLsFiles("a.txt\x00src/lib/x.go\x00src/img.PNG\x00")
	want := []string{"a.txt:file", "src:directory", "src/lib:directory", "src/lib/x.go:file", "src/img.PNG:file"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, g := range got {
		if g.Path+":"+g.Type != want[i] {
			t.Errorf("%d: %s:%s want %s", i, g.Path, g.Type, want[i])
		}
	}
	if got[4].IsBinary == nil || !*got[4].IsBinary || *got[0].IsBinary || got[1].IsBinary != nil {
		t.Errorf("binary flags wrong: %+v", got)
	}
}

func TestEncodeContent(t *testing.T) {
	if c, bin := EncodeContent("a.png", []byte{0, 1}); !bin || c != base64.StdEncoding.EncodeToString([]byte{0, 1}) {
		t.Errorf("png: %q %v", c, bin)
	}
	if c, bin := EncodeContent("a.txt", []byte("hé\xff")); bin || c != "hé\uFFFD" {
		t.Errorf("txt: %q %v", c, bin)
	}
}

func TestPrepareMatchLine(t *testing.T) {
	cases := []struct {
		line       string
		bs, be     int
		text       string
		start, end int
	}{
		{"hello world\n", 6, 11, "hello world", 6, 11},
		{"    indented foo\n", 13, 16, "indented foo", 9, 12},
		{"héllo wörld", 7, 13, "héllo wörld", 6, 11}, // byte -> code point offsets
	}
	for _, c := range cases {
		text, s, e := PrepareMatchLine(c.line, c.bs, c.be)
		if text != c.text || s != c.start || e != c.end {
			t.Errorf("PrepareMatchLine(%q,%d,%d) = %q,%d,%d", c.line, c.bs, c.be, text, s, e)
		}
	}
	long := strings.Repeat("a", 1000) + "NEEDLE" + strings.Repeat("b", 1000)
	text, s, e := PrepareMatchLine(long, 1000, 1006)
	if !strings.HasPrefix(text, "…") || !strings.HasSuffix(text, "…") || []rune(text)[s] != 'N' || string([]rune(text)[s:e]) != "NEEDLE" {
		t.Errorf("windowed: start=%d end=%d", s, e)
	}
}

func TestParseRgJSON(t *testing.T) {
	ev := func(path string, line int, text string, s, e int) string {
		b, _ := json.Marshal(map[string]any{"type": "match", "data": map[string]any{
			"path": map[string]any{"text": path}, "lines": map[string]any{"text": text},
			"line_number": line, "submatches": []any{map[string]any{"start": s, "end": e}},
		}})
		return string(b)
	}
	out := strings.Join([]string{
		`{"type":"begin","data":{}}`,
		ev("./src/a.go", 3, "foo bar\n", 0, 3),
		ev("./src/a.go", 9, "x foo\n", 2, 5),
		ev("b.go", 1, "foo\n", 0, 3),
		`not json`,
	}, "\n")
	res := ParseRgJSON(out, 2000, 100, ".worktrees/abc/")
	if len(res.Results) != 2 || res.Results[0].Path != ".worktrees/abc/src/a.go" || len(res.Results[0].Matches) != 2 || res.Truncated {
		t.Fatalf("got %+v", res)
	}
	if res.Results[0].Matches[1].MatchStart != 2 || res.Results[0].Matches[1].LineNumber != 9 {
		t.Errorf("match = %+v", res.Results[0].Matches[1])
	}
	if r := ParseRgJSON(out, 1, 100, ""); !r.Truncated || len(r.Results) != 1 {
		t.Errorf("total cap: %+v", r)
	}
	if r := ParseRgJSON(out, 2000, 2, ""); !r.Truncated {
		t.Errorf("per-file cap should flag truncation")
	}
}

func TestSplitUTF8(t *testing.T) {
	euro := []byte("€") // 3 bytes
	cases := []struct {
		in   []byte
		text string
		rest int
	}{
		{[]byte("abc"), "abc", 0},
		{append([]byte("a"), euro[:2]...), "a", 2},
		{append([]byte("a"), euro...), "a€", 0},
		{[]byte{'a', 0xff, 'b'}, "a\uFFFDb", 0},
		{euro[:1], "", 1},
	}
	for _, c := range cases {
		text, rest := SplitUTF8(c.in)
		if text != c.text || len(rest) != c.rest {
			t.Errorf("SplitUTF8(%v) = %q, %v", c.in, text, rest)
		}
	}
}

func TestParseDimension(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", 24}, {"40", 40}, {"0", 1}, {"9999", 500}, {"40.5", 24}, {`"40"`, 24}, {"true", 24}, {"null", 24},
	}
	for _, c := range cases {
		if got := ParseDimension(json.RawMessage(c.raw), 24, 1, 500); got != c.want {
			t.Errorf("ParseDimension(%s) = %d want %d", c.raw, got, c.want)
		}
	}
}

func TestTmux(t *testing.T) {
	if n := TmuxSessionName("ab-cd", "terminal-1", ".worktrees/abcd1234"); n != "agentrove_ab_cd_terminal_1__worktrees_abcd1234" {
		t.Errorf("name = %s", n)
	}
	if n := TmuxSessionName("x", "terminal-1", ""); n != "agentrove_x_terminal_1" {
		t.Errorf("name = %s", n)
	}
	cmd := BuildTmuxCommand("s1", "/bin/zsh", map[string]string{"HOME": "/h"}, []string{"GITHUB_TOKEN"})
	for _, want := range []string{"new -A -e 'HOME=/h' -s 's1'", "update-environment 'DISPLAY KRB5CCNAME SSH_ASKPASS SSH_AUTH_SOCK SSH_AGENT_PID SSH_CONNECTION WINDOWID XAUTHORITY GITHUB_TOKEN'", "|| exec '/bin/zsh'"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command missing %q:\n%s", want, cmd)
		}
	}
}

func TestHostFileOps(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}
	sb := &sandbox.Sandbox{Kind: sandbox.KindDocker, Root: ws, WorkspacePath: ws}

	if err := writeFile(sb, "dir/sub/a.txt", []byte("hi")); err != nil {
		t.Fatal(err)
	}
	fc, err := readFile(sb, "dir/sub/a.txt")
	if err != nil || fc.Content != "hi" || fc.IsBinary {
		t.Errorf("read back = %+v %v", fc, err)
	}
	if _, err := readFile(sb, "link/secret"); err == nil {
		t.Error("read through escaping symlink must fail")
	}
	if err := writeFile(sb, "link/evil", []byte("x")); err == nil {
		t.Error("write through escaping symlink must fail")
	}
	if _, err := readFile(sb, "dir"); err == nil {
		t.Error("reading a directory must fail")
	}
}

// The agent controls file sizes; opening a huge file must not load it.
func TestReadFileSizeCap(t *testing.T) {
	ws := t.TempDir()
	sb := &sandbox.Sandbox{Kind: sandbox.KindDocker, Root: "/home/user/workspace", WorkspacePath: ws}
	big := filepath.Join(ws, "big.txt")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxReadBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := readFile(sb, "big.txt"); !errors.Is(err, errTooLarge) {
		t.Errorf("oversized file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "ok.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fc, err := readFile(sb, "ok.txt"); err != nil || fc.Content != "hi" {
		t.Errorf("small file = %+v, %v", fc, err)
	}
}
