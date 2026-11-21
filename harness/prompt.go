package harness

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Prompt kinds. A rule names one in its manifest, or RuleKind guesses from the
// rule's own words.
const (
	PromptKindApproval = "approval"
	PromptKindQuestion = "question"
	// PromptKindAuth is a block on a login, a token or a credential. An explicit
	// report names it (OSC 7501 kind=auth, or set-agent-state kind=auth), or a
	// rule in its manifest; no rule guesses it. It is answered in the pane, never
	// from the Inbox, and it reaches the person from a quiet pane.
	PromptKindAuth = "auth"
)

var promptKinds = map[string]bool{PromptKindApproval: true, PromptKindQuestion: true, PromptKindAuth: true}

var approvalWords = []string{"approv", "permission", "allow", "proceed", "confirm", "trust"}

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
	if rl.Show == ShowDialog {
		box, ok := ruleDialog(m, rl, lines)
		if !ok {
			return ""
		}
		if msg := dialogApproval(box.inside, rl); msg != "" {
			return msg
		}
		_, inBox := promptRow(m, rl, box.inside)
		return inBox
	}
	_, clean := promptRow(m, rl, lines)
	return clean
}

// PartialSuffix ends a message built from a dialog that does not show the
// whole call: the command scrolls or is left out, the message has only the
// model's description of it, or it was cut to length. The risk rules read
// such a message as cut short, so allowing it takes a second press.
const PartialSuffix = " (not all shown)"

func dialogApproval(box []string, rl *ScreenRule) string {
	if rl.ToolField == "" {
		return ""
	}
	tool := dialogField(box, rl.ToolField)
	if tool == "" || strings.ContainsAny(tool, " \t") {
		return ""
	}
	what, partial := "", false
	for _, label := range rl.WhatFields {
		if label == WhatBody {
			what, partial = dialogBody(box, rl.ToolField)
		} else {
			what = dialogField(box, label)
		}
		if what != "" {
			break
		}
	}
	if what == "" {
		for _, label := range rl.HintFields {
			if what = dialogField(box, label); what != "" {
				partial = true
				break
			}
		}
	}
	if what == "" {
		return "approve " + tool
	}
	msg := "approve " + tool + ": " + what
	if n := utf8.RuneCountInString(msg); partial || n > maxPromptRunes {
		keep := maxPromptRunes - utf8.RuneCountInString(PartialSuffix)
		if r := []rune(msg); len(r) > keep {
			msg = strings.TrimSpace(string(r[:keep]))
		}
		msg += PartialSuffix
	}
	return msg
}

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
		return cleanLine(val, maxDialogRunes)
	}
	return ""
}

const maxDialogRunes = 4 * maxPromptRunes

func leadingSpaces(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
}

func dialogBody(box []string, firstField string) (body string, partial bool) {
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
		if !hasField {
			continue
		}
		var parts []string
		for _, blk := range blocks[min(i+1, len(blocks)):max(len(blocks)-2, i+1)] {
			for _, line := range blk {
				if strings.ContainsAny(line, dialogScrollbar) {
					partial = true
				}
				if t := cleanLine(line, maxDialogRunes); t != "" {
					parts = append(parts, t)
				}
			}
		}
		return cleanLine(strings.Join(parts, " "), maxDialogRunes), partial
	}
	return "", false
}

const dialogScrollbar = "┃│"

// RuleShowsDialog reports whether a screen rule reads a dialog (show =
// "dialog").
func (r *Registry) RuleShowsDialog(id string, rule int) bool {
	m := r.Lookup(id)
	return m != nil && rule >= 0 && rule < len(m.Screen.Rule) && m.Screen.Rule[rule].Show == ShowDialog
}

// RuleLines returns the lines of tail that screen rule rule of harness id
// reads: its region, as Classify reads it. nil for an unknown harness or rule.
func (r *Registry) RuleLines(id string, rule int, tail []string) []string {
	m := r.Lookup(id)
	if m == nil || rule < 0 || rule >= len(m.Screen.Rule) {
		return nil
	}
	return regionLines(tail, m.Screen.Rule[rule].Region)
}

// RuleDialog returns the inside of the one dialog a show = "dialog" screen
// rule reads on its region of tail, each row cut to the dialog's columns. ok
// is false when the dialog is not on the screen, when more than one box could
// be it, or when the rule shows no dialog.
func (r *Registry) RuleDialog(id string, rule int, tail []string) (inside []string, ok bool) {
	m := r.Lookup(id)
	if m == nil || rule < 0 || rule >= len(m.Screen.Rule) || m.Screen.Rule[rule].Show != ShowDialog {
		return nil, false
	}
	rl := &m.Screen.Rule[rule]
	box, ok := ruleDialog(m, rl, regionLines(tail, rl.Region))
	if !ok {
		return nil, false
	}
	return box.inside, true
}

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
	return cleanLine(s, maxPromptRunes)
}

func cleanLine(s string, limit int) string {
	var b strings.Builder
	b.Grow(len(s))
	space := true
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
			continue
		case r == '❯' || r == '▶' || r == '›':
			continue
		}
		b.WriteRune(r)
		space = false
		if n++; n >= limit {
			break
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimSpace(strings.TrimPrefix(out, ">"))
	return out
}
