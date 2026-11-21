package harness

import "strings"

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
	return firstMatch(m.Notify.order, m.Notify.Rule, func(rl *ScreenRule) bool {
		return checkRule(rl, text, folded, nil, strings.Contains)
	})
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
