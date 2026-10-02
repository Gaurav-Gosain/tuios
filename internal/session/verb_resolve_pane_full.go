//go:build !slim

package session

import (
	"encoding/json"
	"strconv"
)

// verbResolvePane names the pane a process runs in, for a hook reporter that
// lost the pane's environment. A harness that scrubs its hooks' environment,
// or a sandbox wrapper that starts clean, leaves TUIOS_PANE_ID unset, and the
// report would otherwise land on whichever window is focused.
func (d *Daemon) verbResolvePane(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		SID  int   `json:"sid"`
		PIDs []int `json:"pids"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.SID <= 1 && len(p.PIDs) == 0 {
		return nil, invalidParam("sid", "pass sid, pids, or both, so there is a process to trace")
	}
	m, ok := matchPaneByProcess(d.localPaneShells(), p.SID, p.PIDs)
	if !ok {
		return nil, newVerbError(ErrVerbWindowNotFound, "no pane on this daemon runs session "+strconv.Itoa(p.SID)+" or any of the pids given")
	}
	return map[string]any{
		"type":      "pane_resolved",
		"session":   m.pane.session,
		"window_id": m.pane.windowID,
		"by":        m.by,
		"pid":       m.pid,
	}, nil
}
