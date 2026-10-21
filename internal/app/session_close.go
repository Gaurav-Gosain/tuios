package app

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The confirmation in front of closing a session.
//
// It is raised every time, whatever the session is holding. A dialog that only
// appears when something is running is a dialog people learn to dismiss without
// reading, and the one time it carries news is the one time they have already
// stopped looking at it.
//
// What makes it worth the keystroke is the second line: the panes and the
// agents are counted off live state as the dialog draws, so it says what is
// about to be lost rather than warning in general. An agent mid-task or waiting
// on an answer is called out by name, because that is the case where the user
// either genuinely means it or has just made the mistake this dialog exists to
// catch.

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

// sessionCloseRowCount is how many rows the open dialog draws: the two
// answers, plus the delete-worktree row only when the resolved target is a
// session that sits in a worktree. Every cursor bound, hit rect and drawn row
// reads it, so the count on the model and the rows on the screen cannot drift
// apart. Window and workspace closes never carry the row: they are not
// sessions, and the dialog's destructive extra would have nothing to point at.
func (m *OS) sessionCloseRowCount() int {
	if m.SessionCloseWindow == "" && m.SessionCloseWorkspace == 0 &&
		m.sessionWorktreeInfo(m.sessionCloseTarget()) != nil {
		return 3
	}
	return 2
}

// sessionToll is what closing the session would take down.
type sessionToll struct {
	Panes   int
	Working int // agents mid-task
	Blocked int // agents that need the user: needs_input or errored
}

// sessionToll counts the live session. Panes are every window this client
// holds, across workspaces, since the session ends for all of them at once.
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

// count adds one pane's state. "Needs you" is the rail's definition,
// sidebarAttention: a pane on needs_input or errored. It counted needs_input
// alone, so the dialog and the rail's header said "needs you" of different
// sets.
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

// Line reads the toll back as the sentence the dialog turns on.
func (t sessionToll) Line() string {
	parts := []string{countOf(t.Panes, "pane")}
	if t.Working > 0 {
		parts = append(parts, countOf(t.Working, "agent")+" still working")
	}
	if t.Blocked > 0 {
		// Named apart from working: one is a task that would be thrown away, the
		// other is a task already stopped and asking for the user. It drops the
		// noun where the clause in front of it already said "agents", which is
		// what keeps both call-outs on the line instead of the second one being
		// the part that gets cut.
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

// countOf renders a count with its noun, pluralised by adding an s.
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// sessionCloseQuestion is the dialog's first line, naming the session where
// there is a name to use. It is the session's label rather than its identity,
// because the question is being asked of a person: a session renamed to
// "Payments API" is not recognisable by the name it is addressed by.
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

// windowToll counts the one pane a link-close link named. The dialog's toll
// line is what makes reading it worth the pause, and for a single pane that
// is the agent state: a pane whose agent is mid-task is the case the
// confirmation exists to catch.
func (m *OS) windowToll(i int) sessionToll {
	var t sessionToll
	if i < 0 || i >= len(m.Windows) || m.Windows[i] == nil {
		return t
	}
	t.Panes = 1
	t.count(m.Windows[i].AgentState)
	return t
}

// windowCloseLabel names the pane at i the way the dialog's question shows
// it: its title where it has one, its id where it does not.
func (m *OS) windowCloseLabel(i int) string {
	if i < 0 || i >= len(m.Windows) || m.Windows[i] == nil {
		return "that pane"
	}
	w := m.Windows[i]
	if name := printableTitle(w.Title()); name != "" {
		return "pane '" + name + "'"
	}
	return "pane " + shortID(w.ID)
}

// sessionCloseTarget is the session the open dialog is about, resolved: the one
// it was raised on, or the attached one when it was raised without a target.
func (m *OS) sessionCloseTarget() string {
	if m.SessionCloseTarget != "" {
		return m.SessionCloseTarget
	}
	return m.SessionName
}

// OpenSessionClose raises the close confirmation for the attached session.
func (m *OS) OpenSessionClose() { m.OpenSessionCloseFor("") }

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
	m.SessionCloseSelected = SessionCloseRowCancel
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
func (m *OS) OpenSessionCloseFor(sessionID string) {
	if sessionID == m.sidebarCurrentSessionID() {
		sessionID = ""
	}
	m.SessionCloseWindow = ""
	m.SessionCloseTarget = sessionID
	m.SessionCloseSelected = SessionCloseRowCancel
	m.ShowSessionClose = true
}

// CloseSessionClose dismisses the confirmation, changing nothing.
func (m *OS) CloseSessionClose() {
	m.ShowSessionClose = false
	m.SessionCloseTarget = ""
	m.SessionCloseWorkspace = 0
	m.SessionCloseWindow = ""
	m.SessionCloseSelected = SessionCloseRowCancel
}

// SessionCloseMove moves the selection by delta, clamped to the rows.
//
// This is the one kind of list that does not wrap: the cursor opens on
// Cancel, and up from Cancel landing on the row that kills the session is a
// keystroke that walks onto the destructive answer without meaning to.
func (m *OS) SessionCloseMove(delta int) {
	m.SessionCloseSelected = listnav.Step(m.SessionCloseSelected, delta, m.sessionCloseRowCount(), false)
}

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
	m.CloseSessionClose()
	switch idx {
	case SessionCloseRowClose:
	case SessionCloseRowDeleteWorktree:
		// The row is only drawn for a session that sits in a worktree, so the
		// window and workspace targets never reach here. The attached flag is
		// captured now because the dialog closes before the reply does: the
		// handler has to know whether the session it is losing is the one this
		// client is in.
		name := target
		if name == "" {
			name = m.SessionName
		}
		return m.deleteWorktreeCmd(name, name == m.sidebarCurrentSessionID())
	default:
		return nil
	}
	if win != "" {
		// The pane is found again by its ID: it may have moved in
		// m.Windows, or closed on its own, while the dialog was open.
		if i := m.windowIndexByID(win); i >= 0 {
			m.CloseWindowByHand(i)
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
	return tea.Quit
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

// sessionKillChan is the buffered channel carrying kill results back to Update,
// made on first use so a client that never kills another session pays nothing.
func (m *OS) sessionKillChan() chan SessionKilledMsg {
	if m.PendingSessionKill == nil {
		m.PendingSessionKill = make(chan SessionKilledMsg, 4)
	}
	return m.PendingSessionKill
}

// deleteWorktreeCmd runs the daemon's remove-worktree verb, the same one
// `tuios worktree rm` runs, in a command: a removal is a round trip and this
// runs on the Update goroutine. The changes are always stashed rather than
// discarded — nothing a close-confirmation deletes is ever lost silently —
// and the branch is kept, which the verb never deletes. The bound is the
// CLI's own 60-second one: a stash and a worktree removal are git, not a
// clone.
//
// The verb is asked to keep the session alive. The daemon killing the session
// announces its end to an attached client, and that announcement racing the
// verb's own reply would let the client quit as "session killed" — the wrong
// story, and it would drop the note that says where the stashed changes
// went. The session is killed from here instead, after the note is shown:
// QuitSession and KillOtherSession both record the intent first.
func (m *OS) deleteWorktreeCmd(name string, attached bool) tea.Cmd {
	dial := m.verbDialer()
	params := map[string]any{"session": name, "stash": true, "keep_session": true}
	return func() tea.Msg {
		c, err := dial()
		if err != nil {
			return worktreeRemovedMsg{Name: name, Attached: attached, Err: fmt.Errorf("failed to reach the daemon: %w", err)}
		}
		defer func() { _ = c.Close() }()

		raw, err := c.CallWithTimeout("remove-worktree", params, 60*time.Second)
		if err != nil {
			return worktreeRemovedMsg{Name: name, Attached: attached, Err: fmt.Errorf("failed to remove the worktree: %w", err)}
		}
		var res struct {
			Gone          bool   `json:"gone"`
			Path          string `json:"path"`
			Branch        string `json:"branch"`
			Note          string `json:"note"`
			Changes       int    `json:"changes"`
			Stashed       bool   `json:"stashed"`
			SessionKilled bool   `json:"session_killed"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return worktreeRemovedMsg{Name: name, Attached: attached, Err: fmt.Errorf("failed to read the daemon's reply: %w", err)}
		}
		return worktreeRemovedMsg{
			Name: name, Attached: attached,
			Gone: res.Gone, Path: res.Path, Branch: res.Branch, Note: res.Note,
			Changes: res.Changes, Stashed: res.Stashed,
		}
	}
}

// worktreeRemovedMsg is a finished removal on its way back to the loop.
// Attached is captured at activation time, before the dialog closes, because
// the handler has to quit this client when the deleted session is the one it
// was attached to.
type worktreeRemovedMsg struct {
	// Name is the session whose worktree was removed.
	Name string
	// Attached says whether that session was the one this client is in.
	Attached bool
	// Gone says the directory had already been deleted out from under the
	// daemon, which it reports rather than failing.
	Gone bool
	// Path is the worktree's directory as the daemon knew it.
	Path string
	// Branch is the branch the worktree was on, which the verb keeps.
	Branch string
	// Note is the verb's own word for a gone directory, empty otherwise.
	Note string
	// Changes is how many uncommitted changes the worktree held.
	Changes int
	// Stashed says the changes were put in git stash rather than discarded.
	Stashed bool
	// Err is what went wrong, nil on success.
	Err error
}

// handleWorktreeRemoved says what became of a removal: what was deleted and
// where its changes went, or what failed. The session outlives the verb (it
// was asked to keep it), so the kill happens here, after the note — when the
// deleted session was the one this client was attached to, QuitSession kills
// it with the intent recorded, which makes this the one quit path; any other
// session goes through KillOtherSession, whose round trip refreshes the rail
// the way every other kill does. The switch touches the model, which is why
// it lives here and not in the command.
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
	m.ShowNotification(note, "info", m.Settings.NotificationDuration)
	if msg.Attached {
		// The note is the one record of where the stashed changes went, and
		// quitting tears the alt screen down with it. Dwell for the
		// notification's duration, then quit — the tick returns tea.Quit, so
		// the program ends at the same place, just readable.
		return tea.Tick(m.Settings.NotificationDuration, func(time.Time) tea.Msg {
			return worktreeRemoveQuitMsg{Name: msg.Name}
		})
	}
	m.KillOtherSession(msg.Name)
	return nil
}

// worktreeRemoveQuitMsg is the dwell after an attached worktree deletion
// running out: the note has been on screen for the notification duration and
// the client can go. Name is the session whose worktree was deleted — the
// user has had a whole dwell to switch to another one, and the quit must
// follow the deleted session, not wherever the client now sits.
type worktreeRemoveQuitMsg struct {
	Name string
}
