package session

import (
	"fmt"
	"log"
	"maps"
	"slices"
	"time"
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
	// count names messages nobody holds any more. The subagents the count
	// below says ran in a process that is gone.
	w.AgentQueued = 0
	w.AgentSubagents = 0
	w.ForegroundCmd = ""
	// OSC 7501 records live in the pane's emulator, which a restore does
	// not bring back.
	w.ProgramStatus = nil
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
	// The saved id comes back, so a client that knew the session by its id
	// (a phone bridge, a script) still finds it. State from before ids were
	// saved has none and gets a new one.
	sess, err := d.manager.CreateSession(state.Name, &SessionConfig{restoreID: state.SessionID}, width, height)
	if err != nil {
		return nil, nil, err
	}

	// The start directory comes back before any window is spawned. It is not
	// checked: a folder missing when the daemon starts (a drive not mounted
	// yet) would otherwise be saved as empty on the next save and lost. A
	// shell asked to start in a missing folder starts in the daemon's.
	sess.SetStartDir(state.StartDir)
	// When the person last used it comes back too. Without it every restored
	// session looked unused, and a bare attach fell to the restore order.
	// Nothing below touches it: restoring panes is not the person.
	if state.LastUsed > 0 {
		sess.setLastUsed(time.Unix(0, state.LastUsed))
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
	restored.WorkspaceMasterSplits = cloneMasterSplits(state.WorkspaceMasterSplits)
	restored.WorkspaceMasterLayout = maps.Clone(state.WorkspaceMasterLayout)
	restored.WorkspaceHasCustom = maps.Clone(state.WorkspaceHasCustom)

	// A window whose shell will not start is dropped rather than kept. Keeping it
	// left its PTYID naming the dead daemon's PTY, which nothing will ever answer
	// to: no output, no input, no way to revive it, and no UI anywhere that draws
	// a pane as dead. Closing the window is what the daemon already does whenever
	// a PTY goes away (see notifyPTYClosed), so the restore does the same.
	// Each pane's saved history, by window id, which its new emulator shows
	// above the banner. None when the setting is off.
	var history map[string]*savedHistory
	if d.manager.HistoryPolicy().Enabled {
		history = loadHistory(state.Name)
	}

	kept := restored.Windows[:0]
	saved := make(map[string]WindowState, len(restored.Windows))
	for i := range restored.Windows {
		w := &restored.Windows[i]

		// A popup does not survive the daemon. It is a pane for one command, and
		// a restore respawns a shell rather than the command, so bringing one
		// back gives the user a floating box holding a shell that will never
		// exit and that nothing asked for. Dropping it is the same answer the
		// loop gives a window whose shell will not start.
		//
		// A scratch pane is not a popup (see scratch_workspace.go): every
		// pane of every scratch group comes back, a fresh shell in its own
		// folder, on its group's workspace. So the group is hidden until its
		// key shows it, with the layout it had. A scratch pane saved as a
		// popup, before groups had a workspace, moves to one here.
		if w.Popup && !w.Scratch {
			debugLog("[DEBUG] dropping restored popup %s, a popup lives only as long as its command", shortID(w.ID))
			continue
		}
		if w.Scratch {
			w.Popup, w.IsFloating, w.Minimized = false, false, false
			if !IsScratchWorkspace(w.Workspace) {
				w.Workspace = legacyScratchWorkspace(&restored, w.ScratchKey())
			}
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
		var pty *PTY
		var err error
		if w.Kind != "" && w.Kind != "terminal" {
			pty, err = sess.restorePlaceholderPTY(w.ID, ptyWidth, ptyHeight, w.Cwd, w.Kind, w.URI, w.Workspace, onExit)
		} else {
			pty, err = sess.restorePTYWithGrants(w.ID, ptyWidth, ptyHeight, w.Cwd, savedGrants(w.Grants), history[w.ID], w.Workspace, onExit)
		}
		if err != nil {
			LogError("Dropping restored window %s, its shell could not be respawned: %v", shortID(w.ID), err)
			continue
		}
		w.PTYID = pty.ID
		if h := history[w.ID]; h != nil {
			// The first save records the restored history's own time. See
			// historyMark.savedAt.
			sess.history.setMark(w.ID, historyMark{ptyID: pty.ID, seq: -1, savedAt: h.SavedAt})
		}
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

	scratchPanes := map[string]scratchPane{}
	views := map[string][2]string{}
	for _, w := range kept {
		if w.Scratch {
			scratchPanes[w.ID] = scratchPane{name: w.ScratchName, workspace: w.Workspace}
		}
		if w.Kind != "" || w.URI != "" {
			views[w.ID] = [2]string{w.Kind, w.URI}
		}
	}

	sess.UpdateState(&restored)
	// After UpdateState, which takes this field from canonical state and would
	// undo it if the restore wrote it into the pushed snapshot instead.
	sess.MarkRestored()
	// The master-stack shapes are daemon-owned in the same way. See
	// RestoreMasterLayouts.
	sess.RestoreMasterLayouts(state.WorkspaceMasterLayout)
	// Whether the rail is shown is daemon-owned in the same way. See
	// RestoreSidebar.
	sess.RestoreSidebar(state.Sidebar)
	// The scratch mark is daemon-owned in the same way: UpdateState keeps
	// only the marks canonical state already has, and a new session has none.
	if len(scratchPanes) > 0 {
		sess.markRestoredScratch(scratchPanes)
	}
	// A window a renderer draws natively comes back as that kind of window,
	// for the same reason.
	if len(views) > 0 {
		sess.markRestoredViews(views)
	}

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

// scratchPane is what a restore re-marks on one scratch pane.
type scratchPane struct {
	name      string
	workspace int
}

// legacyScratchWorkspace is the workspace for a scratch group saved before
// groups had one: the one another pane of the group already moved to, or the
// lowest free one.
func legacyScratchWorkspace(state *SessionState, key string) int {
	for i := range state.Windows {
		if w := &state.Windows[i]; w.Scratch && w.ScratchKey() == key && IsScratchWorkspace(w.Workspace) {
			return w.Workspace
		}
	}
	return freeScratchWorkspace(state)
}
