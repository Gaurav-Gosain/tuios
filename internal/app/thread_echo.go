package app

import (
	"cmp"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The local echo of a sent reply.
//
// A reply goes to the daemon's queue and is typed when the agent comes to
// rest, and the thread shows it only once the agent's transcript has it. So
// a reply shows at the thread's end, as the person's message, from the moment
// it is sent, with a state line under it:
//
//	Sending                       on its way to the daemon, or being typed
//	Queued, typed when it rests   in the queue; a click on cancel drops it
//	Not sent: <why>               refused; a click on retry sends it again
//
// It goes when the transcript entry for it arrives: a prompt of the person's
// with the same text, that the thread did not hold when the reply was sent,
// so an earlier prompt with the same words never takes it. A reply typed into
// a pane whose transcript never shows it goes after echoTypedFor.
//
// The queue is the daemon's, so a queued reply shows again when the view
// opens again, or in a new client: the thread asks list-queued for the pane
// and makes an echo for each message the person queued that it does not show,
// from the daemon's preview of it. While a reply waits in the queue, the
// thread asks again every echoPoll, to see it leave.

// echoState is where a reply is.
type echoState uint8

const (
	// echoSending is a reply on its way to the daemon, or being typed.
	echoSending echoState = iota
	// echoQueued waits in the pane's queue for the agent's rest.
	echoQueued
	// echoTyped has left the queue, typed, and waits for the transcript.
	echoTyped
	// echoFailed was refused.
	echoFailed
)

// echoTypedFor is how long a typed reply shows without its transcript entry:
// a harness that writes no transcript, or one that writes it late.
const echoTypedFor = 2 * time.Minute

// echoPoll is how often the thread asks the daemon about a queued reply.
const echoPoll = 2 * time.Second

// echoMax bounds the echoes the client keeps.
const echoMax = 64

// replyEcho is one sent reply the thread shows before the transcript does.
type replyEcho struct {
	// n numbers the echo, for the hits of its cancel and retry.
	n uint64
	// key is the thread's: host, session and window, as agentKey.
	key                        string
	host, session, window, who string
	// id is the queue entry's, once the daemon queued it.
	id string
	// text is the reply. preview says it is the daemon's one-line preview of
	// it, for an echo made from list-queued, and matches the start of the
	// transcript entry.
	text    string
	preview bool
	parts   []composer.Part
	at      time.Time
	state   echoState
	why     string
	// send is the send whose answer sets the state, 0 once it came.
	send uint64
	// seen is the ids of the person's prompts the thread held when the reply
	// was sent: none of them is its entry.
	seen map[string]bool
	// typedAt is when it left the queue.
	typedAt time.Time
}

// threadEchoes is the client's echoes and the generation of what they show.
type threadEchoes struct {
	list []*replyEcho
	seq  uint64
	gen  uint64
	// polling says an echoPoll tick is out.
	polling bool
}

// echoTickMsg asks the daemon about the queued replies again.
type echoTickMsg struct{}

// echoQueueMsg is a pane's queue, as list-queued gave it.
type echoQueueMsg struct {
	key                        string
	host, session, window, who string
	entries                    []echoQueueEntry
	err                        error
	// restore makes echoes of the person's messages the client does not
	// know: the thread was opened.
	restore bool
}

// echoQueueEntry is one message in a pane's queue.
type echoQueueEntry struct {
	ID      string `json:"id"`
	By      string `json:"by"`
	From    string `json:"from"`
	Preview string `json:"preview"`
	State   string `json:"state"`
	At      int64  `json:"at"`
}

// echoCancelledMsg is the answer to a click on cancel.
type echoCancelledMsg struct {
	n   uint64
	err error
}

// addEcho shows a reply just sent at the end of its thread.
func (m *OS) addEcho(host, sessionID, windowID, who, text string, parts []composer.Part, send uint64) {
	ec := &m.Inbox.echoes
	ec.seq++
	key := host + "\x00" + sessionID + "\x00" + windowID
	e := &replyEcho{n: ec.seq, key: key, host: host, session: sessionID, window: windowID, who: who,
		text: text, parts: parts, at: time.Now(), state: echoSending, send: send, seen: map[string]bool{}}
	if t := m.Inbox.threads.byKey[key]; t != nil {
		for _, en := range t.entries {
			if ckPrompt(en) {
				e.seen[en.ID] = true
			}
		}
	}
	ec.list = append(ec.list, e)
	if len(ec.list) > echoMax {
		ec.list = ec.list[len(ec.list)-echoMax:]
	}
	m.echoChanged(key)
}

// echoChanged redraws the thread an echo is in.
func (m *OS) echoChanged(key string) {
	m.Inbox.echoes.gen++
	if t := m.Inbox.threads.byKey[key]; t != nil {
		t.gen++
	}
	m.cockpitTouch()
}

// echoReplied takes the daemon's answer to a reply's queue-prompt.
func (m *OS) echoReplied(msg InboxRepliedMsg) tea.Cmd {
	if msg.Send == 0 {
		return nil
	}
	for _, e := range m.Inbox.echoes.list {
		if e.send != msg.Send || e.session != msg.Session || e.window != msg.Window {
			continue
		}
		e.send = 0
		switch {
		case msg.Err != nil:
			e.state, e.why = echoFailed, echoRefusal(msg.Err)
		case msg.Now:
			// Typed at once: it waits for the transcript only.
			e.state, e.typedAt = echoTyped, time.Now()
		case msg.Delivering:
			e.id, e.state = msg.ID, echoSending
		default:
			e.id, e.state = msg.ID, echoQueued
		}
		m.echoChanged(e.key)
		return m.echoPollCmd()
	}
	return nil
}

// echoRefusal is why the daemon refused a reply, in the state line's words.
func echoRefusal(err error) string {
	callErr, ok := errors.AsType[*session.VerbCallError](err)
	if !ok {
		return echoWhy(err.Error())
	}
	switch callErr.Code {
	case session.ErrVerbQueueFull:
		if strings.Contains(callErr.Message, "bytes") {
			return "the queue is full"
		}
		return "the queue already holds as many messages as [agents.queue] max allows"
	case session.ErrVerbUnknownVerb:
		return "this daemon cannot queue a reply"
	case session.ErrVerbInvalidParams:
		if strings.Contains(callErr.Message, "cannot take images") {
			return "this agent cannot take images"
		}
	}
	return echoWhy(callErr.Message)
}

// echoWhy is a refusal as the state line says it: its first sentence, short.
func echoWhy(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(s, ".")
	if utf8.RuneCountInString(s) > 120 {
		s = string([]rune(s)[:119]) + "…"
	}
	return s
}

// echoDropped forgets the echo of a queued message that was dropped.
func (m *OS) echoDropped(sessionID, windowID, id string) {
	if id == "" {
		return
	}
	ec := &m.Inbox.echoes
	for i, e := range ec.list {
		if e.id == id && e.session == sessionID && e.window == windowID {
			ec.list = append(ec.list[:i], ec.list[i+1:]...)
			m.echoChanged(e.key)
			return
		}
	}
}

// echoTake drops the echoes of a thread whose transcript entry has arrived:
// a prompt of the person's the thread did not hold at the send, at or after
// it, with the reply's text. Each entry takes one echo, the oldest.
func (m *OS) echoTake(t *agentThread) {
	ec := &m.Inbox.echoes
	if t == nil || len(ec.list) == 0 {
		return
	}
	taken := map[string]bool{}
	kept := ec.list[:0]
	changed := false
	for _, e := range ec.list {
		if e.key != t.key || e.state == echoFailed || !m.echoFound(t, e, taken) {
			kept = append(kept, e)
			continue
		}
		changed = true
	}
	ec.list = kept
	if changed {
		m.echoChanged(t.key)
	}
}

// echoFound reports whether t holds the transcript entry of e, and marks the
// entry taken.
func (m *OS) echoFound(t *agentThread, e *replyEcho, taken map[string]bool) bool {
	want := echoNorm(e.text)
	for _, en := range t.entries {
		if !ckPrompt(en) || e.seen[en.ID] || taken[en.ID] {
			continue
		}
		// A transcript's clock and this one can differ a little.
		if en.At > 0 && time.UnixMilli(en.At).Before(e.at.Add(-5*time.Second)) && !e.preview {
			continue
		}
		got := echoNorm(en.Text)
		if got == want || e.preview && strings.HasPrefix(got, strings.TrimSuffix(want, "…")) || want != "" && strings.HasPrefix(got, want) {
			taken[en.ID] = true
			return true
		}
	}
	return false
}

// echoNorm is text as a match reads it: its words, one space apart.
func echoNorm(s string) string { return strings.Join(strings.Fields(s), " ") }

// echoPollCmd asks the daemon, after echoPoll, about the panes with a reply
// in their queue. It is nil while one is out or none waits.
func (m *OS) echoPollCmd() tea.Cmd {
	ec := &m.Inbox.echoes
	if ec.polling || !m.echoWaiting() {
		return nil
	}
	ec.polling = true
	return tea.Tick(echoPoll, func(time.Time) tea.Msg { return echoTickMsg{} })
}

// echoWaiting reports whether an echo waits on the daemon's queue or on its
// transcript entry.
func (m *OS) echoWaiting() bool {
	for _, e := range m.Inbox.echoes.list {
		if e.state == echoQueued || e.state == echoTyped || e.state == echoSending && e.id != "" {
			return true
		}
	}
	return false
}

// applyEchoTick asks list-queued for each pane with a queued reply, and lets
// a typed reply go once echoTypedFor has passed.
func (m *OS) applyEchoTick() tea.Cmd {
	ec := &m.Inbox.echoes
	ec.polling = false
	now := time.Now()
	kept := ec.list[:0]
	asked := map[string]bool{}
	var cmds []tea.Cmd
	for _, e := range ec.list {
		if e.state == echoTyped && now.Sub(e.typedAt) > echoTypedFor {
			m.echoChanged(e.key)
			continue
		}
		kept = append(kept, e)
		if (e.state == echoQueued || e.state == echoSending && e.id != "") && !asked[e.key] {
			asked[e.key] = true
			cmds = append(cmds, m.echoQueueCmd(e.host, e.session, e.window, e.who, false))
		}
	}
	ec.list = kept
	cmds = append(cmds, m.echoPollCmd())
	return tea.Batch(cmds...)
}

// echoQueueCmd reads a pane's queue. restore makes echoes of the person's
// queued messages the client does not show.
func (m *OS) echoQueueCmd(host, sessionID, windowID, who string, restore bool) tea.Cmd {
	call := m.inboxCaller()
	if call == nil {
		return nil
	}
	key := host + "\x00" + sessionID + "\x00" + windowID
	params := map[string]any{"session": sessionID, "window": windowID}
	if host != "" {
		// This machine's daemon asks that machine's over the link.
		params["host"] = host
	}
	return func() tea.Msg {
		msg := echoQueueMsg{key: key, host: host, session: sessionID, window: windowID, who: who, restore: restore}
		raw, err := call("list-queued", params, inboxReplyTimeout)
		if err != nil {
			msg.err = err
			return msg
		}
		var list struct {
			Entries []echoQueueEntry `json:"entries"`
		}
		if err := jsonx.Unmarshal(raw, &list); err != nil {
			msg.err = err
			return msg
		}
		msg.entries = list.Entries
		return msg
	}
}

// applyEchoQueue moves each echo of the pane to where the daemon has it: still
// queued, being typed, or gone from the queue, which is typed.
func (m *OS) applyEchoQueue(msg echoQueueMsg) tea.Cmd {
	if msg.err != nil {
		return nil
	}
	inQueue := map[string]echoQueueEntry{}
	for _, q := range msg.entries {
		inQueue[q.ID] = q
	}
	changed := false
	known := map[string]bool{}
	for _, e := range m.Inbox.echoes.list {
		if e.key != msg.key || e.id == "" {
			continue
		}
		known[e.id] = true
		q, ok := inQueue[e.id]
		next := e.state
		switch {
		case !ok && (e.state == echoQueued || e.state == echoSending):
			next = echoTyped
			e.typedAt = time.Now()
		case ok && q.State == "delivering":
			next = echoSending
		case ok && q.State == "stalled":
			// Typed, and not taken by the agent yet: the daemon watches it,
			// and the transcript shows it once the agent takes it.
			next = echoTyped
			if e.typedAt.IsZero() {
				e.typedAt = time.Now()
			}
		case ok:
			next = echoQueued
		}
		if next != e.state {
			e.state, changed = next, true
		}
	}
	if msg.restore {
		for _, q := range msg.entries {
			if known[q.ID] || !echoMine(msg.host, q) {
				continue
			}
			m.addEcho(msg.host, msg.session, msg.window, msg.who, q.Preview, nil, 0)
			e := m.Inbox.echoes.list[len(m.Inbox.echoes.list)-1]
			e.id, e.preview, e.state = q.ID, true, echoQueued
			if q.At > 0 {
				e.at = time.Unix(0, q.At)
			}
			switch q.State {
			case "delivering":
				e.state = echoSending
			case "stalled":
				e.state, e.typedAt = echoTyped, time.Now()
			}
			changed = true
		}
	}
	if changed {
		m.echoChanged(msg.key)
		if t := m.Inbox.threads.byKey[msg.key]; t != nil {
			m.echoTake(t)
		}
	}
	return m.echoPollCmd()
}

// echoMine reports whether a queued message is one the person sent: queued
// as the person on this machine, or, on a linked machine, by this machine's
// link for the person here.
func echoMine(host string, q echoQueueEntry) bool {
	if host == "" {
		return q.By == "human"
	}
	return strings.HasPrefix(q.By, "link:") && strings.HasPrefix(q.From, "the person on ")
}

// echoAction runs a click on an echo's cancel or retry.
func (m *OS) echoAction(action string) (tea.Cmd, bool) {
	what, rest, ok := strings.Cut(strings.TrimPrefix(action, "echo:"), ":")
	if !ok || !strings.HasPrefix(action, "echo:") {
		return nil, false
	}
	n, err := strconv.ParseUint(rest, 10, 64)
	if err != nil {
		return nil, true
	}
	var e *replyEcho
	for _, x := range m.Inbox.echoes.list {
		if x.n == n {
			e = x
		}
	}
	if e == nil {
		return nil, true
	}
	// Both act as the person, with the attach nonce: only a click or a key
	// from this terminal does that.
	if m.ProcessingRemoteKeys {
		m.ShowNotification(inboxRemoteQueueRefusal, "error", m.Settings.NotificationDuration)
		return nil, true
	}
	nonce := m.inboxNonce()
	if nonce == "" {
		m.ShowNotification(m.noNonceText(), "error", m.Settings.NotificationDuration*2)
		return nil, true
	}
	switch what {
	case "cancel":
		if e.state != echoQueued || e.id == "" {
			return nil, true
		}
		call := m.inboxCaller()
		id, sessionID, windowID := e.id, e.session, e.window
		params := map[string]any{"session": sessionID, "window": windowID, "id": id, "human_nonce": nonce}
		if e.host != "" {
			params["host"] = e.host
		}
		return func() tea.Msg {
			_, err := call("cancel-queued", params, inboxReplyTimeout)
			return echoCancelledMsg{n: n, err: err}
		}, true
	case "retry":
		if e.state != echoFailed || e.preview {
			return nil, true
		}
		st := &m.Inbox.reply
		st.sendSeq++
		e.state, e.why, e.send, e.at = echoSending, "", st.sendSeq, time.Now()
		m.echoChanged(e.key)
		text := e.text
		if e.parts != nil {
			text = ""
			images := false
			for _, p := range e.parts {
				images = images || p.Image != nil
			}
			if !images && len(e.text) <= inboxReplyMax {
				text = e.text
			}
		}
		return m.inboxQueueCmdOn(e.host, e.session, e.window, e.who, text, nonce, false, e.send, e.parts), true
	}
	return nil, true
}

// applyEchoCancelled forgets a cancelled echo, or says why the cancel failed.
func (m *OS) applyEchoCancelled(msg echoCancelledMsg) {
	ec := &m.Inbox.echoes
	for i, e := range ec.list {
		if e.n != msg.n {
			continue
		}
		if msg.err != nil {
			m.ShowNotification("The queued reply to "+e.who+" was not cancelled: "+msg.err.Error(), "error", m.Settings.NotificationDuration*2)
			return
		}
		ec.list = append(ec.list[:i], ec.list[i+1:]...)
		m.echoChanged(e.key)
		m.ShowNotification("Cancelled the queued reply to "+e.who, "info", m.Settings.NotificationDuration)
		return
	}
}

// threadEchoLines draws the echoes of thread key at its end: each as the
// person's message, with its state line. It returns the lines and their hits.
func (m *OS) threadEchoLines(key string, width int) ([]string, []string) {
	var mine []*replyEcho
	for _, e := range m.Inbox.echoes.list {
		if e.key == key {
			mine = append(mine, e)
		}
	}
	if len(mine) == 0 {
		return nil, nil
	}
	l := m.cockpitLook()
	pal := l.pal
	var out, hits []string
	var c *ckCanvas
	add := func(ln ckLine) {
		c = c.reset(width, 1, l.canvas)
		out = append(out, ckRender(c, width, ln))
		hits = append(hits, ln.hit)
	}
	for _, e := range mine {
		add(ckLine{label: true, segs: []ckSeg{{t: "You", fg: pal.AccentBright, attrs: ckBold}, {t: "  " + e.at.Format("15:04"), fg: pal.FgMute}}})
		add(ckLine{bg: l.panel, bar: "▎", barInk: pal.FgMute})
		rows, _ := threadWrapMore(e.text, max(width-6, 8))
		for _, s := range rows {
			add(ckLine{bg: l.panel, bar: "▎", barInk: pal.FgMute, indent: 3, segs: []ckSeg{{t: s, fg: pal.FgDim}}})
		}
		add(ckLine{bg: l.panel, bar: "▎", barInk: pal.FgMute})
		add(m.echoStateLine(l, e))
		add(ckLine{})
	}
	return out, hits
}

// echoStateLine is an echo's state, under it, with its cancel or retry.
func (m *OS) echoStateLine(l ckLook, e *replyEcho) ckLine {
	pal := l.pal
	mark, what, ink := "◌", "Sending", pal.Info
	act, verb := "", ""
	switch e.state {
	case echoQueued:
		mark, what, ink = "◷", "Queued, typed when it rests", pal.FgDim
		if e.id != "" {
			act, verb = "echo:cancel:"+strconv.FormatUint(e.n, 10), "cancel"
		}
	case echoTyped:
		mark, what, ink = "◌", "Sent, waiting for "+cmp.Or(e.who, "the agent"), pal.FgDim
	case echoFailed:
		mark, what, ink = "✕", "Not sent: "+e.why, pal.Warn
		if !e.preview {
			act, verb = "echo:retry:"+strconv.FormatUint(e.n, 10), "retry"
		}
	}
	if l.ascii {
		mark = map[string]string{"◌": "..", "◷": "..", "✕": "x"}[mark]
	}
	segs := []ckSeg{{t: mark + " " + what, fg: overlay.Readable(ink, l.canvas)}}
	if verb != "" {
		segs = append(segs, ckSeg{t: "   " + verb, fg: pal.AccentBright, attrs: ckBold})
	}
	return ckLine{indent: 3, segs: segs, hit: act}
}
