package session

import (
	"encoding/json"
	"strconv"
)

type paneShell struct {
	session  string
	windowID string
	shellPID int
}

type paneMatch struct {
	pane paneShell
	by   string
	pid  int
}

func matchPaneByProcess(panes []paneShell, sid int, pids []int) (paneMatch, bool) {
	byPID := make(map[int]paneShell, len(panes))
	for _, p := range panes {
		if p.shellPID > 1 {
			byPID[p.shellPID] = p
		}
	}
	if sid > 1 {
		if p, ok := byPID[sid]; ok {
			return paneMatch{pane: p, by: "tty", pid: sid}, true
		}
	}
	for _, pid := range pids {
		if pid <= 1 {
			continue
		}
		if p, ok := byPID[pid]; ok {
			return paneMatch{pane: p, by: "pid", pid: pid}, true
		}
	}
	return paneMatch{}, false
}

func (d *Daemon) localPaneShells() []paneShell {
	var out []paneShell
	for _, sess := range d.manager.AllSessions() {
		state := sess.GetState()
		for i := range state.Windows {
			w := &state.Windows[i]
			if w.Host != "" || w.PTYID == "" {
				continue
			}
			pty := sess.GetPTY(w.PTYID)
			if pty == nil {
				continue
			}
			if pid := pty.ShellPID(); pid > 1 {
				out = append(out, paneShell{session: sess.Name(), windowID: w.ID, shellPID: pid})
			}
		}
	}
	return out
}

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
