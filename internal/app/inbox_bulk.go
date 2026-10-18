package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Acting on many agents at once, in the agents view.
//
// x marks the selected agent and * marks every agent shown (or clears the
// marks when all of them are marked). A marked row shows its mark, and a bulk
// key acts only on the marked agents the fleet shows: a row in a folded group
// or past a search is left out. Closing the view clears the marks. With
// marks:
//
//   - r opens one reply editor for all of them, and enter queues the same
//     message to each, at most bulkMax, as send-agent-message --select does.
//     An agent waiting on a prompt, or on another machine, is left out, and
//     the dock says so.
//   - 3 rejects every held approval of the marked agents.
//   - 1 opens a sheet that lists each approval it would allow, whole. enter
//     allows them, once the sheet has been on screen for the settle time,
//     and only those that are as the sheet showed them. An approval the
//     screen has no room to show is not allowed: the sheet says how many
//     wait, and 1 again shows them. A risky approval, a
//     plan, and one with characters the terminal cannot show are never in a
//     bulk allow. They stay marked, and the sheet says to allow them one by
//     one.
//   - d dismisses the marked agents' Inbox items, and z snoozes them after
//     the length is picked.
//
// Every one of these acts as the person, with the attach nonce, so a key from
// send-keys does none of them.

// bulkMax is how many agents one bulk action reaches.
const bulkMax = 32

// bulkSnoozeID is the snooze picker's target while it picks for the marks.
const bulkSnoozeID = "\x00bulk"

// bulkSheet is the bulk allow's confirm sheet.
type bulkSheet struct {
	// allow is what enter allows, by item id, with the key each had when
	// the sheet was drawn.
	allow []bulkAllow
	// held are the approvals left out, as their agents' names.
	held []string
	// shown is how many of allow, from the first, the last frame drew
	// whole. enter allows those and no more.
	shown int
	// at is when the sheet was first drawn as it is now. It starts again
	// when shown changes, so an approval that came into view has to be on
	// screen for the settle time too.
	at time.Time
}

// bulkAllow is one approval the sheet allows.
type bulkAllow struct {
	item session.AttentionItem
	key  string
}

// InboxAgentsMarks is how many agents are marked.
func (m *OS) InboxAgentsMarks() int { return len(m.Inbox.agents.marks) }

// InboxAgentsToggleMark marks or unmarks the selected agent, and moves the
// cursor on.
func (m *OS) InboxAgentsToggleMark() {
	e, ok := m.cockpitAgent()
	if !ok {
		return
	}
	a := &m.Inbox.agents
	k := agentKey(e)
	a.marksGen++
	if a.marks[k] {
		delete(a.marks, k)
	} else {
		if a.marks == nil {
			a.marks = make(map[string]bool)
		}
		a.marks[k] = true
	}
	m.CockpitMove(1)
}

// InboxAgentsMarkAll marks every agent shown, or clears every mark when they
// are all marked already.
func (m *OS) InboxAgentsMarkAll() {
	a := &m.Inbox.agents
	a.marksGen++
	m.cockpitTouch()
	list := m.cockpitShownAgents()
	all := len(list) > 0
	for _, e := range list {
		if !a.marks[agentKey(e)] {
			all = false
			break
		}
	}
	if all {
		a.marks = nil
		return
	}
	if a.marks == nil {
		a.marks = make(map[string]bool)
	}
	for _, e := range list {
		a.marks[agentKey(e)] = true
	}
}

// markedAgents are the marked agents the fleet shows, in fleet order.
func (m *OS) markedAgents() []sidebarAgentEntry {
	a := &m.Inbox.agents
	var out []sidebarAgentEntry
	for _, e := range m.cockpitShownAgents() {
		if a.marks[agentKey(e)] {
			out = append(out, e)
		}
	}
	return out
}

// cockpitShownAgents are the agents with a row in the fleet, in fleet order:
// not those in a folded group or past the rows a long group shows.
func (m *OS) cockpitShownAgents() []sidebarAgentEntry {
	ck := &m.Inbox.ck
	var out []sidebarAgentEntry
	seen := map[int]bool{}
	for _, r := range m.cockpitRows() {
		if r.selectable() && r.agent >= 0 && !seen[r.agent] {
			seen[r.agent] = true
			out = append(out, ck.agents[r.agent])
		}
	}
	return out
}

// refuseBulkRemote refuses a bulk action from a key that did not come from
// the keyboard.
func (m *OS) refuseBulkRemote() bool {
	return m.refuseRemoteAnswer("An action on marked agents")
}

// InboxAgentsBulkReply opens one reply editor for the marked agents.
func (m *OS) InboxAgentsBulkReply() tea.Cmd {
	if m.refuseBulkRemote() || !m.inboxReplySupported() {
		return nil
	}
	var targets []replyTarget
	var skipped []string
	for _, e := range m.markedAgents() {
		who := agentName(e)
		switch {
		case e.Host != "":
			skipped = append(skipped, who+" (on "+printableTitle(e.Host)+")")
			continue
		case e.State == "needs_input":
			skipped = append(skipped, who+" (waits on a prompt)")
			continue
		}
		if len(targets) == bulkMax {
			skipped = append(skipped, who+" (past "+strconv.Itoa(bulkMax)+")")
			continue
		}
		targets = append(targets, replyTarget{session: e.SessionID, window: e.WindowID, who: who})
	}
	if len(skipped) > 0 {
		m.ShowNotification("Not in this reply: "+strings.Join(skipped, ", "), "info", m.Settings.NotificationDuration*2)
	}
	if len(targets) == 0 {
		return nil
	}
	if len(targets) == 1 {
		t := targets[0]
		m.openInboxReply(t.session, t.window, t.who, "", false)
		return nil
	}
	// The bulk draft esc or ctrl+] kept opens again.
	comp := m.Inbox.reply.bulkDraft
	m.Inbox.reply.bulkDraft = nil
	if comp == nil {
		comp = composer.New(composer.Options{MaxRows: inboxReplyRows, Placeholder: "Type the reply to send to each.", Images: true})
	}
	// No history: the targets each keep their own.
	wires := m.wireDraft(comp, targets[0].session, targets[0].window, false)
	wires.mentions.dir = "" // the targets have different folders
	m.Inbox.reply.editor = &inboxReplyEditor{
		draftWires: wires,
		session:    targets[0].session,
		window:     targets[0].window,
		who:        strconv.Itoa(len(targets)) + " agents",
		comp:       comp,
		targets:    targets,
	}
	return nil
}

// replyTarget is one agent a bulk reply goes to.
type replyTarget struct {
	session, window, who string
}

// agentName is an agent's name as a row shows it.
func agentName(e sidebarAgentEntry) string {
	if n := printableTitle(e.Title); n != "" {
		return n
	}
	return shortWindowLabel(e.WindowID)
}

// bulkHeld is every held approval of the marked agents.
func (m *OS) bulkHeld() []session.AttentionItem {
	var out []session.AttentionItem
	for _, e := range m.markedAgents() {
		for _, it := range m.Inbox.Items {
			if it.Host == "" && it.Window == e.WindowID && it.Session == e.SessionID && inboxHeld(it) {
				out = append(out, it)
			}
		}
	}
	return out
}

// InboxAgentsBulkDeny rejects every held approval of the marked agents.
func (m *OS) InboxAgentsBulkDeny() tea.Cmd {
	if m.refuseBulkRemote() {
		return nil
	}
	held := m.bulkHeld()
	if len(held) == 0 {
		m.ShowNotification("No marked agent waits on an approval", "info", m.Settings.NotificationDuration)
		return nil
	}
	var cmds []tea.Cmd
	for _, it := range held {
		cmds = append(cmds, m.inboxReplyCmd(it, session.ApprovalDeny))
	}
	m.Inbox.agents.marks = nil
	m.Inbox.agents.marksGen++
	return tea.Batch(cmds...)
}

// InboxAgentsBulkAllow opens the confirm sheet for allowing the marked
// agents' approvals.
func (m *OS) InboxAgentsBulkAllow() {
	if m.refuseBulkRemote() {
		return
	}
	sheet := &bulkSheet{}
	for _, it := range m.bulkHeld() {
		if it.Kind != session.AttentionApproval || inboxRisky(it) || !inboxShowsWhole(it) ||
			!slices.Contains(it.Options, session.ApprovalOnce) {
			sheet.held = append(sheet.held, inboxWho(it))
			continue
		}
		sheet.allow = append(sheet.allow, bulkAllow{item: it, key: inboxShownKey(it)})
	}
	if len(sheet.allow) == 0 && len(sheet.held) == 0 {
		m.ShowNotification("No marked agent waits on an approval", "info", m.Settings.NotificationDuration)
		return
	}
	m.Inbox.agents.sheet = sheet
}

// InboxAgentsSheetOpen reports whether the bulk allow's sheet is up.
func (m *OS) InboxAgentsSheetOpen() bool { return m.ShowInbox && m.Inbox.agents.sheet != nil }

// InboxAgentsSheetKey takes a key while the sheet is up: enter allows, any
// other key closes the sheet and allows nothing.
func (m *OS) InboxAgentsSheetKey(key string) tea.Cmd {
	a := &m.Inbox.agents
	sheet := a.sheet
	if key != "enter" {
		a.sheet = nil
		return nil
	}
	if m.refuseBulkRemote() {
		a.sheet = nil
		return nil
	}
	if sheet.at.IsZero() || time.Since(sheet.at) < inboxAnswerSettle {
		m.ShowNotification("The list just appeared. Read it, then press enter", "info", m.Settings.NotificationDuration)
		return nil
	}
	a.sheet = nil
	if sheet.shown == 0 {
		return nil
	}
	var cmds []tea.Cmd
	changed := 0
	for _, b := range sheet.allow[:sheet.shown] {
		it, ok := m.inboxItem(b.item.ID)
		if !ok || inboxShownKey(it) != b.key {
			// Answered, gone or changed since the sheet showed it: what
			// would be allowed is not what the person read.
			changed++
			continue
		}
		cmds = append(cmds, m.inboxReplyCmd(it, session.ApprovalOnce))
		delete(a.marks, agentKey(sidebarAgentEntry{SessionID: it.Session, WindowID: it.Window}))
		a.marksGen++
	}
	if changed > 0 {
		m.ShowNotification(strconv.Itoa(changed)+" changed after the list showed them and were not allowed. Look at them again", "info", m.Settings.NotificationDuration*2)
	}
	return tea.Batch(cmds...)
}

// bulkSheetLines draws the sheet in width cells and at most rows lines. Each
// approval is drawn whole or not at all, and the sheet records how many it
// drew: enter allows only what the person could read.
func (m *OS) bulkSheetLines(width, rows int) []string {
	sheet := m.Inbox.agents.sheet
	pal := theme.UI()
	bg := pal.Surface
	st := overlay.Style(bg)
	fg := overlay.Readable(pal.Fg, bg)
	dim := overlay.Readable(pal.FgDim, bg)
	var out []string
	line := func(s string, ink color.Color) {
		for _, l := range wrapPlain(s, max(width-2, 8)) {
			out = append(out, overlay.Fill(st.Foreground(ink).Render("  "+l), width, bg))
		}
	}
	if n := len(sheet.held); n > 0 {
		words := strconv.Itoa(n) + " need a second look. Allow them one by one."
		if n == 1 {
			words = "1 needs a second look. Allow it one by one."
		}
		line(words+" ("+strings.Join(sheet.held, ", ")+")", overlay.Readable(pal.Warning, bg))
	}
	held := out
	out = nil
	// The approvals, each whole, under the heading line, in the room the
	// held note and the more line (two lines) leave. Whatever does not fit
	// is cut from the bottom of the sheet, so it is never an approval.
	const moreRows = 2
	room := rows - 1 - len(held) - moreRows
	var items [][]string
	used := 0
	for i, b := range sheet.allow {
		lines := []string{overlay.Fill(st.Foreground(fg).Render("  "+ansi.Truncate(inboxWho(b.item)+":", width-2, "…")), width, bg)}
		for _, l := range wrapPlain(printableTitle(b.item.Summary), max(width-4, 8)) {
			lines = append(lines, overlay.Fill(st.Foreground(dim).Render("    "+l), width, bg))
		}
		left := room
		if i == len(sheet.allow)-1 {
			// The last approval needs no more line under it.
			left += moreRows
		}
		if i == 0 && len(lines) > left {
			// A held note too long to leave room for one approval gives
			// way: the approval goes first, and the note is cut.
			left = rows - 1
		}
		if used+len(lines) > left {
			break
		}
		items = append(items, lines)
		used += len(lines)
	}
	if len(items) != sheet.shown {
		sheet.shown, sheet.at = len(items), time.Now()
	}
	if sheet.at.IsZero() {
		sheet.at = time.Now()
	}
	if n := len(items); n > 0 {
		head := "Allow " + strconv.Itoa(n) + " approvals once?"
		if n == 1 {
			head = "Allow 1 approval once?"
		}
		out = append(out, overlay.Fill(st.Foreground(fg).Bold(true).Render("  "+head), width, bg))
		for _, lines := range items {
			out = append(out, lines...)
		}
	}
	if more := len(sheet.allow) - len(items); more > 0 {
		words := strconv.Itoa(more) + " more do not fit on the screen. This allow leaves them out. Press 1 again to see them."
		if len(items) == 0 {
			words = "The screen is too small to show an approval whole. Make the window larger, or allow them one by one."
		}
		line(words, overlay.Readable(pal.Warning, bg))
	}
	return append(out, held...)
}

// bulkSheetHints are the sheet's keys.
func (m *OS) bulkSheetHints() []overlay.Hint {
	if m.Inbox.agents.sheet.shown == 0 {
		return []overlay.Hint{{Key: "esc", Label: "close"}}
	}
	return []overlay.Hint{{Key: overlay.EnterKey(), Label: "allow"}, {Key: "esc", Label: "cancel"}}
}

// InboxAgentsBulkDismiss dismisses the marked agents' Inbox items.
func (m *OS) InboxAgentsBulkDismiss() tea.Cmd {
	if m.refuseBulkRemote() {
		return nil
	}
	var cmds []tea.Cmd
	for _, it := range m.bulkItems() {
		if cmd := m.inboxDismissCmd(it.ID, false); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		m.ShowNotification("The marked agents have nothing that needs you", "info", m.Settings.NotificationDuration)
		return nil
	}
	m.Inbox.agents.marks = nil
	m.Inbox.agents.marksGen++
	return tea.Batch(cmds...)
}

// bulkItems are the marked agents' Inbox items.
func (m *OS) bulkItems() []session.AttentionItem {
	var out []session.AttentionItem
	for _, e := range m.markedAgents() {
		for _, it := range m.Inbox.Items {
			if it.Host == e.Host && it.Window == e.WindowID && it.Session == e.SessionID {
				out = append(out, it)
			}
		}
	}
	return out
}

// InboxAgentsBulkSnooze opens the snooze picker for the marked agents' items.
// Items that cannot be snoozed, such as a held approval, are left out.
func (m *OS) InboxAgentsBulkSnooze() {
	if m.refuseBulkRemote() || !m.inboxCanMark("Snoozing") {
		return
	}
	a := &m.Inbox.agents
	a.snooze = a.snooze[:0]
	for _, it := range m.bulkItems() {
		if inboxSnoozeRefusal(it) == "" && it.SnoozedUntil == 0 {
			a.snooze = append(a.snooze, it.ID)
		}
	}
	if len(a.snooze) == 0 {
		m.ShowNotification("The marked agents have nothing to snooze", "info", m.Settings.NotificationDuration)
		return
	}
	m.Inbox.life.pickFor = bulkSnoozeID
}

// bulkSnoozeCmd snoozes every item the picker was opened for, with the
// chosen length's params.
func (m *OS) bulkSnoozeCmd(params func() map[string]any, words string) tea.Cmd {
	a := &m.Inbox.agents
	var cmds []tea.Cmd
	for _, id := range a.snooze {
		it, ok := m.inboxItem(id)
		if !ok {
			continue
		}
		p := params()
		p["id"], p["action"] = id, "snooze"
		cmds = append(cmds, m.inboxMarkCmd(p, inboxWho(it), words))
	}
	a.snooze, a.marks = nil, nil
	a.marksGen++
	return tea.Batch(cmds...)
}

// bulkHints are the agents view's keys while agents are marked.
func (m *OS) bulkHints() []overlay.Hint {
	hints := m.keyHints(config.ActionInboxReply, "reply to each")
	hints = append(hints,
		overlay.Hint{Key: "1", Label: "allow"},
		overlay.Hint{Key: "3", Label: "reject"},
	)
	hints = append(hints, m.keyHints(config.ActionInboxDismiss, "dismiss", config.ActionInboxSnooze, "snooze",
		config.ActionInboxMark, "mark", config.ActionInboxMarkAll, "all or none")...)
	return append(hints, m.keyHints(config.ActionInboxClose, "close")...)
}
