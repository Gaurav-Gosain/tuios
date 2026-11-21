package session

import (
	"maps"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

func retainDaemonExclusive(incoming, canonical *SessionState) {
	if len(canonical.Windows) > 0 {
		at := make(map[string]int64, len(canonical.Windows))
		for _, w := range canonical.Windows {
			if w.MinimizedAt != 0 {
				at[w.ID] = w.MinimizedAt
			}
		}
		for i := range incoming.Windows {
			incoming.Windows[i].MinimizedAt = at[incoming.Windows[i].ID]
		}
	}
	if incoming.Options == nil {
		incoming.Options = canonical.Options
	}
	if incoming.DisplayName == "" {
		incoming.DisplayName = canonical.DisplayName
	}
	if incoming.Accent == "" {
		incoming.Accent = canonical.Accent
	}
	incoming.Note = canonical.Note
	incoming.IconNames = nil
	incoming.Restored = canonical.Restored
	incoming.WorkspaceMasterLayout = canonical.WorkspaceMasterLayout
	incoming.Sidebar = canonical.Sidebar
	incoming.Global = canonical.Global
	incoming.Space = canonical.Space
	incoming.Rank = canonical.Rank
	incoming.Worktree = canonical.Worktree
	incoming.WindowMoves = canonical.WindowMoves
	incoming.Tasks, incoming.NextTaskID = canonical.Tasks, canonical.NextTaskID
	if incoming.WorkspaceNames == nil {
		incoming.WorkspaceNames = canonical.WorkspaceNames
	}
	if incoming.FocusHistory == nil {
		incoming.FocusHistory = canonical.FocusHistory
	}
	incoming.WorkspaceTabKeys = canonical.WorkspaceTabKeys
	incoming.NextWorkspaceTabKey = canonical.NextWorkspaceTabKey
	incoming.WorkspaceOrder = nil
	if incoming.ResurrectionVersion == 0 {
		incoming.ResurrectionVersion = canonical.ResurrectionVersion
	}
	if incoming.WorkspaceTrees == nil {
		incoming.WorkspaceTrees = canonical.WorkspaceTrees
		incoming.WindowToBSPID = canonical.WindowToBSPID
		incoming.NextBSPWindowID = canonical.NextBSPWindowID
	}
	if incoming.PaneGeometry == nil {
		incoming.PaneGeometry = canonical.PaneGeometry
	}
	if incoming.ScrollStrip == nil {
		incoming.ScrollStrip = canonical.ScrollStrip
	}
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

	cwds := make(map[string]string, len(canonical.Windows))
	fgCmds := make(map[string]string, len(canonical.Windows))
	var offers map[string]*SSHOffer
	shellPIDs := make(map[string]int, len(canonical.Windows))
	type agent struct {
		state   AgentState
		message string
		harness string
		at      int64
		reason  string
	}
	agents := make(map[string]agent, len(canonical.Windows))
	type agentIdentity struct {
		kind           string
		sessionID      string
		sessionHarness string
		flags          *KeptFlags
	}
	agentIDs := make(map[string]agentIdentity, len(canonical.Windows))
	metas := make(map[string][]AgentMetaToken)
	queued := make(map[string]int)
	var draftHeld map[string]bool
	var subagents map[string]int
	var settled map[string]bool
	var quiet map[string]bool
	var subagentLists map[string][]SubagentInfo
	type popup struct {
		width   string
		height  string
		dim     *config.PopupDim
		scratch bool
		name    string
	}
	popups := make(map[string]popup, len(canonical.Windows))
	type scratchMark struct {
		name      string
		workspace int
	}
	scratches := make(map[string]scratchMark)
	var rails map[string]string
	workspaces := make(map[string]int, len(canonical.Windows))
	hosts := make(map[string]string, len(canonical.Windows))
	type viewMark struct{ kind, uri string }
	views := make(map[string]viewMark)
	completions := make(map[string]uint64, len(canonical.Windows))
	paneGrants := make(map[string][]string)
	programStatus := make(map[string][]ProgramStatusRecord)
	startedBy := make(map[string]*PaneRef)
	agentLinks := make(map[string]*AgentLink)
	delegations := make(map[string]*Delegation)
	startDirs := make(map[string]string, len(canonical.Windows))
	var notes map[string]string
	for i := range canonical.Windows {
		w := &canonical.Windows[i]
		if w.StartDir != "" {
			startDirs[w.ID] = w.StartDir
		}
		if w.Note != "" {
			if notes == nil {
				notes = make(map[string]string)
			}
			notes[w.ID] = w.Note
		}
		if w.StartedBy != nil {
			startedBy[w.ID] = w.StartedBy
		}
		if w.AgentLink != nil {
			agentLinks[w.ID] = w.AgentLink
		}
		if w.Delegation != nil {
			delegations[w.ID] = w.Delegation
		}
		if w.Grants != nil {
			paneGrants[w.ID] = w.Grants
		}
		if w.ProgramStatus != nil {
			programStatus[w.ID] = w.ProgramStatus
		}
		if w.CompletionSeq != 0 {
			completions[w.ID] = w.CompletionSeq
		}
		if cwd := w.Cwd; cwd != "" {
			cwds[w.ID] = cwd
		}
		if w.Popup {
			popups[w.ID] = popup{w.PopupWidth, w.PopupHeight, w.PopupDim, w.Scratch, w.ScratchName}
		}
		if w.Scratch {
			scratches[w.ID] = scratchMark{w.ScratchName, w.Workspace}
		}
		if w.Rail != "" {
			if rails == nil {
				rails = make(map[string]string)
			}
			rails[w.ID] = w.Rail
		}
		if w.Kind != "" || w.URI != "" {
			views[w.ID] = viewMark{w.Kind, w.URI}
		}
		workspaces[w.ID] = w.Workspace
		if w.Host != "" {
			hosts[w.ID] = w.Host
		}
		if cmd := w.ForegroundCmd; cmd != "" {
			fgCmds[w.ID] = cmd
		}
		if o := w.SSHOffer; o != nil {
			if offers == nil {
				offers = make(map[string]*SSHOffer)
			}
			offers[w.ID] = o
		}
		if pid := w.ShellPID; pid != 0 {
			shellPIDs[w.ID] = pid
		}
		if w.AgentState != AgentStateNone || w.AgentMessage != "" || w.AgentHarness != "" || w.AgentStateAt != 0 {
			agents[w.ID] = agent{w.AgentState, w.AgentMessage, w.AgentHarness, w.AgentStateAt, w.AgentReason}
		}
		if w.AgentKind != "" || w.AgentSessionID != "" || w.AgentSessionHarness != "" || w.AgentFlags != nil {
			agentIDs[w.ID] = agentIdentity{w.AgentKind, w.AgentSessionID, w.AgentSessionHarness, w.AgentFlags}
		}
		if len(w.AgentMeta) > 0 {
			metas[w.ID] = w.AgentMeta
		}
		if w.AgentQueued != 0 {
			queued[w.ID] = w.AgentQueued
		}
		if w.AgentDraftHeld {
			if draftHeld == nil {
				draftHeld = make(map[string]bool)
			}
			draftHeld[w.ID] = true
		}
		if w.AgentQuiet {
			if quiet == nil {
				quiet = make(map[string]bool)
			}
			quiet[w.ID] = true
		}
		if w.AgentSettled {
			if settled == nil {
				settled = make(map[string]bool)
			}
			settled[w.ID] = true
		}
		if w.AgentSubagents != 0 {
			if subagents == nil {
				subagents = make(map[string]int)
			}
			subagents[w.ID] = w.AgentSubagents
		}
		if len(w.AgentSubagentList) != 0 {
			if subagentLists == nil {
				subagentLists = make(map[string][]SubagentInfo)
			}
			subagentLists[w.ID] = w.AgentSubagentList
		}
	}
	for i := range incoming.Windows {
		w := &incoming.Windows[i]
		if cwd, ok := cwds[w.ID]; ok || w.Cwd == "" {
			w.Cwd = cwd
		}
		w.StartDir = startDirs[w.ID]
		w.Note = notes[w.ID]
		if pid, ok := shellPIDs[w.ID]; ok || w.ShellPID == 0 {
			w.ShellPID = pid
		}
		if host, ok := hosts[w.ID]; ok || w.Host == "" {
			w.Host = host
		}
		w.ForegroundCmd = fgCmds[w.ID]
		w.SSHOffer = offers[w.ID]
		if c := completions[w.ID]; c > w.CompletionSeq {
			w.CompletionSeq = c
		}
		v := views[w.ID]
		w.Kind, w.URI = v.kind, v.uri
		p, ok := popups[w.ID]
		sm, isScratch := scratches[w.ID]
		w.Scratch, w.ScratchName = isScratch, sm.name
		w.Rail = rails[w.ID]
		switch {
		case isScratch && IsScratchWorkspace(sm.workspace):
			w.Workspace = sm.workspace
		case IsScratchWorkspace(w.Workspace):
			if ws, known := workspaces[w.ID]; known {
				w.Workspace = ws
			}
		}
		w.PopupDim = p.dim
		if ok {
			w.Popup = true
			w.IsFloating = true
			if !p.scratch || w.PopupWidth == "" {
				w.PopupWidth = p.width
			}
			if !p.scratch || w.PopupHeight == "" {
				w.PopupHeight = p.height
			}
		}
		ids := agentIDs[w.ID]
		w.AgentKind = ids.kind
		w.AgentSessionID = ids.sessionID
		w.AgentSessionHarness = ids.sessionHarness
		w.AgentFlags = ids.flags
		a := agents[w.ID]
		w.AgentState = a.state
		w.AgentMessage = a.message
		w.AgentHarness = a.harness
		w.AgentStateAt = a.at
		w.AgentReason = a.reason
		w.AgentMeta = metas[w.ID]
		w.AgentQueued = queued[w.ID]
		w.AgentDraftHeld = draftHeld[w.ID]
		w.AgentSettled = settled[w.ID]
		w.AgentQuiet = quiet[w.ID]
		w.AgentSubagents = subagents[w.ID]
		w.AgentSubagentList = subagentLists[w.ID]
		w.ProgramStatus = programStatus[w.ID]
		w.Grants = paneGrants[w.ID]
		w.StartedBy = startedBy[w.ID]
		w.AgentLink = agentLinks[w.ID]
		w.Delegation = delegations[w.ID]
	}
}

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
			continue
		}
		win.CustomName = cw.CustomName
		win.Workspace = cw.Workspace
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
			continue
		}
		incoming.Windows = append(incoming.Windows, win)
	}

	if canonical.CurrentWorkspace != incoming.CurrentWorkspace {
		incoming.ScrollStrip = canonical.ScrollStrip
	}

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

func (f focusView) sameFocus(g focusView) bool {
	return f.window == g.window && f.workspace == g.workspace && maps.Equal(f.perWS, g.perWS)
}

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

func sameStrip(a, b *ScrollStripState) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

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
