package skills

import (
	"errors"
	"strconv"
	"strings"
)

// ParseFrontmatter extracts the top-level scalar fields of a SKILL.md YAML
// frontmatter block. It covers what skills use for name/description: plain
// scalars (with loose continuation lines, which the Python parser normalized
// into a literal block), quoted scalars and |/> block scalars.
func ParseFrontmatter(content string) (map[string]string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, errors.New("Content must start with YAML frontmatter (---)")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, errors.New("YAML frontmatter must end with ---")
	}
	body := lines[1:end]
	out := map[string]string{}
	for i := 0; i < len(body); {
		line := strings.TrimRight(body[i], "\r")
		key, value, ok := topLevelField(line)
		if !ok {
			i++
			continue
		}
		// Collect the indented/continuation lines that belong to this key.
		j := i + 1
		for j < len(body) {
			next := strings.TrimRight(body[j], "\r")
			if _, _, isField := topLevelField(next); isField {
				break
			}
			j++
		}
		cont := make([]string, 0, j-i-1)
		for _, l := range body[i+1 : j] {
			cont = append(cont, strings.TrimRight(l, "\r"))
		}
		out[key] = scalarValue(value, cont)
		i = j
	}
	return out, nil
}

// topLevelField matches `key: value` at column 0. Like the Python normalizer,
// only known keys end a description/name continuation, but any identifier at
// column 0 followed by ':' starts a new field otherwise.
func topLevelField(line string) (string, string, bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
		return "", "", false
	}
	k, v, ok := strings.Cut(line, ":")
	if !ok || k == "" {
		return "", "", false
	}
	for i, c := range k {
		if !(c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9')) {
			return "", "", false
		}
	}
	if v != "" && v[0] != ' ' && v[0] != '\t' {
		return "", "", false
	}
	return k, strings.TrimSpace(v), true
}

func scalarValue(value string, cont []string) string {
	switch {
	case strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">"):
		return blockScalar(value, cont)
	case strings.HasPrefix(value, `"`):
		joined := value
		if !strings.HasSuffix(strings.TrimSpace(value), `"`) || len(strings.TrimSpace(value)) == 1 {
			for _, c := range cont {
				joined += " " + strings.TrimSpace(c)
			}
		}
		if s, err := strconv.Unquote(strings.TrimSpace(joined)); err == nil {
			return s
		}
		return strings.Trim(strings.TrimSpace(joined), `"`)
	case strings.HasPrefix(value, "'"):
		joined := value
		for _, c := range cont {
			if strings.HasSuffix(strings.TrimSpace(value), "'") && len(strings.TrimSpace(value)) > 1 {
				break
			}
			joined += " " + strings.TrimSpace(c)
		}
		joined = strings.TrimSpace(joined)
		joined = strings.TrimPrefix(joined, "'")
		joined = strings.TrimSuffix(joined, "'")
		return strings.ReplaceAll(joined, "''", "'")
	}
	value = stripComment(value)
	var nonEmpty []string
	for _, c := range cont {
		if strings.TrimSpace(c) != "" {
			nonEmpty = append(nonEmpty, c)
		}
	}
	if len(nonEmpty) == 0 {
		return value
	}
	// Loose continuation lines: the Python normalizer rewrote these as a
	// `|-` literal block, keeping line breaks.
	parts := []string{}
	if value != "" {
		parts = append(parts, value)
	}
	for _, c := range cont {
		parts = append(parts, strings.TrimSpace(c))
	}
	return strings.TrimRight(strings.Join(parts, "\n"), "\n")
}

func stripComment(v string) string {
	if i := strings.Index(v, " #"); i >= 0 {
		return strings.TrimSpace(v[:i])
	}
	return v
}

// blockScalar renders a literal (|) or folded (>) block with clip/strip/keep
// chomping.
func blockScalar(header string, cont []string) string {
	literal := header[0] == '|'
	chomp := ""
	if strings.Contains(header, "-") {
		chomp = "-"
	} else if strings.Contains(header, "+") {
		chomp = "+"
	}
	indent := -1
	for _, c := range cont {
		if strings.TrimSpace(c) == "" {
			continue
		}
		n := len(c) - len(strings.TrimLeft(c, " "))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	if indent < 0 {
		return ""
	}
	var rows []string
	for _, c := range cont {
		if len(c) >= indent {
			rows = append(rows, c[indent:])
		} else {
			rows = append(rows, "")
		}
	}
	// Trailing blank lines only matter for keep chomping.
	trailing := 0
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1]
		trailing++
	}
	var text string
	if literal {
		text = strings.Join(rows, "\n")
	} else {
		var b strings.Builder
		for i, r := range rows {
			if i > 0 {
				switch {
				case r == "":
					b.WriteString("\n")
				case rows[i-1] != "":
					b.WriteString(" ")
				}
			}
			b.WriteString(r)
		}
		text = b.String()
	}
	switch chomp {
	case "-":
		return text
	case "+":
		return text + strings.Repeat("\n", trailing+1)
	}
	return text + "\n"
}
