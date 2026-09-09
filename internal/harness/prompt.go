package harness

import (
	"strings"
	"unicode"
)

// A blocked agent's alert used to say that the agent needs somebody and not
// what it wants. The screen rule that read the prompt had the line in hand; it
// reported a fixed sentence from the manifest instead. This file is the other
// half of that read: which line the rule matched on, cleaned of the chrome a
// TUI paints around it, and what sort of block it is.

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
	rl := &m.Screen.Rule[rule]
	if rl.Kind != "" {
		return rl.Kind
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(rl.Message))
	for _, list := range [][]string{rl.All, rl.Any} {
		for _, s := range list {
			b.WriteByte(' ')
			b.WriteString(strings.ToLower(s))
		}
	}
	words := b.String()
	for _, w := range approvalWords {
		if strings.Contains(words, w) {
			return PromptKindApproval
		}
	}
	return PromptKindQuestion
}

// RulePrompt is the line of tail the matched rule read as the prompt, cleaned
// with CleanPromptLine: the first line carrying one of the rule's all[] strings,
// else the first carrying one of its any[] strings. Empty when the rule matched
// on a regex alone, or when the line is chrome all the way through.
func (r *Registry) RulePrompt(id string, rule int, tail []string) string {
	m := r.Lookup(id)
	if m == nil || rule < 0 || rule >= len(m.Screen.Rule) {
		return ""
	}
	rl := &m.Screen.Rule[rule]
	for _, list := range [][]string{rl.All, rl.Any} {
		for _, line := range tail {
			hay := line
			if m.Screen.FoldCase {
				hay = strings.ToLower(line)
			}
			for _, s := range list {
				if s == "" || !strings.Contains(hay, s) {
					continue
				}
				if clean := CleanPromptLine(line); clean != "" {
					return clean
				}
			}
		}
	}
	return ""
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
