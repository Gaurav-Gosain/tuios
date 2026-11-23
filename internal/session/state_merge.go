package session

import (
	"maps"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// Merging a client state sync into daemon-owned state.
//
// The daemon and its attached client both write session state, and they write
// different parts of it. The daemon owns what a user would be surprised to lose
// across a detach and reattach: which windows exist, what they are called, which
// workspace they are on, whether they are minimized, and what is focused. The
// client owns what is derived from its own viewport or is purely visual: pixel
// geometry, z-order, the shell-reported title, pre-restore geometry, alt-screen
// state, and the tiling topology it computes.
//
// A client sync does not replace the whole state, because that would silently
// undo any daemon-side mutation made after the client built its snapshot. On
// the fields the daemon owns, the daemon's value wins whenever the client is
// demonstrably behind.

// retainDaemonExclusive carries over the parts of canonical state that no client
// ever sets, so a sync that simply omits them does not wipe them. Options come
// from the JSON verb protocol; Cwd is captured daemon-side from the live shell
// process; ResurrectionVersion is stamped when state is written to disk.
func retainDaemonExclusive(incoming, canonical *SessionState) {
	if incoming.Options == nil {
		incoming.Options = canonical.Options
	}
	// The session label and accent are set only through their verbs, so a sync
	// that omits them (every client sync today) must not clear them. Clearing
	// stays the verb's job, which is why an empty incoming value is treated as
	// "not sent" rather than "set to empty".
	if incoming.DisplayName == "" {
		incoming.DisplayName = canonical.DisplayName
	}
	if incoming.Accent == "" {
		incoming.Accent = canonical.Accent
	}
	// The return note is written only by set-note, so canonical wins
	// unconditionally: a sync can neither set it nor clear it.
	incoming.Note = canonical.Note
	// The OSC 1 names are the emulators', filled into each snapshot as it
	// leaves (fillLiveFacts), so the stored state never holds any.
	incoming.IconNames = nil
	// Restored is set by the restore and cleared by the first attach, both
	// daemon-side. A bool cannot say "not sent", so canonical simply wins: no
	// client can either raise the mark or clear it by syncing.
	incoming.Restored = canonical.Restored
	// The master-stack shapes are written only by MsgMasterLayout, so the
	// daemon's copy is the whole answer and a push never moves it. See
	// master_layout.go.
	incoming.WorkspaceMasterLayout = canonical.WorkspaceMasterLayout
	// Whether the rail is shown is written only by MsgSidebarVisibility, on
	// the same terms. See sidebar_visibility.go.
	incoming.Sidebar = canonical.Sidebar
	// Global is stamped once, when the session is created, and canonical wins
	// for the same reason: a bool cannot say "not sent", and no client should
	// be able to turn an ordinary session into a global one by syncing.
	incoming.Global = canonical.Global
	// The space is written only by set-session-space and new-session, so
	// canonical wins the same way: no client sync can file a session under a
	// space or take it out.
	incoming.Space = canonical.Space
	// The rank is written only by set-session-order, the same way.
	incoming.Rank = canonical.Rank
	// The worktree record is written by detection and by the worktree verbs,
	// both daemon-side, so canonical wins the same way Restored does.
	incoming.Worktree = canonical.Worktree
	// The window moves are recorded by a move between sessions, daemon-side.
	incoming.WindowMoves = canonical.WindowMoves
	// The task cards are written only by the task verbs, so canonical wins
	// and no client sync can add, change or drop one. See task.go.
	incoming.Tasks, incoming.NextTaskID = canonical.Tasks, canonical.NextTaskID
	if incoming.WorkspaceNames == nil {
		incoming.WorkspaceNames = canonical.WorkspaceNames
	}
	// Older clients omit focus history.
	if incoming.FocusHistory == nil {
		incoming.FocusHistory = canonical.FocusHistory
	}
	// The herdr tab keys are written by a renumber and when a tab closes, both
	// daemon-side, so canonical wins. A display order is never kept: a build
	// that sent one arranged without renumbering, and the session's numbering
	// is the arrangement.
	incoming.WorkspaceTabKeys = canonical.WorkspaceTabKeys
	incoming.NextWorkspaceTabKey = canonical.NextWorkspaceTabKey
	incoming.WorkspaceOrder = nil
	if incoming.ResurrectionVersion == 0 {
		incoming.ResurrectionVersion = canonical.ResurrectionVersion
	}
	// The BSP trees are written by ops (see layout_tree.go), and a client that
	// sends them that way leaves them out of its push. So a push without trees
	// is saying nothing about them, and the session keeps its own, with the
	// numbering the trees' leaves are read through. A push that does carry
	// trees is from a client too old to send ops, and it is taken as sent, which
	// is what every daemon did before: its trees and its numbering replace the
	// session's together, so the two still agree.
	if incoming.WorkspaceTrees == nil {
		incoming.WorkspaceTrees = canonical.WorkspaceTrees
		incoming.WindowToBSPID = canonical.WindowToBSPID
		incoming.NextBSPWindowID = canonical.NextBSPWindowID
	}
	// The agreed pane geometry is written by every current client on every
	// push, so nil means a client that predates the field, and letting it wipe
	// the agreement would put the session back where the field started: every
	// client on its own arithmetic.
	if incoming.PaneGeometry == nil {
		incoming.PaneGeometry = canonical.PaneGeometry
	}
	// The strip is carried over on the same terms, and for one more reason
	// besides: a client says nothing about it whenever it is not in the
	// scrolling layout, so without this the session would forget where its strip
	// is every time anybody pushed from another layout, and a client joining
	// later would come up at the left end.
	if incoming.ScrollStrip == nil {
		incoming.ScrollStrip = canonical.ScrollStrip
	}
	// The per-workspace master ratios are unioned rather than replaced. Every
	// current client sends every entry it holds, but a client only ever holds the
	// ones it has been told about or tuned itself, so a snapshot built before
	// another client tuned a workspace would otherwise drop that workspace's ratio
	// out of the session, which is the whole failure this field exists to stop,
	// arriving one layer lower down. Nothing ever removes an entry, so the union
	// is the complete answer, and the incoming value wins where both sides hold
	// one: that is a client saying the ratio moved. A nil incoming map (an older
	// peer, or a client with tiling off) is left holding the canonical set for the
	// reason the pane geometry is.
	if len(canonical.WorkspaceMasterRatio) > 0 {
		if incoming.WorkspaceMasterRatio == nil {
			incoming.WorkspaceMasterRatio = make(map[int]float64, len(canonical.WorkspaceMasterRatio))
		}
		for ws, ratio := range canonical.WorkspaceMasterRatio {
			if _, ok := incoming.WorkspaceMasterRatio[ws]; !ok {
				incoming.WorkspaceMasterRatio[ws] = ratio
			}
		}
	}
	// The stack ratios beside them are unioned on the same terms and for the
	// same reason.
	if len(canonical.WorkspaceStackRatio) > 0 {
		if incoming.WorkspaceStackRatio == nil {
			incoming.WorkspaceStackRatio = make(map[int]float64, len(canonical.WorkspaceStackRatio))
		}
		for ws, ratio := range canonical.WorkspaceStackRatio {
			if _, ok := incoming.WorkspaceStackRatio[ws]; !ok {
				incoming.WorkspaceStackRatio[ws] = ratio
			}
		}
	}
	// And the splits of every other pane, on the same terms again.
	if len(canonical.WorkspaceMasterSplits) > 0 {
		if incoming.WorkspaceMasterSplits == nil {
			incoming.WorkspaceMasterSplits = make(map[int]layout.MasterSplits, len(canonical.WorkspaceMasterSplits))
		}
		for ws, sp := range canonical.WorkspaceMasterSplits {
			if _, ok := incoming.WorkspaceMasterSplits[ws]; !ok {
				incoming.WorkspaceMasterSplits[ws] = sp.Clone()
			}
		}
	}

	// The custom-layout flags are unioned on the same terms and for the same
	// reason. A client only holds an entry for a workspace it has been told about
	// or arranged itself, so replacing the set would drop the flag for every
	// workspace the pushing client never heard of: the failure the field exists
	// to stop, one layer lower down. The incoming value wins where both sides hold
	// one, including an incoming false: a client that moved a pane off a workspace
	// clears the flag there, and that is news. A push with no entry for a
	// workspace is saying nothing about it and keeps what the session holds.
	if len(canonical.WorkspaceHasCustom) > 0 {
		if incoming.WorkspaceHasCustom == nil {
			incoming.WorkspaceHasCustom = make(map[int]bool, len(canonical.WorkspaceHasCustom))
		}
		for ws, custom := range canonical.WorkspaceHasCustom {
			if _, ok := incoming.WorkspaceHasCustom[ws]; !ok {
				incoming.WorkspaceHasCustom[ws] = custom
			}
		}
	}

	// The per-workspace layout modes are unioned on the same terms and for the
	// same reason. A client only holds an entry for a workspace it has been told
	// about or chose a layout on, so replacing the set would drop the choice for
	// every workspace the pushing client never heard of: an older client attached
	// to the same session would wipe every choice out of the daemon with one
	// push, and the next client to attach would find none of them.
	if len(canonical.WorkspaceLayoutModes) > 0 {
		if incoming.WorkspaceLayoutModes == nil {
			incoming.WorkspaceLayoutModes = make(map[int]string, len(canonical.WorkspaceLayoutModes))
		}
		for ws, mode := range canonical.WorkspaceLayoutModes {
			if _, ok := incoming.WorkspaceLayoutModes[ws]; !ok {
				incoming.WorkspaceLayoutModes[ws] = mode
			}
		}
	}

	// A push lists the windows in the session's order, so the window at the same
	// position is the match; the index by ID is built only at the first miss.
	// With two windows of one ID the last one the index holds is taken.
	var index map[string]int
	var absent WindowState
	for i := range incoming.Windows {
		w := &incoming.Windows[i]
		cw, held := &absent, false
		if i < len(canonical.Windows) && canonical.Windows[i].ID == w.ID {
			cw, held = &canonical.Windows[i], true
		} else if len(canonical.Windows) > 0 {
			if index == nil {
				index = make(map[string]int, len(canonical.Windows))
				for j := range canonical.Windows {
					index[canonical.Windows[j].ID] = j
				}
			}
			if j, ok := index[w.ID]; ok {
				cw, held = &canonical.Windows[j], true
			}
		}
		carryDaemonOwned(w, cw, held, len(canonical.Windows) > 0)
	}
}

// carryDaemonOwned sets the fields of w that only the daemon writes from cw,
// the session's copy of the same window. Where the daemon holds a value it wins
// over whatever the push says: no client reads these from anywhere but the
// daemon, so a different value in a push is an older copy. Where it holds none,
// the push's value stands for the fields that say so (Cwd, ShellPID, Host).
// held is false for a window the session does not hold, and cw is then the zero
// window. sessionHasWindows is false for a session that holds no window.
func carryDaemonOwned(w, cw *WindowState, held, sessionHasWindows bool) {
	// When a window was minimized is the daemon's stamp (stampMinimizedLocked):
	// a client knows it only from a push, so its copy is never newer.
	if sessionHasWindows {
		w.MinimizedAt = cw.MinimizedAt
	}
	// Where the session holds none, the push's value stands: a restore hands in
	// the directory and the pid of the shell it just respawned to a session
	// created empty.
	if cw.Cwd != "" || w.Cwd == "" {
		w.Cwd = cw.Cwd
	}
	// Only the daemon holds the process and a push never reports one, and the
	// corroboration reads the pid, so a push must not strip it.
	if cw.ShellPID != 0 || w.ShellPID == 0 {
		w.ShellPID = cw.ShellPID
	}
	// The machine is stamped once, when the daemon creates the window, and
	// nothing moves a process between machines. The rail reads it to name the
	// machine and the file section to ask the right one for a directory.
	if cw.Host != "" || w.Host == "" {
		w.Host = cw.Host
	}
	// A window's start folder is fixed by the daemon, and a grant is held to it,
	// so a push can neither set one nor move it, even where the session has none.
	w.StartDir = cw.StartDir
	// The return note is written only by set-note.
	w.Note = cw.Note
	// The foreground command is read daemon-side on the detector's poll, and a
	// command that exited has to be able to clear, so the session's wins, empty
	// included.
	w.ForegroundCmd = cw.ForegroundCmd
	w.SSHOffer = cw.SSHOffer
	// The turn count only goes up: a client echoes the value it was last sent,
	// which can be behind, and an older client sends none.
	if cw.CompletionSeq > w.CompletionSeq {
		w.CompletionSeq = cw.CompletionSeq
	}
	// What draws a window and what it shows are set when it is made.
	w.Kind, w.URI = cw.Kind, cw.URI
	// The rail pane's mark is the daemon's, like the scratch one.
	w.Rail = cw.Rail
	// Only the daemon marks the scratch popup and a scratch pane's workspace
	// (a tiled window on its group's workspace), so a push can neither set the
	// mark on another pane, nor clear it, nor move a scratch pane off that
	// workspace or put an ordinary window on one.
	w.Scratch, w.ScratchName = cw.Scratch, ""
	if cw.Scratch {
		w.ScratchName = cw.ScratchName
	}
	switch {
	case cw.Scratch && IsScratchWorkspace(cw.Workspace):
		w.Workspace = cw.Workspace
	case IsScratchWorkspace(w.Workspace) && held:
		w.Workspace = cw.Workspace
	}
	// The popup mark, its requested size and the dim override are stamped once,
	// when the daemon creates the window. A sync that omits them would turn a
	// popup back into a floating pane, which tiles it away.
	w.PopupDim = nil // A client cannot set, clear or replace the override.
	if cw.Popup {
		w.PopupDim = cw.PopupDim
		w.Popup = true
		w.IsFloating = true
		// The scratch terminal takes its size from [scratch] on each show, so
		// the client that shows it may change the size. Any other popup keeps
		// the size its caller asked for.
		if !cw.Scratch || w.PopupWidth == "" {
			w.PopupWidth = cw.PopupWidth
		}
		if !cw.Scratch || w.PopupHeight == "" {
			w.PopupHeight = cw.PopupHeight
		}
	}
	// The kind of a block and the harness's session id are the daemon's too.
	w.AgentKind, w.AgentSessionID, w.AgentSessionHarness, w.AgentFlags = cw.AgentKind, cw.AgentSessionID, cw.AgentSessionHarness, cw.AgentFlags
	// Agent state is taken whatever the push says: the TUI never sends it, so a
	// value in a push is an echo of an older state, and taking it would let a
	// client put an old state back or set one around the precedence
	// set-agent-state enforces.
	w.AgentState, w.AgentMessage, w.AgentHarness, w.AgentStateAt = cw.AgentState, cw.AgentMessage, cw.AgentHarness, cw.AgentStateAt
	w.AgentReason = ""
	if cw.AgentState != AgentStateNone || cw.AgentMessage != "" || cw.AgentHarness != "" || cw.AgentStateAt != 0 {
		w.AgentReason = cw.AgentReason
	}
	// Agent metadata has its own verb: a pane can have metadata and no state.
	w.AgentMeta = nil
	if len(cw.AgentMeta) > 0 {
		w.AgentMeta = cw.AgentMeta
	}
	// The queue length and the subagent count are the daemon's counts (only the
	// pane's hooks move the latter), and only mark-attention and the daemon's own
	// wake move the settle.
	w.AgentQueued, w.AgentDraftHeld, w.AgentSettled, w.AgentQuiet = cw.AgentQueued, cw.AgentDraftHeld, cw.AgentSettled, cw.AgentQuiet
	w.AgentSubagents = cw.AgentSubagents
	w.AgentSubagentList = nil
	if len(cw.AgentSubagentList) != 0 {
		w.AgentSubagentList = cw.AgentSubagentList
	}
	// A pane's OSC 7501 records are written only by its emulator. Grants are set
	// by the daemon alone: this copy is shown and saved, and they are enforced
	// from the daemon's own table.
	w.ProgramStatus, w.Grants = cw.ProgramStatus, cw.Grants
	// Which pane started a pane, the agent link and a delegation record
	// (verb_delegate.go) are the daemon's records of a call.
	w.StartedBy, w.AgentLink, w.Delegation = cw.StartedBy, cw.AgentLink, cw.Delegation
	// The chain depth is stamped when the daemon starts the pane, and the depth
	// cap reads it.
	w.ChainDepth = cw.ChainDepth
}

// reconcileStale rewrites a client snapshot that was built before a daemon-side
// mutation the client has never seen. Every field the daemon owns is taken from
// canonical state, because canonical is newer there by construction; everything
// the client owns is left as the client sent it.
//
// hasLivePTY reports whether a PTY is still open on the daemon. It is how a
// window missing from the client's snapshot is classified: closing a window
// closes its PTY before the sync goes out, so a missing window whose PTY is gone
// was closed by the client and stays closed, while one whose PTY is still live
// was created by the daemon after the snapshot and is restored.
// keepDaemonZoom takes each window's zoom from canonical into a stale push.
// Zoom is shared, and zoom-window sets it in the daemon with a client
// attached. A push the client built before that state reached it still says
// the pane is not zoomed, and taking it ended the zoom before any client drew
// it.
func keepDaemonZoom(incoming, canonical *SessionState) {
	for i := range incoming.Windows {
		w := &incoming.Windows[i]
		if cw, ok := findWindowState(canonical, w.ID); ok {
			w.Zoomed = cw.Zoomed
			w.PreZoomX, w.PreZoomY, w.PreZoomW, w.PreZoomH = cw.PreZoomX, cw.PreZoomY, cw.PreZoomW, cw.PreZoomH
		}
	}
}

func reconcileStale(incoming, canonical *SessionState, hasLivePTY func(ptyID string) bool) {
	canonicalByID := make(map[string]*WindowState, len(canonical.Windows))
	for i := range canonical.Windows {
		canonicalByID[canonical.Windows[i].ID] = &canonical.Windows[i]
	}

	seen := make(map[string]bool, len(incoming.Windows))
	kept := incoming.Windows[:0]
	for i := range incoming.Windows {
		win := incoming.Windows[i]
		cw, ok := canonicalByID[win.ID]
		if !ok {
			// A window the daemon has never heard of. Clients do not create
			// windows any more (they ask the daemon to, and it pushes the result
			// back), so this is not news, it is a snapshot taken before the daemon
			// closed the window. Keeping it would undo the close, which is exactly
			// what happened when the user pressed the close chord: the intent
			// removed the window and the keystroke's own state push put it back.
			continue
		}
		win.CustomName = cw.CustomName
		win.Workspace = cw.Workspace
		// A stack member's minimized flag says which member is open, and it
		// goes with the tree op the same client sent for the edit, which the
		// daemon took (tree ops are never stale). The stack key and order
		// come from the push as well. Taking canonical's flag here split the
		// two: a stack edit pushed just after another change came back with
		// the member it collapsed open and with no tile, and every client's
		// tiler gave it a new tile beside the stack.
		if win.Stack == "" && cw.Stack == "" {
			win.Minimized = cw.Minimized
		}
		seen[win.ID] = true
		kept = append(kept, win)
	}
	incoming.Windows = kept

	for i := range canonical.Windows {
		win := canonical.Windows[i]
		if seen[win.ID] {
			continue
		}
		if win.PTYID != "" && !hasLivePTY(win.PTYID) {
			continue // closed by the client; the close stands
		}
		incoming.Windows = append(incoming.Windows, win)
	}

	// The strip offset is the offset of the workspace being shown, so it is only
	// meaningful beside the workspace it was measured on. Where the workspace is
	// rewritten to the daemon's, the offset the client sent belongs to the
	// workspace it thought it was on and would scroll everyone's strip to a
	// place taken from a different one, so the daemon's travels with it. On the
	// same workspace the client's own offset stands: a scroll is exactly the
	// kind of thing a client owns and a stale snapshot still reports correctly.
	if canonical.CurrentWorkspace != incoming.CurrentWorkspace {
		incoming.ScrollStrip = canonical.ScrollStrip
	}

	// WorkspaceHasCustom is deliberately not taken from canonical here, for the
	// reason spelled out for the master ratio below: the daemon never marks a
	// layout custom and never clears one, so canonical is not newer there by
	// construction, and a stale snapshot still reports a flag the client itself
	// just set correctly. What a stale snapshot can do is omit an entry it never
	// learned, and the union in retainDaemonExclusive, which runs on this path
	// too, is what stops that.

	// WorkspaceMasterRatio is deliberately not taken from canonical here. The
	// daemon never moves a master ratio (no headless operation touches it), so
	// canonical is not newer there by construction the way it is for the focus,
	// and a stale snapshot still reports a ratio the client itself just moved
	// correctly. What a stale snapshot can do is omit an entry it never learned,
	// and the union in retainDaemonExclusive, which runs on this path too, is what
	// stops that. WorkspaceStackRatio is left alone for the same reason.
	incoming.FocusedWindowID = canonical.FocusedWindowID
	incoming.CurrentWorkspace = canonical.CurrentWorkspace
	if canonical.WorkspaceFocus != nil {
		incoming.WorkspaceFocus = maps.Clone(canonical.WorkspaceFocus)
	}
	if canonical.FocusHistory != nil {
		incoming.FocusHistory = CloneFocusHistory(canonical.FocusHistory)
	}
}

// CloneFocusHistory returns an independent copy of a workspace MRU map.
func CloneFocusHistory(history map[int][]string) map[int][]string {
	if history == nil {
		return nil
	}
	clone := make(map[int][]string, len(history))
	for workspace, ids := range history {
		clone[workspace] = slices.Clone(ids)
	}
	return clone
}

// RecordFocus places id at the front of workspace's MRU history.
func RecordFocus(history map[int][]string, workspace int, id string) map[int][]string {
	const maxFocusHistory = 16

	if id == "" {
		return history
	}
	if history == nil {
		history = make(map[int][]string)
	}
	items := history[workspace]
	kept := items[:0]
	for _, candidate := range items {
		if candidate != id {
			kept = append(kept, candidate)
		}
	}
	items = append([]string{id}, kept...)
	if len(items) > maxFocusHistory {
		items = items[:maxFocusHistory]
	}
	history[workspace] = items
	return history
}

// RemoveFocus removes id from every MRU history.
func RemoveFocus(history map[int][]string, id string) map[int][]string {
	if history == nil {
		return nil
	}
	for workspace, items := range history {
		kept := items[:0]
		for _, candidate := range items {
			if candidate != id {
				kept = append(kept, candidate)
			}
		}
		if len(kept) == 0 {
			delete(history, workspace)
		} else {
			history[workspace] = kept
		}
	}
	return history
}

// focusView is the part of a state that says what the person is looking at:
// the focused window, the workspace, each workspace's focus, and the strip
// offset that is only meaningful beside the workspace.
type focusView struct {
	window    string
	workspace int
	perWS     map[int]string
	history   map[int][]string
	strip     *ScrollStripState
}

func focusViewOf(s *SessionState) focusView {
	if s == nil {
		return focusView{}
	}
	return focusView{
		window:    s.FocusedWindowID,
		workspace: s.CurrentWorkspace,
		perWS:     maps.Clone(s.WorkspaceFocus),
		history:   CloneFocusHistory(s.FocusHistory),
		strip:     s.ScrollStrip,
	}
}

// sameFocus reports whether two views name the same focus. The strip offset is
// left out: scrolling is not a move of the focus.
func (f focusView) sameFocus(g focusView) bool {
	return f.window == g.window && f.workspace == g.workspace && maps.Equal(f.perWS, g.perWS)
}

// keepSessionFocus puts the session's focus on a push that did not move it
// (SessionState.FocusUnmoved), and reports whether that changed the push.
//
// The session's focus is kept only when its window is in the push on the
// workspace the session names. Otherwise the push's focus stands, as it did
// before the flag existed.
func keepSessionFocus(incoming, canonical *SessionState) bool {
	theirs := focusViewOf(canonical)
	if theirs.window != "" {
		found := false
		for i := range incoming.Windows {
			if w := &incoming.Windows[i]; w.ID == theirs.window {
				found = w.Workspace == theirs.workspace
				break
			}
		}
		if !found {
			return false
		}
	}
	mine := focusViewOf(incoming)
	// A session that holds no strip offset has none to keep, and the push's
	// stands.
	strip := theirs.strip
	if strip == nil {
		strip = mine.strip
	}
	if mine.sameFocus(theirs) && sameStrip(mine.strip, strip) {
		return false
	}
	incoming.FocusedWindowID = theirs.window
	incoming.CurrentWorkspace = theirs.workspace
	incoming.WorkspaceFocus = theirs.perWS
	incoming.FocusHistory = theirs.history
	incoming.ScrollStrip = strip
	return true
}

// sameStrip reports whether two strip offsets are the same, nil being none.
func sameStrip(a, b *ScrollStripState) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// keepClientFocus puts a stale push's own focus back after reconcileStale took
// the daemon's. It is for a push whose snapshot predates some daemon mutation
// but not any that moved the focus: then the focus in it is the person's own
// latest move, and canonical's is the one they just left. Taking canonical's
// there snapped the focus back whenever an agent reported a state between a
// keypress and its push, and the reconcile reply carried the old focus to the
// client.
//
// The push's focus is kept only when its window survived the merge on the
// workspace the push names. Otherwise the daemon's stands.
func keepClientFocus(incoming *SessionState, mine focusView) {
	if mine.window != "" {
		found := false
		for i := range incoming.Windows {
			if w := &incoming.Windows[i]; w.ID == mine.window {
				found = w.Workspace == mine.workspace
				break
			}
		}
		if !found {
			return
		}
	}
	incoming.FocusedWindowID = mine.window
	incoming.CurrentWorkspace = mine.workspace
	incoming.WorkspaceFocus = mine.perWS
	incoming.FocusHistory = mine.history
	incoming.ScrollStrip = mine.strip
}

// stampMinimizedLocked stamps now on every minimized window with no stamp yet
// and clears the stamp of every window that is not minimized, and reports
// whether it stamped one. Windows minimized by one write are stamped in window
// order, a nanosecond apart, so the order is total.
func stampMinimizedLocked(state *SessionState, now int64) (stamped bool) {
	if state == nil {
		return false
	}
	for i := range state.Windows {
		w := &state.Windows[i]
		switch {
		case !w.Minimized:
			w.MinimizedAt = 0
		case w.MinimizedAt == 0:
			w.MinimizedAt = now
			now++
			stamped = true
		}
	}
	return stamped
}
