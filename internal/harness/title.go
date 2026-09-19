package harness

import "strings"

// The pane's window title as evidence, and the one rule that makes it safe to
// read.
//
// A title is a short string the program publishes about itself with OSC 0 or
// OSC 2, and the agents already use it: Claude Code puts a spinner there while
// it works, Codex writes "Action Required" there when it is blocked. tuios
// parsed the sequence and kept the string for the window's name, and no tier
// ever looked at it.
//
// The catch is what a title is made of. A screen is prose, so matching a
// substring anywhere in it is right. A title is mostly paths, branches and
// program names, and a substring test against those finds the agent's own name
// inside words that are not it: a pane sitting in ~/src/opencode-blinker
// matches a rule for "opencode", and the false positive arrives wearing the
// right label. So a title predicate has to match a whole token.

// tokenByte reports whether b continues a word. The hyphen is in here, which is
// the whole point: "opencode-blinker" has to fail a rule for "opencode", and it
// only fails if the hyphen is a letter as far as the boundary is concerned.
// Paths are split on their separators because a rule for a program name should
// still match ~/bin/opencode.
func tokenByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '_' || b == '-' || b == '.':
		return true
	}
	return false
}

// containsToken reports whether needle appears in hay bounded by non-word
// bytes on both sides. An empty needle is not a match: a rule that names an
// empty string is asking for every pane, which is what checkRule refuses a
// rule with no predicates for.
//
// A needle that itself begins or ends with a non-word byte is matched plainly
// at that end, because the boundary it would be held to is already written
// into the rule. That is what lets a rule say "] " or "> " and mean it.
func containsToken(hay, needle string) bool {
	if needle == "" {
		return false
	}
	headBound := tokenByte(needle[0])
	tailBound := tokenByte(needle[len(needle)-1])

	for from := 0; ; {
		i := strings.Index(hay[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(needle)
		beforeOK := !headBound || i == 0 || !tokenByte(hay[i-1])
		afterOK := !tailBound || end == len(hay) || !tokenByte(hay[end])
		if beforeOK && afterOK {
			return true
		}
		// Advance by one rather than by the needle: overlapping candidates are
		// rare but real, and skipping the whole needle would step over the one
		// that is bounded.
		from = i + 1
	}
}

// ClassifyTitle matches a harness's title rules against a pane's window title
// and returns the state the best matching rule names.
//
// It mirrors Classify and differs in two ways: the haystack is one string
// rather than the bottom of a screen, and a substring has to be a whole token.
// A miss returns ok=false and never a state, for the reason Classify does: a
// rule written against one release of an agent's TUI has to degrade to no
// opinion rather than to a confident wrong one.
func (r *Registry) ClassifyTitle(id, title string) (state string, rule int, ok bool) {
	m := r.Lookup(id)
	if m == nil || !m.Title.Enabled || len(m.Title.Rule) == 0 || title == "" {
		return "", -1, false
	}

	folded := title
	if m.Title.FoldCase {
		folded = strings.ToLower(title)
	}

	best, bestIdx := "", -1
	bestPri := 0
	for i := range m.Title.Rule {
		rl := &m.Title.Rule[i]
		if !checkRule(rl, title, folded, nil, containsToken) {
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

// ExplainTitle is ClassifyTitle with the working shown, for the same reason
// Explain exists: a rule is matched inside a daemon against a string nobody
// can see, and writing one was otherwise guesswork.
func (r *Registry) ExplainTitle(id, title string) (state string, rule int, reports []RuleReport) {
	m := r.Lookup(id)
	if m == nil || len(m.Title.Rule) == 0 {
		return "", -1, nil
	}
	folded := title
	if m.Title.FoldCase {
		folded = strings.ToLower(title)
	}

	reports = make([]RuleReport, 0, len(m.Title.Rule))
	bestIdx, bestPri := -1, 0
	for i := range m.Title.Rule {
		rl := &m.Title.Rule[i]
		rep := RuleReport{Index: i, State: rl.State, Priority: rl.Priority}
		rep.Matched = checkRule(rl, title, folded, &rep, containsToken)
		reports = append(reports, rep)
		if !rep.Matched || !m.Title.Enabled || title == "" {
			continue
		}
		if bestIdx == -1 || rl.Priority > bestPri {
			state, bestIdx, bestPri = rl.State, i, rl.Priority
		}
	}
	return state, bestIdx, reports
}

// TitleRuleMessage is what a title claim says about itself.
func (r *Registry) TitleRuleMessage(id string, rule int) string {
	m := r.Lookup(id)
	if m == nil || rule < 0 || rule >= len(m.Title.Rule) {
		return ""
	}
	return m.Title.Rule[rule].Message
}
