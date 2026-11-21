package app

import (
	"context"
	"errors"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

const threadsKept = 3

const threadReadTimeout = 5 * time.Second

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
	At        int64                      `json:"at"`
	ToolID    string                     `json:"tool_id"`
	Diff      *threadDiff                `json:"diff"`
	Image     *transcriptview.Image      `json:"image"`
	Calls     []transcriptview.AgentCall `json:"calls"`
	Truncated bool                       `json:"truncated"`
	sum       uint64
}

func threadSum(e *threadEntry) uint64 {
	h := uint64(14695981039346656037)
	for _, s := range [2]string{e.Text, e.Plan} {
		for i := 0; i < len(s); i++ {
			h = (h ^ uint64(s[i])) * 1099511628211
		}
		h = (h ^ 0xff) * 1099511628211
	}
	return h
}

type agentThread struct {
	root                    string
	key                     string
	session                 string
	window                  string
	harness                 string
	entries                 []threadEntry
	cursor                  string
	read                    bool
	loading                 bool
	again                   bool
	err                     string
	recapKey                string
	recap                   *session.AgentActivityRecap
	fallback                bool
	fallbackAt              int64
	gen                     uint64
	echoAsked               bool
	body                    []string
	bodyAt                  threadBodyKey
	hits                    []string
	folded                  map[string]bool
	expandAll               bool
	follow                  transcriptview.Follow
	taken                   int
	prompts                 []int
	fromTop                 bool
	cards                   map[string]ckCard
	codeX, codeMax, codeGen map[string]int
	code                    map[string]ckCode
	act                     string
	actAt                   time.Time
	actTimed, actSeen       bool
	actRow                  bool
	pill                    string
	images                  map[string]*ckThreadImage
	imgWidth                int
	pinned                  bool
	lines                   []ckLine
	lineIDs                 []string
	viewW, viewH            int
	viewX, viewY            int
	keep                    int
	older                   bool
	toTop                   bool
}

type threadBodyKey struct {
	gen, mailGen, theme uint64
	look                uint64
	width               int
	minute              int64
	ascii               bool
	title, message      string
	expand, changes     bool
	working             bool
	kids                uint64
	pics                uint64
	keep                int
	card                bool
	echo                uint64
}

type threadsState struct {
	byKey    map[string]*agentThread
	order    []string
	watchKey string
	stop     context.CancelFunc
	events   chan tea.Msg
	open     bool
	copy     *ckThreadCopy
}

type threadPageMsg struct {
	key     string
	entries []threadEntry
	cursor  string
	reset   bool
	more    bool
	after   bool
	err     error
	stateAt int64
}

type threadRecapMsg struct {
	key, recapKey string
	recap         *session.AgentActivityRecap
	err           error
}

type threadGrewMsg struct{ key string }

type threadWatchEnded struct{ key string }

func threadTranscriptHarness(h string) bool {
	switch h {
	case "claude-code", "codex", "gemini-cli", "qwen", "copilot", "amp", "opencode", "cursor-agent", "pi":
		return true
	}
	return false
}

func (m *OS) threadReadsTranscript(e sidebarAgentEntry) bool {
	if !threadTranscriptHarness(e.Harness) {
		return false
	}
	t := m.Inbox.threads.byKey[agentKey(e)]
	return t == nil || !t.fallback
}

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
	if m.threadShown() {
		if e, ok := m.cockpitAgent(); ok {
			m.markAgentViewed(e)
		}
	}
	if !m.threadShown() || (m.DaemonClient == nil && m.Inbox.call == nil) {
		m.stopThreadWatch()
		return nil
	}
	e, ok := m.cockpitAgent()
	if !ok {
		m.stopThreadWatch()
		return nil
	}
	t := m.thread(e)
	var cmds []tea.Cmd
	if e.Host != "" {
		m.stopThreadWatch()
		if !t.echoAsked {
			t.echoAsked = true
			return m.echoQueueCmd(e.Host, e.SessionID, e.WindowID, printableTitle(e.Title), true)
		}
		return nil
	}
	if !t.echoAsked {
		t.echoAsked = true
		cmds = append(cmds, m.echoQueueCmd(e.Host, e.SessionID, e.WindowID, printableTitle(e.Title), true))
	}
	if t.fallback && e.StateAt != t.fallbackAt && !t.loading {
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
		if cmd := m.threadImagesCmd(t); cmd != nil {
			cmds = append(cmds, cmd)
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

func (m *OS) threadRetryOnState() tea.Cmd {
	if !m.threadShown() {
		return nil
	}
	m.cockpitSync()
	e, ok := m.cockpitAgent()
	if !ok {
		return nil
	}
	t := m.Inbox.threads.byKey[agentKey(e)]
	if t == nil || !t.fallback || t.loading || e.StateAt == t.fallbackAt {
		return nil
	}
	return m.InboxThreadFetch()
}

func (m *OS) agentCaller() inboxVerbCall { return m.inboxActCaller() }

func (m *OS) threadPageCmd(t *agentThread, after string) tea.Cmd {
	call, nonce := m.agentCaller(), m.inboxNonce()
	key, sess, window := t.key, t.session, t.window
	var stateAt int64
	if e, ok := m.cockpitAgent(); ok && agentKey(e) == key {
		stateAt = e.StateAt
	}
	return func() tea.Msg {
		params := map[string]any{"session": sess, "window": window, "human_nonce": nonce, "limit": threadReadLimit}
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
		if msg.err = jsonx.Unmarshal(raw, &res); msg.err == nil {
			msg.entries, msg.cursor, msg.reset, msg.more = res.Entries, res.Cursor, res.Reset, res.More
		}
		return msg
	}
}

func (m *OS) threadRecapCmd(t *agentThread, recapKey string) tea.Cmd {
	call, key, sess, window := m.agentCaller(), t.key, t.session, t.window
	return func() tea.Msg {
		rc, err := agentActivityRecapCall(call, sess, window, 0)
		return threadRecapMsg{key: key, recapKey: recapKey, recap: rc, err: err}
	}
}

func (m *OS) applyThreadPage(msg threadPageMsg) tea.Cmd {
	t := m.Inbox.threads.byKey[msg.key]
	if t == nil {
		return nil
	}
	t.loading = false
	t.gen++
	if msg.err != nil {
		t.read = true
		t.err = threadReadError(msg.err, m.AttachedHost)
		if threadNoTranscript(msg.err) && !msg.after {
			t.fallback, t.fallbackAt, t.err, t.recapKey = true, msg.stateAt, "", ""
			m.stopThreadWatch()
			return m.InboxThreadFetch()
		}
		return nil
	}
	t.read, t.err = true, ""
	if !msg.after || msg.reset {
		t.entries, t.cards = nil, nil
	}
	at := make(map[string]int, len(t.entries))
	for i, e := range t.entries {
		at[e.ID] = i
	}
	for _, e := range msg.entries {
		e.sum = threadSum(&e)
		if i, ok := at[e.ID]; ok {
			t.entries[i] = e
			continue
		}
		at[e.ID] = len(t.entries)
		t.entries = append(t.entries, e)
		if msg.after && !msg.reset && e.Kind != "tool_result" {
			t.taken++
		}
	}
	results := make(map[string]string)
	for i := len(t.entries) - 1; i >= 0; i-- {
		e := &t.entries[i]
		if e.ToolID == "" {
			continue
		}
		switch e.Kind {
		case "tool_result":
			results[e.ToolID] = e.Status
		case "tool_call":
			if status, ok := results[e.ToolID]; ok {
				e.Status = status
				if i == 0 || t.entries[i-1].Kind != "tool_call" || t.entries[i-1].ToolID != e.ToolID {
					delete(results, e.ToolID)
				}
			}
		}
	}
	if n := len(t.entries); n > 200 {
		t.entries = append(t.entries[:0], t.entries[n-200:]...)
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
	m.echoTake(t)
	if msg.more || t.again {
		t.again = false
		t.loading = true
		return m.threadPageCmd(t, t.cursor)
	}
	if e, ok := m.cockpitAgent(); ok && agentKey(e) == t.key && m.threadShown() {
		return m.threadImagesCmd(t)
	}
	return nil
}

func threadNoTranscript(err error) bool {
	code := session.VerbErrCode(err)
	return code == session.ErrVerbNoTranscript || code == session.ErrVerbUnsupportedHarness
}

func threadReadError(err error, host string) string {
	daemon := "The daemon"
	if host != "" {
		daemon = "tuios on " + printableTitle(host)
	}
	if ce, ok := errors.AsType[*session.VerbCallError](err); ok {
		switch ce.Code {
		case session.ErrVerbNoTranscript:
			return "This agent has no transcript to read yet."
		case session.ErrVerbUnknownVerb:
			return daemon + " cannot read transcripts. " + restartHintPlain
		case session.ErrVerbNotHuman:
			return daemon + " did not accept this client as the person, so it did not send the conversation. Attach again from a terminal outside tuios."
		case session.ErrVerbForbidden:
			if h := ce.Hint; host != "" && h != nil && len(h.Missing) > 0 && h.Table != "" {
				quoted := make([]string, len(h.Missing))
				for i, c := range h.Missing {
					quoted[i] = `"` + c + `"`
				}
				return daemon + " does not let this machine read agent conversations. To allow it, add " +
					strings.Join(quoted, ", ") + " to allow in " + h.Table + " in the config on " + printableTitle(host) + "."
			}
		}
	}
	return "The transcript could not be read: " + err.Error()
}

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

func (m *OS) startThreadWatch(t *agentThread) tea.Cmd {
	ts := &m.Inbox.threads
	if m.Inbox.call != nil || m.DaemonClient == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan tea.Msg, 4)
	ts.watchKey, ts.stop, ts.events = t.key, cancel, ch
	build := m.DaemonClient.ClientVersion()
	dial := func() (*session.VerbClient, error) { return session.DialVerbClientAs(build) }
	if m.AttachedHost != "" {
		dial = m.verbDialer()
	}
	key, sess, window := t.key, t.session, t.window
	go func() {
		defer close(ch)
		client, err := dial()
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
		ch <- threadGrewMsg{key: key}
		for ctx.Err() == nil {
			if _, err := client.ReadEventLine(0); err != nil {
				return
			}
			select {
			case ch <- threadGrewMsg{key: key}:
			default:
			}
		}
	}()
	return listenThread(m.stopSignal(), ch, key)
}

func listenThread(stop <-chan struct{}, ch <-chan tea.Msg, key string) tea.Cmd {
	return func() tea.Msg {
		select {
		case msg, ok := <-ch:
			if !ok {
				return threadWatchEnded{key: key}
			}
			return msg
		case <-stop:
			return nil
		}
	}
}

func (m *OS) stopThreadWatch() {
	ts := &m.Inbox.threads
	if ts.stop != nil {
		ts.stop()
	}
	ts.stop, ts.events, ts.watchKey = nil, nil, ""
}

func (m *OS) applyThreadGrew(msg threadGrewMsg) tea.Cmd {
	ts := &m.Inbox.threads
	if ts.watchKey != msg.key || ts.events == nil {
		return nil
	}
	listen := listenThread(m.stopSignal(), ts.events, msg.key)
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

func (m *OS) applyThreadWatchEnded(msg threadWatchEnded) {
	if ts := &m.Inbox.threads; ts.watchKey == msg.key {
		ts.stop, ts.events, ts.watchKey = nil, nil, ""
	}
}

func (m *OS) threadBody(e sidebarAgentEntry, width, room int, now time.Time, card bool) []string {
	t := m.Inbox.threads.byKey[agentKey(e)]
	if t == nil {
		return m.threadBodyLines(e, nil, width, room, now, card)
	}
	key := threadBodyKey{
		gen: t.gen, mailGen: m.AgentMail.Gen, theme: theme.Generation(), look: m.cockpitLookGen(),
		width: width, minute: m.threadMinute(e, now), ascii: overlay.UseASCII(),
		title: e.Title + "\x00" + e.Host, message: e.Message,
		expand: t.expandAll, changes: m.cockpitTab() == "Changes",
		working: e.State == string(session.AgentStateWorking),
		kids:    m.ckKidsSig(e, t),
		pics:    m.threadPicsVersion(t),
		card:    card,
		echo:    m.Inbox.echoes.gen,
	}
	if t.keep == 0 && m.threadReadsTranscript(e) {
		t.keep = ckKeepTurns
	}
	for {
		key.keep = t.keep
		if t.body != nil && t.bodyAt == key {
			if !t.older || len(t.body) >= room+ckKeepMargin {
				t.toTop = false
				return t.body
			}
			t.keep *= 2
			continue
		}
		first, kept := "", t.bodyAt.keep
		if len(t.lineIDs) > 0 {
			first = t.lineIDs[0]
		}
		t.hits, t.prompts, t.lines, t.lineIDs = nil, nil, nil, nil
		t.body, t.bodyAt = m.threadBodyLines(e, t, width, room, now, key.card), key
		if t.body == nil {
			t.body = []string{}
		}
		if at := slices.Index(t.lineIDs, first); kept != 0 && kept != key.keep && at > 0 {
			t.follow.Older(at)
			if t.toTop {
				t.follow.Scroll(1 << 30)
			}
		}
	}
}

const threadReadLimit = 200

const (
	ckKeepTurns  = 8
	ckKeepMargin = 60
)

func threadKeepAll(t *agentThread) {
	if t.older {
		t.keep, t.toTop = 1<<30, true
	}
}

func (m *OS) threadMinute(e sidebarAgentEntry, now time.Time) int64 {
	if m.threadReadsTranscript(e) {
		return ckThreadMinute(e.State == string(session.AgentStateWorking), now)
	}
	return now.Unix() / 60
}

func (m *OS) threadBodyLines(e sidebarAgentEntry, t *agentThread, width, room int, now time.Time, card bool) []string {
	pal := m.cockpitPalette()
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
		if t != nil {
			echo, hits := m.threadEchoLines(t.key, width, e.StateAt)
			t.hits = append(make([]string, len(out)), hits...)
			out = append(out, echo...)
		}
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
		out, t.hits = m.threadCardLines(e, t, width, m.cockpitTab() == "Changes", now)
		if m.cockpitTab() != "Changes" {
			echo, hits := m.threadEchoLines(t.key, width, e.StateAt)
			if n := len(t.lines); len(echo) > 0 && n > 0 && !t.lines[n-1].blank() {
				c := (*ckCanvas)(nil).reset(width, 1, m.cockpitLook().canvas)
				echo, hits = append([]string{c.render()}, echo...), append([]string{""}, hits...)
			}
			out, t.hits = append(out, echo...), append(t.hits, hits...)
			for range echo {
				t.lines, t.lineIDs = append(t.lines, ckLine{}), append(t.lineIDs, "")
			}
		}
		if len(out) == 0 {
			note("No conversation yet.")
		}
		return out
	}
	start := len(out)
	switch {
	case card:
	case t.recap != nil && !recapEmpty(t.recap):
		for _, s := range recapSentences(t.recap, 0, now, t.recap.Folder) {
			note(s)
		}
		if facts := m.agentFactsLine(e.SessionID, e.WindowID, e.Harness); facts != "" {
			note(facts)
		}
	case e.Message != "":
		note("Says: " + printableTitle(e.Message))
	}
	echo, hits := m.threadEchoLines(t.key, width, e.StateAt)
	if m.echoesBefore(t.key, e.StateAt) {
		says := append([]string(nil), out[start:]...)
		out = append(append(out[:start], echo...), says...)
		t.hits = append(append(make([]string, start), hits...), make([]string, len(says))...)
		out = append(out, m.threadMail(e, width, bg, pal)...)
		for len(t.hits) < len(out) {
			t.hits = append(t.hits, "")
		}
	} else {
		out = append(out, m.threadMail(e, width, bg, pal)...)
		t.hits = append(make([]string, len(out)), hits...)
		out = append(out, echo...)
	}
	if len(out) == 0 && !card {
		note(threadNothingYet(e.Harness))
	}
	return out
}

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

func threadAgentLine(text string, width int, bg color.Color, pal overlay.Palette) string {
	st := overlay.Style(bg)
	mark := "│ "
	if overlay.UseASCII() {
		mark = "| "
	}
	return overlay.Fill(st.Render("  ")+st.Foreground(overlay.Readable(pal.Edge, bg)).Render(mark)+
		st.Foreground(overlay.Readable(pal.FgDim, bg)).Render(text), width, bg)
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func threadWrap(text string, width int) []string {
	out, _ := threadWrapMore(text, width)
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
	ck.back = &back
	m.inboxJump(session.AttentionItem{Session: e.SessionID, Window: e.WindowID})
	if w := m.GetFocusedWindow(); w != nil && w.ID == e.WindowID && !w.Zoomed {
		m.ToggleZoom()
	}
	key := m.sidebarAgentKeyOr("prefix_inbox", "ctrl+b i")
	m.ShowNotification("Press "+key+" to go back to the agents.", "info", m.Settings.NotificationDuration*2)
	return nil
}

func threadNothingYet(harness string) string {
	if threadTranscriptHarness(harness) {
		return "Nothing to show yet. The conversation shows when tuios finds the agent's transcript. Install the hooks to find it at once: tuios integration install " + harness
	}
	return "Nothing to show yet. tuios cannot read the conversation of this agent."
}
