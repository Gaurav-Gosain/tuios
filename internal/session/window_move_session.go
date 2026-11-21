package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// Error codes of move-window with to_session.
const (
	// ErrVerbSameSession is a move to the session the window is already in.
	ErrVerbSameSession = "same_session"
	// ErrVerbPaneNotMovable is a window that cannot leave its session: a
	// popup, a scratch pane, or a pane whose process runs on another machine.
	ErrVerbPaneNotMovable = "pane_not_movable"
)

type errPaneNotMovable struct{ why string }

func (e errPaneNotMovable) Error() string { return e.why }

func movableRefusal(w WindowState) string {
	switch {
	case w.Host != "":
		return "the pane runs on " + w.Host + ", and only a pane on this machine can move to another session"
	case w.Scratch:
		return "the pane is a scratch pane, and a scratch pane stays in its session"
	case w.Popup:
		return "the pane is a popup, and a popup stays in its session"
	case w.Kind == WindowKindTML:
		return "the pane shows a TML surface, and a surface stays in the session it was opened in"
	case w.PTYID == "":
		return "the pane has no process"
	}
	return ""
}

type paneTransfer struct {
	win WindowState
	pty *PTY

	claim     *agentClaim
	harness   harnessProc
	subagents *paneSubagents
	turn      *agentTurn
	seen      *uint64
	askShow   *askShow
	hold      *agentHold
	startDir  any
	agentDirs map[string]string

	transcript *transcriptRejoin

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

const windowMovesKept = 8

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
	for _, v := range slices.Backward(state.WindowMoves) {
		if v.Window == windowID {
			return v, true
		}
	}
	return WindowMove{}, false
}

func sessionLabel(s *Session) string {
	if st := s.GetState(); st != nil && st.DisplayName != "" {
		return st.DisplayName
	}
	return s.Name()
}

type transcriptRejoin struct {
	harness, format, path, session string
	exact                          bool
}

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
		if h, ok := s.agentHarnessPIDs[id]; ok {
			t.harness = h
			delete(s.agentHarnessPIDs, id)
		}
		delete(s.agentSharedGuards, id)
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
		if a, ok := s.askShows[id]; ok {
			t.askShow = &a
			delete(s.askShows, id)
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
		t.transcript = &transcriptRejoin{harness: j.harness, format: j.format, path: j.reader.Path(), session: j.session, exact: j.exact}
		delete(s.transcripts.joins, id)
		s.releaseJoinLocked(j)
	}
	delete(s.transcripts.nextTry, id)
	s.transcripts.mu.Unlock()

	s.stateDirty.Set()
	return t, nil
}

func (s *Session) adoptWindow(t *paneTransfer, workspace int, focus bool) (WindowState, error) {
	pty := t.pty
	s.ptysMu.Lock()
	s.ptys[pty.ID] = pty
	if pty.host == "" {
		s.places.Store(pty.ID, pty)
	}
	pty.home.Store(s)
	s.ptysMu.Unlock()
	pty.setNoClients(s.tuiClients.Load() == 0)
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
			return errfmt.Errorf("workspace %d out of range (1-%d)", workspace, state.workspaceBound())
		}
		win = t.win
		win.Workspace = workspace
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
		if t.harness.pid != 0 {
			if s.agentHarnessPIDs == nil {
				s.agentHarnessPIDs = make(map[string]harnessProc)
			}
			s.agentHarnessPIDs[id] = t.harness
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
		if t.askShow != nil {
			if s.askShows == nil {
				s.askShows = make(map[string]askShow)
			}
			s.askShows[id] = *t.askShow
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
		if t.transcript.session != "" {
			s.JoinAgentSession(id, t.transcript.harness, t.transcript.format, t.transcript.path, t.transcript.session)
		} else {
			_ = s.JoinAgentTranscriptAs(id, t.transcript.harness, t.transcript.format, t.transcript.path, t.transcript.exact)
		}
	}
	s.stateDirty.Set()
	return win, nil
}

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

var windowMove sync.Mutex

func (d *Daemon) moveWindowToSession(src, dst *Session, windowID string, workspace int, follow bool) (WindowState, error) {
	windowMove.Lock()
	defer windowMove.Unlock()

	if workspace == 0 {
		workspace = max(dst.GetState().CurrentWorkspace, 1)
	}
	if st := dst.GetState(); !st.workspaceAccepts(workspace) || IsScratchWorkspace(workspace) {
		return WindowState{}, errfmt.Errorf("workspace %d out of range (1-%d)", workspace, st.workspaceBound())
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
		src.returnWindow(t)
		return WindowState{}, err
	}

	srcName, dstName := src.Name(), dst.Name()
	d.manager.grants.moveWindow(windowID, dstName)
	d.attention.moveWindow(srcName, dstName, windowID)
	d.activity.moveWindow(src.ID, dst.ID, windowID)
	d.moveQueuedWindow(windowID, dstName)
	d.noteMovedWindow(windowID, src.ID, srcName)
	moveWindowHistory(src, dst, windowID)

	if t.pty.IsExited() {
		d.notifyPTYClosed(dst.ID, t.pty.ID)
	}
	return win, nil
}

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

var movedWindows sync.Map

func (d *Daemon) noteMovedWindow(windowID, fromID, fromName string) {
	v, _ := movedWindows.LoadOrStore(windowID, &movedFrom{})
	m := v.(*movedFrom)
	m.mu.Lock()
	if len(m.ids) < 16 {
		m.ids = append(m.ids, fromID)
		m.names = append(m.names, fromName)
	}
	m.mu.Unlock()
}

func forgetMovedWindow(windowID string) { movedWindows.Delete(windowID) }

type movedFrom struct {
	mu    sync.Mutex
	ids   []string
	names []string
}

func (m *movedFrom) has(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Contains(m.ids, id)
}

func (m *movedFrom) hasName(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Contains(m.names, name) || slices.Contains(m.ids, name)
}

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
	if err := jsonx.Unmarshal(params, &m); err != nil {
		return params
	}
	var session, window string
	if raw, ok := m["session"]; !ok || jsonx.Unmarshal(raw, &session) != nil || session == "" {
		return params
	}
	if raw, ok := m["window"]; !ok || jsonx.Unmarshal(raw, &window) != nil || window == "" {
		return params
	}
	v, ok := movedWindows.Load(window)
	if !ok {
		return params
	}
	named, _ := d.manager.ResolveSession(session)
	from := v.(*movedFrom)
	if (named == nil || !from.has(named.ID)) && !from.hasName(session) {
		return params
	}
	holder := d.sessionHoldingWindow(window)
	if holder == nil || holder == named {
		return params
	}
	m["session"], _ = jsonx.Marshal(holder.Name())
	out, err := jsonx.Marshal(m)
	if err != nil {
		return params
	}
	return out
}

func moveWindowHistory(src, dst *Session, windowID string) {
	if !src.historyPolicy().Enabled || !validHistoryWindowID(windowID) {
		return
	}
	src.persistMu.Lock()
	defer src.persistMu.Unlock()
	from, err := historyPath(src.Name(), windowID)
	if err != nil {
		return
	}
	if _, err := os.Stat(from); err != nil {
		return
	}
	to, err := historyPath(dst.Name(), windowID)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return
	}
	if err := os.Rename(from, to); err != nil {
		LogError("Moving the saved history of pane %s to session %q failed: %v", shortID(windowID), dst.Name(), err)
	}
}

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

func isPaneNotMovable(err error) (string, bool) {
	if e, ok := errors.AsType[errPaneNotMovable](err); ok {
		return e.why, true
	}
	return "", false
}
