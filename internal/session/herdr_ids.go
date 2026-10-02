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
//	herdr tab         tuios workspace   w<...>:t<workspace number>
//	herdr pane        tuios window      w<...>:p<12 hex of the window id>
//
// The ids are made from tuios's own UUIDs, so they stay the same for as long
// as the session and the window live, whatever the session or the window is
// renamed to. A tab id is the workspace number, which a workspace keeps
// through a rename and a reorder. A pane id is found by its window part
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

// herdrTabID is the herdr tab id of workspace n of a session.
func herdrTabID(sessionID string, n int) string {
	return herdrWorkspaceID(sessionID) + ":t" + strconv.Itoa(n)
}

// herdrPaneID is the herdr pane id of a window in a session.
func herdrPaneID(sessionID, windowID string) string {
	return herdrWorkspaceID(sessionID) + ":p" + herdrHex(windowID)
}

// herdrIDError is a failed lookup: herdr's code and message.
type herdrIDError struct{ code, msg string }

// herdrTabFor is the workspace a window's shell starts on, for its
// HERDR_TAB_ID: spawnWS, the one it is being made on, else the one the window
// is on, else the one showing. 0 for a scratch workspace, which is not a tab.
// The state is read only when its lock is free at once: a shell can be
// started while the state is being written, and HERDR_TAB_ID is not worth a
// wait.
//
// The variable is fixed when the shell starts, as herdr's is. A window moved
// to another workspace later keeps the id it started with.
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
	return max(ws, 1)
}

// HerdrSocketPath is the socket tuios accepts herdr's pane state protocol on,
// beside the daemon's own socket.
func HerdrSocketPath(socketPath string) string {
	return socketPath + ".herdr"
}
