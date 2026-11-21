package session

import (
	"encoding/json"
	"errors"
	"os"
)

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
		if _, err := sess.CloseDaemonWindow(existing.ID, d.askerOf(cs)+" (rail-pane, which replaces it)"); err != nil {
			if _, gone := findWindowStateIndex(sess.GetState().Windows, existing.ID); gone == nil {
				return nil, newVerbError(ErrVerbInternal, "cannot close the rail pane: "+err.Error())
			}
		}
	}
	if p.Off {
		return map[string]any{"type": "rail_pane", "window_id": "", "created": false}, nil
	}

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
