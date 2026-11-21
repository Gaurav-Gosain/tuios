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

func isProgressPayload(msg string) bool {
	return msg == "4" || strings.HasPrefix(msg, "4;") || strings.HasPrefix(msg, "4\a")
}

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
