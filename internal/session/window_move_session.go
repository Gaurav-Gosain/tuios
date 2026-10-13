package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Moving a window to another session.
//
// A window is a pane and a process, and the process is the part that cannot
// be made again: an agent mid-turn, a build half done, a REPL holding state.
// So the move hands the running PTY from one session to the other. Nothing is
// respawned, the emulator and its scrollback go with it, and the window keeps
// its id.
//
// What a session keeps about a pane is in three places, and each moves:
//
//   - The window's row (WindowState): name, cwd, start folder, agent state and
//     metadata, grants, floating geometry. It leaves the source's window set
//     and joins the destination's in two mutations, each pushed to its own
//     clients, so a client of the source sees the window leave and a client
//     of the destination sees it arrive.
//   - The session's daemon memory keyed by window id: agent claims, harness
//     pids, subagents, turns, holds, the transcript join, start folders.
//     paneTransfer carries them.
//   - The daemon's own stores keyed by session: the grant table, the Inbox,
//     the activity rings, queued prompts. Daemon.moveWindowToSession moves
//     the window's entries in each.
//
// The PTY's callbacks read the session they belong to through PTY.home rather
// than a captured *Session, so a pane's events, focus and place reach its new
// session from the moment the move lands.
//
// The process keeps the environment it started with: TUIOS_SESSION still
// names the source. A call that names the source session and the moved
// window is sent to the window's session (followMovedWindow), so the hooks of
// an agent in the pane go on reporting to the right place.
//
// A stream of the pane (stream-pane, pane-view) stays open through the move.
// Its lease and viewer report follow the pane to the destination, and the
// pane is sized again against the destination's clients when it arrives.

// Error codes of move-window with to_session.
const (
	// ErrVerbSameSession is a move to the session the window is already in.
	ErrVerbSameSession = "same_session"
	// ErrVerbPaneNotMovable is a window that cannot leave its session: a
	// popup, a scratch pane, or a pane whose process runs on another machine.
	ErrVerbPaneNotMovable = "pane_not_movable"
)

// errPaneNotMovable says why a window cannot leave its session.
type errPaneNotMovable struct{ why string }

func (e errPaneNotMovable) Error() string { return e.why }

// movableRefusal is why w cannot move to another session, "" when it can.
func movableRefusal(w WindowState) string {
	switch {
	case w.Host != "":
		return "the pane runs on " + w.Host + ", and only a pane on this machine can move to another session"
	case w.Scratch:
		return "the pane is a scratch pane, and a scratch pane stays in its session"
	case w.Popup:
		return "the pane is a popup, and a popup stays in its session"
	case w.PTYID == "":
		return "the pane has no process"
	}
	return ""
}

// paneTransfer is what a window takes from one session to another.
type paneTransfer struct {
	win WindowState
	pty *PTY

	claim      *agentClaim
	harnessPID int
	subagents  *paneSubagents
	turn       *agentTurn
	seen       *uint64
	hold       *agentHold
	startDir   any
	agentDirs  map[string]string

	// transcript is the join the window had, made again in the destination.
	transcript *transcriptRejoin

	// from is the label of the session the window left, recorded in the
	// destination's WindowMoves when it arrives. Empty for a window put
	// back where it was.
	from string
}

// WindowMove is one window that moved between this session and another, as
// SessionState.WindowMoves records it.
type WindowMove struct {
	// Window is the window's id.
	Window string
	// Other is the label of the other session: the one the window went to,
	// or the one it came from.
	Other string
	// Away is true for a window that left this session and false for one
	// that arrived.
	Away bool
}

// windowMovesKept bounds SessionState.WindowMoves. A client reads only the
// entries for windows that left or arrived in the push it applies.
const windowMovesKept = 8

// recordWindowMove appends m to the session's moves, keeping the newest
// windowMovesKept. It builds a new slice, so a snapshot that shares the old
// one is not written through.
func recordWindowMove(state *SessionState, m WindowMove) {
	moves := state.WindowMoves
	if over := len(moves) + 1 - windowMovesKept; over > 0 {
		moves = moves[over:]
	}
	out := make([]WindowMove, 0, len(moves)+1)
	out = append(out, moves...)
	state.WindowMoves = append(out, m)
}

// LastWindowMove is the newest move recorded for the window, false when none
// is.
func (state *SessionState) LastWindowMove(windowID string) (WindowMove, bool) {
	if state == nil {
		return WindowMove{}, false
	}
	for i := len(state.WindowMoves) - 1; i >= 0; i-- {
		if state.WindowMoves[i].Window == windowID {
			return state.WindowMoves[i], true
		}
	}
	return WindowMove{}, false
}

// sessionLabel is the name a person knows the session by: its display name,
// or its name when it has none.
func sessionLabel(s *Session) string {
	if st := s.GetState(); st != nil && st.DisplayName != "" {
		return st.DisplayName
	}
	return s.Name()
}

// transcriptRejoin is a transcript join to make again.
type transcriptRejoin struct {
	harness, path string
	exact         bool
}

// releaseWindow takes a window out of the session for a move: its row, its
// PTY and the daemon memory kept for it. The process goes on running and the
// PTY is in no session until adoptWindow takes it.
//
// to is the label of the session the window goes to. It is recorded in the
// session's WindowMoves in the same mutation, so a client of this session
// says where the window went rather than that it closed.
func (s *Session) releaseWindow(windowID, to string) (*paneTransfer, error) {
	t := &paneTransfer{}
	err := s.mutateState(func(state *SessionState) error {
		idx, err := findWindowStateIndex(state.Windows, windowID)
		if err != nil {
			return err
		}
		if why := movableRefusal(state.Windows[idx]); why != "" {
			return errPaneNotMovable{why}
		}
		if !s.hasLivePTY(state.Windows[idx].PTYID) {
			return errPaneNotMovable{"the pane's process is gone"}
		}
		t.win = s.removeWindowLocked(state, idx)
		id := t.win.ID
		recordWindowMove(state, WindowMove{Window: id, Other: to, Away: true})
		if c, ok := s.agentClaims[id]; ok {
			t.claim = &c
			delete(s.agentClaims, id)
		}
		if pid, ok := s.agentHarnessPIDs[id]; ok {
			t.harnessPID = pid
			delete(s.agentHarnessPIDs, id)
		}
		if sa, ok := s.agentSubagents[id]; ok {
			t.subagents = sa
			delete(s.agentSubagents, id)
		}
		if turn, ok := s.agentTurns[id]; ok {
			t.turn = &turn
			delete(s.agentTurns, id)
		}
		if seen, ok := s.completionSeen[id]; ok {
			t.seen = &seen
			delete(s.completionSeen, id)
		}
		s.movingWindow = id
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.ptysMu.Lock()
	t.pty = s.ptys[t.win.PTYID]
	delete(s.ptys, t.win.PTYID)
	s.places.Delete(t.win.PTYID)
	s.ptysMu.Unlock()
	s.forgetCwdCache()

	id := t.win.ID
	s.agentHoldMu.Lock()
	if h, ok := s.agentHolds[id]; ok {
		t.hold = &h
		delete(s.agentHolds, id)
	}
	s.agentHoldMu.Unlock()
	s.idle.forget(id)
	if v, ok := s.startDirs.LoadAndDelete(id); ok {
		t.startDir = v
	}
	s.agentDirsMu.Lock()
	t.agentDirs = s.agentDirs[id]
	delete(s.agentDirs, id)
	s.agentDirsMu.Unlock()

	s.transcripts.mu.Lock()
	if j, ok := s.transcripts.joins[id]; ok {
		t.transcript = &transcriptRejoin{harness: j.harness, path: j.reader.Path(), exact: j.exact}
		delete(s.transcripts.joins, id)
		s.releaseJoinLocked(j)
	}
	delete(s.transcripts.nextTry, id)
	s.transcripts.mu.Unlock()

	s.stateDirty.Store(true)
	return t, nil
}

// adoptWindow puts a window released by another session into this one, on
// workspace, and focuses it when focus is set. A tiled window arrives
// unplaced, so a client of this session tiles it into the layout there. A
// floating one keeps its box.
func (s *Session) adoptWindow(t *paneTransfer, workspace int, focus bool) (WindowState, error) {
	pty := t.pty
	s.ptysMu.Lock()
	s.ptys[pty.ID] = pty
	if pty.host == "" {
		s.places.Store(pty.ID, pty)
	}
	pty.home.Store(s)
	s.ptysMu.Unlock()
	// Whether a client shows the session was the source's. A viewer's fit
	// lease sizes the pane only while none shows this one, so the pane is
	// sized again against this session's clients.
	pty.setNoClients(s.tuiClients.Load() == 0)
	// The look the event sink installed reads the session it was made in.
	// The sink here installs a new one on the pane's next output.
	pty.setScreenLook(nil)
	s.forgetCwdCache()

	id := t.win.ID
	if t.startDir != nil {
		s.startDirs.Store(id, t.startDir)
	}
	if len(t.agentDirs) > 0 {
		s.agentDirsMu.Lock()
		if s.agentDirs == nil {
			s.agentDirs = make(map[string]map[string]string)
		}
		s.agentDirs[id] = t.agentDirs
		s.agentDirsMu.Unlock()
	}

	var win WindowState
	var metaAt, subagentsAt int64
	err := s.mutateState(func(state *SessionState) error {
		if !state.workspaceAccepts(workspace) || IsScratchWorkspace(workspace) {
			return fmt.Errorf("workspace %d out of range (1-%d)", workspace, state.workspaceBound())
		}
		win = t.win
		win.Workspace = workspace
		// The zoom and the minimize were the source's layout. The pane
		// arrives shown, and a tiled one is placed by the destination's
		// client.
		win.Zoomed = false
		win.Minimized = false
		if !win.IsFloating {
			win.Unplaced = true
		}
		state.Windows = append(state.Windows, win)
		if t.from != "" {
			recordWindowMove(state, WindowMove{Window: id, Other: t.from})
		}
		if t.claim != nil {
			if s.agentClaims == nil {
				s.agentClaims = make(map[string]agentClaim)
			}
			s.agentClaims[id] = *t.claim
		}
		if t.harnessPID != 0 {
			if s.agentHarnessPIDs == nil {
				s.agentHarnessPIDs = make(map[string]int)
			}
			s.agentHarnessPIDs[id] = t.harnessPID
		}
		if t.subagents != nil {
			if s.agentSubagents == nil {
				s.agentSubagents = make(map[string]*paneSubagents)
			}
			s.agentSubagents[id] = t.subagents
			subagentsAt = s.subagentExpiryLocked()
		}
		if t.turn != nil {
			if s.agentTurns == nil {
				s.agentTurns = make(map[string]agentTurn)
			}
			s.agentTurns[id] = *t.turn
		}
		if t.seen != nil {
			if s.completionSeen == nil {
				s.completionSeen = make(map[string]uint64)
			}
			s.completionSeen[id] = *t.seen
		}
		if state.WorkspaceFocus == nil {
			state.WorkspaceFocus = make(map[int]string)
		}
		if state.WorkspaceFocus[workspace] == "" {
			state.WorkspaceFocus[workspace] = id
		}
		if state.FocusedWindowID == "" {
			state.FocusedWindowID = id
			state.CurrentWorkspace = workspace
		}
		metaAt = earliestAgentMetaExpiry(state.Windows)
		s.movingWindow = id
		return nil
	})
	if err != nil {
		// Nothing of the window stays here: the caller puts it back.
		s.ptysMu.Lock()
		delete(s.ptys, pty.ID)
		s.places.Delete(pty.ID)
		s.ptysMu.Unlock()
		s.startDirs.Delete(id)
		s.agentDirsMu.Lock()
		delete(s.agentDirs, id)
		s.agentDirsMu.Unlock()
		return WindowState{}, err
	}
	s.armAgentMetaPrune(metaAt)
	s.armSubagentPrune(subagentsAt)
	if t.hold != nil {
		s.agentHoldMu.Lock()
		if s.agentHolds == nil {
			s.agentHolds = make(map[string]agentHold)
		}
		s.agentHolds[id] = *t.hold
		s.scheduleHoldSettleLocked(time.Now())
		s.agentHoldMu.Unlock()
	}
	if focus {
		if err := s.FocusDaemonWindow(id); err != nil {
			LogError("Focusing moved window %s in session %q failed: %v", shortID(id), s.Name(), err)
		}
	}
	if t.transcript != nil {
		_ = s.JoinAgentTranscript(id, t.transcript.harness, t.transcript.path, t.transcript.exact)
	}
	s.stateDirty.Store(true)
	return win, nil
}

// returnWindow puts a window back into the session it was released from,
// after the destination refused it. It is adoptWindow on the source, on the
// workspace the window had.
func (s *Session) returnWindow(t *paneTransfer) {
	t.from = ""
	ws := t.win.Workspace
	if !s.GetState().workspaceAccepts(ws) {
		ws = 1
	}
	if _, err := s.adoptWindow(t, ws, false); err != nil {
		LogError("Putting window %s back in session %q failed: %v", shortID(t.win.ID), s.Name(), err)
	}
}

// windowMove serialises moves between sessions, so two moves of one window
// cannot both release it.
var windowMove sync.Mutex

// moveWindowToSession moves a window of src to dst, onto workspace (0 for
// the workspace dst shows), and focuses it there when follow is set. It
// returns the window as it is in dst.
func (d *Daemon) moveWindowToSession(src, dst *Session, windowID string, workspace int, follow bool) (WindowState, error) {
	windowMove.Lock()
	defer windowMove.Unlock()

	if workspace == 0 {
		workspace = max(dst.GetState().CurrentWorkspace, 1)
	}
	// Checked before anything leaves the source: a refusal here moves
	// nothing.
	if st := dst.GetState(); !st.workspaceAccepts(workspace) || IsScratchWorkspace(workspace) {
		return WindowState{}, fmt.Errorf("workspace %d out of range (1-%d)", workspace, st.workspaceBound())
	}

	srcLabel, dstLabel := sessionLabel(src), sessionLabel(dst)
	t, err := src.releaseWindow(windowID, dstLabel)
	if err != nil {
		return WindowState{}, err
	}
	t.from = srcLabel
	d.unsubscribeSessionClients(src.ID, t.pty)

	win, err := dst.adoptWindow(t, workspace, follow)
	if err != nil {
		// The destination refused the window: it goes back where it was.
		src.returnWindow(t)
		return WindowState{}, err
	}

	srcName, dstName := src.Name(), dst.Name()
	d.manager.grants.moveWindow(windowID, dstName)
	d.attention.moveWindow(srcName, dstName, windowID)
	d.activity.moveWindow(src.ID, dst.ID, windowID)
	d.moveQueuedWindow(windowID, dstName)
	d.noteMovedWindow(windowID, src.ID)
	moveWindowHistory(src, dst, windowID)

	// A process that exited while the pane was between the two sessions was
	// heard by nobody attached to the destination.
	if t.pty.IsExited() {
		d.notifyPTYClosed(dst.ID, t.pty.ID)
	}
	return win, nil
}

// unsubscribeSessionClients ends the streams of pty to every client of the
// session with id sessionID. A client of the source drops the window when
// the state push reaches it, and its unsubscribe then finds no PTY in its
// session to take the stream off.
func (d *Daemon) unsubscribeSessionClients(sessionID string, pty *PTY) {
	if pty == nil {
		return
	}
	d.clientsMu.RLock()
	var clients []*connState
	for _, cs := range d.clients {
		cs.mu.Lock()
		if _, ok := cs.ptySubscriptions[pty.ID]; ok && cs.sessionID == sessionID {
			delete(cs.ptySubscriptions, pty.ID)
			clients = append(clients, cs)
		}
		cs.mu.Unlock()
	}
	d.clientsMu.RUnlock()
	for _, cs := range clients {
		pty.Unsubscribe(cs.clientID)
	}
}

// sessionHoldingPTY is the session that holds the PTY now, nil for none.
func (d *Daemon) sessionHoldingPTY(ptyID string) *Session {
	if ptyID == "" || d.manager == nil {
		return nil
	}
	for _, sess := range d.manager.AllSessions() {
		if sess.GetPTY(ptyID) != nil {
			return sess
		}
	}
	return nil
}

// movedWindows records, by window id, the ids of the sessions a window was
// moved out of. See followMovedWindow.
var movedWindows sync.Map

// noteMovedWindow records that the window left the session with id from.
func (d *Daemon) noteMovedWindow(windowID, from string) {
	v, _ := movedWindows.LoadOrStore(windowID, &movedFrom{})
	m := v.(*movedFrom)
	m.mu.Lock()
	if len(m.ids) < 16 {
		m.ids = append(m.ids, from)
	}
	m.mu.Unlock()
}

// forgetMovedWindow drops what noteMovedWindow recorded, for a window that
// closed.
func forgetMovedWindow(windowID string) { movedWindows.Delete(windowID) }

type movedFrom struct {
	mu  sync.Mutex
	ids []string
}

func (m *movedFrom) has(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.ids {
		if x == id {
			return true
		}
	}
	return false
}

// followMovedWindow sends a call that names a moved window together with a
// session the window was moved out of to the session the window is in now.
// A process in the pane still has the old session in TUIOS_SESSION, and its
// agent's hooks pass that with the pane's own id. It runs before the pane
// grants, beside followRenamedSession.
func (d *Daemon) followMovedWindow(params json.RawMessage) json.RawMessage {
	if len(params) == 0 {
		return params
	}
	empty := true
	movedWindows.Range(func(any, any) bool { empty = false; return false })
	if empty {
		return params
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(params, &m); err != nil {
		return params
	}
	var session, window string
	if raw, ok := m["session"]; !ok || json.Unmarshal(raw, &session) != nil || session == "" {
		return params
	}
	if raw, ok := m["window"]; !ok || json.Unmarshal(raw, &window) != nil || window == "" {
		return params
	}
	v, ok := movedWindows.Load(window)
	if !ok {
		return params
	}
	named, _ := d.manager.ResolveSession(session)
	if named == nil || !v.(*movedFrom).has(named.ID) {
		return params
	}
	holder := d.sessionHoldingWindow(window)
	if holder == nil || holder == named {
		return params
	}
	m["session"], _ = json.Marshal(holder.Name())
	out, err := json.Marshal(m)
	if err != nil {
		return params
	}
	return out
}

// moveWindowHistory moves the window's saved history file to the
// destination's folder, so a daemon that stops before the destination's next
// save still brings the history back. Best effort: the destination's next
// save writes the file anyway.
func moveWindowHistory(src, dst *Session, windowID string) {
	if !src.historyPolicy().Enabled || !validHistoryWindowID(windowID) {
		return
	}
	src.persistMu.Lock()
	defer src.persistMu.Unlock()
	from := historyPath(src.Name(), windowID)
	if _, err := os.Stat(from); err != nil {
		return
	}
	to := historyPath(dst.Name(), windowID)
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return
	}
	if err := os.Rename(from, to); err != nil {
		LogError("Moving the saved history of pane %s to session %q failed: %v", shortID(windowID), dst.Name(), err)
	}
}

// moveWindow points the pane's entry at the session it moved to.
func (t *paneGrantTable) moveWindow(windowID, session string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.panes[windowID]; ok {
		e.session = session
		t.panes[windowID] = e
	}
}

// moveWindow moves the items about one window of the session named old to
// newName, the way renameSession moves a whole session's.
func (a *attentionStore) moveWindow(old, newName, window string) {
	if a == nil || old == newName || window == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	moved := false
	for id, it := range a.items {
		if it.Session != old || it.Window != window {
			continue
		}
		if a.byKey[attentionItemKey(it)] == id {
			delete(a.byKey, attentionItemKey(it))
		}
		it.Session = newName
		a.byKey[attentionItemKey(it)] = id
		moved = true
	}
	for id, it := range a.snoozed {
		if it.Session != old || it.Window != window {
			continue
		}
		if a.snoozedKey[attentionItemKey(it)] == id {
			delete(a.snoozedKey, attentionItemKey(it))
		}
		it.Session = newName
		a.snoozedKey[attentionItemKey(it)] = id
		moved = true
	}
	for _, h := range a.asks {
		if h.session == old && h.window == window {
			h.session = newName
		}
	}
	for _, h := range a.holds {
		if h.session == old && h.window == window {
			h.session = newName
		}
	}
	if moved {
		a.rev++
		a.changedLocked()
	}
}

// moveWindow moves a pane's ring to the session it moved to.
func (a *activityStore) moveWindow(fromID, toID, windowID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if r, ok := a.rings[activityKey{fromID, windowID}]; ok {
		delete(a.rings, activityKey{fromID, windowID})
		a.rings[activityKey{toID, windowID}] = r
	}
}

// moveQueuedWindow points a pane's queued prompts at the session it moved to.
func (d *Daemon) moveQueuedWindow(window, session string) {
	q := &d.queue
	if q.total.Load() == 0 && q.stamped.Load() == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if st, ok := q.typed[window]; ok {
		st.session = session
		q.typed[window] = st
	}
	if pq := q.panes[window]; pq != nil {
		pq.session = session
	}
}

// isPaneNotMovable reports whether err is a refusal to move the pane.
func isPaneNotMovable(err error) (string, bool) {
	var e errPaneNotMovable
	if errors.As(err, &e) {
		return e.why, true
	}
	return "", false
}
