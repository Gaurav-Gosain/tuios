//go:build !slim

package session

import (
	"strconv"
	"strings"
)

func herdrNotFound(kind, id string) *herdrIDError {
	return &herdrIDError{code: kind + "_not_found", msg: kind + " " + echoName(id) + " not found"}
}

// herdrFindSession finds the session a herdr workspace id names.
func (d *Daemon) herdrFindSession(id string) (*Session, *herdrIDError) {
	hex, ok := strings.CutPrefix(id, "w")
	if !ok || hex == "" || strings.Contains(hex, ":") {
		return nil, herdrNotFound("workspace", id)
	}
	var found *Session
	for _, s := range d.manager.AllSessions() {
		if herdrHex(s.ID) == hex || strings.ReplaceAll(s.ID, "-", "") == hex {
			if found != nil {
				return nil, herdrNotFound("workspace", id)
			}
			found = s
		}
	}
	if found == nil {
		return nil, herdrNotFound("workspace", id)
	}
	return found, nil
}

// herdrFindTab finds the session and workspace number a herdr tab id names. The
// workspace must be one the session has, and a tab: one herdrListedWorkspace
// lists.
func (d *Daemon) herdrFindTab(id string) (*Session, int, *herdrIDError) {
	ws, num, ok := strings.Cut(id, ":t")
	if !ok {
		return nil, 0, herdrNotFound("tab", id)
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 1 {
		return nil, 0, herdrNotFound("tab", id)
	}
	sess, ierr := d.herdrFindSession(ws)
	if ierr != nil {
		return nil, 0, herdrNotFound("tab", id)
	}
	st := sess.GetState()
	if n > st.workspaceBound() {
		return nil, 0, herdrNotFound("tab", id)
	}
	if !herdrListedWorkspace(st, n) {
		return nil, 0, herdrNotFound("tab", id)
	}
	return sess, n, nil
}

// herdrFindPane finds the session and window a pane id names. It takes herdr's
// form, and a tuios window id (the full UUID, or its first 8 or more hex
// digits) as well, which is what HERDR_PANE_ID held before this form
// existed and what a tuios user may type.
func (d *Daemon) herdrFindPane(id string) (*Session, WindowState, *herdrIDError) {
	want := id
	if _, win, ok := strings.Cut(id, ":p"); ok {
		want = win
	}
	want = strings.ToLower(strings.ReplaceAll(want, "-", ""))
	if len(want) < 8 {
		return nil, WindowState{}, herdrNotFound("pane", id)
	}
	var (
		sess  *Session
		win   WindowState
		found bool
	)
	for _, s := range d.manager.AllSessions() {
		for _, w := range s.GetState().Windows {
			if herdrScratch(&w) || !strings.HasPrefix(strings.ReplaceAll(w.ID, "-", ""), want) {
				continue
			}
			if found {
				return nil, WindowState{}, herdrNotFound("pane", id)
			}
			sess, win, found = s, w, true
		}
	}
	if !found {
		return nil, WindowState{}, herdrNotFound("pane", id)
	}
	return sess, win, nil
}
