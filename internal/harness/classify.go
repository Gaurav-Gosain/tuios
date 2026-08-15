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

	// Joined once rather than per predicate: a rule carries several strings and
	// every one of them would otherwise walk the slice again.
	hay := strings.Join(tail, "\n")

	best, bestIdx := "", -1
	bestPri := 0
	for i := range m.Screen.Rule {
		rl := &m.Screen.Rule[i]
		if !checkRule(rl, hay, nil) {
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

// checkRule applies the three predicates: every string in All present, at least
// one in Any present, none in Not present. An empty list is satisfied, so a rule
// carrying only Any is an "any of these".
//
// A non-nil rep collects why each predicate refused, which is what a person
// writing a rule needs and what classification does not. Passing nil skips every
// allocation, so the diagnostic costs the hot path nothing and neither of them
// carries a second copy of the predicates.
func checkRule(rl *ScreenRule, hay string, rep *RuleReport) bool {
	ok := true
	for _, s := range rl.All {
		if strings.Contains(hay, s) {
			continue
		}
		ok = false
		if rep == nil {
			return false
		}
		rep.Missing = append(rep.Missing, s)
	}
	for _, s := range rl.Not {
		if !strings.Contains(hay, s) {
			continue
		}
		ok = false
		if rep == nil {
			return false
		}
		rep.Blocked = append(rep.Blocked, s)
	}
	if len(rl.Any) == 0 {
		// A rule with no Any and no All says nothing about the screen, and would
		// match every pane the harness runs in.
		if len(rl.All) == 0 {
			if rep != nil {
				rep.Empty = true
			}
			return false
		}
		return ok
	}
	for _, s := range rl.Any {
		if strings.Contains(hay, s) {
			return ok
		}
	}
	if rep != nil {
		rep.NoneOf = rl.Any
	}
	return false
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
	// NoneOf is the any[] list when the screen contains none of it.
	NoneOf []string `json:"none_of,omitempty"`
	// Blocked lists the not[] strings the screen does contain. Each one alone is
	// enough to refuse.
	Blocked []string `json:"blocked,omitempty"`
	// Empty marks a rule that names no strings at all, which would otherwise
	// match every pane the harness runs in and is refused for that reason.
	Empty bool `json:"empty,omitempty"`
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
	hay := strings.Join(tail, "\n")
	reports = make([]RuleReport, 0, len(m.Screen.Rule))
	best, bestIdx, bestPri := "", -1, 0
	for i := range m.Screen.Rule {
		rl := &m.Screen.Rule[i]
		rep := RuleReport{Index: i, State: rl.State, Priority: rl.Priority}
		rep.Matched = checkRule(rl, hay, &rep)
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
