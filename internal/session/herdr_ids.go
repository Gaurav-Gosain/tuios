package session

import (
	"strconv"
	"strings"
)

// herdr ids for tuios objects.
//
// herdr names a workspace "w<id>", a tab in it "w<id>:t<n>" and a pane in it
// "w<id>:p<n>". tuios maps its own objects onto those three (herdr_api.go):
//
//	herdr workspace   tuios session     w<12 hex of the session id>
//	herdr tab         tuios workspace   w<...>:t<workspace tab key>
//	herdr pane        tuios window      w<...>:p<12 hex of the window id>
//
// The ids are made from tuios's own UUIDs, so they stay the same for as long
// as the session and the window live, whatever the session or the window is
// renamed to. A tab id is the workspace's tab key (see
// SessionState.WorkspaceTabKey), which a workspace keeps through a rename
// and a move, while its number follows its position. A pane id is found by
// its window part
// alone, so a window that moved to another session is still found by the id
// a client read before the move.
//
// Clients treat the ids as opaque strings. herdr's own ids are the same shape
// with a counter in place of the hex, so a client that splits on ':' still
// gets the workspace part.

// herdrHexLen is how many hex digits of a UUID an id keeps: 48 bits, far past
// any count of live sessions or windows. A prefix that matches two objects is
// refused rather than guessed.
const herdrHexLen = 12

// herdrHex is the first herdrHexLen hex digits of a UUID, dashes dropped.
func herdrHex(uuid string) string {
	var b strings.Builder
	for i := 0; i < len(uuid) && b.Len() < herdrHexLen; i++ {
		if c := uuid[i]; c != '-' {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// herdrWorkspaceID is the herdr workspace id of a session.
func herdrWorkspaceID(sessionID string) string { return "w" + herdrHex(sessionID) }

// herdrTabID is the herdr tab id of the workspace whose tab key is key.
func herdrTabID(sessionID string, key int) string {
	return herdrWorkspaceID(sessionID) + ":t" + strconv.Itoa(key)
}

// herdrPaneID is the herdr pane id of a window in a session.
func herdrPaneID(sessionID, windowID string) string {
	return herdrWorkspaceID(sessionID) + ":p" + herdrHex(windowID)
}

// herdrIDError is a failed lookup: herdr's code and message.
type herdrIDError struct{ code, msg string }

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

// herdrFindTab finds the session and workspace number a herdr tab id names,
// by the workspace's tab key, wherever a move has put the workspace. The
// workspace must be a tab: one herdrListedWorkspace lists.
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
	at := st.WorkspaceForTabKey(n)
	if at == 0 || !herdrListedWorkspace(st, at) {
		return nil, 0, herdrNotFound("tab", id)
	}
	return sess, at, nil
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

// herdrTabFor is the tab key of the workspace a window's shell starts on, for
// its HERDR_TAB_ID: spawnWS, the one it is being made on, else the one the
// window is on, else the one showing. 0 for a scratch workspace, which is not
// a tab. The state is read only when its lock is free at once: a shell can be
// started while the state is being written, and HERDR_TAB_ID is not worth a
// wait. The key is read from Session.tabKeys, which needs no lock.
//
// The variable is fixed when the shell starts, as herdr's is. The key follows
// the workspace through a renumber, so the id still names the pane's tab
// then. A window moved to another workspace later keeps the id it started
// with.
func (s *Session) herdrTabFor(windowID string, spawnWS int) int {
	ws := spawnWS
	if ws == 0 && s.stateMu.TryRLock() {
		if s.state != nil {
			ws = s.state.CurrentWorkspace
			if w, ok := findWindowState(s.state, windowID); ok {
				ws = w.Workspace
			}
		}
		s.stateMu.RUnlock()
	}
	if IsScratchWorkspace(ws) {
		return 0
	}
	ws = max(ws, 1)
	if keys := s.tabKeys.Load(); keys != nil {
		if k, ok := (*keys)[ws]; ok {
			return k
		}
	}
	return ws
}
