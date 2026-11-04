package client

import "slices"

// A pane whose program exits closes, as in the tuios TUI: the daemon keeps
// such a window until a client closes it, and tells the clients attached to
// the session (MsgPTYClosed), which close it. guios shows every session, so
// it closes the window of each pane whose program exited, attached TUI or
// not; a TUI that closes it first leaves guios's close nothing to do. A
// window opened with keep_open stays, with the line the daemon writes under
// its output, and shows as exited.
//
// The last pane of a session closing ends the session, as closing that pane
// in guios does, unless a TUI client shows the session: the TUI keeps a
// session with no pane and shows its start screen, and guios then shows the
// empty workspace.
//
// list-windows says which windows exited and which stay. A daemon from
// before those fields says neither: there a window-exit event marks the
// window as exited, and every such window closes.

// reap is the closing of a session's exited windows: end ends the session,
// whose last windows they are.
type reap struct {
	session string
	windows []string
	end     bool
}

// exitedIn returns the windows of a session whose program exited and that
// do not stay. hide says to leave them out of the state now: only while
// another pane stays on the shown workspace. Otherwise the workspace would
// show empty for the moment until the close lands and the daemon shows
// another workspace, or the session ends; the pane keeps its last frame
// until then. end says no window of the session stays.
func (k *stateKeeper) exitedIn(wins []windowRow, shown int) (gone []string, hide, end bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	stays, staysShown := 0, false
	for _, w := range wins {
		if (w.Exited || k.exits[w.ID]) && !w.KeepOpen {
			gone = append(gone, w.ID)
			continue
		}
		stays++
		if w.Workspace == shown && !w.Scratch && w.Rail == "" && !w.Minimized {
			staysShown = true
		}
	}
	return gone, len(gone) > 0 && staysShown, len(gone) > 0 && stays == 0
}

// forgetExits drops what it knows of windows that are gone. Hold k.mu.
func (k *stateKeeper) forgetExits(live map[string]string) {
	for id := range k.exits {
		if _, ok := live[id]; !ok {
			delete(k.exits, id)
		}
	}
	for id := range k.closing {
		if _, ok := live[id]; !ok {
			delete(k.closing, id)
		}
	}
}

// reap closes the exited windows, each once, or ends their session. The
// daemon's window-closed and session-closed events then bring the state
// without them. An error is another client's close that came first.
func (k *stateKeeper) reap(jobs []reap) {
	k.mu.Lock()
	jobs = slices.DeleteFunc(jobs, func(j reap) bool {
		fresh := false
		for _, id := range j.windows {
			if !k.closing[id] {
				k.closing[id] = true
				fresh = true
			}
		}
		return !fresh
	})
	k.mu.Unlock()
	for _, j := range jobs {
		go func() {
			if j.end {
				_, _ = k.c.verb("kill-session", map[string]any{"session": j.session})
				return
			}
			for _, id := range j.windows {
				_, _ = k.c.verb("close-window", map[string]any{"session": j.session, "window": id})
			}
		}()
	}
}
