package harness

import (
	"strconv"
	"strings"
)

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
	return r.ClassifyOSC(id, title, "")
}

// ClassifyOSC is ClassifyTitle with the pane's last OSC 9;4 progress report as
// well, for the title-block rules that read osc_progress. progress is written
// as ProgressText writes it, and empty when the pane has sent none.
func (r *Registry) ClassifyOSC(id, title, progress string) (state string, rule int, ok bool) {
	m := r.Lookup(id)
	if m == nil || !m.Title.Enabled || len(m.Title.Rule) == 0 || (title == "" && progress == "") {
		return "", -1, false
	}
	text := newOSCText(title, progress, m.Title.FoldCase)
	return firstMatch(m.Title.order, m.Title.Rule, func(rl *ScreenRule) bool {
		hay, folded := text.get(rl.Region)
		return hay != "" && checkRule(rl, hay, folded, nil, containsToken)
	})
}

// oscText holds the two strings title rules read, each folded once when the
// block folds case.
type oscText struct {
	title, titleFolded       string
	progress, progressFolded string
}

func newOSCText(title, progress string, foldCase bool) oscText {
	t := oscText{title: title, titleFolded: title, progress: progress, progressFolded: progress}
	if foldCase {
		t.titleFolded = strings.ToLower(title)
		t.progressFolded = strings.ToLower(progress)
	}
	return t
}

func (t oscText) get(region string) (hay, folded string) {
	if region == RegionOSCProgress {
		return t.progress, t.progressFolded
	}
	return t.title, t.titleFolded
}

// HasProgressRules reports whether the harness has an enabled title-block rule
// reading osc_progress. The daemon asks before it maps a progress report onto
// a state by the sequence's published meaning, so a harness that uses the
// sequence its own way is read by its own rules instead.
func (r *Registry) HasProgressRules(id string) bool {
	m := r.Lookup(id)
	if m == nil || !m.Title.Enabled {
		return false
	}
	for i := range m.Title.Rule {
		if m.Title.Rule[i].Region == RegionOSCProgress {
			return true
		}
	}
	return false
}

// ProgressText writes an OSC 9;4 progress report the way osc_progress rules
// read it, which is the payload after "9;" as herdr keeps it: "4;<state>" for
// the states whose percentage means nothing (0 remove, 3 indeterminate) and
// "4;<state>;<percent>" for the others (1 set, 2 error, 4 paused).
func ProgressText(state, percent int) string {
	if state == 3 || state == 0 {
		return "4;" + strconv.Itoa(state)
	}
	return "4;" + strconv.Itoa(state) + ";" + strconv.Itoa(percent)
}

// ExplainTitle is ClassifyTitle with the working shown, for the same reason
// Explain exists: a rule is matched inside a daemon against a string nobody
// can see, and writing one was otherwise guesswork.
func (r *Registry) ExplainTitle(id, title string) (state string, rule int, reports []RuleReport) {
	return r.ExplainOSC(id, title, "")
}

// ExplainOSC is ClassifyOSC with the working shown.
func (r *Registry) ExplainOSC(id, title, progress string) (state string, rule int, reports []RuleReport) {
	m := r.Lookup(id)
	if m == nil || len(m.Title.Rule) == 0 {
		return "", -1, nil
	}
	text := newOSCText(title, progress, m.Title.FoldCase)
	reports = make([]RuleReport, 0, len(m.Title.Rule))
	bestIdx, bestPri := -1, 0
	for i := range m.Title.Rule {
		rl := &m.Title.Rule[i]
		rep := RuleReport{Index: i, State: rl.State, Priority: rl.Priority, Region: rl.Region}
		hay, folded := text.get(rl.Region)
		if hay == "" {
			rep.NoRegion = true
		} else {
			rep.Matched = checkRule(rl, hay, folded, &rep, containsToken)
		}
		if rl.Region != "" {
			rep.Text = reportText(hay)
		}
		reports = append(reports, rep)
		if !rep.Matched || !m.Title.Enabled {
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
