package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
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
		m.sessionDeletableWorktree(m.sessionCloseTarget()) != nil {
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
func (m *OS) OpenSessionClose() tea.Cmd { return m.OpenSessionCloseFor("") }

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
func (m *OS) OpenSessionCloseFor(sessionID string) tea.Cmd {
	if sessionID == m.sidebarCurrentSessionID() {
		sessionID = ""
	}
	m.SessionCloseWindow = ""
	m.SessionCloseTarget = sessionID
	m.SessionCloseSelected = SessionCloseRowCancel
	m.SessionCloseWorktreeToll = nil
	m.ShowSessionClose = true
	// The delete row's toll is a round trip away, so the fetch starts now and
	// the dialog draws without the worktree line until the reply lands.
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
	m.SessionCloseSelected = SessionCloseRowCancel
	m.SessionCloseWorktreeToll = nil
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
	// The dialog refuses the delete it knows would lose commits: a detached
	// HEAD's own commits become unreachable when the worktree goes. It stays
	// open, so the answer can change instead of the commits.
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

// callWorktreeVerb runs one worktree verb over a fresh daemon connection and
// decodes the JSON reply into res. fail names the action for the call's own
// error; the reach and decode wraps are the same for every verb. Shared by the
// create and delete commands, which differ only in verb, params and reply.

// worktreeToll is what the delete-worktree row would take from the worktree's
// files, as list-worktrees with changes reports them. The counts come from a
// round trip, so Loaded gates the dialog's line: it draws before the toll
// lands and says nothing about the worktree until it has.
type worktreeToll struct {
	// Loaded says the counts below were read, whatever they were. A failed
	// read stays unloaded: the dialog must not read a failed fetch as clean.
	Loaded bool
	// Changes is every tracked modification and untracked file, the part the
	// delete row stashes.
	Changes int
	// Ignored is the part nothing saves: git never stashes ignored files and
	// git worktree remove deletes them without --force.
	Ignored int
	// Own is the commits no branch, tag or remote holds. The delete refuses
	// them: on a detached HEAD they become unreachable.
	Own int
	// Gone says the worktree's directory was already missing, so the delete
	// has nothing to take and the dialog says so instead of counting.
	Gone bool
}

// worktreeTollMsg is the fetched toll for the open close dialog. Name is the
// session it was fetched for, so a reply for a dialog closed and reopened on
// another session never lands on the wrong one.
type worktreeTollMsg struct {
	Name string
	Toll worktreeToll
}

// worktreeTollCmd reads the toll for one session's worktree off the daemon's
// listing, the one the CLI reads. It runs off the Update goroutine: the dialog
// is already open and drawing while git counts. A failed read answers an
// unloaded toll, which the dialog treats as no news yet, not as clean.
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

// worktreeTollLine is the sentence the dialog turns on when the delete row is
// drawn: what the row does with the worktree's files. Empty while the toll is
// unloaded, or when the dialog is not on a session.
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
	// A -1 is a count git would not give, not a zero: silence here would read
	// as the clean line below, which promises nothing is lost.
	if t.Changes < 0 || t.Ignored < 0 || t.Own < 0 {
		parts = append(parts, "some counts could not be read")
	}
	if len(parts) == 0 {
		return "The worktree is clean: nothing stashed, nothing lost."
	}
	return strings.Join(parts, "; ") + "."
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
	// Ignored is how many ignored files the removal deleted unsaved: git
	// never stashes them, and git worktree remove does not ask.
	Ignored int
	// Stashed says the changes were put in git stash rather than discarded.
	Stashed bool
	// SessionKilled says the daemon already killed the session, on Windows,
	// where the removal cannot run while the session's shells hold the
	// worktree as their cwd. There is nothing left for this client to kill.
	SessionKilled bool
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
	if !msg.Gone && msg.Ignored > 0 {
		// The part nothing saves: git never stashes ignored files, and the
		// removal deletes them without --force. Saying so is the whole point
		// of the note.
		note += " " + countOf(msg.Ignored, "ignored file") + " deleted."
	}
	m.ShowNotification(note, "info", m.Settings.NotificationDuration)
	if msg.SessionKilled {
		// The daemon killed the session for the removal (Windows), so there
		// is nothing left to kill from here.
		return nil
	}
	if msg.Attached {
		// The note is the one record of where the stashed changes went, and
		// quitting tears the alt screen down with it. Dwell for the
		// notification's duration, then quit — the tick returns tea.Quit, so
		// the program ends at the same place, just readable.
		return m.tickUntilStop(m.Settings.NotificationDuration, func(time.Time) tea.Msg {
			return worktreeRemoveQuitMsg{Name: msg.Name}
		})
	}
	// The kill's own "Killed session" note would replace the deletion note,
	// which is the one record of where the stashed changes went. The same
	// guards KillOtherSession applies tell whether it will post at all; when
	// it does, the kill handler stays quiet once.
	if msg.Name != "" && msg.Name != m.sidebarCurrentSessionID() && m.DaemonClient != nil {
		m.pendingWorktreeKill = true
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
