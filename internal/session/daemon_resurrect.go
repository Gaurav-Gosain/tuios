package session

import (
	"fmt"
	"log"
	"maps"
	"slices"
)

// hasWindow reports whether id names one of these windows.
func hasWindow(windows []WindowState, id string) bool {
	return slices.ContainsFunc(windows, func(w WindowState) bool { return w.ID == id })
}

// restoreAllSessions recreates every resurrectable session that is not already
// live. It is called once on daemon start (unless auto-restore is disabled).
// Corrupt or incompatible state files are archived and skipped; a failure to
// restore one session never blocks the others or the daemon.
func (d *Daemon) restoreAllSessions() {
	names, err := ListResurrectableSessions()
	if err != nil {
		LogError("Failed to list resurrectable sessions: %v", err)
		return
	}

	for _, name := range names {
		if d.manager.GetSession(name) != nil {
			continue // already live
		}
		state, err := LoadResurrectionState(name)
		if err != nil {
			// Corrupt/incompatible files are archived inside LoadResurrectionState.
			log.Printf("Skipping resurrection of %q: %v", name, err)
			continue
		}
		if len(state.Windows) == 0 {
			// It can never restore, so leaving it would keep offering a session
			// that 'tuios resurrect' lists and cannot bring back.
			log.Printf("Discarding saved state for %q: it has no windows", name)
			RemoveResurrectionState(name)
			continue
		}
		if _, err := d.restoreSession(state); err != nil {
			LogError("Failed to restore session %q: %v", name, err)
			continue
		}
		log.Printf("Restored session %q (%d windows)", name, len(state.Windows))
	}
}

// restoreSession recreates a single session from a saved SessionState. It
// respawns a fresh shell for every window (in the window's saved cwd, marked as
// restored) and remaps each window to its new PTY, since the PTY IDs from the
// previous daemon are dead. If the session is already live it is returned
// unchanged.
func (d *Daemon) restoreSession(state *SessionState) (*Session, error) {
	if state == nil || state.Name == "" {
		return nil, fmt.Errorf("cannot restore session from empty state")
	}

	if existing := d.manager.GetSession(state.Name); existing != nil {
		return existing, nil
	}

	// A session whose windows were all closed leaves a state file behind, and
	// restoring it produced a session with nothing in it that no surface tells
	// apart from a real one. There is nothing to bring back, so it is not a
	// session; refusing here covers the automatic restore and the on-demand
	// 'tuios resurrect' alike. Checked after the live lookup above, which is
	// about the session that already exists rather than about what was saved.
	if len(state.Windows) == 0 {
		return nil, fmt.Errorf("saved state for session %q has no windows, there is nothing to restore", state.Name)
	}

	width, height := state.Width, state.Height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	// No client is connected at restore time, so the shell/term config falls
	// back to daemon defaults (getShell uses $SHELL).
	sess, err := d.manager.CreateSession(state.Name, &SessionConfig{}, width, height)
	if err != nil {
		return nil, err
	}

	sessionID := sess.ID
	onExit := func(ptyID string) {
		d.notifyPTYClosed(sessionID, ptyID)
	}

	// Work on a copy so the original (loaded) state is untouched.
	restored := *state
	restored.Windows = make([]WindowState, len(state.Windows))
	copy(restored.Windows, state.Windows)
	restored.WorkspaceFocus = maps.Clone(state.WorkspaceFocus)
	restored.WorkspaceMasterRatio = maps.Clone(state.WorkspaceMasterRatio)
	restored.WorkspaceHasCustom = maps.Clone(state.WorkspaceHasCustom)

	// A window whose shell will not start is dropped rather than kept. Keeping it
	// left its PTYID naming the dead daemon's PTY, which nothing will ever answer
	// to: no output, no input, no way to revive it, and no UI anywhere that draws
	// a pane as dead. Closing the window is what the daemon already does whenever
	// a PTY goes away (see notifyPTYClosed), so the restore does the same.
	kept := restored.Windows[:0]
	for i := range restored.Windows {
		w := &restored.Windows[i]

		// A popup does not survive the daemon. It is a pane for one command, and
		// a restore respawns a shell rather than the command, so bringing one
		// back gives the user a floating box holding a shell that will never
		// exit and that nothing asked for. Dropping it is the same answer the
		// loop gives a window whose shell will not start.
		if w.Popup {
			debugLog("[DEBUG] dropping restored popup %s, a popup lives only as long as its command", shortID(w.ID))
			continue
		}

		// WindowState dimensions are the outer window box (including the border);
		// the shell gets the inner content size, matching AddDaemonWindow.
		ptyWidth := max(w.Width-2, 1)
		ptyHeight := max(w.Height-2, 1)

		pty, err := sess.RestorePTY(w.ID, ptyWidth, ptyHeight, w.Cwd, onExit)
		if err != nil {
			LogError("Dropping restored window %s, its shell could not be respawned: %v", shortID(w.ID), err)
			continue
		}
		w.PTYID = pty.ID
		// The pid of the shell just respawned. Resurrection state carries no pid
		// (see WindowState.ShellPID), so without this a restored pane waits for
		// the detector's poll before the rail can check what it reports.
		w.ShellPID = pty.ShellPID()
		kept = append(kept, *w)
	}
	restored.Windows = kept

	// Focus must not name a window that was just dropped, or the client restores
	// a focus onto a pane that is not there.
	if restored.FocusedWindowID != "" && !hasWindow(kept, restored.FocusedWindowID) {
		restored.FocusedWindowID = ""
	}
	for ws, id := range restored.WorkspaceFocus {
		if !hasWindow(kept, id) {
			delete(restored.WorkspaceFocus, ws)
		}
	}

	sess.UpdateState(&restored)
	// After UpdateState, which takes this field from canonical state and would
	// undo it if the restore wrote it into the pushed snapshot instead.
	sess.MarkRestored()
	return sess, nil
}
