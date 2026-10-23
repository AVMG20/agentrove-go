package channel

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Pure text logic: mentions/audience routing, prompt building and the PASS
// detection that keeps silent members out of the channel.

const introduction = "You are %NAME%, a member of a group chat channel with the user and %OTHERS%. " +
	"You share the same working directory with the other members. Before editing files, " +
	"say what you're about to change, and don't edit files another member is working on. " +
	`You will receive new channel messages as lines of "[author]: text". ` +
	"After each batch, either post ONE short chat message, or reply with exactly `PASS` (nothing else). " +
	"Speak only when you add something new: answering a question nobody has answered well, " +
	"disagreeing, correcting, giving a concrete proposal the others missed, " +
	"or when the user addresses you or everyone. Otherwise reply `PASS`. " +
	"Never echo or agree just to agree. When the discussion has reached a conclusion " +
	"and nothing remains for you, reply `PASS`. Your reply is either exactly `PASS` " +
	"or a chat message, never both. Write like Slack: short, no headers."

const routingNote = `A line like "[author → @name]: text" is addressed only to those members; ` +
	"the rest see it as context. Answer a line addressed to you when it asks a question " +
	"or requests action or a decision from you; reply `PASS` when it is only an " +
	"acknowledgment, non-actionable status note, or closing message, even when you're " +
	"named. @mention a member only when you need something from them, never to acknowledge."

// broadcastMentions address every member.
var broadcastMentions = map[string]bool{"all": true, "everyone": true}

var mentionRE = regexp.MustCompile(`(?:^|\s)@(\S+)`)

// mentions returns the member ids @mentioned in text (sorted, excluding the
// author). all=true means @all/@everyone; an empty list means nobody.
func mentions(members []*Member, text, authorID string) (ids []string, all bool) {
	byName := map[string]string{}
	for _, m := range members {
		byName[strings.ToLower(m.DisplayName)] = m.ID
	}
	set := map[string]bool{}
	for _, match := range mentionRE.FindAllStringSubmatch(text, -1) {
		name := strings.ToLower(match[1])
		for {
			if _, ok := byName[name]; ok || name == "" {
				break
			}
			last := name[len(name)-1]
			if !strings.ContainsRune(".,:;!?", rune(last)) {
				break
			}
			name = name[:len(name)-1]
		}
		id, ok := byName[name]
		if !ok && broadcastMentions[name] {
			return nil, true
		}
		if ok && id != authorID {
			set[id] = true
		}
	}
	ids = make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, false
}

// userAudience is the stored audience of a user message: nil (broadcast)
// unless specific members were mentioned.
func userAudience(members []*Member, text string) []string {
	ids, all := mentions(members, text, "")
	if all || len(ids) == 0 {
		return nil
	}
	return ids
}

func addressed(msg *Message, memberID string) bool {
	if msg.Audience == nil {
		return true
	}
	for _, id := range msg.Audience {
		if id == memberID {
			return true
		}
	}
	return false
}

// replyAudience routes a member's reply: explicit @mentions win; otherwise it
// goes back to whoever addressed this member in the batch. nil broadcasts;
// an empty (non-nil) slice addresses only the user.
func replyAudience(members []*Member, member *Member, text string, batch []*Message) []string {
	explicit, all := mentions(members, text, member.ID)
	if all {
		return nil
	}
	if len(explicit) > 0 {
		return explicit
	}
	set := map[string]bool{}
	for _, msg := range batch {
		if !addressed(msg, member.ID) {
			continue
		}
		if msg.Audience == nil {
			return nil
		}
		for _, id := range msg.Audience {
			set[id] = true
		}
		if msg.MemberID != nil {
			set[*msg.MemberID] = true
		}
	}
	delete(set, member.ID)
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func label(msg *Message, names map[string]string) string {
	author := "user"
	if msg.MemberID != nil {
		author = nameOf(names, *msg.MemberID)
	}
	if msg.Audience == nil {
		return author
	}
	targets := make([]string, 0, len(msg.Audience))
	for _, id := range msg.Audience {
		targets = append(targets, "@"+nameOf(names, id))
	}
	to := strings.Join(targets, ", ")
	if to == "" {
		to = "user"
	}
	return author + " → " + to
}

func nameOf(names map[string]string, id string) string {
	if n, ok := names[id]; ok {
		return n
	}
	return "unknown"
}

// buildPrompt renders a batch of channel messages for one member's turn.
func buildPrompt(members []*Member, member *Member, batch []*Message) string {
	names := map[string]string{}
	for _, m := range members {
		names[m.ID] = m.DisplayName
	}
	var lines []string
	routed := false
	for _, msg := range batch {
		lines = append(lines, "["+label(msg, names)+"]: "+msg.Content)
		if msg.Audience != nil {
			routed = true
		}
	}
	if routed {
		lines = append([]string{routingNote}, lines...)
	}
	if !member.Introduced {
		var others []string
		for _, m := range members {
			if m.ID != member.ID {
				others = append(others, m.DisplayName)
			}
		}
		o := strings.Join(others, ", ")
		if o == "" {
			o = "no other agents"
		}
		intro := strings.NewReplacer("%NAME%", member.DisplayName, "%OTHERS%", o).Replace(introduction)
		lines = append([]string{intro}, lines...)
	}
	return strings.Join(lines, "\n")
}

const wrappers = "`*\"' \t\r\n"

func normalized(text string) string {
	t := strings.TrimSpace(text)
	t = strings.Trim(t, wrappers)
	t = strings.TrimSuffix(t, ".")
	t = strings.TrimSpace(t)
	return strings.Trim(t, wrappers)
}

var (
	fenceOpenRE   = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	passPrefixRE  = regexp.MustCompile(`^PASS(?:\s*[—–:(,.!]|\s+-\s)`)
	passCommaRE   = regexp.MustCompile(`(?i)^pass[,.]\s`)
	passTrailerRE = regexp.MustCompile(`(?:^|[.!?;—–)]\s*)PASS\.?$`)
)

// indentedCode reports whether the line is an indented code block line
// (leading whitespace reaching column 4, tabs expanded to 4).
func indentedCode(line string) bool {
	col := 0
	for _, r := range line {
		switch r {
		case ' ':
			col++
		case '\t':
			col = (col/4 + 1) * 4
		default:
			return col >= 4
		}
		if col >= 4 {
			return true
		}
	}
	return false
}

func closesFence(line, fence string) bool {
	trimmed := strings.TrimRightFunc(line, unicode.IsSpace)
	indent := len(trimmed) - len(strings.TrimLeft(trimmed, " "))
	if indent > 3 {
		return false
	}
	rest := trimmed[indent:]
	if len(rest) < len(fence) {
		return false
	}
	for _, r := range rest {
		if byte(r) != fence[0] || r > 127 {
			return false
		}
	}
	return true
}

// isSilent reports whether a member's reply is a PASS (or still could become
// one while streaming), ignoring fenced/indented code.
func isSilent(text string) bool {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var lines []string
	fence := ""
	for _, line := range strings.Split(text, "\n") {
		if fence != "" {
			if closesFence(line, fence) {
				fence = ""
			}
			continue
		}
		if m := fenceOpenRE.FindStringSubmatch(line); m != nil {
			fence = m[1]
			continue
		}
		if indentedCode(line) {
			continue
		}
		if n := normalized(line); n != "" {
			lines = append(lines, n)
		}
	}
	if len(lines) == 0 {
		return normalized(text) == ""
	}
	if len(lines) == 1 && strings.HasPrefix("PASS", strings.ToUpper(lines[0])) {
		return true
	}
	for _, line := range []string{lines[0], lines[len(lines)-1]} {
		if strings.ToLower(line) == "pass" || passPrefixRE.MatchString(line) || passCommaRE.MatchString(line) {
			return true
		}
	}
	return passTrailerRE.MatchString(lines[len(lines)-1])
}

var displayNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

// defaultDisplayName derives a mention-friendly name from a model name.
func defaultDisplayName(modelName string, used map[string]bool) string {
	name := strings.ToLower(strings.TrimSuffix(modelName, " (latest)"))
	name = regexp.MustCompile(`\s+`).ReplaceAllString(name, "-")
	name = regexp.MustCompile(`[^a-z0-9.-]`).ReplaceAllString(name, "")
	name = regexp.MustCompile(`-+`).ReplaceAllString(name, "-")
	name = strings.Trim(name, "-.")
	if len(name) > 32 {
		name = name[:32]
	}
	if name == "" {
		name = "claude"
	}
	display := name
	for n := 2; used[strings.ToLower(display)]; n++ {
		suffix := "-" + strconv.Itoa(n)
		base := name
		if len(base) > 32-len(suffix) {
			base = base[:32-len(suffix)]
		}
		display = base + suffix
	}
	return display
}
