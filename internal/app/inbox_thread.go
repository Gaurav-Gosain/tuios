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

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The agent thread: what the selected agent in the agents view said and did,
// beside the list on a wide screen and in its place on a narrow one.
//
//   - For an agent whose transcript the daemon reads (Claude Code, Codex,
//     Gemini CLI, Qwen Code, Copilot CLI, Amp), the conversation, read with
//     agent-transcript (the
//     person's own verb, with the attach nonce: secrets masked, every string
//     bounded, control characters gone). The client subscribes to the
//     transcript event of the selected pane alone, and reads again from its
//     cursor when the transcript grows. When the selection moves, the
//     subscription moves with it.
//   - For any other agent, and for one whose pane is not joined to its
//     transcript yet, the recap of its turns (agent-activity) and the mail
//     between it and other agents.
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
		Text   string `json:"text"`
		Status string `json:"status"`
	} `json:"todos"`
	At     int64       `json:"at"`
	ToolID string      `json:"tool_id"`
	Diff   *threadDiff `json:"diff"`
}

// agentThread is one agent's thread.
type agentThread struct {
	key     string
	session string
	window  string
	harness string
	entries []threadEntry
	cursor  string
	read    bool
	loading bool
	// again says the transcript grew while a read was out, so another
	// read follows it.
	again    bool
	err      string
	recapKey string
	recap    *session.AgentActivityRecap
	// fallback says the daemon has no transcript for the pane: its harness
	// has a reader, but the pane is not joined to a file yet, or the daemon
	// is older. The thread shows the recap. fallbackAt is the agent's state
	// time then, and a new state tries the transcript again.
	fallback   bool
	fallbackAt int64
	// gen changes when what the thread shows changes, for the drawn lines.
	gen uint64
	// body is the conversation's lines as last drawn, and bodyAt what they
	// were drawn from, so a frame drawn for another reason does not wrap
	// and style the conversation again.
	body   []string
	bodyAt threadBodyKey
	// hits are what a click on each line of body does, "" for nothing.
	hits []string
	// folded are the cards whose fold the person flipped, by entry id;
	// expandAll shows every line of every card (e); scroll is how many
	// lines the thread is scrolled up from its newest.
	folded    map[string]bool
	expandAll bool
	scroll    int
	// cards are the entries' cards as last built, by entry id.
	cards map[string]ckCard
}

// threadBodyKey is what a thread's drawn lines depend on.
type threadBodyKey struct {
	gen, mailGen, theme uint64
	look                uint64
	width               int
	minute              int64
	ascii               bool
	title, message      string
	expand, changes     bool
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
	// stateAt is the agent's state time when the read went out.
	stateAt int64
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
// transcript: the harnesses whose manifests have a [transcript] block with a
// reader internal/transcriptview has.
func threadTranscriptHarness(h string) bool {
	switch h {
	case "claude-code", "codex", "gemini-cli", "qwen", "copilot", "amp", "opencode", "cursor-agent":
		return true
	}
	return false
}

// threadReadsTranscript reports whether an agent's thread shows its
// transcript, and not the recap: its harness has a reader, and the daemon
// did not say the pane has no transcript.
func (m *OS) threadReadsTranscript(e sidebarAgentEntry) bool {
	if !threadTranscriptHarness(e.Harness) {
		return false
	}
	t := m.Inbox.threads.byKey[agentKey(e)]
	return t == nil || !t.fallback
}

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
	return m.ShowInbox && m.Inbox.Peek == nil && (m.cockpitWideFocus() || m.Inbox.threads.open)
}

// InboxThreadFetch reads what the selected agent's thread needs and moves the
// transcript subscription to it. It is called after every input and every
// thread delivery while the Inbox is up, and costs a few comparisons when
// there is nothing to read.
func (m *OS) InboxThreadFetch() tea.Cmd {
	ts := &m.Inbox.threads
	if m.ShowInbox {
		m.cockpitSync()
	}
	if !m.threadShown() || (m.DaemonClient == nil && m.Inbox.call == nil) {
		m.stopThreadWatch()
		return nil
	}
	e, ok := m.cockpitAgent()
	if !ok || e.Host != "" {
		m.stopThreadWatch()
		return nil
	}
	t := m.thread(e)
	var cmds []tea.Cmd
	if t.fallback && e.StateAt != t.fallbackAt && !t.loading {
		// The agent moved on since the daemon had no transcript. The hook
		// that joins the pane to its file may have run.
		t.fallback, t.read = false, false
	}
	if m.threadReadsTranscript(e) {
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
	var stateAt int64
	if e, ok := m.cockpitAgent(); ok && agentKey(e) == key {
		stateAt = e.StateAt
	}
	return func() tea.Msg {
		params := map[string]any{"session": sess, "window": window, "human_nonce": nonce, "limit": 200}
		if after != "" {
			params["after"] = after
		}
		raw, err := call("agent-transcript", params, threadReadTimeout)
		msg := threadPageMsg{key: key, after: after != "", err: err, stateAt: stateAt}
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
		if threadNoTranscript(msg.err) && !msg.after {
			// No file to read for this pane: show the recap, as for a
			// harness with no reader, until the agent's state changes.
			t.fallback, t.fallbackAt, t.err, t.recapKey = true, msg.stateAt, "", ""
			m.stopThreadWatch()
			return m.InboxThreadFetch()
		}
		return nil
	}
	t.read, t.err = true, ""
	if !msg.after || msg.reset {
		t.entries = nil
	}
	// An entry with an id the thread holds replaces it where it is: a tool
	// call's status changes once its result is in, and Gemini CLI writes a
	// message again, whole, when one of its calls finishes.
	at := make(map[string]int, len(t.entries))
	for i, e := range t.entries {
		at[e.ID] = i
	}
	for _, e := range msg.entries {
		if i, ok := at[e.ID]; ok {
			t.entries[i] = e
			continue
		}
		at[e.ID] = len(t.entries)
		t.entries = append(t.entries, e)
	}
	// The thread draws the newest; the page bound holds the rest.
	if n := len(t.entries); n > 200 {
		t.entries = append(t.entries[:0], t.entries[n-200:]...)
		// The cards of the entries cut go with them.
		if len(t.cards) > len(t.entries) {
			keep := make(map[string]bool, len(t.entries))
			for _, en := range t.entries {
				keep[en.ID] = true
			}
			for id := range t.cards {
				if !keep[id] {
					delete(t.cards, id)
				}
			}
		}
	}
	if msg.cursor != "" {
		t.cursor = msg.cursor
	}
	if msg.more || t.again {
		t.again = false
		t.loading = true
		return m.threadPageCmd(t, t.cursor)
	}
	return nil
}

// threadNoTranscript reports an error that says the pane has no transcript
// the daemon can read, so the thread shows the recap instead.
func threadNoTranscript(err error) bool {
	ce, ok := err.(*session.VerbCallError)
	return ok && (ce.Code == session.ErrVerbNoTranscript || ce.Code == session.ErrVerbUnsupportedHarness)
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
	if t == nil {
		return listen
	}
	if t.loading {
		t.again = true
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

// threadBody is the thread's text, oldest first: the conversation for a
// transcript agent, the recap and the mail for the rest. It holds at least
// the last room lines, and is kept on the thread until what it shows changes.
func (m *OS) threadBody(e sidebarAgentEntry, width, room int, now time.Time) []string {
	t := m.Inbox.threads.byKey[agentKey(e)]
	if t == nil {
		return m.threadBodyLines(e, nil, width, room, now)
	}
	key := threadBodyKey{
		gen: t.gen, mailGen: m.AgentMail.Gen, theme: theme.Generation(), look: m.cockpitLookGen(),
		width: width, minute: m.threadMinute(e, now), ascii: overlay.UseASCII(),
		title: e.Title + "\x00" + e.Host, message: e.Message,
		expand: t.expandAll, changes: m.cockpitTab() == "Changes",
	}
	if t.body != nil && t.bodyAt == key {
		return t.body
	}
	t.hits = nil
	t.body, t.bodyAt = m.threadBodyLines(e, t, width, room, now), key
	return t.body
}

// threadMinute is the minute a thread's lines depend on: a recap says how
// long ago a turn ended, and a transcript's cards carry clock times only, so
// a minute passing builds them again for nothing.
func (m *OS) threadMinute(e sidebarAgentEntry, now time.Time) int64 {
	if m.threadReadsTranscript(e) {
		return 0
	}
	return now.Unix() / 60
}

// threadBodyLines draws threadBody. A conversation is drawn from its newest
// entry back, until room lines are drawn: the thread shows the bottom, and
// two hundred entries wrapped and styled on every change cost milliseconds.
func (m *OS) threadBodyLines(e sidebarAgentEntry, t *agentThread, width, room int, now time.Time) []string {
	pal := theme.UI()
	bg := pal.Canvas
	st := overlay.Style(bg)
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
	if m.threadReadsTranscript(e) {
		if t.err != "" {
			note(t.err)
			return out
		}
		out, t.hits = m.threadCardLines(e, t, width, m.cockpitTab() == "Changes")
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
		note(threadNothingYet(e.Harness))
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

// firstNonEmptyStr is a when it is not empty, b otherwise.
func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
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

// InboxAgentsViewPane closes the Inbox, goes to the selected agent's pane and
// zooms it, so the person can read the agent's own screen. The dock says how
// to come back. The Inbox opens again on the agents view with the same agent
// selected, at the same scroll, and every draft is kept.
func (m *OS) InboxAgentsViewPane() tea.Cmd {
	e, ok := m.cockpitAgent()
	if !ok {
		return nil
	}
	if e.Host != "" {
		m.ShowNotification(printableTitle(e.Title)+" runs on "+printableTitle(e.Host)+". Attach there to see its pane", "info", m.Settings.NotificationDuration)
		return nil
	}
	ck := &m.Inbox.ck
	back := ckBack{key: ck.key, akey: ck.akey, scroll: ck.scroll}
	m.CloseInbox()
	// The view opens again on this agent, as far down the fleet as it was.
	ck.back = &back
	m.inboxJump(session.AttentionItem{Session: e.SessionID, Window: e.WindowID})
	if w := m.GetFocusedWindow(); w != nil && w.ID == e.WindowID && !w.Zoomed {
		m.ToggleZoom()
	}
	key := m.sidebarAgentKeyOr("prefix_inbox", "ctrl+b i")
	m.ShowNotification("Press "+key+" to go back to the agents.", "info", m.Settings.NotificationDuration*2)
	return nil
}

// threadNothingYet is what an empty recap thread says.
func threadNothingYet(harness string) string {
	if threadTranscriptHarness(harness) {
		return "Nothing to show yet. The conversation shows when tuios finds the agent's transcript. Install the hooks to find it at once: tuios integration install " + harness
	}
	return "Nothing to show yet. tuios cannot read the conversation of this agent."
}
