package sandboxapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"agentrove/internal/httpx"
	"agentrove/internal/sandbox"
)

const (
	searchMaxFileMatches  = 100
	searchMaxTotalMatches = 2000
	searchMaxLineLength   = 500
	searchContextBefore   = 40
	searchMaxErrorLength  = 300
)

type SearchMatch struct {
	LineNumber int    `json:"line_number"`
	LineText   string `json:"line_text"`
	MatchStart int    `json:"match_start"`
	MatchEnd   int    `json:"match_end"`
}

type SearchFileResult struct {
	Path    string        `json:"path"`
	Matches []SearchMatch `json:"matches"`
}

type SearchResponse struct {
	Results   []SearchFileResult `json:"results"`
	Truncated bool               `json:"truncated"`
}

type searchOptions struct {
	Query         string
	CaseSensitive bool
	Regex         bool
	WholeWord     bool
	Include       string
	Exclude       string
}

func rgArgs(o searchOptions) []string {
	args := []string{"--json", "-n", "--max-count=" + strconv.Itoa(searchMaxFileMatches), "--max-columns=500"}
	if !o.Regex {
		args = append(args, "-F")
	}
	if !o.CaseSensitive {
		args = append(args, "-i")
	}
	if o.WholeWord {
		args = append(args, "-w")
	}
	if o.Include != "" {
		args = append(args, "-g", o.Include)
	}
	if o.Exclude != "" {
		args = append(args, "-g", "!"+o.Exclude)
	}
	return append(args, "--", o.Query, ".")
}

// search runs ripgrep in the workspace dir rel: on the host when rg is
// installed there (works with the sandbox stopped), else inside the sandbox.
func search(ctx context.Context, sb *sandbox.Sandbox, rel, hostDir string, o searchOptions) (SearchResponse, error) {
	args := rgArgs(o)
	var stdout, stderr string
	var exit int
	if rg, err := exec.LookPath("rg"); err == nil {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(cctx, rg, args...)
		cmd.Dir = hostDir
		var so, se bytes.Buffer
		cmd.Stdout, cmd.Stderr = &so, &se
		err := cmd.Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exit = exitErr.ExitCode()
		} else if err != nil {
			return SearchResponse{}, httpx.BadRequest("Search failed: " + err.Error())
		}
		stdout, stderr = so.String(), se.String()
	} else {
		res, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: append([]string{"rg"}, args...), Cwd: sb.Resolve(rel), Timeout: 2 * time.Minute})
		if err != nil {
			return SearchResponse{}, httpx.BadRequest("Search failed: " + err.Error())
		}
		stdout, stderr, exit = res.Stdout, res.Stderr, res.ExitCode
	}
	// rg exits 1 when nothing matched.
	if exit != 0 && exit != 1 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		if msg == "" {
			msg = "unknown error"
		}
		if r := []rune(msg); len(r) > searchMaxErrorLength {
			msg = string(r[:searchMaxErrorLength]) + "…"
		}
		return SearchResponse{}, httpx.BadRequest("Search failed: " + msg)
	}
	prefix := ""
	if rel != "" {
		prefix = rel + "/"
	}
	return ParseRgJSON(stdout, searchMaxTotalMatches, searchMaxFileMatches, prefix), nil
}

type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber *int `json:"line_number"`
		Submatches []struct {
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

// ParseRgJSON keeps the first submatch of every match line, grouped by file
// (paths re-based onto prefix), capped at maxTotal matches.
func ParseRgJSON(output string, maxTotal, maxPerFile int, prefix string) SearchResponse {
	results := []SearchFileResult{}
	index := map[string]int{}
	total := 0
	truncated := false
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		var ev rgEvent
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Type != "match" {
			continue
		}
		d := ev.Data
		if d.Path.Text == "" || d.LineNumber == nil || len(d.Submatches) == 0 {
			continue
		}
		if total >= maxTotal {
			truncated = true
			break
		}
		text, start, end := PrepareMatchLine(d.Lines.Text, d.Submatches[0].Start, d.Submatches[0].End)
		m := SearchMatch{LineNumber: *d.LineNumber, LineText: text, MatchStart: start, MatchEnd: end}
		full := prefix + strings.TrimPrefix(d.Path.Text, "./")
		if i, ok := index[full]; ok {
			results[i].Matches = append(results[i].Matches, m)
		} else {
			index[full] = len(results)
			results = append(results, SearchFileResult{Path: full, Matches: []SearchMatch{m}})
		}
		total++
	}
	// rg's --max-count doesn't flag clipped files; treat exactly N as clipped.
	if !truncated {
		for _, r := range results {
			if len(r.Matches) >= maxPerFile {
				truncated = true
				break
			}
		}
	}
	return SearchResponse{Results: results, Truncated: truncated}
}

// PrepareMatchLine converts rg's UTF-8 byte offsets to code-point offsets,
// strips leading whitespace and windows very long lines around the match.
func PrepareMatchLine(lineText string, byteStart, byteEnd int) (string, int, int) {
	raw := []byte(lineText)
	bs := clamp(byteStart, 0, len(raw))
	be := clamp(byteEnd, 0, len(raw))
	if be < bs {
		be = bs
	}
	charStart := utf8.RuneCount(raw[:bs])
	charEnd := charStart + utf8.RuneCount(raw[bs:be])

	line := []rune(strings.TrimRight(lineText, "\n\r"))
	charEnd = min(charEnd, len(line))
	charStart = min(charStart, len(line))

	leading := 0
	for leading < len(line) && unicode.IsSpace(line[leading]) {
		leading++
	}
	if leading > 0 {
		line = line[leading:]
		charStart = max(0, charStart-leading)
		charEnd = max(charStart, charEnd-leading)
	}
	if len(line) <= searchMaxLineLength {
		return string(line), charStart, charEnd
	}
	windowStart := max(0, min(charStart-searchContextBefore, len(line)-searchMaxLineLength))
	windowEnd := windowStart + searchMaxLineLength
	windowed := line[windowStart:windowEnd]
	prefix, suffix := "", ""
	if windowStart > 0 {
		prefix = "…"
	}
	if windowEnd < len(line) {
		suffix = "…"
	}
	pl := utf8.RuneCountInString(prefix)
	adjStart := charStart - windowStart + pl
	adjEnd := min(charEnd-windowStart+pl, pl+len(windowed))
	return prefix + string(windowed) + suffix, adjStart, adjEnd
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }
