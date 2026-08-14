package vt

import (
	"strconv"
	"strings"
)

// ProgressState is the state field of an OSC 9;4 progress report, the ConEmu
// progress sequence. It is a structured, in-band statement by the program in the
// pane about whether it is busy, which is why tuios reads it: a coding agent that
// emits it is describing its own state far more honestly than any guess made from
// its output.
type ProgressState int

const (
	// ProgressClear removes the progress indicator: the program is no longer
	// busy.
	ProgressClear ProgressState = 0
	// ProgressNormal is a determinate progress bar carrying a percentage.
	ProgressNormal ProgressState = 1
	// ProgressError means the operation failed.
	ProgressError ProgressState = 2
	// ProgressIndeterminate is a busy indicator with no known percentage.
	ProgressIndeterminate ProgressState = 3
	// ProgressWarning is a determinate bar flagged as needing attention.
	ProgressWarning ProgressState = 4
)

// isProgressPayload reports whether an OSC 9 payload is a 9;4 progress report
// rather than a notification body. The test is deliberately narrow: a
// notification whose text merely begins with "4" is not a progress report, so
// only a bare "4" or a "4" followed by the field separator (or the terminating
// BEL of a bell-terminated sequence) qualifies.
func isProgressPayload(msg string) bool {
	return msg == "4" || strings.HasPrefix(msg, "4;") || strings.HasPrefix(msg, "4\a")
}

// parseProgress reads an OSC 9;4 payload, "4", "4;<state>" or
// "4;<state>;<percent>", into its state and percentage. A payload with no state
// clears, matching emitters that send a bare 9;4 to mean done. The percentage is
// clamped to 0..100 and is 0 for the states that do not carry one. It reports
// false for a payload that is not a progress report or names no known state,
// leaving the caller to ignore it rather than invent a state.
func parseProgress(msg string) (ProgressState, int, bool) {
	if !isProgressPayload(msg) {
		return 0, 0, false
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(msg, "4"), "\a")
	rest = strings.TrimSpace(strings.TrimPrefix(rest, ";"))
	if rest == "" {
		return ProgressClear, 0, true
	}
	fields := strings.Split(rest, ";")
	state, err := strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil || state < int(ProgressClear) || state > int(ProgressWarning) {
		return 0, 0, false
	}
	percent := 0
	if len(fields) > 1 {
		if v, err := strconv.Atoi(strings.TrimSpace(fields[1])); err == nil {
			percent = min(max(v, 0), 100)
		}
	}
	return ProgressState(state), percent, true
}
