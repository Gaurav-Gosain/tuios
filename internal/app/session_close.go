package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Session-close rows, in drawn order. Cancel is first and is what the dialog
// opens on: the destructive row is never the default.
const (
	// SessionCloseRowCancel dismisses without doing anything.
	SessionCloseRowCancel = iota
	// SessionCloseRowClose ends the session.
	SessionCloseRowClose
	// SessionCloseRowDeleteWorktree also removes the session's worktree,
	// offered only when the target session sits in one.
	SessionCloseRowDeleteWorktree
)

func (m *OS) sessionCloseRowCount() int {
	if m.SessionCloseWindow == "" && m.SessionCloseWorkspace == 0 &&
		m.sessionDeletableWorktree(m.sessionCloseTarget()) != nil {
		return 3
	}
	return 2
}

type sessionToll struct {
	Panes   int
	Working int
	Blocked int
}

func (m *OS) sessionToll() sessionToll {
	t := sessionToll{Panes: len(m.Windows)}
	for _, w := range m.Windows {
		if w == nil {
			continue
		}
		t.count(w.AgentState)
	}
	return t
}

func (t *sessionToll) count(state string) {
	switch {
	case state == string(session.AgentStateWorking):
		t.Working++
	case sidebarAttention(state):
		t.Blocked++
	}
}

// SessionTollFor counts any session by identity. Another session's panes are
// read from the daemon listing this client already caches, which is the same
// read the rail's rows for that session are drawn from: a dialog that has to
// ask the daemon what it is about would block the Update goroutine to say what
// it is destroying.
//
// A session whose windows have not reached the cache yet counts zero panes,
// and the line the dialog turns on says so rather than inventing a number.
func (m *OS) SessionTollFor(sessionID string) sessionToll {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		return m.sessionToll()
	}
	var t sessionToll
	if m.DaemonClient == nil {
		return t
	}
	for _, w := range m.DaemonClient.SessionWindows(sessionID) {
		t.Panes++
		t.count(w.AgentState)
	}
	return t
}

func (t sessionToll) Line() string {
	parts := []string{countOf(t.Panes, "pane")}
	if t.Working > 0 {
		parts = append(parts, countOf(t.Working, "agent")+" still working")
	}
	if t.Blocked > 0 {
		blocked := countOf(t.Blocked, "agent")
		if t.Working > 0 {
			blocked = strconv.Itoa(t.Blocked)
		}
		parts = append(parts, blocked+" "+agentNeedsYou(t.Blocked))
	}
	if t.Working == 0 && t.Blocked == 0 {
		parts = append(parts, "no agent working")
	}
	return strings.Join(parts, ", ")
}

func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func (m *OS) sessionCloseQuestion() string {
	if id := m.SessionCloseWindow; id != "" {
		return "Close " + m.windowCloseLabel(m.windowIndexByID(id)) + "?"
	}
	if ws := m.SessionCloseWorkspace; ws != 0 {
		return "Close every pane on " + m.workspaceLabel(ws) + "?"
	}
	name := m.sessionCloseTarget()
	if name == "" {
		return "Close this session?"
	}
	return "Close " + printableTitle(m.sessionTitle(name)) + "?"
}

func (m *OS) windowToll(i int) sessionToll {
	var t sessionToll
	if i < 0 || i >= len(m.Windows) || m.Windows[i] == nil {
		return t
	}
	t.Panes = 1
	t.count(m.Windows[i].AgentState)
	return t
}

func (m *OS) windowTollLine(i int, toll sessionToll) string {
	if toll.Working > 0 || toll.Blocked > 0 || i < 0 || i >= len(m.Windows) || m.Windows[i] == nil {
		return toll.Line()
	}
	w := m.Windows[i]
	cmd := ""
	if w.HasForegroundProcess() {
		cmd = w.ForegroundCommand()
	}
	if cmd == "" {
		cmd = w.ForegroundCmd
	}
	name := printableTitle(m.getWindowDisplayName(w))
	if name == "" {
		name = "The pane"
	}
	if cmd = printableTitle(cmd); cmd != "" {
		return name + " is running " + cmd + "."
	}
	return "Only its shell runs in " + name + "."
}

func (m *OS) windowCloseLabel(i int) string {
	if i < 0 || i >= len(m.Windows) || m.Windows[i] == nil {
		return "that pane"
	}
	w := m.Windows[i]
	if name := printableTitle(m.getWindowDisplayName(w)); name != "" {
		return "pane '" + name + "'"
	}
	return "pane " + shortID(w.ID)
}

func (m *OS) sessionCloseTarget() string {
	if m.SessionCloseTarget != "" {
		return m.SessionCloseTarget
	}
	return m.SessionName
}

// OpenSessionClose raises the close confirmation for the attached session.
func (m *OS) OpenSessionClose() tea.Cmd { return m.OpenSessionCloseFor("") }

// CloseNeedsConfirm reports whether closing the pane at i by hand, with a
// key or the pane's menu, asks first (appearance.confirm_close, issue 1669).
// Busy is the quit dialog's rule: the pane runs something besides its
// shell. A local pane is asked of its PTY, and a daemon pane by the program
// the daemon names in its foreground.
func (m *OS) CloseNeedsConfirm(i int) bool {
	if i < 0 || i >= len(m.Windows) || m.Windows[i] == nil {
		return false
	}
	switch m.Settings.ConfirmClose {
	case config.ConfirmCloseAlways:
		return true
	case config.ConfirmCloseBusy:
		w := m.Windows[i]
		return w.HasForegroundProcess() || w.ForegroundCmd != ""
	}
	return false
}

// OpenSessionCloseForWindow raises the confirmation for the one pane at i.
// It is the path a tuios://window/<id>/close link takes: a program in any
// pane can print such a link, its visible text names whatever it likes, and
// the answer has to name the pane back before the click does anything.
func (m *OS) OpenSessionCloseForWindow(i int) {
	if i < 0 || i >= len(m.Windows) || m.Windows[i] == nil {
		return
	}
	m.SessionCloseTarget = ""
	m.SessionCloseWorkspace = 0
	m.SessionCloseWindow = m.Windows[i].ID
	m.closeList = listnav.State{Cursor: SessionCloseRowCancel}
	m.ShowSessionClose = true
}

// OpenSessionCloseFor raises it for any session, named by identity. One dialog
// answers for all of them: it is the only place that counts what a close would
// take down, and a second one built for other sessions would be the same
// question asked with less information.
//
// The attached session resolves back to the empty target, so the path that
// quits this client is chosen by what the session IS rather than by which
// surface raised the dialog.
func (m *OS) OpenSessionCloseFor(sessionID string) tea.Cmd {
	if sessionID == m.sidebarCurrentSessionID() {
		sessionID = ""
	}
	m.SessionCloseWindow = ""
	m.SessionCloseTarget = sessionID
	m.closeList = listnav.State{Cursor: SessionCloseRowCancel}
	m.SessionCloseWorktreeToll = nil
	m.ShowSessionClose = true
	if m.sessionCloseRowCount() > SessionCloseRowDeleteWorktree {
		return m.worktreeTollCmd(m.sessionCloseTarget())
	}
	return nil
}

// CloseSessionClose dismisses the confirmation, changing nothing.
func (m *OS) CloseSessionClose() {
	m.ShowSessionClose = false
	m.SessionCloseTarget = ""
	m.SessionCloseWorkspace = 0
	m.SessionCloseWindow = ""
	m.closeList = listnav.State{Cursor: SessionCloseRowCancel}
	m.SessionCloseWorktreeToll = nil
}

// SessionCloseMove moves the selection by delta, clamped to the rows.
//
// This is the one kind of list that does not wrap: the cursor opens on
// Cancel, and up from Cancel landing on the row that kills the session is a
// keystroke that walks onto the destructive answer without meaning to.
func (m *OS) SessionCloseMove(delta int) {
	n := m.sessionCloseRowCount()
	m.closeList.Move(delta, n, n, false, nil)
}

// SessionCloseCursor is the dialog's row under the cursor.
func (m *OS) SessionCloseCursor() int { return m.closeList.Cursor }

// SessionCloseActivate runs the row at idx and dismisses the dialog. Closing
// the attached session goes through QuitSession, which is the one
// implementation of ending the session this client is in: it kills the
// daemon-side session where there is one and cleans up where there is not.
//
// Any other session is killed where it stands. Nothing about this client
// changes: it stays attached to what it was attached to, and the rail loses a
// row when the daemon's next listing says so.
func (m *OS) SessionCloseActivate(idx int) tea.Cmd {
	target := m.SessionCloseTarget
	ws := m.SessionCloseWorkspace
	win := m.SessionCloseWindow
	if idx == SessionCloseRowDeleteWorktree {
		if t := m.SessionCloseWorktreeToll; t != nil && t.Loaded && t.Own > 0 {
			m.ShowNotification(fmt.Sprintf("The worktree sits on a detached HEAD holding %s. Commit them onto a branch first.", countOf(t.Own, "own commit")),
				"error", m.Settings.NotificationDuration*2)
			return nil
		}
	}
	m.CloseSessionClose()
	switch idx {
	case SessionCloseRowClose:
	case SessionCloseRowDeleteWorktree:
		name := target
		if name == "" {
			name = m.SessionName
		}
		return m.deleteWorktreeCmd(name, name == m.sidebarCurrentSessionID())
	default:
		return nil
	}
	if win != "" {
		if i := m.windowIndexByID(win); i >= 0 {
			w := m.Windows[i]
			m.FireHook(hooks.AfterCloseWindow, w.ID, w.Title())
			m.CloseWindowByHand(i)
			if len(m.Windows) == 0 && m.Mode == TerminalMode {
				m.ChooseMode(WindowManagementMode, ModeCauseNoPane)
			}
		}
		return nil
	}
	if ws != 0 {
		m.CloseWorkspacePanes(ws)
		return nil
	}
	if target != "" {
		m.KillOtherSession(target)
		return nil
	}
	m.QuitSession()
	return m.QuitCmd()
}

// KillOtherSession ends a session this client is not attached to, by identity.
//
// The kill is a daemon round trip that waits for the post-kill listing, so it
// runs off the Update goroutine: doing it inline parks input, rendering and
// socket draining for as long as the daemon takes. The outcome comes back as a
// SessionKilledMsg, which is where it is reported, since that is the goroutine
// that owns notifications.
func (m *OS) KillOtherSession(sessionID string) {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() || m.DaemonClient == nil {
		return
	}
	label, client, ch := m.SessionLabel(sessionID), m.DaemonClient, m.sessionKillChan()
	go func() {
		ch <- SessionKilledMsg{Label: label, Err: client.KillSessionByName(sessionID)}
	}()
}

func (m *OS) sessionKillChan() chan SessionKilledMsg {
	if m.PendingSessionKill == nil {
		m.PendingSessionKill = make(chan SessionKilledMsg, 4)
	}
	return m.PendingSessionKill
}

func (m *OS) deleteWorktreeCmd(name string, attached bool) tea.Cmd {
	dial := m.verbDialer()
	params := map[string]any{"session": name, "stash": true, "keep_session": true}
	return func() tea.Msg {
		var res struct {
			Gone          bool   `json:"gone"`
			Path          string `json:"path"`
			Branch        string `json:"branch"`
			Note          string `json:"note"`
			Changes       int    `json:"changes"`
			Ignored       int    `json:"ignored"`
			Stashed       bool   `json:"stashed"`
			SessionKilled bool   `json:"session_killed"`
		}
		if err := callWorktreeVerb(dial, "remove-worktree", "remove the worktree", params, 60*time.Second, &res); err != nil {
			return worktreeRemovedMsg{Name: name, Attached: attached, Err: err}
		}
		return worktreeRemovedMsg{
			Name: name, Attached: attached,
			Gone: res.Gone, Path: res.Path, Branch: res.Branch, Note: res.Note,
			Changes: res.Changes, Ignored: res.Ignored, Stashed: res.Stashed,
			SessionKilled: res.SessionKilled,
		}
	}
}

type worktreeToll struct {
	Loaded  bool
	Changes int
	Ignored int
	Own     int
	Gone    bool
}

type worktreeTollMsg struct {
	Name string
	Toll worktreeToll
}

func (m *OS) worktreeTollCmd(name string) tea.Cmd {
	info := m.sessionWorktreeInfo(name)
	if info == nil {
		return nil
	}
	dial := m.verbDialer()
	params := map[string]any{"repo": info.Repo, "changes": true, "session": name}
	return func() tea.Msg {
		var res struct {
			Worktrees []struct {
				Session    string `json:"session"`
				Changes    int    `json:"changes"`
				Ignored    int    `json:"ignored"`
				OwnCommits int    `json:"own_commits"`
				Gone       bool   `json:"gone"`
			} `json:"worktrees"`
		}
		if err := callWorktreeVerb(dial, "list-worktrees", "read the worktrees", params, 60*time.Second, &res); err != nil {
			return worktreeTollMsg{Name: name}
		}
		for _, wt := range res.Worktrees {
			if wt.Session != name {
				continue
			}
			return worktreeTollMsg{Name: name, Toll: worktreeToll{
				Loaded: true, Changes: wt.Changes, Ignored: wt.Ignored, Own: wt.OwnCommits, Gone: wt.Gone,
			}}
		}
		return worktreeTollMsg{Name: name}
	}
}

func (m *OS) worktreeTollLine() string {
	t := m.SessionCloseWorktreeToll
	if t == nil || !t.Loaded || m.sessionDeletableWorktree(m.sessionCloseTarget()) == nil {
		return ""
	}
	if t.Gone {
		return "The worktree's directory was already gone."
	}
	parts := make([]string, 0, 3)
	if t.Changes > 0 {
		parts = append(parts, fmt.Sprintf("%d %s to git stash", t.Changes,
			plural.Word(t.Changes, "change goes", "changes go")))
	}
	if t.Ignored > 0 {
		parts = append(parts, countOf(t.Ignored, "ignored file")+" deleted unsaved")
	}
	if t.Own > 0 {
		parts = append(parts, fmt.Sprintf("%s on a detached HEAD: the delete refuses %s",
			countOf(t.Own, "own commit"), plural.Word(t.Own, "it", "them")))
	}
	if t.Changes < 0 || t.Ignored < 0 || t.Own < 0 {
		parts = append(parts, "some counts could not be read")
	}
	if len(parts) == 0 {
		return "The worktree is clean: nothing stashed, nothing lost."
	}
	return strings.Join(parts, "; ") + "."
}

type worktreeRemovedMsg struct {
	Name          string
	Attached      bool
	Gone          bool
	Path          string
	Branch        string
	Note          string
	Changes       int
	Ignored       int
	Stashed       bool
	SessionKilled bool
	Err           error
}

func (m *OS) handleWorktreeRemoved(msg worktreeRemovedMsg) tea.Cmd {
	if msg.Err != nil {
		m.ShowNotification(msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
		return nil
	}
	var note string
	switch {
	case msg.Gone:
		note = msg.Note
	case msg.Stashed:
		note = fmt.Sprintf("Deleted worktree %s. Branch %s kept. %d %s in git stash.",
			shortenHome(msg.Path), msg.Branch, msg.Changes, countOf(msg.Changes, "change"))
	default:
		note = "Deleted worktree " + shortenHome(msg.Path) + ". Branch " + msg.Branch + " kept."
	}
	if !msg.Gone && msg.Ignored > 0 {
		note += " " + countOf(msg.Ignored, "ignored file") + " deleted."
	}
	m.ShowNotification(note, "info", m.Settings.NotificationDuration)
	if msg.SessionKilled {
		return nil
	}
	if msg.Attached {
		return m.tickUntilStop(m.Settings.NotificationDuration, func(time.Time) tea.Msg {
			return worktreeRemoveQuitMsg{Name: msg.Name}
		})
	}
	if msg.Name != "" && msg.Name != m.sidebarCurrentSessionID() && m.DaemonClient != nil {
		m.pendingWorktreeKill = true
	}
	m.KillOtherSession(msg.Name)
	return nil
}

type worktreeRemoveQuitMsg struct {
	Name string
}
