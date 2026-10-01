package harness

import (
	"strings"
	"unicode"
)

// A blocked agent's alert says what the agent wants, not only that it needs
// somebody. The screen rule that reads the prompt has the line in hand, and
// this file turns it into the alert: which line the rule matched on, cleaned
// of the chrome a TUI paints around it, and what sort of block it is.

// Prompt kinds. A rule names one in its manifest, or RuleKind guesses from the
// rule's own words.
const (
	PromptKindApproval = "approval"
	PromptKindQuestion = "question"
)

var promptKinds = map[string]bool{PromptKindApproval: true, PromptKindQuestion: true}

// approvalWords are the words that mark a prompt as a yes-or-no on something the
// agent proposed rather than a question wanting an answer in words. They are
// checked against the rule's message and its predicate strings, lowercased.
var approvalWords = []string{"approv", "permission", "allow", "proceed", "confirm", "trust"}

// maxPromptRunes bounds what one prompt line may carry into a state message. A
// prompt is one line of a pane, and the rail and the dock both cut it again to
// their own width; the cap is so a rule matching a wall of text cannot push a
// screenful through the state sync on every settle.
const maxPromptRunes = 160

// RuleKind says whether a rule reads an approval or a question. The manifest's
// own word wins; otherwise the rule's message and predicates are read for the
// words that mean approval, and anything else is a question.
func (r *Registry) RuleKind(id string, rule int) string {
	m := r.Lookup(id)
	if m == nil || rule < 0 || rule >= len(m.Screen.Rule) {
		return ""
	}
	return ruleKind(&m.Screen.Rule[rule])
}

// TitleRuleKind is RuleKind for a title rule.
func (r *Registry) TitleRuleKind(id string, rule int) string {
	m := r.Lookup(id)
	if m == nil || rule < 0 || rule >= len(m.Title.Rule) {
		return ""
	}
	return ruleKind(&m.Title.Rule[rule])
}

// NotifyRuleKind is RuleKind for a notify rule.
func (r *Registry) NotifyRuleKind(id string, rule int) string {
	m := r.Lookup(id)
	if m == nil || rule < 0 || rule >= len(m.Notify.Rule) {
		return ""
	}
	return ruleKind(&m.Notify.Rule[rule])
}

// GuessPromptKind reads free text, such as the message a hook reported with
// needs_input, for the words that mean approval. It is the guess RuleKind makes
// for a rule that names no kind, applied to text that came with no rule. Empty
// text says nothing and gets no kind.
func GuessPromptKind(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return kindOfWords(strings.ToLower(text))
}

// ruleKind is the kind a rule names, or the guess from its own words.
func ruleKind(rl *ScreenRule) string {
	if rl.Kind != "" {
		return rl.Kind
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(rl.Message))
	for _, s := range rl.positiveStrings(nil) {
		b.WriteByte(' ')
		b.WriteString(strings.ToLower(s))
	}
	return kindOfWords(b.String())
}

// kindOfWords is approval when lowercased text carries one of approvalWords,
// and question otherwise.
func kindOfWords(words string) string {
	for _, w := range approvalWords {
		if strings.Contains(words, w) {
			return PromptKindApproval
		}
	}
	return PromptKindQuestion
}

// RulePrompt is the line of tail the matched rule read as the prompt, cleaned
// with CleanPromptLine: the first line carrying one of the rule's all[]
// strings, else one of its any[] strings, else one its nested groups need
// present. Empty when the rule matched on patterns alone, or when the line is
// chrome all the way through.
func (r *Registry) RulePrompt(id string, rule int, tail []string) string {
	m := r.Lookup(id)
	if m == nil || rule < 0 || rule >= len(m.Screen.Rule) {
		return ""
	}
	rl := &m.Screen.Rule[rule]
	lines := regionLines(tail, rl.Region)
	row, clean := promptRow(m, rl, lines)
	if rl.Show == ShowDialog && row >= 0 {
		// The row runs through the box and the chat on either side of it.
		// Inside the box, the same search finds the words alone.
		if box, ok := dialogAround(lines, row); ok {
			if msg := dialogApproval(box, rl.ToolField, rl.WhatFields); msg != "" {
				return msg
			}
			if _, inBox := promptRow(m, rl, box); inBox != "" {
				return inBox
			}
		}
	}
	return clean
}

// dialogApproval is the message tool_field and what_fields build from a
// dialog's lines, "approve <tool>: <what>", or "approve <tool>" when the
// dialog shows none of what_fields. Empty when the dialog shows no tool.
func dialogApproval(box []string, toolField string, whatFields []string) string {
	if toolField == "" {
		return ""
	}
	tool := dialogField(box, toolField)
	if tool == "" || strings.ContainsAny(tool, " \t") {
		return ""
	}
	for _, label := range whatFields {
		what := ""
		if label == WhatBody {
			what = dialogBody(box, toolField)
		} else {
			what = dialogField(box, label)
		}
		if what != "" {
			return CleanPromptLine("approve " + tool + ": " + what)
		}
	}
	return "approve " + tool
}

// dialogField is the value of the first field line with this label, cleaned,
// or "". A value the dialog wrapped goes on in the lines under it that are
// indented further than the label; a value with no space in it (a path, a
// URL) is joined back without one.
func dialogField(box []string, label string) string {
	for i, line := range box {
		word, rest, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !strings.EqualFold(word, label) {
			continue
		}
		val := strings.TrimSpace(rest)
		indent := leadingSpaces(line)
		for _, more := range box[i+1:] {
			t := strings.TrimSpace(more)
			if t == "" || leadingSpaces(more) <= indent {
				break
			}
			if strings.Contains(val, " ") {
				val += " "
			}
			val += t
		}
		return CleanPromptLine(val)
	}
	return ""
}

// leadingSpaces counts the spaces a line starts with.
func leadingSpaces(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
}

// dialogBody is the first line of the dialog's body: the block of lines,
// between blank lines, that follows the block holding the first field line,
// when that block is not one of the dialog's last two. Crush lays a dialog
// out as a title, its fields, a body, its buttons and a help line, and a
// narrow dialog leaves the body out. dialogInside keeps one blank line of
// each run.
func dialogBody(box []string, firstField string) string {
	var blocks [][]string
	var cur []string
	for _, line := range box {
		if strings.TrimSpace(line) == "" {
			if cur != nil {
				blocks = append(blocks, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, line)
	}
	if cur != nil {
		blocks = append(blocks, cur)
	}
	for i, b := range blocks {
		hasField := false
		for _, line := range b {
			w, _, _ := strings.Cut(strings.TrimSpace(line), " ")
			if strings.EqualFold(w, firstField) {
				hasField = true
			}
		}
		if hasField {
			if i+1 < len(blocks)-2 {
				return CleanPromptLine(blocks[i+1][0])
			}
			return ""
		}
	}
	return ""
}

// promptRow is the index in lines of the line RulePrompt reads as the prompt,
// with that line cleaned, or -1 and "" when no line holds one.
func promptRow(m *Manifest, rl *ScreenRule, lines []string) (int, string) {
	for _, list := range [][]string{rl.All, rl.Any, rl.nestedPositiveStrings()} {
		for i, line := range lines {
			hay := line
			if m.Screen.FoldCase {
				hay = strings.ToLower(line)
			}
			for _, s := range list {
				if s == "" || !strings.Contains(hay, s) {
					continue
				}
				if clean := CleanPromptLine(line); clean != "" {
					return i, clean
				}
			}
		}
	}
	return -1, ""
}

// CleanPromptLine strips what a TUI paints around a prompt so the words can be
// read as chrome elsewhere: control runes, the box the prompt sits in, the
// cursor mark in front of it, and the runs of space the box left behind. The
// result is trimmed and capped at maxPromptRunes.
func CleanPromptLine(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := true // leading space is dropped, so start as if one was just seen
	n := 0
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
			continue
		case r >= 0x2500 && r <= 0x259f:
			// Box drawing and block elements: the frame around the prompt.
			continue
		case r == '❯' || r == '▶' || r == '›':
			// The cursor marks agent TUIs put in front of the live line.
			continue
		}
		b.WriteRune(r)
		space = false
		if n++; n >= maxPromptRunes {
			break
		}
	}
	out := strings.TrimSpace(b.String())
	// A bare ">" at the front is the same cursor mark in ASCII.
	out = strings.TrimSpace(strings.TrimPrefix(out, ">"))
	return out
}
