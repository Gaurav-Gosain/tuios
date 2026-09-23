package harness

import "strings"

// Classify matches a harness's screen rules against the bottom of a pane and
// returns the state the best matching rule names, with the index of that rule so
// a diagnostic can say which one fired.
//
// A miss returns ok=false and never a state. A rule is written against one
// agent's TUI at one version, and agent TUIs change in patch releases, so a rule
// that stops matching has to degrade to no opinion. Falling back to a state here
// would turn a stale rule into a confident lie, which is worse than the silence
// it replaces.
//
// Rules are considered highest priority first, and ties go to the one declared
// first, so a manifest reads in the order it is evaluated.
func (r *Registry) Classify(id string, tail []string) (state string, rule int, ok bool) {
	m := r.Lookup(id)
	if m == nil || !m.Screen.Enabled || len(m.Screen.Rule) == 0 || len(tail) == 0 {
		return "", -1, false
	}

	// Joined once per region rather than per predicate: a rule carries several
	// strings and every one of them would otherwise walk the slice again. The
	// folded copy is likewise made once per scan, not per rule, and only when
	// the manifest asks.
	// Rules are tried highest priority first, so the first match is the answer
	// and no rule that could not win is read at all.
	text := newRegionText(tail, m.Screen.FoldCase)
	return firstMatch(m.Screen.order, m.Screen.Rule, func(rl *ScreenRule) bool {
		hay, folded, ok := text.get(rl.Region, rl.substrings)
		return ok && checkRule(rl, hay, folded, nil, strings.Contains)
	})
}

// RuleReport says what one rule made of a pane's screen, and when it refused,
// which strings were the reason.
type RuleReport struct {
	Index    int    `json:"index"`
	State    string `json:"state"`
	Priority int    `json:"priority"`
	Matched  bool   `json:"matched"`
	// Missing lists the all[] strings the screen does not contain. Each one
	// alone is enough to refuse.
	Missing []string `json:"missing,omitempty"`
	// MissingRegex lists the regex[] patterns that matched nothing.
	MissingRegex []string `json:"missing_regex,omitempty"`
	// NoneOf is the any[] list when the screen contains none of it.
	NoneOf []string `json:"none_of,omitempty"`
	// Blocked lists the not[] strings the screen does contain. Each one alone is
	// enough to refuse.
	Blocked []string `json:"blocked,omitempty"`
	// BlockedRegex lists the not_regex[] patterns that matched.
	BlockedRegex []string `json:"blocked_regex,omitempty"`
	// Groups names each nested group that refused, by its path in the rule:
	// "all_of[1] did not match", "no any_of group matched", "none_of[0]
	// matched".
	Groups []string `json:"groups,omitempty"`
	// Empty marks a rule that names no strings at all, which would otherwise
	// match every pane the harness runs in and is refused for that reason.
	Empty bool `json:"empty,omitempty"`
	// Region is the part of the screen the rule reads, empty for the whole
	// tail.
	Region string `json:"region,omitempty"`
	// NoRegion marks a rule whose region is not on the screen at all, such as
	// a prompt_box rule on a screen with no input box.
	NoRegion bool `json:"no_region,omitempty"`
	// Text is what the rule read: its region's lines joined with newlines, cut
	// to maxReportText bytes. Omitted for a rule reading the whole tail, which
	// the caller already has.
	Text string `json:"text,omitempty"`
}

// maxReportText bounds the region text one report carries, so an explanation of
// a manifest with many narrow rules stays a readable size.
const maxReportText = 2048

// reportText is region text cut for a report.
func reportText(s string) string {
	if len(s) <= maxReportText {
		return s
	}
	cut := maxReportText
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "..."
}

// Explain classifies tail and reports what every rule made of it.
//
// It exists because writing a screen rule was otherwise guesswork: the rule is
// matched against text nobody can see, inside a daemon, against a pane that has
// already moved on by the time anyone looks. This answers both halves at once,
// what the classifier read and what each rule did with it, which turns adding a
// harness into an edit and a re-run.
func (r *Registry) Explain(id string, tail []string) (state string, rule int, reports []RuleReport) {
	m := r.Lookup(id)
	if m == nil {
		return "", -1, nil
	}
	text := newRegionText(tail, m.Screen.FoldCase)
	reports = make([]RuleReport, 0, len(m.Screen.Rule))
	best, bestIdx, bestPri := "", -1, 0
	for i := range m.Screen.Rule {
		rl := &m.Screen.Rule[i]
		rep := RuleReport{Index: i, State: rl.State, Priority: rl.Priority, Region: rl.Region}
		hay, folded, ok := text.get(rl.Region, true)
		if ok {
			rep.Matched = checkRule(rl, hay, folded, &rep, strings.Contains)
			if rl.Region != "" {
				rep.Text = reportText(hay)
			}
		} else {
			rep.NoRegion = true
		}
		reports = append(reports, rep)
		if !rep.Matched || !m.Screen.Enabled || len(tail) == 0 {
			continue
		}
		if bestIdx == -1 || rl.Priority > bestPri {
			best, bestIdx, bestPri = rl.State, i, rl.Priority
		}
	}
	return best, bestIdx, reports
}

// RuleMessage is the reason a harness's rule reports with its state, or a
// default in the rule's state's own words when the manifest gives none.
func (r *Registry) RuleMessage(id string, rule int) string {
	m := r.Lookup(id)
	if m == nil || rule < 0 || rule >= len(m.Screen.Rule) {
		return ""
	}
	if msg := m.Screen.Rule[rule].Message; msg != "" {
		return msg
	}
	if m.Screen.Rule[rule].State == "needs_input" {
		return "Waits for your answer on the screen."
	}
	return ""
}

// ScreenLines is how many lines from the bottom this harness's rules see, or the
// default when the manifest does not say. Callers read the tail before they know
// whether any rule will match, so this has to be answerable without classifying.
func (r *Registry) ScreenLines(id string) int {
	m := r.Lookup(id)
	if m == nil || !m.Screen.Enabled {
		return 0
	}
	if m.Screen.Lines > 0 {
		return m.Screen.Lines
	}
	return defaultScreenLines
}
