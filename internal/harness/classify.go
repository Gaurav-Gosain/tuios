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
		if !ruleMatches(rl, hay) {
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

// ruleMatches applies the three predicates: every string in All present, at
// least one in Any present, none in Not present. An empty list is satisfied, so
// a rule carrying only Any is an "any of these".
func ruleMatches(rl *ScreenRule, hay string) bool {
	for _, s := range rl.All {
		if !strings.Contains(hay, s) {
			return false
		}
	}
	for _, s := range rl.Not {
		if strings.Contains(hay, s) {
			return false
		}
	}
	if len(rl.Any) == 0 {
		// A rule with no Any and no All says nothing about the screen, and would
		// match every pane the harness runs in.
		return len(rl.All) > 0
	}
	for _, s := range rl.Any {
		if strings.Contains(hay, s) {
			return true
		}
	}
	return false
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
