package app

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func windowRowTitle(w *terminal.Window) string {
	if label, ok := tmlRailLabel(w); ok {
		return label
	}
	return railWindowLabel(w.CustomName, w.AgentHarness, w.ForegroundCmd, w.Title())
}

func railWindowLabel(customName, harnessID, foregroundCmd, title string) string {
	if customName != "" {
		return customName
	}
	if t := harness.TitleLabel(harnessID, foregroundCmd, title); t != "" {
		return t
	}
	if foregroundCmd != "" {
		return foregroundCmd
	}
	if harnessID != "" {
		title = harness.TrimTitleMarks(title)
	}
	if s := shellTitleLabel(title); s != "" {
		return s
	}
	return "shell"
}

func agentRowUnnamed(customName, harnessID, foregroundCmd, label string) bool {
	return customName == "" && harnessID != "" && foregroundCmd != "" && label == foregroundCmd
}

func shellTitleLabel(title string) string {
	head, _, _ := strings.Cut(title, " - ")
	head = strings.TrimSpace(head)
	if head == "" {
		return strings.TrimSpace(title)
	}
	if _, after, ok := strings.Cut(head, ":"); ok && after != "" {
		head = after
	}
	if !strings.Contains(head, "/") {
		return head
	}
	if base := path.Base(head); base != "" && base != "/" && base != "." {
		return base
	}
	return head
}

func (m *OS) currentSessionInput() sessiontree.SessionInput {
	windows := make([]sessiontree.WindowInput, 0, len(m.Windows))
	for i, w := range m.Windows {
		if w == nil || hiddenFromLists(w) {
			continue
		}
		state, seen := m.railAgentState(w.ID, w.AgentState, w.AgentCompletionSeq)
		message, kind := m.paneAskNote(w.ID, w.AgentMessage, w.AgentKind)
		title := m.railTitleShown(w)
		windows = append(windows, sessiontree.WindowInput{
			ID:            w.ID,
			Title:         title,
			AgentUnnamed:  agentRowUnnamed(w.CustomName, w.AgentHarness, w.ForegroundCmd, title),
			AgentState:    state,
			DoneSeen:      seen,
			StateAt:       w.AgentStateAt,
			Harness:       w.AgentHarness,
			Message:       message,
			Reason:        w.AgentReason,
			AgentKind:     kind,
			Meta:          w.AgentMeta,
			Queued:        w.AgentQueued,
			DraftHeld:     w.AgentDraftHeld,
			Settled:       w.AgentSettled,
			Quiet:         w.AgentQuiet,
			Cwd:           firstNonEmptyStr(w.Cwd, w.DaemonCwd),
			Subagents:     w.AgentSubagents,
			SubagentList:  w.AgentSubagentList,
			Program:       w.ProgramStatus,
			Parent:        agentParentNode(w),
			Focused:       i == m.FocusedWindow,
			Workspace:     w.Workspace,
			Host:          w.Host,
			HostLink:      w.HostLink,
			ScreenStopped: w.ScreenStopped,
			Recording:     w.Recording,
		})
	}
	name := m.SessionName
	if name == "" {
		name = "local"
	}
	dir, branch := m.sessionPlace(name)
	return sessiontree.SessionInput{
		Name:             name,
		DisplayName:      m.SessionDisplayName,
		Named:            sessionNamed(name, m.SessionDisplayName, m.SessionWorktree),
		Dir:              dir,
		AgentTitle:       m.sessionAgentTitle(name),
		Branch:           branch,
		Attached:         true,
		IsCurrent:        true,
		Restored:         m.SessionRestored,
		Global:           m.SessionGlobal,
		CurrentWorkspace: m.dockWorkspace(),
		Worktree:         m.worktreeRef(m.SessionWorktree),
		Space:            m.SessionSpace,
		Note:             m.SessionNote,
		Windows:          windows,
	}
}

func (m *OS) foreignSessionInput(client *session.TUIClient, name string) sessiontree.SessionInput {
	summaries := client.SessionWindows(name)
	windows := make([]sessiontree.WindowInput, 0, len(summaries))
	for _, w := range summaries {
		if w.Scratch {
			continue
		}
		state, seen := m.railAgentState(w.ID, w.AgentState, w.CompletionSeq)
		message, kind := m.paneAskNote(w.ID, w.AgentMessage, w.AgentKind)
		title := railWindowLabel("", w.AgentHarness, w.ForegroundCmd, w.Title)
		windows = append(windows, sessiontree.WindowInput{
			ID:           w.ID,
			Title:        title,
			AgentUnnamed: agentRowUnnamed("", w.AgentHarness, w.ForegroundCmd, title),
			AgentState:   state,
			DoneSeen:     seen,
			StateAt:      w.AgentStateAt,
			Harness:      w.AgentHarness,
			Message:      message,
			Reason:       w.AgentReason,
			AgentKind:    kind,
			Meta:         agentMetaFromWire(nil, w.AgentMeta),
			Queued:       w.AgentQueued,
			DraftHeld:    w.DraftHeld,
			Settled:      w.AgentSettled,
			Quiet:        w.AgentQuiet,
			Cwd:          w.Cwd,
			Subagents:    w.Subagents,
			SubagentList: subagentsFromWire(nil, w.SubagentList),
			Program:      programStatusFromWire(nil, w.ProgramStatus),
			Parent:       agentParentFromWire(w.AgentParent),
			Workspace:    w.Workspace,
		})
	}
	display, _ := client.SessionLabel(name)
	dir, branch := m.sessionPlace(name)
	return sessiontree.SessionInput{
		Name:             name,
		DisplayName:      display,
		Named:            sessionNamed(name, display, client.SessionWorktree(name)),
		Dir:              dir,
		AgentTitle:       m.sessionAgentTitle(name),
		Branch:           branch,
		Restored:         client.SessionRestored(name),
		Global:           client.SessionGlobal(name),
		CurrentWorkspace: client.SessionCurrentWorkspace(name),
		Worktree:         m.worktreeRef(client.SessionWorktree(name)),
		Space:            client.SessionSpace(name),
		Note:             client.SessionNote(name),
		Windows:          windows,
	}
}

// BuildSessionTree builds the unified tree for the session-management surfaces.
// The attached session is built rich from live state; other sessions are added
// coarse (name only) from the client's CACHED session-name list.
//
// Ordering is a promise: sessions keep the daemon's creation order, with the
// attached session marked current IN PLACE rather than hoisted to the front.
// Hoisting looked helpful but meant every session switch reshuffled the list,
// so a row was never where the eye left it. The user's drag-defined order
// (SidebarOrder) overlays that base order; sessions it does not name keep
// their creation-order slots after the named ones, so a new session appends.
//
// It performs no daemon round trip. This is deliberate: the palette opens on the
// UI goroutine, and a blocking RefreshSessionList there froze the client and
// dropped the daemon connection while the daemon was busy (a browser flooding
// graphics over ssh). The cache is seeded on connect and refreshed off the UI
// goroutine (see the foreign-session refresh in Update), so non-attached
// sessions carry their window summaries and expand from the cache alone.
func (m *OS) BuildSessionTree() sessiontree.Tree {
	current := m.currentSessionInput()

	if m.DaemonClient == nil {
		return m.withHostGroups(sessiontree.Build([]sessiontree.SessionInput{current}))
	}

	names := m.DaemonClient.AvailableSessionNames()
	sessions := make([]sessiontree.SessionInput, 0, len(names)+1)
	seen := false
	for _, name := range names {
		if name == current.Name {
			sessions = append(sessions, current)
			seen = true
			continue
		}
		sessions = append(sessions, m.foreignSessionInput(m.DaemonClient, name))
	}
	if !seen {
		sessions = append(sessions, current)
	}
	sessions = orderByKey(sessions, func(s sessiontree.SessionInput) string { return s.Name },
		m.sidebarSessionOrderFor(m.attachedMachine()))
	return m.withHostGroups(sessiontree.Build(sessions))
}

func (m *OS) withHostGroups(tree sessiontree.Tree) sessiontree.Tree {
	tree.Sessions = append(tree.Sessions, m.hostGroupNodes()...)
	if !m.agentsOn() {
		clearAgentMarks(tree.Sessions)
	}
	return tree
}

func clearAgentMarks(nodes []sessiontree.Node) {
	for i := range nodes {
		nodes[i].AgentState, nodes[i].AgentKind, nodes[i].DoneSeen = "", "", false
		clearAgentMarks(nodes[i].Children)
	}
}

func (m *OS) railNeighbourSession(delta int) string {
	return railNeighbourIn(m.railSessionOrder(m.BuildSessionTree().Sessions), delta)
}

func (m *OS) railSessionOrder(nodes []sessiontree.Node) []sessiontree.Node {
	rows := sessiontree.GroupBySpace(localSessionNodes(nodes), m.sidebarRepoGrouping())
	sessions := make([]sessiontree.Node, 0, len(rows))
	for _, n := range rows {
		if n.Kind == sessiontree.KindSession {
			sessions = append(sessions, n)
		}
	}
	return sessions
}

func railNeighbourIn(sessions []sessiontree.Node, delta int) string {
	current := slices.IndexFunc(sessions, func(s sessiontree.Node) bool { return s.IsCurrent })
	if len(sessions) < 2 || current < 0 {
		return ""
	}
	n := len(sessions)
	return sessions[((current+delta)%n+n)%n].ID
}

// CycleSession switches to the next (delta 1) or previous (delta -1) session.
// Standalone has one synthetic session, so it says there is nowhere to go rather
// than failing inside SwitchToSession.
func (m *OS) CycleSession(delta int) {
	target := m.railNeighbourSession(delta)
	if target == "" {
		m.ShowNotification("No other sessions", "info", m.Settings.NotificationDuration)
		return
	}
	m.openSession("", target)
}

// SwitchToSessionByIndex attaches to the session at the rail position n
// (0-based), the same order the rail draws the rows in and the same
// local-only scope CycleSession cycles. Pressing a number past the sessions
// there are says so instead of failing inside the switch.
func (m *OS) SwitchToSessionByIndex(n int) {
	target, ok := m.railSessionAt(n)
	if !ok {
		m.ShowNotification(fmt.Sprintf("No session %d", n+1), "info", m.Settings.NotificationDuration)
		return
	}
	if target.IsCurrent {
		return
	}
	m.openSession("", target.ID)
}

func (m *OS) railSessionAt(n int) (sessiontree.Node, bool) {
	sessions := m.railSessionOrder(m.BuildSessionTree().Sessions)
	if n < 0 || n >= len(sessions) {
		return sessiontree.Node{}, false
	}
	return sessions[n], true
}

func sessionPaletteLabel(prefix, name, agentState string, doneSeen bool, running int) string {
	if glyph := agentStateIndicator(displayState(agentState, doneSeen, running)); glyph != "" {
		return prefix + glyph + agentMarkGap(glyph) + name
	}
	return prefix + name
}

func getSessionPaletteItems(m *OS) []CommandPaletteItem {
	tree := m.BuildSessionTree()

	items := make([]CommandPaletteItem, 0, len(tree.Sessions))
	for _, th := range m.agentMailThreads() {
		if !th.Unread {
			continue
		}
		thread := th.ID
		items = append(items, CommandPaletteItem{
			Name:     "Mail #" + strconv.FormatUint(th.ID, 10) + " " + th.From + " → " + th.To + ": " + th.Subject,
			Shortcut: "unread",
			Category: "Sessions",
			Action: func(m *OS) (*OS, tea.Cmd) {
				return m, m.OpenAgentMailThread(thread)
			},
		})
	}
	for _, s := range tree.Sessions {
		if s.Kind == sessiontree.KindHost {
			continue
		}
		if isRemoteNode(s) {
			host, name := s.Host, remoteSessionName(s)
			items = append(items, CommandPaletteItem{
				Name:           sessionPaletteLabel("Session: ", name+" @ "+host, s.AgentState, s.DoneSeen, s.Subagents),
				Shortcut:       "another machine",
				Category:       "Sessions",
				AgentState:     s.AgentState,
				AgentSeen:      s.DoneSeen,
				AgentSubagents: s.Subagents,
				Action: func(m *OS) (*OS, tea.Cmd) {
					m.sidebarLeaveForJump()
					m.openRemoteSession(host, name)
					return m, m.TakeSidebarCmd()
				},
			})
			continue
		}

		sessionName := s.ID
		isCurrent := s.IsCurrent
		items = append(items, CommandPaletteItem{
			Name:           sessionPaletteLabel("Session: ", s.Title, s.AgentState, s.DoneSeen, s.Subagents),
			Category:       "Sessions",
			AgentState:     s.AgentState,
			AgentSeen:      s.DoneSeen,
			AgentSubagents: s.Subagents,
			Action: func(m *OS) (*OS, tea.Cmd) {
				if isCurrent {
					m.ShowNotification("Already on this session", "info", m.Settings.NotificationDuration)
					return m, nil
				}
				m.sidebarLeaveForJump()
				m.openSession("", sessionName)
				return m, nil
			},
		})

		for _, w := range s.Children {
			windowID, label, warn := w.ID, w.Title, ""
			if !isCurrent {
				label, warn = s.Title+"/"+w.Title, "switches session"
			}
			items = append(items, CommandPaletteItem{
				Name:           sessionPaletteLabel("Window: ", label, w.AgentState, w.DoneSeen, w.Subagents),
				Shortcut:       warn,
				Category:       "Sessions",
				AgentState:     w.AgentState,
				AgentSeen:      w.DoneSeen,
				AgentSubagents: w.Subagents,
				Action: func(m *OS) (*OS, tea.Cmd) {
					m.sidebarLeaveForJump()
					if !isCurrent {
						if !m.openSession("", sessionName) {
							return m, nil
						}
					}
					m.focusWindowByID(windowID)
					return m, nil
				},
			})
		}
	}
	return items
}

func (m *OS) focusWindowByID(id string) {
	if i := m.windowIndexByID(id); i >= 0 {
		m.FocusWindow(i)
		m.RevealFocusedColumn()
	}
}

func (m *OS) sidebarLeaveForJump() {
	if !m.SidebarFocused {
		return
	}
	m.clearSidebarReturn()
	m.ExitSidebarFocus()
}
