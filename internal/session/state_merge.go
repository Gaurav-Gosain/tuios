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
	if incoming.ResurrectionVersion == 0 {
		incoming.ResurrectionVersion = canonical.ResurrectionVersion
	}

	cwds := make(map[string]string, len(canonical.Windows))
	for i := range canonical.Windows {
		if cwd := canonical.Windows[i].Cwd; cwd != "" {
			cwds[canonical.Windows[i].ID] = cwd
		}
	}
	for i := range incoming.Windows {
		if incoming.Windows[i].Cwd == "" {
			incoming.Windows[i].Cwd = cwds[incoming.Windows[i].ID]
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
	for i := range incoming.Windows {
		win := &incoming.Windows[i]
		seen[win.ID] = true
		cw, ok := canonicalByID[win.ID]
		if !ok {
			// A window the daemon does not know about yet: the client just
			// created it, and this sync is how the daemon learns of it.
			continue
		}
		win.CustomName = cw.CustomName
		win.Workspace = cw.Workspace
		win.Minimized = cw.Minimized
	}

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

	incoming.FocusedWindowID = canonical.FocusedWindowID
	incoming.CurrentWorkspace = canonical.CurrentWorkspace
	if canonical.WorkspaceFocus != nil {
		incoming.WorkspaceFocus = maps.Clone(canonical.WorkspaceFocus)
	}
}
