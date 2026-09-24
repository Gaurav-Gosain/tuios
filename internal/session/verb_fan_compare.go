package session

import (
	"encoding/json"
	"strings"
)

// Fan compare: the attempts of a fan side by side, one check run in all of
// them, and keeping one.
//
// compare-fan reads counts and states, not file contents, so it is scopeRead
// and a link needs only list. verify-fan starts a window in each sibling, so it
// is scopeLaunch (the fan grant, reaching the caller's fan group) and a link
// needs open and write. The command is always the caller's and the verify
// window is opened with no grants, so a check cannot call tuios and cloning a
// repository cannot make fan run code. keep-fan removes worktrees, so it is
// the person's or admin's (scopeDeny), like remove-worktree, and a link needs
// write.

// compareFanParams are what compare-fan takes.
type compareFanParams struct {
	Session string `json:"session"`
	Changes *bool  `json:"changes"`
}

// verbCompareFan answers compare-fan.
func (d *Daemon) verbCompareFan(_ *connState, params json.RawMessage) (any, *verbError) {
	var p compareFanParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	return nil, notBuilt("compare-fan")
}

// verifyFanParams are what verify-fan takes.
type verifyFanParams struct {
	Session   string `json:"session"`
	Command   string `json:"command"`
	TimeoutMS int    `json:"timeout_ms"`
}

// verbVerifyFan answers verify-fan.
func (d *Daemon) verbVerifyFan(_ *connState, params json.RawMessage) (any, *verbError) {
	var p verifyFanParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Command) == "" {
		return nil, invalidParam("command", "command is required: the check to run in every attempt, as a shell line")
	}
	if p.TimeoutMS < 0 {
		return nil, invalidParam("timeout_ms", "timeout_ms is a number of milliseconds, 0 for no limit")
	}
	return nil, notBuilt("verify-fan")
}

// keepFanParams are what keep-fan takes.
type keepFanParams struct {
	Session string `json:"session"`
	Stash   bool   `json:"stash"`
	Force   bool   `json:"force"`
}

// verbKeepFan answers keep-fan.
func (d *Daemon) verbKeepFan(_ *connState, params json.RawMessage) (any, *verbError) {
	var p keepFanParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Session == "" {
		return nil, invalidParam("session", "session is required: the attempt to keep")
	}
	return nil, notBuilt("keep-fan")
}
