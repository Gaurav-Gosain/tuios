package session

import (
	"encoding/json"
	"strconv"
)

// paneShell is one local pane and the pid of the shell the daemon started in
// it, the only process of a pane's tree the daemon knows for certain.
type paneShell struct {
	session  string
	windowID string
	shellPID int
}

// paneMatch is the pane a process was traced to, and how.
type paneMatch struct {
	pane paneShell
	// by is "tty" when the caller's terminal session led to the pane's shell
	// and "pid" when one of its ancestors was the shell.
	by  string
	pid int
}

// matchPaneByProcess finds the pane a process belongs to from what the
// process can say about itself without any help from tuios's environment.
//
// sid is the process's session id. The shell in a pane is the session leader
// of the pane's terminal, so every process whose controlling terminal is that
// pane shares its session id, however far down the tree it is. That is the
// controlling-tty answer, and it is tried first because it survives a
// reparented process. pids is the ancestor chain, nearest first, for a process
// that left the terminal's session: a sandbox that calls setsid, or a harness
// that runs its hooks detached. The first ancestor that is a pane's shell
// names the pane.
//
// Zero and one are never matched. Zero is what an unset sid decodes to, and
// pid 1 is every orphan's parent.
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

// localPaneShells lists every pane on this daemon with a live shell. Panes on
// another machine are left out: their pids belong to that machine's process
// table and could collide with a local one.
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
				out = append(out, paneShell{session: sess.Name, windowID: w.ID, shellPID: pid})
			}
		}
	}
	return out
}

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
