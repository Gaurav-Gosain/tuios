package session

import (
	"encoding/json"
	"errors"
	"os"
)

// The rail-pane verb: the session's rail pane, the one live pane an attached
// client draws in its rail's pane section ([appearance.sidebar.pane]).
//
// The pane is an ordinary daemon window with a PTY, so the daemon owns its
// process and a detach leaves it running. Two things set it apart, both
// decided here at creation: it carries WindowState.Rail, the command line the
// client's config gave it, and it sits on a scratch-range workspace that no
// client shows, which keeps it out of every layout, tiler and workspace list
// without a check in each of them. A session has at most one.
//
// The key is how a client says "my pane". The same key keeps the pane there
// is, so a second client or a reattach does not restart it. A different key
// replaces it, which is a config edit. off closes it.

func (d *Daemon) verbRailPane(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string   `json:"session"`
		Key     string   `json:"key"`
		Command []string `json:"command"`
		Cwd     string   `json:"cwd"`
		Width   int      `json:"width"`
		Height  int      `json:"height"`
		Off     bool     `json:"off"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Off && (p.Key != "" || len(p.Command) > 0) {
		return nil, invalidParam("off", "off closes the rail pane, so it takes no key and no command. Pass one or the other")
	}
	if !p.Off {
		switch {
		case p.Key == "":
			return nil, invalidParam("key", "name the command line the pane runs, as the config gives it")
		case len(p.Command) == 0 || p.Command[0] == "":
			return nil, invalidParam("command", "name the program the pane runs")
		case p.Width < 0 || p.Height < 0:
			return nil, invalidParam("width", "width and height are cells and cannot be negative")
		}
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if p.Cwd != "" {
		if info, err := os.Stat(p.Cwd); err != nil || !info.IsDir() {
			// A folder that is gone is not worth a failed pane: the program
			// starts where the session's panes do.
			p.Cwd = ""
		}
	}

	var existing *WindowState
	for _, w := range sess.GetState().Windows {
		if w.Rail != "" {
			existing = &w
			break
		}
	}
	if existing != nil && !p.Off && existing.Rail == p.Key {
		return map[string]any{"type": "rail_pane", "window_id": existing.ID, "created": false}, nil
	}
	if existing != nil {
		if _, err := sess.CloseDaemonWindow(existing.ID); err != nil {
			// Gone meanwhile is what was asked for.
			if _, gone := findWindowStateIndex(sess.GetState().Windows, existing.ID); gone == nil {
				return nil, newVerbError(ErrVerbInternal, "cannot close the rail pane: "+err.Error())
			}
		}
	}
	if p.Off {
		return map[string]any{"type": "rail_pane", "window_id": "", "created": false}, nil
	}

	// The pane's program ending ends the pane, as a popup's does: a rail
	// section over a dead shell would say nothing about why it is empty, and
	// the client says it stopped and offers to start it again.
	onExit := func(ptyID string) {
		d.notifyPTYClosed(sess.ID, ptyID)
		d.closeWindowOfPTY(sess, ptyID, false)
	}
	opts := NewWindowOptions{
		Cwd:        p.Cwd,
		Command:    p.Command,
		Rail:       p.Key,
		RailWidth:  p.Width,
		RailHeight: p.Height,
	}
	win, bverr, err := d.addWindowFor(cs, "rail-pane", sess, opts, onExit)
	if bverr != nil {
		return nil, bverr
	}
	if errors.Is(err, ErrRailPaneExists) {
		// A second client raced this one past the check above. Its pane is
		// the session's pane.
		for _, w := range sess.GetState().Windows {
			if w.Rail != "" {
				return map[string]any{"type": "rail_pane", "window_id": w.ID, "created": false}, nil
			}
		}
	}
	if err != nil {
		return nil, newWindowErr(err, sess, 0)
	}
	return map[string]any{"type": "rail_pane", "window_id": win.ID, "created": true}, nil
}
