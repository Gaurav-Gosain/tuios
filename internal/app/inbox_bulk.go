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
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const bulkMax = 32

const bulkSnoozeID = "\x00bulk"

type bulkSheet struct {
	allow []bulkAllow
	held  []string
	shown int
	at    time.Time
}

type bulkAllow struct {
	item session.AttentionItem
	key  string
}

// InboxAgentsMarks is how many agents are marked.
func (m *OS) InboxAgentsMarks() int { return len(m.Inbox.agents.marks) }

// InboxAgentsToggleMark marks or unmarks the selected row, and moves the
// cursor on. A row is markable when it is an agent's, or when it offers a
// resume with no agent of its own.
func (m *OS) InboxAgentsToggleMark() {
	r, ok := m.cockpitSelected()
	if !ok {
		return
	}
	k, ok := m.ckMarkKey(r)
	if !ok {
		return
	}
	a := &m.Inbox.agents
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

// InboxAgentsMarkAll marks every markable row shown, or clears every mark
// when they are all marked already.
func (m *OS) InboxAgentsMarkAll() {
	a := &m.Inbox.agents
	a.marksGen++
	m.cockpitTouch()
	keys := m.cockpitShownMarkKeys()
	all := len(keys) > 0
	for _, k := range keys {
		if !a.marks[k] {
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
	for _, k := range keys {
		a.marks[k] = true
	}
}

func (m *OS) ckMarkKey(r ckRow) (string, bool) {
	switch {
	case r.agent >= 0:
		return agentKey(m.Inbox.ck.agents[r.agent]), true
	case r.kind == ckRowItem && r.hasItem && r.item.Kind == session.AttentionResume:
		return "\x01" + r.item.ID, true
	}
	return "", false
}

func (m *OS) markedShown() int {
	n := 0
	for _, k := range m.cockpitShownMarkKeys() {
		if m.Inbox.agents.marks[k] {
			n++
		}
	}
	return n
}

func (m *OS) cockpitShownMarkKeys() []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range m.cockpitRows() {
		if !r.selectable() {
			continue
		}
		if k, ok := m.ckMarkKey(r); ok && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

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

func (m *OS) refuseBulkRemote() bool {
	return m.refuseRemoteAnswer("An action on marked agents")
}

// InboxAgentsBulkReply opens one reply editor for the marked agents.
func (m *OS) InboxAgentsBulkReply() tea.Cmd {
	if m.refuseBulkRemote() {
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
		m.ShowNotification("Not in this reply: "+strings.Join(skipped, ", "), "warning", m.Settings.NotificationDuration*2)
	}
	if len(targets) == 0 {
		return nil
	}
	if len(targets) == 1 {
		t := targets[0]
		m.openInboxReply(t.session, t.window, t.who, "", false)
		return nil
	}
	comp := m.Inbox.reply.bulkDraft
	m.Inbox.reply.bulkDraft = nil
	if comp == nil {
		comp = composer.New(composer.Options{MaxRows: m.composerRows(inboxReplyRows), Placeholder: "Type the reply to send to each.", Images: true, ListKey: m.composerListKey, ListKeyHint: m.composerListKeyHint})
	}
	wires := m.wireDraft(comp, targets[0].session, targets[0].window, false)
	wires.mentions.dir = ""
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

type replyTarget struct {
	session, window, who string
}

func agentName(e sidebarAgentEntry) string {
	if n := printableTitle(e.Title); n != "" {
		return n
	}
	return shortWindowLabel(e.WindowID)
}

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
		m.ShowNotification("No marked agent waits on an approval", "warning", m.Settings.NotificationDuration)
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
		m.ShowNotification("No marked agent waits on an approval", "warning", m.Settings.NotificationDuration)
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
		m.ShowNotification("The list just appeared. Read it, then press enter", "warning", m.Settings.NotificationDuration)
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
			changed++
			continue
		}
		cmds = append(cmds, m.inboxReplyCmd(it, session.ApprovalOnce))
		delete(a.marks, agentKey(sidebarAgentEntry{SessionID: it.Session, WindowID: it.Window}))
		a.marksGen++
	}
	if changed > 0 {
		m.ShowNotification(strconv.Itoa(changed)+" changed after the list showed them and were not allowed. Look at them again", "warning", m.Settings.NotificationDuration*2)
	}
	return tea.Batch(cmds...)
}

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
			left += moreRows
		}
		if i == 0 && len(lines) > left {
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
		m.ShowNotification("The marked agents have nothing that needs you", "warning", m.Settings.NotificationDuration)
		return nil
	}
	m.Inbox.agents.marks = nil
	m.Inbox.agents.marksGen++
	return tea.Batch(cmds...)
}

func (m *OS) bulkItems() []session.AttentionItem {
	var out []session.AttentionItem
	seen := map[string]bool{}
	for _, e := range m.markedAgents() {
		for _, it := range m.Inbox.Items {
			if it.Host == e.Host && it.Window == e.WindowID && it.Session == e.SessionID {
				out = append(out, it)
				seen[it.ID] = true
			}
		}
	}
	for _, it := range m.markedResumes() {
		if !seen[it.ID] {
			out = append(out, it)
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
		m.ShowNotification("The marked agents have nothing to snooze", "warning", m.Settings.NotificationDuration)
		return
	}
	m.Inbox.life.pickFor = bulkSnoozeID
}

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

func (m *OS) bulkHints() []overlay.Hint {
	var hints []overlay.Hint
	resumes := len(m.markedResumes())
	if resumes > 0 {
		hints = m.keyHints(config.ActionInboxResume, "resume marked")
	}
	if resumes > 0 && resumes == m.markedShown() {
		hints = append(hints, m.keyHints(config.ActionInboxDismiss, "dismiss",
			config.ActionInboxMark, "mark", config.ActionInboxMarkAll, "all or none")...)
		return append(hints, m.cockpitEscHints()...)
	}
	hints = append(hints, m.keyHints(config.ActionInboxReply, "reply to each")...)
	hints = append(hints,
		overlay.Hint{Key: "1", Label: "allow"},
		overlay.Hint{Key: "3", Label: "reject"},
	)
	hints = append(hints, m.keyHints(config.ActionInboxDismiss, "dismiss", config.ActionInboxSnooze, "snooze", config.ActionInboxSettle, "settle",
		config.ActionInboxMark, "mark", config.ActionInboxMarkAll, "all or none")...)
	return append(hints, m.cockpitEscHints()...)
}

// InboxBulkResumedMsg is the answer to a resume of more than one offer: how
// many resumed, and the agents that did not, each with the reason.
type InboxBulkResumedMsg struct {
	Resumed int
	Failed  []string
	// IDs are the offers that did not resume, whose rows show Resume again.
	IDs []string
}

func (m *OS) markedResumes() []session.AttentionItem {
	a := &m.Inbox.agents
	var out []session.AttentionItem
	for _, it := range m.shownResumes() {
		if a.marks["\x01"+it.ID] || a.marks[agentKey(sidebarAgentEntry{SessionID: it.Session, WindowID: it.Window})] {
			out = append(out, it)
		}
	}
	return out
}

func (m *OS) shownResumes() []session.AttentionItem {
	a := &m.Inbox.agents
	var out []session.AttentionItem
	seen := map[string]bool{}
	for _, r := range m.cockpitRows() {
		if !r.selectable() || !r.hasItem || r.item.Kind != session.AttentionResume || seen[r.item.ID] || a.resuming[r.item.ID] {
			continue
		}
		seen[r.item.ID] = true
		out = append(out, r.item)
	}
	return out
}

// InboxAgentsBulkResume resumes every marked row that offers a resume. The
// view stays open, and the marks clear. With no marked offer it is
// InboxResume on the row under the cursor.
func (m *OS) InboxAgentsBulkResume() tea.Cmd {
	if m.refuseBulkRemote() {
		return nil
	}
	offers := m.markedResumes()
	if len(offers) == 0 {
		return m.InboxResume()
	}
	if m.AttachedHost != "" || m.DaemonClient == nil {
		m.ShowNotification("These panes are on another machine. Attach there to resume them", "warning", m.Settings.NotificationDuration)
		return nil
	}
	m.Inbox.agents.marks = nil
	m.Inbox.agents.marksGen++
	return m.resumeOffers(offers)
}

func (m *OS) resumeOffers(offers []session.AttentionItem) tea.Cmd {
	type target struct{ id, session, window, who string }
	var targets []target
	var skipped []string
	a := &m.Inbox.agents
	for _, it := range offers {
		who := inboxWho(it)
		switch {
		case it.Host != "":
			skipped = append(skipped, who+" (on "+printableTitle(it.Host)+")")
			continue
		case len(targets) == bulkMax:
			skipped = append(skipped, who+" (past "+strconv.Itoa(bulkMax)+")")
			continue
		}
		targets = append(targets, target{id: it.ID, session: it.Session, window: it.Window, who: who})
		if a.resuming == nil {
			a.resuming = make(map[string]bool)
		}
		a.resuming[it.ID] = true
	}
	a.marksGen++
	if len(skipped) > 0 {
		m.ShowNotification("Not resumed: "+strings.Join(skipped, ", "), "warning", m.Settings.NotificationDuration*2)
	}
	if len(targets) == 0 {
		return nil
	}
	build := m.DaemonClient.ClientVersion()
	return func() tea.Msg {
		var msg InboxBulkResumedMsg
		var command string
		var lastErr error
		fail := func(t target, err error) {
			lastErr = err
			msg.Failed = append(msg.Failed, t.who+" ("+err.Error()+")")
			msg.IDs = append(msg.IDs, t.id)
		}
		client, err := session.DialVerbClientAs(build)
		if err != nil {
			for _, t := range targets {
				fail(t, err)
			}
			return msg
		}
		defer func() { _ = client.Close() }()
		for _, t := range targets {
			raw, err := client.CallWithTimeout("resume-agent", map[string]any{"session": t.session, "window": t.window}, 5*time.Second)
			if err != nil {
				fail(t, err)
				continue
			}
			var res struct {
				Command string `json:"command"`
			}
			if err := jsonx.Unmarshal(raw, &res); err != nil {
				fail(t, errfmt.Errorf("read the resume-agent answer: %w", err))
				continue
			}
			command = res.Command
			msg.Resumed++
		}
		if len(targets) == 1 {
			if len(msg.Failed) > 0 {
				return InboxResumedMsg{Err: lastErr, ID: targets[0].id}
			}
			return InboxResumedMsg{Command: command}
		}
		return msg
	}
}

func (m *OS) applyInboxBulkResumed(msg InboxBulkResumedMsg) {
	m.unmarkResuming(msg.IDs...)
	if len(msg.Failed) > 0 {
		m.ShowNotification("Not resumed: "+strings.Join(msg.Failed, ", "), "error", m.Settings.NotificationDuration*2)
		return
	}
	words := "Resumed " + strconv.Itoa(msg.Resumed) + " agents"
	if msg.Resumed == 1 {
		words = "Resumed 1 agent"
	}
	m.ShowNotification(words, "success", m.Settings.NotificationDuration)
}

func (m *OS) unmarkResuming(ids ...string) {
	a := &m.Inbox.agents
	for _, id := range ids {
		delete(a.resuming, id)
	}
	a.marksGen++
}

func (m *OS) pruneResuming(items []session.AttentionItem) {
	a := &m.Inbox.agents
	if len(a.resuming) == 0 {
		return
	}
	open := make(map[string]bool, len(items))
	for _, it := range items {
		open[it.ID] = true
	}
	for id := range a.resuming {
		if !open[id] {
			delete(a.resuming, id)
			a.marksGen++
		}
	}
}

func (m *OS) resumeAllHint() []overlay.Hint {
	if len(m.shownResumes()) < 2 {
		return nil
	}
	return []overlay.Hint{{Key: m.inboxKeyOr(config.ActionInboxMarkAll, "*") + " " + m.inboxKeyOr(config.ActionInboxResume, "y"), Label: "resume all"}}
}
