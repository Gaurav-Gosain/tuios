package client

import (
	"log"
	"slices"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

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
// Closing a window ends its program, so guios must never close a live one.
// Only the daemon's own report goes: list-windows marks a window exited when
// the process of the window's own PTY ended. A window-exit event is not
// enough. It names a window and a PTY that may no longer be the window's,
// and a guios that took it as the window's exit closed live panes (sf, twice
// on 2026-11-09). The event only asks for a new build, which reads the
// report. Before it closes a window, guios reads list-windows again: the
// window must still be there, with the same PTY, still exited. A daemon from
// before 2026-11-04, whose list-windows says nothing of exits, gets no
// closes: a dead pane stays on screen, which is safe.
//
// Every close is logged with the window, its PTY and how its program ended,
// and so is every close the second read stops.

// reap is the closing of a session's exited windows: end ends the session,
// whose last windows they are.
type reap struct {
	session string
	windows []exitedWindow
	end     bool
}

// exitedWindow is a window list-windows reported exited, with the PTY whose
// process ended.
type exitedWindow struct {
	id, pty      string
	code, signal int
}

// exitedIn returns the windows of a session whose program exited and that
// do not stay. hide says to leave them out of the state now: only while
// another pane stays on the shown workspace. Otherwise the workspace would
// show empty for the moment until the close lands and the daemon shows
// another workspace, or the session ends; the pane keeps its last frame
// until then. end says no window of the session stays.
func exitedIn(wins []windowRow, shown int) (gone []exitedWindow, hide, end bool) {
	stays, staysShown := 0, false
	for _, w := range wins {
		if w.Exited && !w.KeepOpen && w.PTY != "" {
			gone = append(gone, exitedWindow{id: w.ID, pty: w.PTY, code: w.ExitCode, signal: w.Signal})
			continue
		}
		if w.Rail != "" {
			// The rail pane is the rail's, not one of the session's panes:
			// it keeps no session open.
			continue
		}
		stays++
		if w.Workspace == shown && !w.Scratch && w.Rail == "" && !w.Minimized {
			staysShown = true
		}
	}
	return gone, len(gone) > 0 && staysShown, len(gone) > 0 && stays == 0
}

// goneIDs are the window IDs of gone.
func goneIDs(gone []exitedWindow) []string {
	out := make([]string, len(gone))
	for i, g := range gone {
		out[i] = g.id
	}
	return out
}

// forgetExits drops what it knows of windows that are gone. Hold k.mu.
func (k *stateKeeper) forgetExits(live map[string]string) {
	for id := range k.closing {
		if _, ok := live[id]; !ok {
			delete(k.closing, id)
		}
	}
}

// reap closes the exited windows, each once, or ends their session, after
// reading again that each one is still exited (stillExited). The daemon's
// window-closed and session-closed events then bring the state without
// them. An error is another client's close that came first.
func (k *stateKeeper) reap(jobs []reap) {
	k.mu.Lock()
	jobs = slices.DeleteFunc(jobs, func(j reap) bool {
		fresh := false
		for _, w := range j.windows {
			if !k.closing[w.id] {
				k.closing[w.id] = true
				fresh = true
			}
		}
		return !fresh
	})
	k.mu.Unlock()
	for _, j := range jobs {
		go func() {
			sure := k.stillExited(j.session, j.windows)
			if j.end && len(sure) == len(j.windows) {
				log.Printf("guios: ending session %s%s: its last panes exited (%s)", j.session, k.c.hostLabel(), exitList(j.windows))
				_, _ = k.c.verb("kill-session", map[string]any{"session": j.session})
				return
			}
			for _, w := range sure {
				log.Printf("guios: closing window %s (PTY %s) of session %s%s: its program %s", short(w.id), short(w.pty), j.session, k.c.hostLabel(), exitHow(w))
				_, _ = k.c.verb("close-window", map[string]any{"session": j.session, "window": w.id})
			}
		}()
	}
}

// stillExited reads list-windows again and returns the windows of want that
// are still there with the same PTY and still exited. Each other one is
// logged, and may be closed by a later build once the daemon reports it
// exited again.
func (k *stateKeeper) stillExited(session string, want []exitedWindow) []exitedWindow {
	keep := func(w exitedWindow, why string) {
		log.Printf("guios: not closing window %s (PTY %s) of session %s%s: %s", short(w.id), short(w.pty), session, k.c.hostLabel(), why)
		k.mu.Lock()
		delete(k.closing, w.id)
		k.mu.Unlock()
	}
	raw, err := k.c.verb("list-windows", map[string]any{"session": session})
	if err != nil {
		for _, w := range want {
			keep(w, "list-windows failed: "+err.Error())
		}
		return nil
	}
	var wl struct {
		Windows []windowRow `json:"windows"`
	}
	_ = jsonx.Unmarshal(raw, &wl)
	var sure []exitedWindow
	for _, w := range want {
		i := slices.IndexFunc(wl.Windows, func(r windowRow) bool { return r.ID == w.id })
		switch {
		case i < 0:
			// Closed by another client already: nothing to do.
			k.mu.Lock()
			delete(k.closing, w.id)
			k.mu.Unlock()
		case wl.Windows[i].PTY != w.pty:
			keep(w, "its PTY is now "+short(wl.Windows[i].PTY))
		case !wl.Windows[i].Exited:
			keep(w, "the daemon no longer reports it exited")
		case wl.Windows[i].KeepOpen:
			keep(w, "it stays open after its program")
		default:
			sure = append(sure, w)
		}
	}
	return sure
}

// exitHow says how a window's program ended, for a log line.
func exitHow(w exitedWindow) string {
	if w.signal > 0 {
		return "was ended by signal " + strconv.Itoa(w.signal)
	}
	return "exited with code " + strconv.Itoa(w.code)
}

// exitList is the windows and how each ended, for a log line.
func exitList(ws []exitedWindow) string {
	s := ""
	for i, w := range ws {
		if i > 0 {
			s += ", "
		}
		s += short(w.id) + " " + exitHow(w)
	}
	return s
}

// hostLabel names the machine of a client to another one, for a log line.
func (c *Client) hostLabel() string {
	if c.host == "" {
		return ""
	}
	return " on " + c.host
}

// short is the first eight characters of an ID.
func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
