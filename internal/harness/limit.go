package harness

import (
	"regexp"
	"slices"
	"strings"
)

// LimitReset is the reset a harness's usage-limit text names, in the
// harness's own words: "11:10am (Australia/Sydney)" from "You've hit your
// session limit · resets 11:10am (Australia/Sydney)", "7:20 PM" from "Try
// again at 7:20 PM.". It is empty when the text names none.
//
// It is never turned into a time. The texts give a clock time with no date,
// a date with no year or a zone by name, and a wrong absolute time is worse
// than the harness's words shown as they are.
func LimitReset(text string) string {
	m := limitResetRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	at := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(m[1]), ".,;"))
	// A reset names a time or a date, so it holds a digit. "send a message
	// after it resets to continue" names none.
	if at == "" || len(at) > limitResetMax || !strings.ContainsAny(at, "0123456789") {
		return ""
	}
	return at
}

// LimitMessage is the message of a limit_reached claim: line, the harness's
// own words as a message holds them, when the reset can still be read from
// it, and otherwise a sentence that names reset. A message is cut to a line's
// length, and a long limit line (Codex names its plans and two links before
// the time) loses the reset to that cut.
func LimitMessage(line, reset string) string {
	if reset == "" || LimitReset(line) == reset {
		return line
	}
	return "Stopped on the usage limit · resets " + reset
}

// limitResetMax bounds the words kept, so a message that runs on after the
// marker does not become the reset.
const limitResetMax = 48

// limitResetRe finds the reset in the texts testdata/limits holds: Claude
// Code's "resets <when>" and "continuing automatically at <when>", and
// Codex's "try again at <when>" and "Try again <date> at <when>". The words
// end at a middle dot, a sentence end or the line's end.
var limitResetRe = regexp.MustCompile(`(?i)(?:\bresets\s+(?:at\s+)?|\bwill reset at\s+|\bcontinuing automatically at\s+|\btry again (?:at\s+)?)([^·\n]+?)(?:\s*·|\.\s|\.$|$)`)

// RuleLimitReset is the reset a limit_reached rule's screen names, read from
// the lines of the rule's region as they are on the screen. The message a
// screen claim carries is cut to a prompt line's length, and a long limit line
// (Codex names its plans and two links before the time) loses the reset to
// that cut, so the reset is read before it.
func (r *Registry) RuleLimitReset(id string, rule int, tail []string) string {
	m := r.Lookup(id)
	if m == nil || rule < 0 || rule >= len(m.Screen.Rule) || m.Screen.Rule[rule].State != "limit_reached" {
		return ""
	}
	lines := regionLines(tail, m.Screen.Rule[rule].Region)
	for _, line := range slices.Backward(lines) {
		if at := LimitReset(line); at != "" {
			return at
		}
	}
	return ""
}
