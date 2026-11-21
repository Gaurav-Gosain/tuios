package session

import (
	"maps"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// WorkspaceTabKey is the herdr tab number of workspace ws: the number it was
// first listed under, kept through every renumber. A session never renumbered
// has no keys, and every workspace's key is its number.
func (st *SessionState) WorkspaceTabKey(ws int) int {
	if k, ok := st.WorkspaceTabKeys[ws]; ok {
		return k
	}
	return ws
}

// WorkspaceForTabKey is the workspace whose tab key is key, or 0 when no
// workspace of the session holds it any more.
func (st *SessionState) WorkspaceForTabKey(key int) int {
	for ws := 1; ws <= st.workspaceBound(); ws++ {
		if st.WorkspaceTabKey(ws) == key {
			return ws
		}
	}
	return 0
}

func (st *SessionState) denseTabKeys() (map[int]int, int) {
	bound := st.workspaceBound()
	keys := make(map[int]int, bound)
	next := st.NextWorkspaceTabKey
	for _, k := range st.WorkspaceTabKeys {
		next = max(next, k+1)
	}
	if len(st.WorkspaceTabKeys) == 0 {
		for ws := 1; ws <= bound; ws++ {
			keys[ws] = ws
		}
		return keys, max(next, bound+1)
	}
	for ws := 1; ws <= bound; ws++ {
		if k, ok := st.WorkspaceTabKeys[ws]; ok {
			keys[ws] = k
			continue
		}
		keys[ws] = next
		next++
	}
	return keys, next
}

// WorkspaceRenumbering reads where each workspace went between a client's
// copy of the tab keys, from, and the state to: the number it had under from
// mapped to the number it has in to, for the workspaces whose number changed.
// It is nil when none did. A workspace whose key is gone (it stopped being a
// tab) stays where it is.
func WorkspaceRenumbering(from map[int]int, to *SessionState) map[int]int {
	bound := to.workspaceBound()
	key := func(keys map[int]int, ws int) int {
		if k, ok := keys[ws]; ok {
			return k
		}
		return ws
	}
	at := make(map[int]int, bound)
	for ws := 1; ws <= bound; ws++ {
		at[key(to.WorkspaceTabKeys, ws)] = ws
	}
	newOf := make(map[int]int)
	taken := make(map[int]bool, bound)
	var lost []int
	for ws := 1; ws <= bound; ws++ {
		n, ok := at[key(from, ws)]
		if !ok {
			lost = append(lost, ws)
			continue
		}
		taken[n] = true
		if n != ws {
			newOf[ws] = n
		}
	}
	free := 1
	for _, ws := range lost {
		for taken[free] {
			free++
		}
		taken[free] = true
		if free != ws {
			newOf[ws] = free
		}
	}
	if len(newOf) == 0 {
		return nil
	}
	return newOf
}

func workspacePlaces(order []int, bound int) (map[int]int, error) {
	clean := make([]int, 0, len(order))
	for _, ws := range order {
		if ws < 1 || ws > bound || slices.Contains(clean, ws) {
			continue
		}
		clean = append(clean, ws)
	}
	if len(clean) == 0 && len(order) > 0 {
		return nil, errfmt.Errorf("no workspace in the order is in range (1-%d)", bound)
	}
	slots := slices.Sorted(slices.Values(clean))
	newOf := make(map[int]int)
	for i, ws := range clean {
		if slots[i] != ws {
			newOf[ws] = slots[i]
		}
	}
	if len(newOf) == 0 {
		return nil, nil
	}
	return newOf, nil
}

func (st *SessionState) renumberWorkspaces(newOf map[int]int) {
	if len(newOf) == 0 {
		return
	}
	to := func(ws int) int {
		if n, ok := newOf[ws]; ok {
			return n
		}
		return ws
	}
	for i := range st.Windows {
		st.Windows[i].Workspace = to(st.Windows[i].Workspace)
	}
	st.CurrentWorkspace = to(st.CurrentWorkspace)
	keys, next := st.denseTabKeys()
	st.WorkspaceTabKeys = RenumberWorkspaceMap(keys, newOf)
	st.NextWorkspaceTabKey = next
	st.WorkspaceFocus = RenumberWorkspaceMap(st.WorkspaceFocus, newOf)
	st.FocusHistory = RenumberWorkspaceMap(st.FocusHistory, newOf)
	st.WorkspaceNames = RenumberWorkspaceMap(st.WorkspaceNames, newOf)
	st.WorkspaceTrees = RenumberWorkspaceMap(st.WorkspaceTrees, newOf)
	st.WorkspaceMasterRatio = RenumberWorkspaceMap(st.WorkspaceMasterRatio, newOf)
	st.WorkspaceStackRatio = RenumberWorkspaceMap(st.WorkspaceStackRatio, newOf)
	st.WorkspaceMasterSplits = RenumberWorkspaceMap(st.WorkspaceMasterSplits, newOf)
	st.WorkspaceMasterLayout = RenumberWorkspaceMap(st.WorkspaceMasterLayout, newOf)
	st.WorkspaceHasCustom = RenumberWorkspaceMap(st.WorkspaceHasCustom, newOf)
	st.WorkspaceLayoutModes = RenumberWorkspaceMap(st.WorkspaceLayoutModes, newOf)
	st.WorkspaceScrollColumns = RenumberWorkspaceMap(st.WorkspaceScrollColumns, newOf)
	st.WorkspaceOrder = nil
}

// RenumberWorkspaceMap is m with each key in newOf moved to its new number,
// in a new map. Nil stays nil.
func RenumberWorkspaceMap[V any](m map[int]V, newOf map[int]int) map[int]V {
	if m == nil {
		return nil
	}
	out := make(map[int]V, len(m))
	for ws, v := range m {
		if n, ok := newOf[ws]; ok {
			ws = n
		}
		out[ws] = v
	}
	return out
}

func (st *SessionState) applyLegacyWorkspaceOrder() {
	if len(st.WorkspaceOrder) == 0 {
		st.WorkspaceOrder = nil
		return
	}
	full := slices.Clone(st.WorkspaceOrder)
	for ws := 1; ws <= st.workspaceBound(); ws++ {
		if !slices.Contains(full, ws) {
			full = append(full, ws)
		}
	}
	newOf, err := workspacePlaces(full, st.workspaceBound())
	if err == nil {
		st.renumberWorkspaces(newOf)
	}
	st.WorkspaceOrder = nil
}

// RenumberWorkspaces moves the workspaces into the order given (see
// workspacePlaces) and reports where each one went. The daemon-side records
// kept by workspace number move with them.
func (s *Session) RenumberWorkspaces(order []int) (map[int]int, error) {
	var moved map[int]int
	err := s.mutateState(func(state *SessionState) error {
		newOf, err := workspacePlaces(order, state.workspaceBound())
		if err != nil {
			return err
		}
		state.renumberWorkspaces(newOf)
		state.WorkspaceOrder = nil
		s.closeUpPending = len(s.gaps) > 0
		if newOf == nil {
			return nil
		}
		moved = newOf
		s.renumberSessionRecordsLocked(newOf)
		s.markFocusIntentLocked()
		s.renumberedAt = state.Version + 1
		return nil
	})
	return moved, err
}

func (s *Session) renumberSessionRecordsLocked(newOf map[int]int) {
	to := func(ws int) int {
		if n, ok := newOf[ws]; ok {
			return n
		}
		return ws
	}
	if s.returnTo != nil {
		next := make(map[int]int, len(s.returnTo))
		for ws, origin := range s.returnTo {
			next[to(ws)] = to(origin)
		}
		s.returnTo = next
	}
	for i, ws := range s.workspaceTrail {
		s.workspaceTrail[i] = to(ws)
	}
	s.tabListed = RenumberWorkspaceMap(s.tabListed, newOf)
	s.existed = RenumberWorkspaceMap(s.existed, newOf)
	s.hadPanes = RenumberWorkspaceMap(s.hadPanes, newOf)
	s.selfEmptied = RenumberWorkspaceMap(s.selfEmptied, newOf)
	s.gaps = RenumberWorkspaceMap(s.gaps, newOf)
	for id, at := range s.panesAt {
		at.ws = to(at.ws)
		s.panesAt[id] = at
	}
}

func (s *Session) noteWorkspacesLocked() bool {
	st := s.state
	if st == nil {
		return false
	}
	defer func() { s.publishTabKeys(st.WorkspaceTabKeys) }()
	exists := st.existingWorkspaces()
	if s.hadPanes == nil {
		s.hadPanes = make(map[int]bool)
	}
	person := s.closeUpPending
	s.closeUpPending = false
	now, byPerson := s.notePaneMovesLocked()
	if byPerson {
		person = true
	}

	var gone []int
	for ws := range s.existed {
		if ws <= st.workspaceBound() && !exists[ws] && s.hadPanes[ws] {
			gone = append(gone, ws)
		}
	}
	for ws := range s.hadPanes {
		if !exists[ws] {
			delete(s.hadPanes, ws)
		}
	}
	for _, at := range now {
		s.hadPanes[at.ws] = true
	}
	for _, ws := range gone {
		if st.WorkspaceNames[ws] != "" {
			names := maps.Clone(st.WorkspaceNames)
			delete(names, ws)
			st.WorkspaceNames = names
		}
		if s.selfEmptied[ws] {
			if s.gaps == nil {
				s.gaps = make(map[int]bool)
			}
			s.gaps[ws] = true
		} else {
			person = true
		}
		delete(s.selfEmptied, ws)
	}
	s.existed = exists
	s.retireTabKeysLocked()

	if !person {
		return false
	}
	closing := slices.DeleteFunc(gone, func(ws int) bool { return s.gaps[ws] })
	for ws := range s.gaps {
		if !exists[ws] && ws <= st.workspaceBound() {
			closing = append(closing, ws)
		}
	}
	s.gaps = nil
	newOf := st.closeUp(closing)
	if newOf == nil {
		return false
	}
	st.renumberWorkspaces(newOf)
	s.renumberSessionRecordsLocked(newOf)
	return true
}

type paneAt struct {
	ws    int
	pty   string
	agent bool
}

func (s *Session) notePaneMovesLocked() (map[string]paneAt, bool) {
	st := s.state
	now := make(map[string]paneAt, len(st.Windows))
	held := make(map[int]bool)
	for i := range st.Windows {
		if w := &st.Windows[i]; !w.Scratch {
			ws := max(w.Workspace, 1)
			now[w.ID] = paneAt{ws: ws, pty: w.PTYID, agent: agentWasLive(*w)}
			held[ws] = true
		}
	}
	byPerson := false
	left := make(map[int]bool)
	s.exitedAloneMu.Lock()
	for id, was := range s.panesAt {
		if cur, ok := now[id]; ok && cur.ws == was.ws {
			continue
		} else if shell, exited := s.exitedAlone[was.pty]; !ok && was.pty != "" && exited {
			delete(s.exitedAlone, was.pty)
			if !shell || was.agent {
				if _, seen := left[was.ws]; !seen {
					left[was.ws] = false
				}
				continue
			}
		}
		left[was.ws] = true
		byPerson = true
	}
	s.exitedAloneMu.Unlock()
	for ws, person := range left {
		if held[ws] {
			continue
		}
		if s.selfEmptied == nil {
			s.selfEmptied = make(map[int]bool)
		}
		s.selfEmptied[ws] = !person
	}
	for ws := range held {
		delete(s.selfEmptied, ws)
		delete(s.gaps, ws)
	}
	s.panesAt = now
	return now, byPerson
}

func (s *Session) notePaneExitedAlone(ptyID string, shell bool, signal int) {
	if signal > 0 {
		s.signalHold.Store(time.Now().Add(signalHoldFor).UnixNano())
	}
	s.exitedAloneMu.Lock()
	defer s.exitedAloneMu.Unlock()
	if s.exitedAlone == nil {
		s.exitedAlone = make(map[string]bool)
	}
	s.exitedAlone[ptyID] = shell
}

func (st *SessionState) existingWorkspaces() map[int]bool {
	exists := map[int]bool{max(st.CurrentWorkspace, 1): true}
	for i := range st.Windows {
		if w := &st.Windows[i]; !w.Scratch {
			exists[max(w.Workspace, 1)] = true
		}
	}
	return exists
}

func (st *SessionState) closeUp(gone []int) map[int]int {
	if len(gone) == 0 {
		return nil
	}
	order := make([]int, 0, st.workspaceBound())
	for ws := 1; ws <= st.workspaceBound(); ws++ {
		if !slices.Contains(gone, ws) {
			order = append(order, ws)
		}
	}
	order = append(order, slices.Sorted(slices.Values(gone))...)
	newOf := make(map[int]int)
	for i, ws := range order {
		if ws != i+1 {
			newOf[ws] = i + 1
		}
	}
	if len(newOf) == 0 {
		return nil
	}
	return newOf
}

func (s *Session) retireTabKeysLocked() {
	st := s.state
	bound := st.workspaceBound()
	listed := make(map[int]bool)
	for ws := 1; ws <= bound; ws++ {
		if herdrListedWorkspace(st, ws) {
			listed[ws] = true
		}
	}
	var gone []int
	for ws := range s.tabListed {
		if ws <= bound && !listed[ws] {
			gone = append(gone, ws)
		}
	}
	s.tabListed = listed
	short := st.WorkspaceTabKeys != nil && len(st.WorkspaceTabKeys) < bound
	if len(gone) == 0 && !short {
		return
	}
	keys, next := st.denseTabKeys()
	slices.Sort(gone)
	for _, ws := range gone {
		keys[ws] = next
		next++
	}
	st.WorkspaceTabKeys, st.NextWorkspaceTabKey = keys, next
}

func (s *Session) noteRenumberLocked() {
	s.renumberedAt = s.state.Version
	s.focusMovedVersion = s.state.Version
}

func (s *Session) workspaceRenumberMissedLocked(base int) bool {
	return base != 0 && base < s.renumberedAt
}

func takeWorkspaceMapsFrom(incoming, canonical *SessionState) {
	incoming.WorkspaceNames = maps.Clone(canonical.WorkspaceNames)
	incoming.WorkspaceMasterRatio = maps.Clone(canonical.WorkspaceMasterRatio)
	incoming.WorkspaceStackRatio = maps.Clone(canonical.WorkspaceStackRatio)
	incoming.WorkspaceMasterSplits = cloneMasterSplits(canonical.WorkspaceMasterSplits)
	incoming.WorkspaceHasCustom = maps.Clone(canonical.WorkspaceHasCustom)
	incoming.WorkspaceLayoutModes = maps.Clone(canonical.WorkspaceLayoutModes)
	incoming.WorkspaceScrollColumns = canonical.WorkspaceScrollColumns
	if incoming.WorkspaceTrees != nil {
		incoming.WorkspaceTrees = canonical.WorkspaceTrees
		incoming.WindowToBSPID = canonical.WindowToBSPID
		incoming.NextBSPWindowID = canonical.NextBSPWindowID
	}
}

func (s *Session) publishTabKeys(keys map[int]int) {
	s.tabKeys.Store(&keys)
}

// RestoreWorkspaceTabKeys puts saved herdr tab keys back on a session a restore
// just rebuilt, so a tab id a herdr client held across the daemon restart still
// names its tab. UpdateState keeps the daemon's own copy of the keys (see
// retainDaemonExclusive), and a new session has none.
func (s *Session) RestoreWorkspaceTabKeys(keys map[int]int, next int) {
	if len(keys) == 0 {
		return
	}
	_ = s.mutateState(func(state *SessionState) error {
		state.WorkspaceTabKeys = maps.Clone(keys)
		state.NextWorkspaceTabKey = next
		return nil
	})
}
