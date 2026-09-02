package session

import "maps"

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
// A client sync used to replace the whole state, so any daemon-side mutation
// that happened after the client built its snapshot was silently undone. The
// functions below are what replaced that: on the fields the daemon owns, the
// daemon's value wins whenever the client is demonstrably behind.

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
	// Restored is set by the restore and cleared by the first attach, both
	// daemon-side. A bool cannot say "not sent", so canonical simply wins: no
	// client can either raise the mark or clear it by syncing.
	incoming.Restored = canonical.Restored
	if incoming.WorkspaceNames == nil {
		incoming.WorkspaceNames = canonical.WorkspaceNames
	}
	// The order is daemon-owned for the same reason the names are, and an
	// ordinary client sync omits it, so a nil incoming means "not sent" rather
	// than "cleared". Without this a client that has never reordered anything
	// would flatten the arrangement every other client is looking at.
	if incoming.WorkspaceOrder == nil {
		incoming.WorkspaceOrder = canonical.WorkspaceOrder
	}
	if incoming.ResurrectionVersion == 0 {
		incoming.ResurrectionVersion = canonical.ResurrectionVersion
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
	// out of the session - which is the whole failure this field exists to stop,
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

	// The custom-layout flags are unioned on the same terms and for the same
	// reason. A client only holds an entry for a workspace it has been told about
	// or arranged itself, so replacing the set would drop the flag for every
	// workspace the pushing client never heard of - the failure the field exists
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

	cwds := make(map[string]string, len(canonical.Windows))
	// The foreground command is read daemon-side on the detector's poll and no
	// client ever sends it, so canonical is always the truth: carrying it over by
	// id both preserves a live command and lets one that exited clear.
	fgCmds := make(map[string]string, len(canonical.Windows))
	// The shell pid is daemon-owned for the same reason and carried the same way:
	// only the daemon holds the process, and a client sync never reports one.
	// Without this every client push would strip the pid the corroboration needs.
	shellPIDs := make(map[string]int, len(canonical.Windows))
	// Agent state is daemon-owned and clients never set it, so it is carried over
	// by window id exactly as Cwd is; without this a client sync (which omits it)
	// would wipe every pane's reported state.
	type agent struct {
		state   AgentState
		message string
		harness string
		at      int64
	}
	agents := make(map[string]agent, len(canonical.Windows))
	// The popup mark and the size the popup was asked for are stamped once, when
	// the daemon creates the window, and nothing ever changes them. So canonical
	// is always the truth and they are carried over by id the way Cwd is.
	// Without this one client sync that omits them turns a popup back into an
	// ordinary floating pane on every screen, which tiles it away.
	type popup struct {
		width  string
		height string
	}
	popups := make(map[string]popup, len(canonical.Windows))
	for i := range canonical.Windows {
		w := &canonical.Windows[i]
		if cwd := w.Cwd; cwd != "" {
			cwds[w.ID] = cwd
		}
		if w.Popup {
			popups[w.ID] = popup{w.PopupWidth, w.PopupHeight}
		}
		if cmd := w.ForegroundCmd; cmd != "" {
			fgCmds[w.ID] = cmd
		}
		if pid := w.ShellPID; pid != 0 {
			shellPIDs[w.ID] = pid
		}
		if w.AgentState != AgentStateNone || w.AgentMessage != "" || w.AgentHarness != "" || w.AgentStateAt != 0 {
			agents[w.ID] = agent{w.AgentState, w.AgentMessage, w.AgentHarness, w.AgentStateAt}
		}
	}
	for i := range incoming.Windows {
		w := &incoming.Windows[i]
		if w.Cwd == "" {
			w.Cwd = cwds[w.ID]
		}
		if w.ForegroundCmd == "" {
			w.ForegroundCmd = fgCmds[w.ID]
		}
		if w.ShellPID == 0 {
			w.ShellPID = shellPIDs[w.ID]
		}
		if p, ok := popups[w.ID]; ok {
			w.Popup = true
			w.IsFloating = true
			w.PopupWidth = p.width
			w.PopupHeight = p.height
		}
		if a, ok := agents[w.ID]; ok && w.AgentState == AgentStateNone && w.AgentMessage == "" && w.AgentHarness == "" && w.AgentStateAt == 0 {
			w.AgentState = a.state
			w.AgentMessage = a.message
			w.AgentHarness = a.harness
			w.AgentStateAt = a.at
		}
	}
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
		win.Minimized = cw.Minimized
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
	// daemon never moves a master ratio - no headless operation touches it - so
	// canonical is not newer there by construction the way it is for the focus,
	// and a stale snapshot still reports a ratio the client itself just moved
	// correctly. What a stale snapshot can do is omit an entry it never learned,
	// and the union in retainDaemonExclusive, which runs on this path too, is what
	// stops that.
	incoming.FocusedWindowID = canonical.FocusedWindowID
	incoming.CurrentWorkspace = canonical.CurrentWorkspace
	if canonical.WorkspaceFocus != nil {
		incoming.WorkspaceFocus = maps.Clone(canonical.WorkspaceFocus)
	}
}
