package session

import (
	"log"
	"maps"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

func hasWindow(windows []WindowState, id string) bool {
	return slices.ContainsFunc(windows, func(w WindowState) bool { return w.ID == id })
}

func clearLiveAgent(w *WindowState) {
	w.AgentState = AgentStateNone
	w.AgentMessage = ""
	w.AgentKind = ""
	w.AgentStateAt = 0
	w.AgentHarness = ""
	w.AgentMeta = nil
	w.AgentQueued = 0
	w.AgentDraftHeld = false
	w.AgentSettled = false
	w.AgentQuiet = false
	w.AgentSubagents = 0
	w.AgentSubagentList = nil
	w.ForegroundCmd = ""
	w.SSHOffer = nil
	w.ProgramStatus = nil
}

func (d *Daemon) restoreAllSessions() {
	names, err := ListResurrectableSessions()
	if err != nil {
		LogError("Failed to list resurrectable sessions: %v", err)
		return
	}

	for _, name := range names {
		if d.manager.GetSession(name) != nil {
			continue
		}
		state, err := LoadResurrectionState(name)
		if err != nil {
			log.Printf("Skipping resurrection of %q: %v", name, err)
			continue
		}
		if len(state.Windows) == 0 {
			log.Printf("Discarding saved state for %q: it has no windows", name)
			RemoveResurrectionState(name)
			continue
		}
		_, offers, err := d.restoreSessionOffers(state)
		if err != nil {
			LogError("Failed to restore session %q: %v", name, err)
			continue
		}
		d.pendingResumes = append(d.pendingResumes, offers...)
		log.Printf("Restored session %q (%d windows)", name, len(state.Windows))
	}
}

func (d *Daemon) restoreSession(state *SessionState) (*Session, error) {
	sess, offers, err := d.restoreSessionOffers(state)
	if err != nil {
		return nil, err
	}
	d.applyResumeOffers(offers)
	return sess, nil
}

func (d *Daemon) restoreSessionOffers(state *SessionState) (*Session, []resumeOffer, error) {
	if state == nil || state.Name == "" {
		return nil, nil, errfmt.Errorf("cannot restore session from empty state")
	}

	if existing := d.manager.GetSession(state.Name); existing != nil {
		return existing, nil, nil
	}
	if wt := state.Worktree; wt != nil && (wt.JudgeSession != "" || wt.JudgeWindow != "") {
		w := *wt
		w.JudgeSession, w.JudgeWindow = "", ""
		state.Worktree = &w
	}
	if wt := state.Worktree; wt != nil && wt.Setup != nil && (wt.Setup.State == SetupRunning || wt.Setup.State == SetupPending) {
		w, s := *wt, *wt.Setup
		s.State, s.Note = SetupFailed, "The daemon restarted before the setup finished."
		w.Setup = &s
		state.Worktree = &w
	}

	if len(state.Windows) == 0 {
		return nil, nil, errfmt.Errorf("saved state for session %q has no windows, there is nothing to restore", state.Name)
	}

	width, height := state.Width, state.Height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	d.sessionOrderMu.Lock()
	sess, err := d.manager.CreateSession(state.Name, &SessionConfig{restoreID: state.SessionID, Space: state.Space, Rank: d.restoredRankLocked(state.Rank), Global: state.Global}, width, height)
	d.sessionOrderMu.Unlock()
	if err != nil {
		return nil, nil, err
	}

	sess.SetStartDir(state.StartDir)
	if state.KittyGraphics || state.SixelGraphics {
		sess.SetGraphicsCapabilities(state.KittyGraphics, state.SixelGraphics)
	}
	if state.LastUsed > 0 {
		sess.setLastUsed(time.Unix(0, state.LastUsed))
	}

	sessionID := sess.ID
	onExit := func(ptyID string) {
		d.notifyPTYClosed(sessionID, ptyID)
	}

	restored := *state
	restored.KittyGraphics, restored.SixelGraphics = false, false
	restored.Windows = make([]WindowState, len(state.Windows))
	copy(restored.Windows, state.Windows)
	restored.WorkspaceFocus = maps.Clone(state.WorkspaceFocus)
	restored.WorkspaceMasterRatio = maps.Clone(state.WorkspaceMasterRatio)
	restored.WorkspaceStackRatio = maps.Clone(state.WorkspaceStackRatio)
	restored.WorkspaceMasterSplits = cloneMasterSplits(state.WorkspaceMasterSplits)
	restored.WorkspaceMasterLayout = maps.Clone(state.WorkspaceMasterLayout)
	restored.WorkspaceHasCustom = maps.Clone(state.WorkspaceHasCustom)
	restored.WorkspaceLayoutModes = maps.Clone(state.WorkspaceLayoutModes)
	restored.applyLegacyWorkspaceOrder()
	sess.publishTabKeys(maps.Clone(restored.WorkspaceTabKeys))

	var history map[string]*savedHistory
	if d.manager.HistoryPolicy().Enabled {
		history = loadHistory(state.Name)
	}

	kept := restored.Windows[:0]
	saved := make(map[string]WindowState, len(restored.Windows))
	for i := range restored.Windows {
		w := &restored.Windows[i]

		if w.Popup && !w.Scratch {
			debugLog("[DEBUG] dropping restored popup %s, a popup lives only as long as its command", shortID(w.ID))
			continue
		}
		if w.Rail != "" {
			debugLog("[DEBUG] dropping restored rail pane %s, the client starts it again", shortID(w.ID))
			continue
		}
		if w.Kind == WindowKindTML {
			debugLog("[DEBUG] dropping restored TML window %s, its surface ended with the daemon", shortID(w.ID))
			continue
		}
		if w.Scratch {
			w.Popup, w.IsFloating, w.Minimized = false, false, false
			if !IsScratchWorkspace(w.Workspace) {
				w.Workspace = legacyScratchWorkspace(&restored, w.ScratchKey())
			}
		}

		ptyWidth := max(w.Width-2, 1)
		ptyHeight := max(w.Height-2, 1)

		if w.Host != "" {
			debugLog("[DEBUG] restored window %s ran on %s; it comes back on this machine", shortID(w.ID), w.Host)
			w.Host = ""
			w.Cwd = ""
			w.AgentSessionID = ""
			w.AgentSessionHarness = ""
			w.AgentFlags = nil
		}

		saved[w.ID] = *w

		clearLiveAgent(w)

		sess.noteStartDir(w.ID, w.StartDir)
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
			sess.history.setMark(w.ID, historyMark{ptyID: pty.ID, seq: -1, savedAt: h.SavedAt})
		}
		w.ShellPID = pty.ShellPID()
		kept = append(kept, *w)
	}
	restored.Windows = kept

	if restored.FocusedWindowID != "" && !hasWindow(kept, restored.FocusedWindowID) {
		restored.FocusedWindowID = ""
	}
	for ws, id := range restored.WorkspaceFocus {
		if !hasWindow(kept, id) {
			delete(restored.WorkspaceFocus, ws)
		}
	}

	var offers []resumeOffer
	ids := make(map[string]WindowState, len(kept))
	grants := make(map[string][]string)
	startedBy := make(map[string]*PaneRef)
	agentLinks := make(map[string]*AgentLink)
	delegations := make(map[string]*Delegation)
	var notes map[string]string
	for _, w := range kept {
		if w.Note != "" {
			if notes == nil {
				notes = make(map[string]string)
			}
			notes[w.ID] = w.Note
		}
		if w.Grants != nil {
			grants[w.ID] = w.Grants
		}
		if w.StartedBy != nil {
			startedBy[w.ID] = w.StartedBy
		}
		if w.AgentLink != nil {
			agentLinks[w.ID] = w.AgentLink
		}
		if w.Delegation != nil {
			delegations[w.ID] = restoredDelegation(w.Delegation)
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

	tabKeys, nextTabKey := restored.WorkspaceTabKeys, restored.NextWorkspaceTabKey
	masterLayouts := restored.WorkspaceMasterLayout
	sess.UpdateState(&restored)
	sess.MarkRestored()
	sess.RestoreMasterLayouts(masterLayouts)
	sess.RestoreWorkspaceTabKeys(tabKeys, nextTabKey)
	d.restoreTasks(sess, state.Tasks, state.NextTaskID)
	sess.RestoreSidebar(state.Sidebar)
	sess.RestoreNotes(state.Note, notes)
	if len(scratchPanes) > 0 {
		sess.markRestoredScratch(scratchPanes)
	}
	if len(views) > 0 {
		sess.markRestoredViews(views)
	}

	if wt := restoredWorktree(state.Worktree); wt != nil {
		_ = sess.SetWorktree(wt)
	} else if len(kept) > 0 {
		cwd := kept[0].Cwd
		if pty := sess.GetPTY(kept[0].PTYID); pty != nil {
			if live, ok := pty.ProcessCwd(); ok {
				cwd = live
			}
		}
		sess.refreshWorktree(cwd)
	}

	if len(ids) > 0 {
		_ = sess.mutateState(func(st *SessionState) error {
			for i := range st.Windows {
				if saved, ok := ids[st.Windows[i].ID]; ok {
					st.Windows[i].AgentSessionID = saved.AgentSessionID
					st.Windows[i].AgentSessionHarness = saved.AgentSessionHarness
					st.Windows[i].AgentFlags = saved.AgentFlags
				}
			}
			return nil
		})
	}
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
	if len(startedBy) > 0 || len(agentLinks) > 0 || len(delegations) > 0 {
		_ = sess.mutateState(func(st *SessionState) error {
			for i := range st.Windows {
				if ref, ok := startedBy[st.Windows[i].ID]; ok {
					st.Windows[i].StartedBy = ref
				}
				if link, ok := agentLinks[st.Windows[i].ID]; ok {
					st.Windows[i].AgentLink = link
				}
				if dl, ok := delegations[st.Windows[i].ID]; ok {
					st.Windows[i].Delegation = dl
				}
			}
			return nil
		})
	}
	return sess, offers, nil
}

func restoredWorktree(saved *WorktreeInfo) *WorktreeInfo {
	if saved == nil || !saved.Managed {
		return nil
	}
	wt := *saved
	if wt.LaunchedFromID == "" {
		wt.LaunchedFrom = ""
	}
	if PromptWaiting(wt.PromptStatus) {
		wt.PromptStatus = PromptNotSent
		wt.PromptNote = "The daemon restarted before the prompt was typed. Send the prompt with send-text."
	}
	return &wt
}

type scratchPane struct {
	name      string
	workspace int
}

func legacyScratchWorkspace(state *SessionState, key string) int {
	for i := range state.Windows {
		if w := &state.Windows[i]; w.Scratch && w.ScratchKey() == key && IsScratchWorkspace(w.Workspace) {
			return w.Workspace
		}
	}
	return freeScratchWorkspace(state)
}
