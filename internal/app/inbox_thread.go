package app

import (
	"context"
	"encoding/json"
	"image/color"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The agent thread: what the selected agent in the agents view said and did,
// beside the list on a wide screen and in its place on a narrow one.
//
//   - For Claude Code, the conversation, read with agent-transcript (the
//     person's own verb, with the attach nonce: secrets masked, every string
//     bounded, control characters gone). The client subscribes to the
//     transcript event of the selected pane alone, and reads again from its
//     cursor when the transcript grows. When the selection moves, the
//     subscription moves with it.
//   - For any other agent, the recap of its turns (agent-activity) and the
//     mail between it and other agents.
//   - Under either, the approval the agent waits on, if the Inbox holds one:
//     1, 2 and 3 answer it, with the same settle time and second press for a
//     risky allow as in the attention list.
//   - Under that, the reply editor, opened with r.
//
// Everything the agent wrote is drawn as the agent's: a quiet gutter mark and
// the dim ink. Nothing in it is a link or a key. At most threadsKept threads
// are kept in memory, and nothing is read while the view is closed.

// threadsKept is how many threads the client keeps.
const threadsKept = 3

// threadWideMin is the narrowest screen that shows the thread beside the
// list.
const threadWideMin = 110

// threadListWidth is the list's width beside the thread.
const threadListWidth = 48

// threadReadTimeout bounds one transcript or recap read.
const threadReadTimeout = 5 * time.Second

// threadEntry is one transcript entry as the thread draws it.
type threadEntry struct {
	ID     string `json:"id"`
	Role   string `json:"role"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Tool   string `json:"tool"`
	Target string `json:"target"`
	Status string `json:"status"`
	Plan   string `json:"plan"`
	Todos  []struct {
		Status string `json:"status"`
	} `json:"todos"`
}

// agentThread is one agent's thread.
type agentThread struct {
	key      string
	session  string
	window   string
	harness  string
	entries  []threadEntry
	cursor   string
	read     bool
	loading  bool
	err      string
	recapKey string
	recap    *session.AgentActivityRecap
	// gen changes when what the thread shows changes, for the drawn lines.
	gen uint64
}

// threadsState is the threads kept and the transcript subscription.
type threadsState struct {
	byKey map[string]*agentThread
	order []string
	// watchKey is the thread whose transcript events the client follows.
	watchKey string
	stop     context.CancelFunc
	events   chan tea.Msg
	// open is the thread shown in place of the list on a narrow screen.
	open bool
	// panel is the last view drawn with the thread, and panelAt what it was
	// drawn from.
	panel   agentsPanel
	panelAt threadPanelKey
}

// threadPanelKey is what the view with the thread depends on.
type threadPanelKey struct {
	list                agentsListKey
	sel, scroll         int
	width, height       int
	minute              int64
	theme               uint64
	thread              string
	threadGen           uint64
	inboxGen, mailGen   uint64
	editor              uint64
	open, live, replied bool
	keys                *config.KeybindRegistry
	title               string
}

// threadPageMsg is an agent-transcript reply.
type threadPageMsg struct {
	key     string
	entries []threadEntry
	cursor  string
	reset   bool
	more    bool
	after   bool
	err     error
}

// threadRecapMsg is an agent-activity reply for a thread.
type threadRecapMsg struct {
	key, recapKey string
	recap         *session.AgentActivityRecap
	err           error
}

// threadGrewMsg says the followed transcript grew.
type threadGrewMsg struct{ key string }

// threadWatchEnded says the subscription ended.
type threadWatchEnded struct{ key string }

// threadTranscriptHarness reports whether the daemon reads a harness's
// transcript. Only Claude Code has a [transcript] block.
func threadTranscriptHarness(h string) bool { return h == "claude-code" }

// thread is the kept thread of an agent, made when it is first shown.
func (m *OS) thread(e sidebarAgentEntry) *agentThread {
	ts := &m.Inbox.threads
	key := agentKey(e)
	if t := ts.byKey[key]; t != nil {
		return t
	}
	if ts.byKey == nil {
		ts.byKey = make(map[string]*agentThread)
	}
	t := &agentThread{key: key, session: e.SessionID, window: e.WindowID, harness: e.Harness}
	ts.byKey[key] = t
	ts.order = append(ts.order, key)
	for len(ts.order) > threadsKept {
		old := ts.order[0]
		ts.order = ts.order[1:]
		if old != ts.watchKey {
			delete(ts.byKey, old)
		}
	}
	return t
}

// threadShown reports whether a thread is on screen now.
func (m *OS) threadShown() bool {
	a := &m.Inbox.agents
	return m.ShowInbox && a.on && m.Inbox.Peek == nil && (m.GetRenderWidth() >= threadWideMin || m.Inbox.threads.open)
}

// InboxThreadFetch reads what the selected agent's thread needs and moves the
// transcript subscription to it. It is called after every input and every
// thread delivery while the Inbox is up, and costs a few comparisons when
// there is nothing to read.
func (m *OS) InboxThreadFetch() tea.Cmd {
	ts := &m.Inbox.threads
	if !m.threadShown() || (m.DaemonClient == nil && m.Inbox.call == nil) {
		m.stopThreadWatch()
		return nil
	}
	e, ok := m.inboxAgentSelected()
	if !ok || e.Host != "" {
		m.stopThreadWatch()
		return nil
	}
	t := m.thread(e)
	var cmds []tea.Cmd
	if threadTranscriptHarness(e.Harness) {
		if ts.watchKey != t.key {
			m.stopThreadWatch()
			cmds = append(cmds, m.startThreadWatch(t))
		}
		if !t.read && !t.loading {
			t.loading = true
			cmds = append(cmds, m.threadPageCmd(t, ""))
		}
	} else {
		m.stopThreadWatch()
		if rk := strconv.FormatInt(e.StateAt, 10); t.recapKey != rk && !t.loading {
			t.loading, t.recapKey = true, rk
			cmds = append(cmds, m.threadRecapCmd(t, rk))
		}
	}
	return tea.Batch(cmds...)
}

// threadPageCmd reads a transcript page: the newest entries, or those after
// cursor.
func (m *OS) threadPageCmd(t *agentThread, after string) tea.Cmd {
	call, nonce := m.inboxCaller(), m.inboxNonce()
	key, sess, window := t.key, t.session, t.window
	return func() tea.Msg {
		params := map[string]any{"session": sess, "window": window, "human_nonce": nonce, "limit": 200}
		if after != "" {
			params["after"] = after
		}
		raw, err := call("agent-transcript", params, threadReadTimeout)
		msg := threadPageMsg{key: key, after: after != "", err: err}
		if err != nil {
			return msg
		}
		var res struct {
			Entries []threadEntry `json:"entries"`
			Cursor  string        `json:"cursor"`
			Reset   bool          `json:"reset"`
			More    bool          `json:"more"`
		}
		if msg.err = json.Unmarshal(raw, &res); msg.err == nil {
			msg.entries, msg.cursor, msg.reset, msg.more = res.Entries, res.Cursor, res.Reset, res.More
		}
		return msg
	}
}

// threadRecapCmd reads an agent's recap.
func (m *OS) threadRecapCmd(t *agentThread, recapKey string) tea.Cmd {
	call, key, sess, window := m.inboxCaller(), t.key, t.session, t.window
	return func() tea.Msg {
		rc, err := agentActivityRecapCall(call, sess, window, 0)
		return threadRecapMsg{key: key, recapKey: recapKey, recap: rc, err: err}
	}
}

// applyThreadPage takes a transcript page.
func (m *OS) applyThreadPage(msg threadPageMsg) tea.Cmd {
	t := m.Inbox.threads.byKey[msg.key]
	if t == nil {
		return nil
	}
	t.loading = false
	t.gen++
	if msg.err != nil {
		t.read = true
		t.err = threadReadError(msg.err)
		return nil
	}
	t.read, t.err = true, ""
	if !msg.after || msg.reset {
		t.entries = nil
	}
	// An entry with an id the thread holds replaces it: a tool call's
	// status changes once its result is in.
	for _, e := range msg.entries {
		replaced := false
		for i := len(t.entries) - 1; i >= 0 && i >= len(t.entries)-16; i-- {
			if t.entries[i].ID == e.ID {
				t.entries[i], replaced = e, true
				break
			}
		}
		if !replaced {
			t.entries = append(t.entries, e)
		}
	}
	// The thread draws the newest; the page bound holds the rest.
	if n := len(t.entries); n > 200 {
		t.entries = append(t.entries[:0], t.entries[n-200:]...)
	}
	if msg.cursor != "" {
		t.cursor = msg.cursor
	}
	if msg.more {
		t.loading = true
		return m.threadPageCmd(t, t.cursor)
	}
	return nil
}

// threadReadError is a failed read as the thread says it.
func threadReadError(err error) string {
	if ce, ok := err.(*session.VerbCallError); ok {
		switch ce.Code {
		case session.ErrVerbNoTranscript:
			return "This agent has no transcript to read yet."
		case session.ErrVerbUnknownVerb:
			return "This daemon cannot read transcripts. Restart it with a newer tuios: tuios kill-server"
		case session.ErrVerbNotHuman:
			return "Only your own client reads a transcript."
		}
	}
	return "The transcript could not be read: " + err.Error()
}

// applyThreadRecap takes an agent-activity reply.
func (m *OS) applyThreadRecap(msg threadRecapMsg) {
	t := m.Inbox.threads.byKey[msg.key]
	if t == nil || t.recapKey != msg.recapKey {
		return
	}
	t.loading = false
	t.gen++
	t.recap = msg.recap
	if msg.err != nil {
		t.recap = nil
	}
}

// startThreadWatch follows the transcript events of one pane.
func (m *OS) startThreadWatch(t *agentThread) tea.Cmd {
	ts := &m.Inbox.threads
	if m.Inbox.call != nil || m.DaemonClient == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan tea.Msg, 4)
	ts.watchKey, ts.stop, ts.events = t.key, cancel, ch
	build := m.DaemonClient.ClientVersion()
	key, sess, window := t.key, t.session, t.window
	go func() {
		defer close(ch)
		client, err := session.DialVerbClientAs(build)
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		stop := context.AfterFunc(ctx, func() { _ = client.Close() })
		defer stop()
		if _, err := client.CallWithTimeout("subscribe", map[string]any{
			"session": sess, "window": window, "types": []string{session.EventTranscript},
		}, threadReadTimeout); err != nil {
			return
		}
		for ctx.Err() == nil {
			if _, err := client.ReadEventLine(0); err != nil {
				return
			}
			// Only the latest growth matters: the read takes everything
			// after the cursor.
			select {
			case ch <- threadGrewMsg{key: key}:
			default:
			}
		}
	}()
	return listenThread(ch, key)
}

// listenThread waits for one delivery from the transcript subscription.
func listenThread(ch <-chan tea.Msg, key string) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return threadWatchEnded{key: key}
		}
		return msg
	}
}

// stopThreadWatch ends the transcript subscription.
func (m *OS) stopThreadWatch() {
	ts := &m.Inbox.threads
	if ts.stop != nil {
		ts.stop()
	}
	ts.stop, ts.events, ts.watchKey = nil, nil, ""
}

// applyThreadGrew reads what the followed transcript added, and listens for
// more.
func (m *OS) applyThreadGrew(msg threadGrewMsg) tea.Cmd {
	ts := &m.Inbox.threads
	if ts.watchKey != msg.key || ts.events == nil {
		return nil
	}
	listen := listenThread(ts.events, msg.key)
	t := ts.byKey[msg.key]
	if t == nil || t.loading {
		return listen
	}
	t.loading = true
	return tea.Batch(listen, m.threadPageCmd(t, t.cursor))
}

// applyThreadWatchEnded forgets a subscription that ended, so the next fetch
// starts it again.
func (m *OS) applyThreadWatchEnded(msg threadWatchEnded) {
	if ts := &m.Inbox.threads; ts.watchKey == msg.key {
		ts.stop, ts.events, ts.watchKey = nil, nil, ""
	}
}

// threadApproval is the held approval the Inbox has for an agent, if any.
func (m *OS) threadApproval(e sidebarAgentEntry) (session.AttentionItem, bool) {
	if e.Host != "" {
		return session.AttentionItem{}, false
	}
	for _, it := range m.Inbox.Items {
		if it.Window == e.WindowID && it.Session == e.SessionID && it.Host == "" && it.RequestID != "" &&
			(it.Kind == session.AttentionApproval || it.Kind == session.AttentionPlan) {
			return it, true
		}
	}
	return session.AttentionItem{}, false
}

// InboxAgentsNumber answers the selected agent's held approval with a digit,
// as the attention list does: the item becomes the Inbox's selection, so the
// same settle time, risky second press and plan rules apply.
func (m *OS) InboxAgentsNumber(n int) tea.Cmd {
	e, ok := m.inboxAgentSelected()
	if !ok {
		return nil
	}
	it, ok := m.threadApproval(e)
	if !ok {
		return nil
	}
	st := &m.Inbox
	for i, r := range m.inboxRows() {
		if r.item != nil && r.item.ID == it.ID {
			st.Selected, st.SelectedID = i, it.ID
			return m.InboxNumber(n)
		}
	}
	return nil
}

// threadLines draws the thread for an agent in width cells and rows lines,
// the newest at the bottom. edRow is the line the reply editor starts on, -1
// when it is not open.
func (m *OS) threadLines(e sidebarAgentEntry, width, rows int, now time.Time) (lines []string, edRow int) {
	pal := theme.UI()
	bg := pal.Surface
	st := overlay.Style(bg)
	dim := overlay.Readable(pal.FgDim, bg)
	fg := overlay.Readable(pal.Fg, bg)
	name := printableTitle(e.Title)
	if name == "" {
		name = shortWindowLabel(e.WindowID)
	}
	head := st.Foreground(fg).Bold(true).Render(name)
	facts := []string{}
	if e.Harness != "" {
		facts = append(facts, e.Harness)
	}
	facts = append(facts, agentSessionWords(e), agentRowStateWords(e))
	if e.StateAt > 0 {
		facts[len(facts)-1] += " " + inboxWait(e.StateAt, now)
	}
	head += st.Foreground(dim).Render(" · " + strings.Join(facts, " · "))
	top := []string{overlay.Fill(ansi.Truncate(head, width, "…"), width, bg)}

	// The bottom: the approval it waits on, then the reply editor.
	var bottom []string
	if it, ok := m.threadApproval(e); ok {
		m.noteInboxShown(it, inboxShowsWhole(it), now)
		bottom = append(bottom, overlay.Fill("", width, bg))
		for _, l := range inboxApprovalDetail(it, width) {
			bottom = append(bottom, overlay.Fill(st.Foreground(fg).Render(ansi.Truncate(l, width, "…")), width, bg))
		}
		keys := "1 allow · 2 always · 3 deny"
		bottom = append(bottom, overlay.Fill(st.Foreground(overlay.Readable(pal.Warning, bg)).Render("  "+keys), width, bg))
	}
	edRow = -1
	if ed := m.Inbox.reply.editor; ed != nil && ed.window == e.WindowID && ed.session == e.SessionID {
		edRow = len(bottom)
		bottom = append(bottom, ed.lines(width)...)
	}

	room := rows - len(top) - len(bottom)
	body := m.threadBody(e, width, now)
	if len(body) > room {
		body = body[len(body)-max(room, 0):]
	}
	out := make([]string, 0, rows)
	out = append(out, top...)
	out = append(out, body...)
	for len(out) < rows-len(bottom) {
		out = append(out, overlay.Fill("", width, bg))
	}
	if edRow >= 0 {
		edRow += len(out)
	}
	out = append(out, bottom...)
	return out, edRow
}

// threadBody is the thread's text, oldest first: the conversation for a
// transcript agent, the recap and the mail for the rest.
func (m *OS) threadBody(e sidebarAgentEntry, width int, now time.Time) []string {
	pal := theme.UI()
	bg := pal.Surface
	st := overlay.Style(bg)
	t := m.Inbox.threads.byKey[agentKey(e)]
	var out []string
	note := func(s string) {
		for _, l := range wrapPlain(s, max(width-2, 8)) {
			out = append(out, overlay.Fill(st.Foreground(overlay.Readable(pal.FgDim, bg)).Render("  "+l), width, bg))
		}
	}
	if e.Host != "" {
		note(printableTitle(e.Title) + " runs on " + printableTitle(e.Host) + ". Attach there to read its conversation.")
		return out
	}
	if t == nil || (!t.read && t.recap == nil && t.loading) {
		note("Reading...")
		return out
	}
	if threadTranscriptHarness(e.Harness) {
		if t.err != "" {
			note(t.err)
			return out
		}
		for _, en := range t.entries {
			out = append(out, threadEntryLines(en, e, width, bg, pal)...)
		}
		if len(out) == 0 {
			note("No conversation yet.")
		}
		return out
	}
	if t.recap != nil && !recapEmpty(t.recap) {
		for _, s := range recapSentences(t.recap, 0, now) {
			note(s)
		}
		if facts := m.agentFactsLine(e.SessionID, e.WindowID, e.Harness); facts != "" {
			note(facts)
		}
	} else if e.Message != "" {
		note("Says: " + printableTitle(e.Message))
	}
	out = append(out, m.threadMail(e, width, bg, pal)...)
	if len(out) == 0 {
		note("Nothing to show yet. Only Claude Code has a conversation tuios can read.")
	}
	return out
}

// threadMail is the last mail between the agent and other agents.
func (m *OS) threadMail(e sidebarAgentEntry, width int, bg color.Color, pal overlay.Palette) []string {
	var picked []int
	for i := range m.AgentMail.Messages {
		mm := &m.AgentMail.Messages[i]
		if mm.Kind == "message" && (mm.From == e.WindowID || mm.To == e.WindowID) {
			picked = append(picked, i)
		}
	}
	if len(picked) > 4 {
		picked = picked[len(picked)-4:]
	}
	var out []string
	for _, i := range picked {
		mm := &m.AgentMail.Messages[i]
		var who string
		if mm.From == e.WindowID {
			who = "mail to " + firstNonEmptyStr(mm.ToLabel, shortWindowLabel(mm.To))
		} else {
			who = "mail from " + firstNonEmptyStr(mm.FromLabel, shortWindowLabel(mm.From))
		}
		text := mm.Subject
		if text == "" {
			text, _, _ = strings.Cut(mm.Text, "\n")
		}
		line := who + ": " + printableTitle(text)
		out = append(out, threadAgentLine(ansi.Truncate(line, width-4, "…"), width, bg, pal))
	}
	return out
}

// threadEntryLines draws one transcript entry: the person's words in the
// plain ink, the agent's behind a gutter mark in the dim ink.
func threadEntryLines(en threadEntry, e sidebarAgentEntry, width int, bg color.Color, pal overlay.Palette) []string {
	st := overlay.Style(bg)
	var out []string
	switch {
	case en.Kind == "text" && en.Role == "user":
		fg := overlay.Readable(pal.Fg, bg)
		label := st.Foreground(overlay.Readable(pal.AccentBright, bg)).Bold(true).Render("you ")
		for i, l := range threadWrap(en.Text, max(width-6, 8)) {
			lead := "    "
			if i == 0 {
				lead = ""
			}
			out = append(out, overlay.Fill(st.Render("  ")+label+st.Foreground(fg).Render(lead+l), width, bg))
			label = ""
		}
	case en.Kind == "text":
		for _, l := range threadWrap(en.Text, max(width-4, 8)) {
			out = append(out, threadAgentLine(l, width, bg, pal))
		}
	case en.Kind == "tool_call":
		status := ""
		switch en.Status {
		case "error":
			status = " (failed)"
		case "running":
			status = " (running)"
		}
		out = append(out, threadAgentLine(ansi.Truncate(threadToolMark()+" "+en.Tool+" "+printableTitle(en.Target)+status, max(width-4, 8), "…"), width, bg, pal))
	case en.Kind == "plan":
		first, _, _ := strings.Cut(strings.TrimSpace(en.Plan), "\n")
		first = strings.TrimSpace(strings.TrimLeft(first, "#"))
		out = append(out, threadAgentLine(ansi.Truncate("plan: "+printableTitle(first), max(width-4, 8), "…"), width, bg, pal))
	case en.Kind == "todos":
		done := 0
		for _, td := range en.Todos {
			if td.Status == "completed" {
				done++
			}
		}
		out = append(out, threadAgentLine("todos: "+strconv.Itoa(done)+" of "+strconv.Itoa(len(en.Todos))+" done", width, bg, pal))
	}
	_ = e
	return out
}

// threadToolMark is the mark in front of a tool call.
func threadToolMark() string {
	if overlay.UseASCII() {
		return ">"
	}
	return "▸"
}

// threadAgentLine is one line of the agent's: the gutter mark in the edge
// ink, the text in the dim ink.
func threadAgentLine(text string, width int, bg color.Color, pal overlay.Palette) string {
	st := overlay.Style(bg)
	mark := "│ "
	if overlay.UseASCII() {
		mark = "| "
	}
	return overlay.Fill(st.Render("  ")+st.Foreground(overlay.Readable(pal.Edge, bg)).Render(mark)+
		st.Foreground(overlay.Readable(pal.FgDim, bg)).Render(text), width, bg)
}

// threadBodyRows is how many body rows the view takes on this screen.
func (m *OS) threadBodyRows() int {
	return min(max(m.GetRenderHeight()-10, 10), 34)
}

// renderAgentsWithThread draws the agents view with the thread: the list and
// the thread side by side on a wide screen, the thread alone when it is open
// on a narrow one.
func (m *OS) renderAgentsWithThread(list []sidebarAgentEntry, title string, hints []overlay.Hint) (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Surface
	a := &m.Inbox.agents
	ts := &m.Inbox.threads
	now := time.Now()
	rows := m.threadBodyRows()
	width := m.panelWidth(150)
	wide := m.GetRenderWidth() >= threadWideMin
	listW := 0
	if wide {
		listW = min(threadListWidth, width/2)
	}
	threadW := width - listW
	sep := ""
	if wide {
		threadW -= 3
		sep = overlay.Style(bg).Foreground(overlay.Readable(pal.Edge, bg)).Render(" │ ")
	}
	e, _ := m.inboxAgentSelected()
	replying := m.Inbox.reply.editor != nil
	key := threadPanelKey{
		list: a.listAt, sel: a.sel, scroll: a.scroll, width: m.GetRenderWidth(), height: m.GetRenderHeight(),
		minute: now.Unix() / 60, theme: theme.Generation(), thread: agentKey(e),
		inboxGen: m.Inbox.Gen, mailGen: m.AgentMail.Gen, open: ts.open, live: m.Inbox.Live,
		keys: m.KeybindRegistry, title: title,
	}
	if t := ts.byKey[key.thread]; t != nil {
		key.threadGen = t.gen
	}
	if ed := m.Inbox.reply.editor; ed != nil {
		// Drawn first, from its own cache, so its frame counter says
		// whether a key changed it; one more so a fresh editor differs
		// from none.
		ed.lines(threadW)
		key.editor, key.replied = ed.comp.Drawn()+1, true
	}
	if ts.panelAt == key && ts.panel.content != "" {
		return ts.panel.content, ts.panel.geo, ts.panel.rows
	}
	right, edRow := m.threadLines(e, threadW, rows, now)

	var left []string
	if wide {
		a.scroll = min(max(a.scroll, a.sel-rows+1), a.sel)
		a.scroll = max(min(a.scroll, len(list)-rows), 0)
		minute, gen := now.Unix()/60, theme.Generation()
		for i := range rows {
			idx := a.scroll + i
			if idx >= len(list) {
				left = append(left, overlay.Fill("", listW, bg))
				continue
			}
			rs := overlay.RowState{Cursor: idx == a.sel, Focused: !replying}
			ground := pal.Ground(rs, bg)
			left = append(left, pal.Row(m.agentsRow(list[idx], idx == a.sel, !replying, ground, pal, listW, minute, gen, now), listW, rs, bg))
		}
	}
	body := make([]string, rows)
	for i := range rows {
		var b strings.Builder
		if wide {
			b.WriteString(left[i])
			b.WriteString(sep)
		}
		b.WriteString(right[i])
		body[i] = b.String()
	}
	content, geo := overlay.Panel{Title: title, Width: width, Body: strings.Join(body, "\n"), Hints: hints}.Render(pal)
	if ed := m.Inbox.reply.editor; ed != nil && edRow >= 0 {
		x := geo.BodyX
		if wide {
			x += listW + 3
		}
		ed.at.x, ed.at.y, ed.at.ok = x+2, geo.BodyY+edRow+ed.head, true
	}
	var hits []overlayRowHit
	if wide {
		for i := range rows {
			if a.scroll+i >= len(list) {
				break
			}
			y := geo.BodyY + i
			hits = append(hits, overlayRowHit{Rect: overlay.Rect{X0: 0, Y0: y, X1: geo.BodyX + listW, Y1: y + 1}, Idx: a.scroll + i})
		}
	}
	ts.panel, ts.panelAt = agentsPanel{content: content, geo: geo, rows: hits}, key
	return content, geo, hits
}

// firstNonEmptyStr is a when it is not empty, b otherwise.
func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// inboxThreadHints are the keys of the agents view with the thread showing.
func (m *OS) inboxThreadHints() []overlay.Hint {
	var hints []overlay.Hint
	e, ok := m.inboxAgentSelected()
	if ok {
		if _, held := m.threadApproval(e); held {
			hints = append(hints, overlay.Hint{Key: "1 2 3", Label: "answer"})
		}
	}
	if m.Inbox.threads.open && m.GetRenderWidth() < threadWideMin {
		hints = append(hints, m.keyHints(config.ActionInboxGo, "go", config.ActionInboxReply, "reply")...)
		return append(hints, overlay.Hint{Key: "esc", Label: "back"})
	}
	hints = append(hints, m.keyHints(config.ActionInboxGo, "go", config.ActionInboxReply, "reply")...)
	return append(hints, m.keyHints(config.ActionInboxFilter, "show", config.ActionInboxSort, "sort",
		config.ActionInboxSelect, "search", config.ActionInboxAgents, "Inbox", config.ActionInboxClose, "close")...)
}

// InboxAgentsEnter is enter in the agents view: on a narrow screen it opens
// the selected agent's thread, and in an open thread, or beside the list on a
// wide screen, it goes to the agent's pane.
func (m *OS) InboxAgentsEnter() tea.Cmd {
	ts := &m.Inbox.threads
	if m.GetRenderWidth() < threadWideMin && !ts.open {
		if _, ok := m.inboxAgentSelected(); ok {
			ts.open = true
			return m.InboxThreadFetch()
		}
		return nil
	}
	ts.open = false
	return m.InboxAgentsGo()
}

// InboxAgentsBack is esc in the agents view: it closes a thread open on a
// narrow screen, and reports false when there is none, so esc closes the
// Inbox.
func (m *OS) InboxAgentsBack() bool {
	if ts := &m.Inbox.threads; ts.open {
		ts.open = false
		return true
	}
	return false
}

// threadWrap breaks an entry's text onto lines of at most width cells: at its
// own line breaks, then at spaces. Blank lines are kept, at most one in a
// row, and every line is made printable.
func threadWrap(text string, width int) []string {
	var out []string
	blank := false
	for line := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		line = printableTitle(line)
		if strings.TrimSpace(line) == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		out = append(out, wrapPlain(line, width)...)
	}
	return out
}
