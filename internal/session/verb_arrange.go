package session

import (
	"encoding/json"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// arrange: one change to a workspace's layout, worked out and written here
// (parity gap 1, step 3; issue #162).
//
// A client that changes a layout by mode, master shape, strip, float or snap
// used to run the layout engine itself, read the tree, work the change out
// and write the whole tree and the whole layout state back. Two clients that
// did so at once, two guios windows or guios and the CLI, each wrote a tree
// built on the one they read, and the later write dropped the other's
// change. The engine now runs in the daemon, one op per session at a time,
// each reading what the last one wrote. It is the engine guios ran
// (arrange_engine.go), with its logic unchanged: it talks to the session
// through the same verbs, here through callVerb for the connection that
// asked, so its grants and link policy hold for every step.

// arrangeLocks runs one arrange per session at a time.
var arrangeLocks sync.Map // session id -> *sync.Mutex

// RunArrange runs one layout op with call, on the workspace ti reads, over
// the panes wins with focus the focused one. floated, when set, hears of a
// pane about to float out of the split tree.
func RunArrange(call VerbCaller, name string, ti ArrangeTree, wins []WindowRow, focus string, p ArrangeParams, floated func(string)) (ArrangeResult, error) {
	e := &engine{call: call, name: name, ti: ti, wins: wins, focus: focus, floated: floated}
	err := e.run(p)
	return e.result, err
}

// ArrangeShown is the panes of workspace ws a layout lays out, as list-windows
// gives them: none of a scratch group, the rail, or minimized.
func ArrangeShown(rows []WindowRow, ws int) []WindowRow {
	var out []WindowRow
	for _, w := range rows {
		if w.Scratch || w.Rail != "" || w.Minimized || w.Workspace != ws {
			continue
		}
		out = append(out, w)
	}
	return out
}

func (d *Daemon) verbArrange(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		ArrangeParams
		Workspace int `json:"workspace"`
		Cols      int `json:"cols"`
		Rows      int `json:"rows"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Op == "" {
		return nil, invalidParam("op", "op is required and names the layout change")
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	mu, _ := arrangeLocks.LoadOrStore(sess.ID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	call := func(verb string, args any) (json.RawMessage, error) {
		res, verr := d.callVerb(cs, verb, args)
		if verr != nil {
			return nil, verr
		}
		return jsonx.Marshal(res)
	}
	ws := p.Workspace
	if ws == 0 {
		ws = sess.GetState().CurrentWorkspace
	}
	ti := ReadArrangeTree(call, sess.Name(), ws, layout.Rect{W: max(p.Cols, 0), H: max(p.Rows, 0)})
	var list struct {
		Windows []WindowRow `json:"windows"`
	}
	raw, err := call("list-windows", map[string]any{"session": sess.Name()})
	if err == nil {
		err = jsonx.Unmarshal(raw, &list)
	}
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not list the windows: "+err.Error())
	}
	wins := ArrangeShown(list.Windows, ws)
	focus := p.Pane
	if focus == "" {
		for _, w := range wins {
			if w.Focused {
				focus = w.ID
			}
		}
	}
	p.Session = sess.Name()
	res, err := RunArrange(call, sess.Name(), ti, wins, focus, p.ArrangeParams, nil)
	if err != nil {
		if ve, ok := err.(*verbError); ok {
			return nil, ve
		}
		return nil, newVerbError(ErrVerbInvalidRequest, err.Error())
	}
	LogBasic("Client %s arranged workspace %d of session %s: %s", cs.clientID, ws, sess.Name(), p.Op)
	return map[string]any{"type": "arranged", "workspace": ws, "focus": res.Focus}, nil
}
