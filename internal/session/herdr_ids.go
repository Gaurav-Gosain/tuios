package session

import (
	"strconv"
	"strings"
)

const herdrHexLen = 12

func herdrHex(uuid string) string {
	var b strings.Builder
	for i := 0; i < len(uuid) && b.Len() < herdrHexLen; i++ {
		if c := uuid[i]; c != '-' {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func herdrWorkspaceID(sessionID string) string { return "w" + herdrHex(sessionID) }

func herdrTabID(sessionID string, key int) string {
	return herdrWorkspaceID(sessionID) + ":t" + strconv.Itoa(key)
}

func herdrPaneID(sessionID, windowID string) string {
	return herdrWorkspaceID(sessionID) + ":p" + herdrHex(windowID)
}

type herdrIDError struct{ code, msg string }

func herdrNotFound(kind, id string) *herdrIDError {
	return &herdrIDError{code: kind + "_not_found", msg: kind + " " + echoName(id) + " not found"}
}

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
