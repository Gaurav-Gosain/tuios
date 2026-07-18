package session

import (
	"fmt"
	"log"
)

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

	for i := range restored.Windows {
		w := &restored.Windows[i]

		// WindowState dimensions are the outer window box (including the border);
		// the shell gets the inner content size, matching AddDaemonWindow.
		ptyWidth := max(w.Width-2, 1)
		ptyHeight := max(w.Height-2, 1)

		pty, err := sess.RestorePTY(w.ID, ptyWidth, ptyHeight, w.Cwd, onExit)
		if err != nil {
			LogError("Failed to respawn shell for restored window %s: %v", shortID(w.ID), err)
			continue
		}
		w.PTYID = pty.ID
	}

	sess.UpdateState(&restored)
	return sess, nil
}
