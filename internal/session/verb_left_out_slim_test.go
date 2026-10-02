//go:build slim

package session

import "slices"

// verbLeftOut reports a verb tuios-slim leaves out. The tables that name
// verbs keep their entries for those verbs, which the slim build never
// reaches, so a check that every entry names a registered verb skips them.
func verbLeftOut(name string) bool { return slices.Contains(slimDroppedVerbs, name) }

// examplesTrimmed reports a verb whose examples tuios-slim trims, so their
// numbering differs from the full build's and the outcomes the table keys by
// number do not line up. addFeatureVerbs trims wait-for's agent examples.
func examplesTrimmed(verb string) bool { return verb == "wait-for" }

// servedWaitConditions is the wait-for conditions tuios-slim serves: none
// about agents.
func servedWaitConditions() []string {
	return slices.DeleteFunc(slices.Clone(waitConditions), isAgentWaitCondition)
}
