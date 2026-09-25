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

// clearLiveAgent drops what a saved window says about the agent running in it
// and the program in its foreground, for a window whose process is gone. The
// conversation id and its harness are kept: they name a conversation on disk,
// not a process.
func clearLiveAgent(w *WindowState) {
	w.AgentState = AgentStateNone
	w.AgentMessage = ""
	w.AgentKind = ""
	w.AgentStateAt = 0
	w.AgentHarness = ""
	w.AgentMeta = nil
	// The delivery queue lives in daemon memory and dies with it, so a saved
	// count names messages nobody holds any more.
	w.AgentQueued = 0
	w.ForegroundCmd = ""
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
		_, offers, err := d.restoreSessionOffers(state)
		if err != nil {
			LogError("Failed to restore session %q: %v", name, err)
			continue
		}
		// Opened by the caller once the Inbox has loaded; see Daemon.Run.
		d.pendingResumes = append(d.pendingResumes, offers...)
		log.Printf("Restored session %q (%d windows)", name, len(state.Windows))
	}
}

// restoreSession recreates a single session from a saved SessionState. It
// respawns a fresh shell for every window (in the window's saved cwd, marked as
// restored) and remaps each window to its new PTY, since the PTY IDs from the
// previous daemon are dead. If the session is already live it is returned
// unchanged.
//
// It also offers to resume the agent conversations the restored panes held,
// as daemon.resume_agents says. See agent_resume.go.
func (d *Daemon) restoreSession(state *SessionState) (*Session, error) {
	sess, offers, err := d.restoreSessionOffers(state)
	if err != nil {
		return nil, err
	}
	d.applyResumeOffers(offers)
	return sess, nil
}

// restoreSessionOffers is restoreSession without acting on the resume offers:
// it returns them, one for each restored pane with a conversation that can be
// resumed. A session that was already live has none.
func (d *Daemon) restoreSessionOffers(state *SessionState) (*Session, []resumeOffer, error) {
	if state == nil || state.Name == "" {
		return nil, nil, fmt.Errorf("cannot restore session from empty state")
	}

	if existing := d.manager.GetSession(state.Name); existing != nil {
		return existing, nil, nil
	}

	// A session whose windows were all closed leaves a state file behind, and
	// restoring it produced a session with nothing in it that no surface tells
	// apart from a real one. There is nothing to bring back, so it is not a
	// session; refusing here covers the automatic restore and the on-demand
	// 'tuios resurrect' alike. Checked after the live lookup above, which is
	// about the session that already exists rather than about what was saved.
	if len(state.Windows) == 0 {
		return nil, nil, fmt.Errorf("saved state for session %q has no windows, there is nothing to restore", state.Name)
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
		return nil, nil, err
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
	restored.WorkspaceStackRatio = maps.Clone(state.WorkspaceStackRatio)
	restored.WorkspaceHasCustom = maps.Clone(state.WorkspaceHasCustom)

	// A window whose shell will not start is dropped rather than kept. Keeping it
	// left its PTYID naming the dead daemon's PTY, which nothing will ever answer
	// to: no output, no input, no way to revive it, and no UI anywhere that draws
	// a pane as dead. Closing the window is what the daemon already does whenever
	// a PTY goes away (see notifyPTYClosed), so the restore does the same.
	kept := restored.Windows[:0]
	saved := make(map[string]WindowState, len(restored.Windows))
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

		// A window whose process was on another machine comes back on this one,
		// and it has to stop claiming otherwise.
		//
		// A restore respawns a shell here from saved state. It does not dial a
		// host, and it should not: resurrection runs when the daemon starts,
		// which is exactly when links are not up yet, and a restore that waited
		// on a machine that may never answer would hold the whole session.
		//
		// So the record is corrected to match what was actually started. Left
		// alone it would be worse than a gap: the frame marks a pane with the
		// machine its shell runs on, and a mark that names the wrong machine is
		// read and believed. The directory goes with it, because it was a path
		// over there and means nothing here.
		if w.Host != "" {
			debugLog("[DEBUG] restored window %s ran on %s; it comes back on this machine", shortID(w.ID), w.Host)
			w.Host = ""
			w.Cwd = ""
			// The conversation was on that machine too, and a resume here
			// would name one this machine does not have.
			w.AgentSessionID = ""
			w.AgentSessionHarness = ""
		}

		// The window as it was saved, for the resume offer, which asks whether
		// an agent was running in it then.
		saved[w.ID] = *w

		// The pane gets a new shell and no agent, so it must not keep claiming
		// the one it had. Left alone, the saved state came back as sent: a pane
		// at a fresh prompt showed working under the old harness, and nothing
		// cleared it, because the detector clears only a claim whose process it
		// saw. It also made the next save say an agent was live, so the resume
		// offer came back on every restart. The conversation id stays.
		clearLiveAgent(w)

		// A pane saved with grants of its own comes back holding them, from
		// the new shell's first instruction. Without this a pane narrowed on
		// purpose would come back holding the default, which under open is
		// admin.
		pty, err := sess.restorePTYWithGrants(w.ID, ptyWidth, ptyHeight, w.Cwd, savedGrants(w.Grants), onExit)
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

	// Read before UpdateState, which rewrites the daemon-owned fields of the
	// windows it is handed in place.
	var offers []resumeOffer
	ids := make(map[string]WindowState, len(kept))
	grants := make(map[string][]string)
	for _, w := range kept {
		if w.Grants != nil {
			grants[w.ID] = w.Grants
		}
		if w.AgentSessionID == "" {
			continue
		}
		ids[w.ID] = w
		if o, ok := d.resumeOfferFor(state.Name, saved[w.ID]); ok {
			offers = append(offers, o)
		}
	}

	sess.UpdateState(&restored)
	// After UpdateState, which takes this field from canonical state and would
	// undo it if the restore wrote it into the pushed snapshot instead.
	sess.MarkRestored()

	// The worktree record goes back on for the same reason: it is
	// daemon-owned, and the canonical one is what the respawned first shell
	// detected, which knows the repository and the branch and nothing tuios
	// wrote. A fan's sessions lost their group and their managed mark on every
	// restart, so review fell back to the default base and compare found no
	// fan. A detected record is left to detection, which follows the shell.
	if wt := restoredWorktree(state.Worktree); wt != nil {
		_ = sess.SetWorktree(wt)
	}

	// The conversation ids go back on after UpdateState for the same reason:
	// they are daemon-owned, UpdateState takes them from canonical state, and
	// the session was created empty. Without this every restore dropped the
	// ids the state file had kept for exactly this moment.
	if len(ids) > 0 {
		_ = sess.mutateState(func(st *SessionState) error {
			for i := range st.Windows {
				if saved, ok := ids[st.Windows[i].ID]; ok {
					st.Windows[i].AgentSessionID = saved.AgentSessionID
					st.Windows[i].AgentSessionHarness = saved.AgentSessionHarness
				}
			}
			return nil
		})
	}
	// The grants' copy in the state goes back on for the same reason. The
	// panes already hold them: the table was given them before each shell
	// started.
	if len(grants) > 0 {
		_ = sess.mutateState(func(st *SessionState) error {
			for i := range st.Windows {
				if g, ok := grants[st.Windows[i].ID]; ok {
					st.Windows[i].Grants = grantNamesPtr(savedGrants(g))
				}
			}
			return nil
		})
	}
	return sess, offers, nil
}

// restoredWorktree is a saved managed worktree record as a restored session
// takes it, or nil for a record detection owns. A fan prompt still waiting
// when the daemon went down is said not to have been sent: nothing resumes the
// wait across a restart, so a record left pending would say so forever. A
// check that was running needs nothing here; fanVerifyReport reads it as ended
// by the restart.
func restoredWorktree(saved *WorktreeInfo) *WorktreeInfo {
	if saved == nil || !saved.Managed {
		return nil
	}
	wt := *saved
	if PromptWaiting(wt.PromptStatus) {
		wt.PromptStatus = PromptNotSent
		wt.PromptNote = "The daemon restarted before the prompt was typed. Send the prompt with send-text."
	}
	return &wt
}
