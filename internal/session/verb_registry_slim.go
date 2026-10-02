//go:build slim

package session

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/edition"
)

// addFeatureVerbs adds no verb in tuios-slim. A call to a verb it leaves out
// answers unknown_verb with a message that says so. See slimDroppedVerbs.
//
// It takes the agent conditions out of wait-for, which tuios-slim keeps for
// windows and commands, so list-verbs describes only what this build does.
func addFeatureVerbs(m map[string]verbEntry) {
	e := m["wait-for"]
	e.params = slices.DeleteFunc(slices.Clone(e.params), func(p verbParam) bool {
		return slices.Contains(slimAgentWaitParams, p.Name)
	})
	for i, p := range e.params {
		if p.Name == "condition" {
			e.params[i].Accepted = slices.DeleteFunc(slices.Clone(p.Accepted), isAgentWaitCondition)
		}
	}
	e.examples = slices.DeleteFunc(slices.Clone(e.examples), func(ex string) bool {
		return strings.Contains(ex, `"condition":"agent-`)
	})
	m["wait-for"] = e
}

// slimAgentWaitParams are the wait-for parameters that only the agent
// conditions read.
var slimAgentWaitParams = []string{"any_session", "until", "thread", "select", "every"}

// isAgentWaitCondition reports a wait-for condition about agents.
func isAgentWaitCondition(condition string) bool {
	return condition == "agent-state" || condition == "agent-message"
}

// missingCallError refuses a wait-for for an agent condition, which
// tuios-slim leaves out, the way missingVerbError refuses a whole verb. It is
// nil for any other call.
func missingCallError(verb string, params json.RawMessage) *verbError {
	if verb != "wait-for" {
		return nil
	}
	var p struct {
		Condition string `json:"condition"`
	}
	if json.Unmarshal(params, &p) != nil || !isAgentWaitCondition(p.Condition) {
		return nil
	}
	return waitAgentMissing(p.Condition)
}

// missingVerbError is the error for a verb tuios-slim leaves out: the stable
// code unknown_verb, so a caller that handles a missing verb handles this
// one, with a message that says why it is missing. It is nil for any other
// name, which gets the ordinary unknown_verb answer.
func missingVerbError(verb string) *verbError {
	if !slices.Contains(slimDroppedVerbs, verb) {
		return nil
	}
	return hintedVerbError(ErrVerbUnknownVerb, edition.MissingMessage(verb), &VerbHint{
		Verb:   "list-verbs",
		Detail: "This daemon is tuios-slim. Call list-verbs for the verbs it has.",
	})
}
