package session

import (
	"fmt"
	"maps"
	"slices"
)

// A workspace's number is its position. Moving a workspace, by the move keys, by
// dragging its dock pill, through set-workspace-order or through herdr's
// tab.move, renumbers it: the panes on it, its name, its focus, its layout and
// everything else the session keeps by workspace number go to the new number
// together, so opt+N, the pill's {index}, TUIOS_WORKSPACE and every verb agree
// with where it is shown.
//
// The daemon is the one place a renumber happens. A client asks for one and
// takes the result from the state the daemon publishes, which is what keeps two
// attached clients, and a client that attaches later, on one numbering.
//
// A herdr tab id is the one name that does not follow the position. herdr gives
// each tab a number of its own, from a counter that is never reused, and keeps
// it through a move, so a client that read a tab id before a move still holds
// the same tab after it. WorkspaceTabKeys is that number for a tuios workspace.
// It moves with the workspace through a renumber, and a workspace that stops
// being a tab gives its key up for good, so the next tab there has a new one.

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

// denseTabKeys is the tab key of every workspace in range, in a new map, and
// the counter past every key in it. A workspace with no key yet takes the
// workspace's own number while the session has never had keys, which is what
// keeps every tab id a session had before keys existed, and a new key from
// the counter once it has, so a key is never handed out twice.
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

// workspacePlaces turns order, a list of workspace numbers in the order they
// are to be shown, into a renumbering: the workspaces it names take the
// numbers they hold between them, smallest first, in the order given, and a
// workspace it does not name keeps its number. An entry outside the session's
// range, or one already placed, is dropped. It is nil when nothing moves.
func workspacePlaces(order []int, bound int) (map[int]int, error) {
	clean := make([]int, 0, len(order))
	for _, ws := range order {
		if ws < 1 || ws > bound || slices.Contains(clean, ws) {
			continue
		}
		clean = append(clean, ws)
	}
	if len(clean) == 0 && len(order) > 0 {
		return nil, fmt.Errorf("no workspace in the order is in range (1-%d)", bound)
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

// renumberWorkspaces moves every workspace in newOf to its new number, with
// everything the state keeps by workspace number. It writes new maps rather
// than into the ones it holds, because a published snapshot shares some of
// them (see snapshotStateLocked).
//
// A new map keyed by workspace number belongs in this list, or a move leaves
// it describing the wrong workspace.
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

// applyLegacyWorkspaceOrder turns a display order saved by a build that kept
// one into the numbering it showed, once, and drops it. The workspaces come
// back numbered the way their pills were arranged, which is what the person
// was looking at.
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
		if newOf == nil {
			return nil
		}
		moved = newOf
		s.renumberSessionRecordsLocked(newOf)
		// The focus maps were renumbered, so a push built before this keeps
		// the session's focus rather than putting the old numbering back.
		s.markFocusIntentLocked()
		// mutateStateLocked advances the version once this returns.
		s.renumberedAt = state.Version + 1
		return nil
	})
	return moved, err
}

// renumberSessionRecordsLocked moves what the Session itself keeps by
// workspace number, beside the state. The caller holds stateMu.
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
}

// noteWorkspacesLocked runs after every change to the state, a client's push
// included, and keeps a workspace's number its position:
//
//   - A workspace exists while it holds a pane or is the one shown. When one
//     that has held a pane stops existing it loses its name, and every
//     workspace above it moves down one, so closing workspace 2 of three
//     leaves 1 and 2. An empty workspace that was only looked at goes without
//     moving anything, and a number past the last workspace that a pane is put
//     on directly stays as it is until something below it goes: only a
//     workspace going away renumbers.
//   - A workspace that stops being a herdr tab gives up its tab key, so a tab
//     made there later gets a key no client has seen.
//
// It reports whether it renumbered, which the caller counts as a daemon
// mutation. The caller holds stateMu.
func (s *Session) noteWorkspacesLocked() bool {
	st := s.state
	if st == nil {
		return false
	}
	// Published for herdrTabFor whatever happens below. The map is never
	// written in place, so a reader of this copy needs no lock.
	defer func() {
		keys := st.WorkspaceTabKeys
		s.tabKeys.Store(&keys)
	}()
	exists := st.existingWorkspaces()
	if s.hadPanes == nil {
		s.hadPanes = make(map[int]bool)
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
	for i := range st.Windows {
		if w := &st.Windows[i]; !w.Scratch {
			s.hadPanes[max(w.Workspace, 1)] = true
		}
	}
	for _, ws := range gone {
		if st.WorkspaceNames[ws] != "" {
			names := maps.Clone(st.WorkspaceNames)
			delete(names, ws)
			st.WorkspaceNames = names
		}
	}
	s.existed = exists
	s.retireTabKeysLocked()

	newOf := st.closeUp(gone)
	if newOf == nil {
		return false
	}
	st.renumberWorkspaces(newOf)
	s.renumberSessionRecordsLocked(newOf)
	return true
}

// existingWorkspaces is the workspaces that exist: each one holding a pane,
// and the one shown.
//
// Workspace 0, which a state from before workspaces were numbered from 1
// carries, is workspace 1.
func (st *SessionState) existingWorkspaces() map[int]bool {
	exists := map[int]bool{max(st.CurrentWorkspace, 1): true}
	for i := range st.Windows {
		if w := &st.Windows[i]; !w.Scratch {
			exists[max(w.Workspace, 1)] = true
		}
	}
	return exists
}

// closeUp is the renumber that closes the places of the workspaces in gone:
// every workspace above one moves down one, and the places go to the end. Nil
// when nothing moves.
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

// retireTabKeysLocked gives a workspace that stopped being a herdr tab since
// the last change a new tab key. The caller holds stateMu.
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
	// A session given more workspaces since its keys were made gets keys for
	// the new ones from the counter, so none can take a key already in use.
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

// noteRenumberLocked records that the change at the current version
// renumbered the workspaces: a push built before it holds the old numbering in
// its maps and its focus. The caller holds stateMu.
func (s *Session) noteRenumberLocked() {
	s.renumberedAt = s.state.Version
	s.focusMovedVersion = s.state.Version
}

// workspaceRenumberMissedLocked reports whether a push built at base predates
// a renumber. Such a push holds the old numbering in the maps a reconcile
// otherwise takes from the client. The caller holds stateMu.
func (s *Session) workspaceRenumberMissedLocked(base int) bool {
	return base != 0 && base < s.renumberedAt
}

// takeWorkspaceMapsFrom puts canonical's per-workspace maps in place of a
// stale push's, whose keys still name the numbers from before a renumber. The
// trees and the columns are shared with canonical rather than cloned, which is
// safe because nothing writes into a state's maps in place: every change builds
// a new map (see placeTree and renumberWorkspaces).
func takeWorkspaceMapsFrom(incoming, canonical *SessionState) {
	incoming.WorkspaceNames = maps.Clone(canonical.WorkspaceNames)
	incoming.WorkspaceMasterRatio = maps.Clone(canonical.WorkspaceMasterRatio)
	incoming.WorkspaceStackRatio = maps.Clone(canonical.WorkspaceStackRatio)
	incoming.WorkspaceMasterSplits = cloneMasterSplits(canonical.WorkspaceMasterSplits)
	incoming.WorkspaceHasCustom = maps.Clone(canonical.WorkspaceHasCustom)
	incoming.WorkspaceScrollColumns = canonical.WorkspaceScrollColumns
	if incoming.WorkspaceTrees != nil {
		incoming.WorkspaceTrees = canonical.WorkspaceTrees
		incoming.WindowToBSPID = canonical.WindowToBSPID
		incoming.NextBSPWindowID = canonical.NextBSPWindowID
	}
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
