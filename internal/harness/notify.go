package harness

import "strings"

// A desktop notification as evidence.
//
// A harness that wants its user sends a desktop notification: OSC 9 from
// iTerm2's vocabulary, OSC 777 from urxvt's, or OSC 99 from kitty's. Claude
// Code sends "Claude needs your permission to use Bash" that way, and Codex
// sends "Approval requested" before a command and a line of its answer when a
// turn ends. It is the harness speaking about itself on purpose, which makes it
// the same kind of evidence a title is, and it is filed under the same source.
//
// The rules have the shape of title rules and are matched as prose, the way a
// screen is: a notification is a sentence, not a path, so a substring anywhere
// in it counts. The title and the body are read as one text, title first.

// NotifyText joins a notification's title and body into the text the rules
// read.
func NotifyText(title, body string) string {
	title, body = strings.TrimSpace(title), strings.TrimSpace(body)
	switch {
	case title == "":
		return body
	case body == "":
		return title
	default:
		return title + "\n" + body
	}
}

// ClassifyNotify matches a harness's notify rules against a notification and
// returns the state the best matching rule names, with its index. A miss is
// ok=false and never a state, for the reason Classify gives.
func (r *Registry) ClassifyNotify(id, text string) (state string, rule int, ok bool) {
	m := r.Lookup(id)
	if m == nil || !m.Notify.Enabled || len(m.Notify.Rule) == 0 || text == "" {
		return "", -1, false
	}
	folded := text
	if m.Notify.FoldCase {
		folded = strings.ToLower(text)
	}
	best, bestIdx, bestPri := "", -1, 0
	for i := range m.Notify.Rule {
		rl := &m.Notify.Rule[i]
		if !checkRule(rl, text, folded, nil, strings.Contains) {
			continue
		}
		if bestIdx == -1 || rl.Priority > bestPri {
			best, bestIdx, bestPri = rl.State, i, rl.Priority
		}
	}
	if bestIdx == -1 {
		return "", -1, false
	}
	return best, bestIdx, true
}

// NotifyRuleMessage is what a notify claim says about itself: the
// notification's own words, cleaned and capped like a prompt line, fronted by
// the rule's kind when it names one. A notification with no readable words
// falls back to the rule's message.
func (r *Registry) NotifyRuleMessage(id string, rule int, text string) string {
	m := r.Lookup(id)
	if m == nil || rule < 0 || rule >= len(m.Notify.Rule) {
		return ""
	}
	rl := &m.Notify.Rule[rule]
	words := CleanPromptLine(strings.ReplaceAll(text, "\n", ": "))
	if words == "" {
		return rl.Message
	}
	if rl.Kind != "" {
		return rl.Kind + ": " + words
	}
	return words
}
