package app

import (
	"hash/fnv"
	"image/color"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

// Message cards: a tool call that talked to another agent or to the person
// through tuios (transcriptview.AgentCall), drawn as what it said and to
// whom, not as a command line.
//
// A card names the other agent by its name and harness, from the agents the
// cockpit lists. An agent that closed keeps the name the fleet last listed
// it by, marked closed. The message is Markdown, folded when long. What the
// daemon's mail ring holds is the truth where it has it: a sent message
// says whether it was read and carries its replies, an ask its answer, and
// a read the whole of each message. The tool's result is the fallback.
//
// The name of the other agent is a stop for the pill cursor (inbox_child),
// and enter or a click on it selects that agent in the cockpit. A jump
// leaves the thread it came from on a small list, and inbox_jump_back
// (ctrl+o) goes back to it at the place it was scrolled to.

// ckMailShown is how many lines of a message a closed card shows.
const ckMailShown = 6

// ckJumpsMax bounds the jump list.
const ckJumpsMax = 32

// ckJump is one thread a jump left: the agent and how far up it was read.
type ckJump struct {
	key  string
	up   int
	pill string
}

// ckSeenAgent is an agent the fleet listed once, by its agent key.
type ckSeenAgent struct {
	name, harness string
}

var (
	ckGlyphSent  = ckGlyph{"", "→", "->"}
	ckGlyphGot   = ckGlyph{"", "←", "<-"}
	ckGlyphAsk   = ckGlyph{"", "?", "?"}
	ckGlyphStart = ckGlyph{"", "+", "+"}
)

// ckSeeAgents records the agents of a fleet list, so a card can name one
// after it closes.
func (m *OS) ckSeeAgents(list []sidebarAgentEntry) {
	ck := &m.Inbox.ck
	if ck.seen == nil {
		ck.seen = map[string]ckSeenAgent{}
	}
	for _, e := range list {
		if e.Fold > 0 || e.Child != nil || e.More > 0 || e.Head {
			continue
		}
		ck.seen[agentKey(e)] = ckSeenAgent{name: sidebarAgentName(e), harness: e.Harness}
	}
}

// ckPeer is the other side of a message as the card names it.
type ckPeer struct {
	// id is the peer's stop for the pill cursor, "" for none.
	id string
	// key is the agent's key while the fleet lists it.
	key           string
	name, harness string
	state         string
	// closed says the agent was listed once and is gone now; unknown that
	// the cockpit never listed it.
	closed, unknown bool
	human           bool
	// many is a selector's terms, for a message to many panes.
	many string
}

// ckResolvePeer finds the agent a call names, as seen from the pane e: to
// is a window id, a prefix of one, or a name, in session (e's own when
// empty).
func (m *OS) ckResolvePeer(e sidebarAgentEntry, sess, to string) ckPeer {
	if to == session.AgentInboxHuman {
		return ckPeer{human: true, name: "you"}
	}
	host := e.Host
	if h, s, ok := strings.Cut(sess, ":"); ok {
		host, sess = h, s
		if host == m.attachedMachine() {
			host = ""
		}
	}
	if sess == "" {
		sess = e.SessionID
	}
	ck := &m.Inbox.ck
	var found *sidebarAgentEntry
	for i := range ck.agents {
		k := &ck.agents[i]
		if k.Host != host || (k.SessionID != sess && k.SessionLabel != sess) || k.Child != nil || k.Fold > 0 {
			continue
		}
		if k.WindowID == to {
			found = k
			break
		}
		if found == nil && (len(to) >= 4 && strings.HasPrefix(k.WindowID, to) || printableTitle(k.Title) == to) {
			found = k
		}
	}
	if found != nil {
		return ckPeer{key: agentKey(*found), name: sidebarAgentName(*found), harness: found.Harness, state: sidebarKinState(*found)}
	}
	// Gone: the name the fleet or the mail ring last knew it by.
	for key, s := range ck.seen {
		parts := strings.SplitN(key, "\x00", 3)
		if len(parts) == 3 && parts[0] == host && parts[1] == sess && (parts[2] == to || len(to) >= 4 && strings.HasPrefix(parts[2], to) || s.name == to) {
			return ckPeer{name: s.name, harness: s.harness, closed: true}
		}
	}
	for _, msg := range slices.Backward(m.AgentMail.Messages) {
		if msg.Session != sess || host != "" {
			continue
		}
		if msg.To == to || len(to) >= 4 && strings.HasPrefix(msg.To, to) || msg.To != "" && printableTitle(msg.ToLabel) == to {
			if name := printableTitle(msg.ToLabel); name != "" {
				return ckPeer{name: name, closed: true}
			}
		}
		if msg.From == to || len(to) >= 4 && strings.HasPrefix(msg.From, to) || msg.From != "" && printableTitle(msg.FromLabel) == to {
			if name := printableTitle(msg.FromLabel); name != "" {
				return ckPeer{name: name, closed: true}
			}
		}
	}
	return ckPeer{name: printableTitle(to), unknown: true}
}

// ckMailOf is the message of the ring with id, in session.
func (m *OS) ckMailOf(sess string, id uint64) (session.AgentMessage, bool) {
	if id == 0 {
		return session.AgentMessage{}, false
	}
	for _, msg := range slices.Backward(m.AgentMail.Messages) {
		if msg.ID == id && msg.Session == sess {
			return msg, true
		}
	}
	return session.AgentMessage{}, false
}

// ckSentID is the id of the message a send's result names: "message 12
// delivered to ..." as the command prints it, or message_id as JSON.
func ckSentID(c transcriptview.AgentCall, out string) uint64 {
	if c.JSON || strings.HasPrefix(strings.TrimSpace(out), "{") {
		var res struct {
			MessageID uint64 `json:"message_id"`
		}
		if jsonx.Unmarshal([]byte(strings.TrimSpace(out)), &res) == nil {
			return res.MessageID
		}
		return 0
	}
	for line := range strings.SplitSeq(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "message "); ok {
			num, _, _ := strings.Cut(rest, " ")
			if n, err := strconv.ParseUint(num, 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

// ckReadMail is one message a read returned.
type ckReadMail struct {
	id             uint64
	kind           string
	from, fromName string
	subject, text  string
	at             int64
	replyTo        uint64
	verifiedHuman  bool
}

// ckReadResult is the messages a read's result holds, from its JSON or from
// the lines the command prints. more says the result was cut, or it held
// messages it could not read.
func ckReadResult(out string) (msgs []ckReadMail, none bool) {
	t := strings.TrimSpace(out)
	if strings.HasPrefix(t, "{") {
		var res struct {
			Messages []struct {
				ID            uint64 `json:"id"`
				Kind          string `json:"kind"`
				From          string `json:"from"`
				FromLabel     string `json:"from_label"`
				Subject       string `json:"subject"`
				Text          string `json:"text"`
				SentAt        int64  `json:"sent_at"`
				ReplyTo       uint64 `json:"reply_to"`
				VerifiedHuman bool   `json:"verified_human"`
			} `json:"messages"`
		}
		if jsonx.Unmarshal([]byte(t), &res) != nil {
			return nil, false
		}
		for _, r := range res.Messages {
			msgs = append(msgs, ckReadMail{id: r.ID, kind: r.Kind, from: r.From, fromName: r.FromLabel, subject: r.Subject,
				text: r.Text, at: r.SentAt, replyTo: r.ReplyTo, verifiedHuman: r.VerifiedHuman})
		}
		return msgs, len(msgs) == 0
	}
	if strings.HasPrefix(t, "No messages") {
		return nil, true
	}
	var cur *ckReadMail
	inBody := false
	var body []string
	for line := range strings.SplitSeq(t, "\n") {
		switch {
		case strings.HasPrefix(line, "--- begin untrusted content from "):
			inBody, body = true, nil
		case line == session.UntrustedClose:
			if cur != nil {
				cur.text = strings.Join(body, "\n")
			}
			inBody = false
		case inBody:
			body = append(body, strings.TrimPrefix(strings.TrimPrefix(line, session.UntrustedGutter), strings.TrimSpace(session.UntrustedGutter)))
		case strings.HasPrefix(line, "#"):
			// #12  message  from review (c7be946f)  2m ago  new
			f := strings.SplitN(line[1:], "  ", 4)
			n, err := strconv.ParseUint(f[0], 10, 64)
			if err != nil || len(f) < 3 {
				continue
			}
			msgs = append(msgs, ckReadMail{id: n, kind: f[1]})
			cur = &msgs[len(msgs)-1]
			who := strings.TrimPrefix(f[2], "from ")
			who = strings.TrimPrefix(who, "your lead ")
			if name, id, ok := strings.Cut(who, " ("); ok {
				cur.fromName, cur.from = name, strings.TrimSuffix(id, ")")
			} else {
				cur.fromName = who
			}
		case cur != nil && strings.HasPrefix(line, "subject: "):
			cur.subject = strings.TrimPrefix(line, "subject: ")
		}
	}
	return msgs, false
}

// ckAnswerText is what an ask-agent's result says the other agent answered:
// the fenced reply, without the fence and the gutter.
func ckAnswerText(out string) string {
	var body []string
	in := false
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "--- begin untrusted content from "):
			in = true
		case line == session.UntrustedClose:
			in = false
		case in:
			body = append(body, strings.TrimPrefix(line, session.UntrustedGutter))
		}
	}
	if body == nil {
		return strings.TrimSpace(out)
	}
	return strings.TrimSpace(strings.Join(body, "\n"))
}

// ckAskRecord is the ring's record of an ask the pane e made of the window
// to, at or after the call: the answer as the daemon kept it.
func (m *OS) ckAskRecord(e sidebarAgentEntry, en threadEntry, peer string, question string) (session.AgentMessage, bool) {
	q := strings.TrimSpace(question)
	for _, msg := range slices.Backward(m.AgentMail.Messages) {
		if msg.Kind != "ask" || msg.Session != e.SessionID || msg.From != e.WindowID {
			continue
		}
		if peer != "" && msg.To != peer && !strings.HasPrefix(msg.To, peer) {
			continue
		}
		if en.At > 0 && msg.SentAt > 0 && msg.SentAt/1e6 < en.At-2000 {
			break
		}
		if s := strings.TrimSpace(msg.Subject); s != "" && strings.HasPrefix(q, s) {
			return msg, true
		}
	}
	return session.AgentMessage{}, false
}

// ckPeerID is the pill cursor's stop for the k-th peer of entry id.
func ckPeerID(id string, k int) string { return "p:" + id + "/" + strconv.Itoa(k) }

// ckTalkCard is a tool call's agent calls as message cards. A single call
// takes what its result and the ring say: the answer, the messages read,
// whether a sent message was read and its replies.
//
//go:noinline
func (m *OS) ckTalkCard(l ckLook, e sidebarAgentEntry, t *agentThread, en threadEntry, res threadEntry, width int, open bool) []ckLine {
	ls, _ := m.ckTalkCardPeers(l, e, t, en, res, width, open)
	return ls
}

// ckTalkCardPeers is ckTalkCard, and the peers the card names, in the order
// it draws them.
func (m *OS) ckTalkCardPeers(l ckLook, e sidebarAgentEntry, t *agentThread, en threadEntry, res threadEntry, width int, open bool) ([]ckLine, []ckPeer) {
	var ls []ckLine
	var peers []ckPeer
	out := ""
	if len(en.Calls) == 1 {
		out = res.Text
	}
	for i, c := range en.Calls {
		if i > 0 {
			ls = append(ls, ckLine{})
		}
		ls = append(ls, m.ckTalkCall(l, e, t, en, c, out, &peers, width, open)...)
	}
	if en.Status == "error" {
		// The call failed: the end of what it printed.
		lines := ckOutputLines(res.Text, max(width-8, 8), l.ascii)
		if len(lines) > ckFailShown {
			lines = lines[len(lines)-ckFailShown:]
		}
		for _, s := range lines {
			ls = append(ls, ckLine{indent: 4, segs: []ckSeg{{t: s, fg: l.pal.FgDim}}})
		}
	}
	return ls, peers
}

// ckTalkCall is one agent call's lines. peers collects the peers the
// entry's card names, which gives each its stop id.
func (m *OS) ckTalkCall(l ckLook, e sidebarAgentEntry, t *agentThread, en threadEntry, c transcriptview.AgentCall, out string, peers *[]ckPeer, width int, open bool) []ckLine {
	pal := l.pal
	status := m.ckToolLine(l, "", "", en.Status, width).right
	fold := "fold:" + en.ID
	peerOf := func(sess, to string) ckPeer { return ckAddPeer(peers, en.ID, m.ckResolvePeer(e, sess, to)) }
	sel := t.pill
	var ls []ckLine
	switch c.Verb {
	case transcriptview.CallSend, transcriptview.CallQueue:
		verb := "Sent to"
		if c.Verb == transcriptview.CallQueue {
			verb = "Queued for"
		}
		var head ckLine
		switch {
		case c.Select != "":
			head = m.ckTalkHead(l, ckGlyphSent, verb, ckPeer{many: c.Select}, sel, nil, status, width)
		case c.To == "" && c.Verb == transcriptview.CallSend:
			head = m.ckTalkHead(l, ckGlyphSent, "Notice to the session", ckPeer{}, sel, nil, status, width)
		default:
			p := peerOf(c.Session, c.To)
			var note []ckSeg
			if msg, ok := m.ckMailOf(e.SessionID, ckSentID(c, out)); ok && c.Verb == transcriptview.CallSend {
				note = ckReceipt(l, msg)
				head = m.ckTalkHead(l, ckGlyphSent, verb, p, sel, note, status, width)
				ls = append(ls, head)
				ls = append(ls, m.ckTalkBody(l, c.Subject, c.Text, fold, pal.Accent, width, open)...)
				return append(ls, m.ckReplies(l, e, en, msg, peers, sel, fold, width, open)...)
			}
			head = m.ckTalkHead(l, ckGlyphSent, verb, p, sel, note, status, width)
		}
		ls = append(ls, head)
		return append(ls, m.ckTalkBody(l, c.Subject, c.Text, fold, pal.Accent, width, open)...)
	case transcriptview.CallAsk:
		p := peerOf(c.Session, c.To)
		ls = append(ls, m.ckTalkHead(l, ckGlyphAsk, "Asked", p, sel, nil, status, width))
		ls = append(ls, m.ckTalkBody(l, "", c.Text, fold, pal.Accent, width, open)...)
		answer, at := "", int64(0)
		if rec, ok := m.ckAskRecord(e, en, ckPeerWindow(m, p), c.Text); ok {
			answer, at = rec.Text, rec.SentAt
		} else if en.Status == "ok" {
			answer = ckAnswerText(out)
		}
		switch {
		case answer != "":
			name := p.name
			if p.many != "" || name == "" {
				name = "the agent"
			}
			label := []ckSeg{{t: l.g(ckGlyphGot) + " ", fg: pal.FgMute}, {t: name + " answered", fg: pal.FgDim, attrs: ckBold}}
			if at > 0 {
				label = append(label, ckSeg{t: "  " + ckRingClock(at), fg: pal.FgMute})
			}
			ls = append(ls, ckLine{indent: 2, segs: label})
			ls = append(ls, m.ckTalkBody(l, "", answer, fold, pal.FgMute, width, open)...)
		case en.Status == "running":
			ls = append(ls, ckLine{indent: 4, segs: []ckSeg{{t: "Waiting for the answer.", fg: pal.FgMute, attrs: ckItalic}}})
		}
		return ls
	case transcriptview.CallRead:
		return m.ckReadCard(l, e, en, c, out, peers, sel, status, fold, width, open)
	case transcriptview.CallAskHuman:
		ls = append(ls, m.ckTalkHead(l, ckGlyphAsk, "Asked you", ckPeer{}, sel, nil, status, width))
		ls = append(ls, m.ckTalkBody(l, "", c.Text, fold, pal.Accent, width, open)...)
		answer := ""
		if en.Status == "ok" {
			answer = strings.TrimSpace(out)
			if strings.ContainsRune(answer, '\n') {
				answer = ""
			}
		}
		var segs []ckSeg
		for k, o := range c.Options {
			if k > 0 {
				segs = append(segs, ckSeg{t: "  "})
			}
			fg, bg, attrs := pal.FgDim, l.panel, uint8(0)
			if o == answer {
				fg, bg, attrs = pal.PillFg, pal.Accent, ckBold
			}
			segs = append(segs, ckSeg{t: " " + strconv.Itoa(k+1) + " " + ckTrunc(o, 24) + " ", fg: fg, bg: bg, attrs: attrs})
		}
		if segs != nil {
			ls = append(ls, ckLine{indent: 4, segs: segs})
		}
		switch {
		case answer != "":
			ls = append(ls, ckLine{indent: 4, segs: []ckSeg{{t: "You answered ", fg: pal.FgMute}, {t: answer, fg: pal.Fg, attrs: ckBold}}})
		case en.Status == "running":
			ls = append(ls, ckLine{indent: 4, segs: []ckSeg{{t: "Waiting for your answer.", fg: pal.FgMute, attrs: ckItalic}}})
		case en.Status == "error":
			ls = append(ls, ckLine{indent: 4, segs: []ckSeg{{t: "No answer.", fg: pal.FgDim}}})
		}
		return ls
	case transcriptview.CallStart, transcriptview.CallDelegate:
		verb, glyph := "Started", ckGlyphStart
		who := ckHarness(c.Harness)
		if c.Verb == transcriptview.CallDelegate {
			verb = "Delegated"
			if c.Name != "" {
				verb = "Delegated to"
			}
		}
		if who == "" {
			who = ckHarness(e.Harness)
		}
		p := ckPeer{name: c.Name, harness: who}
		if p.name == "" {
			p = ckPeer{name: who}
			if p.name == "" {
				p.name = "an agent"
			}
		}
		ls = append(ls, m.ckTalkHead(l, glyph, verb, p, sel, nil, status, width))
		return append(ls, m.ckTalkBody(l, "", c.Text, fold, pal.Accent, width, open)...)
	}
	return nil
}

// ckAddPeer gives a peer its stop id and adds it to peers. The person and
// a selector take no stop.
func ckAddPeer(peers *[]ckPeer, entryID string, p ckPeer) ckPeer {
	if !p.human && p.many == "" && p.name != "" {
		p.id = ckPeerID(entryID, len(*peers))
		*peers = append(*peers, p)
	}
	return p
}

// ckPeerWindow is the window id of a peer the fleet lists, "" for any
// other.
func ckPeerWindow(m *OS, p ckPeer) string {
	if p.key == "" {
		return ""
	}
	parts := strings.SplitN(p.key, "\x00", 3)
	if len(parts) == 3 {
		return parts[2]
	}
	return ""
}

// ckRingClock is a ring time, in nanoseconds, as a card shows it.
func ckRingClock(ns int64) string { return ckClock(ns / 1e6) }

// ckReceipt is what the ring says of a sent message: read and when, seen,
// or not read yet.
func ckReceipt(l ckLook, msg session.AgentMessage) []ckSeg {
	pal := l.pal
	switch {
	case msg.ReadAt != 0:
		return []ckSeg{{t: "read " + ckRingClock(msg.ReadAt) + "  ", fg: pal.FgMute}}
	case msg.SeenAt != 0:
		return []ckSeg{{t: "seen, not read  ", fg: pal.FgMute}}
	}
	return []ckSeg{{t: "not read yet  ", fg: pal.FgMute}}
}

// ckReplies are the replies the ring holds to a sent message, each a card
// from its sender.
func (m *OS) ckReplies(l ckLook, e sidebarAgentEntry, en threadEntry, sent session.AgentMessage, peers *[]ckPeer, sel, fold string, width int, open bool) []ckLine {
	var ls []ckLine
	for _, msg := range m.AgentMail.Messages {
		if msg.Session != sent.Session || msg.ReplyTo != sent.ID || msg.Kind == "ask" {
			continue
		}
		p := m.ckResolvePeer(e, "", msg.From)
		if p.unknown && msg.FromLabel != "" {
			p.name = printableTitle(msg.FromLabel)
		}
		p = ckAddPeer(peers, en.ID, p)
		right := []ckSeg{{t: ckRingClock(msg.SentAt) + "  ", fg: l.pal.FgMute}}
		head := m.ckTalkHead(l, ckGlyphGot, "Reply from", p, sel, nil, right, width)
		head.indent = 2
		ls = append(ls, head)
		ls = append(ls, m.ckTalkBody(l, msg.Subject, msg.Text, fold, l.pal.FgMute, width, open)...)
	}
	return ls
}

// ckReadCard is a read of the mail: a line per message read, from its
// sender, with the message under it.
func (m *OS) ckReadCard(l ckLook, e sidebarAgentEntry, en threadEntry, c transcriptview.AgentCall, out string, peers *[]ckPeer, sel string, status []ckSeg, fold string, width int, open bool) []ckLine {
	pal := l.pal
	verb := "Read mail"
	switch {
	case c.Sent:
		verb = "Checked sent mail"
	case c.Thread != 0:
		verb = "Read thread #" + strconv.FormatUint(c.Thread, 10)
	case !c.Self && c.To != "":
		verb = "Read the mail of"
	}
	var who ckPeer
	if verb == "Read the mail of" {
		who = ckAddPeer(peers, en.ID, m.ckResolvePeer(e, c.Session, c.To))
	}
	msgs, none := ckReadResult(out)
	if c.Sent || en.Status != "ok" {
		msgs, none = nil, false
	}
	note := []ckSeg(nil)
	if len(msgs) > 0 {
		note = []ckSeg{{t: plural.Count(len(msgs), "message") + "  ", fg: pal.FgMute}}
	}
	ls := []ckLine{m.ckTalkHead(l, ckGlyphGot, verb, who, sel, note, status, width)}
	if none {
		return append(ls, ckLine{indent: 4, segs: []ckSeg{{t: "No new mail.", fg: pal.FgMute, attrs: ckItalic}}})
	}
	for _, r := range msgs {
		// The ring has the whole message; the result is cut.
		if msg, ok := m.ckMailOf(e.SessionID, r.id); ok {
			r.text, r.subject, r.from, r.at, r.verifiedHuman = msg.Text, msg.Subject, msg.From, msg.SentAt, msg.VerifiedHuman
			if r.fromName == "" {
				r.fromName = printableTitle(msg.FromLabel)
			}
		}
		var p ckPeer
		switch {
		case r.from == session.AgentInboxHuman && r.verifiedHuman:
			p = ckPeer{human: true, name: "you"}
		case r.from == session.AgentInboxHuman:
			// Only the daemon can say a message is the person's.
			p = ckPeer{name: "human, not verified", unknown: true}
		case r.from != "":
			p = m.ckResolvePeer(e, c.Session, r.from)
			if p.unknown && r.fromName != "" {
				p.name = r.fromName
			}
			p = ckAddPeer(peers, en.ID, p)
		default:
			p = ckPeer{name: firstNonEmptyStr(r.fromName, "someone")}
		}
		word := "From"
		if r.kind == "notice" {
			word = "Notice from"
		}
		var right []ckSeg
		if r.at > 0 {
			right = append(right, ckSeg{t: ckRingClock(r.at) + "  ", fg: pal.FgMute})
		}
		right = append(right, ckSeg{t: "#" + strconv.FormatUint(r.id, 10) + "  ", fg: pal.FgMute})
		head := m.ckTalkHead(l, ckGlyphGot, word, p, sel, nil, right, width)
		head.indent = 2
		ls = append(ls, head)
		ls = append(ls, m.ckTalkBody(l, r.subject, r.text, fold, pal.FgMute, width, open)...)
	}
	return ls
}

// ckTalkHead is a message card's first line: the direction, the other
// agent's name with its state mark and harness, and on the right what the
// ring says and the call's status. The name lights under the pill cursor.
func (m *OS) ckTalkHead(l ckLook, g ckGlyph, verb string, p ckPeer, sel string, note, status []ckSeg, width int) ckLine {
	pal := l.pal
	right := append(slices.Clone(note), status...)
	segs := []ckSeg{{t: l.g(g) + " ", fg: pal.FgMute}, {t: verb, fg: pal.FgDim, attrs: ckBold}}
	room := max(width-ckWidth(segs...)-ckWidth(right...)-4, 6)
	ln := ckLine{right: right}
	switch {
	case p.many != "":
		segs = append(segs, ckSeg{t: " the agents of ", fg: pal.FgDim}, ckSeg{t: ckTrunc(p.many, room-15), fg: pal.Fg})
	case p.human:
		segs = append(segs, ckSeg{t: " you", fg: pal.Fg, attrs: ckBold})
	case p.name != "":
		var mark string
		ink := color.Color(pal.FgMute)
		if p.state != "" {
			mark, ink = l.ckMark(p.state, false)
		} else if p.closed {
			mark = l.g(ckGlyphIdle)
		}
		tail := ""
		if p.harness != "" {
			tail = sepWord() + ckHarness(p.harness)
		}
		switch {
		case p.closed:
			tail += sepWord() + "closed"
		case p.unknown:
			tail += sepWord() + "not listed"
		}
		name := ckTrunc(p.name, max(room-ckWidth(ckSeg{t: mark + "  " + tail}), 6))
		fg, bg, tailFg := pal.Fg, color.Color(nil), pal.FgMute
		if p.closed || p.unknown {
			fg = pal.FgDim
		}
		attrs := uint8(ckBold)
		if p.id != "" && p.id == sel {
			fg, bg, ink, tailFg = pal.PillFg, pal.Accent, pal.PillFg, pal.PillFg
		}
		segs = append(segs, ckSeg{t: " "})
		if mark != "" {
			segs = append(segs, ckSeg{t: " " + mark + " ", fg: ink, bg: bg})
		} else if bg != nil {
			segs = append(segs, ckSeg{t: " ", bg: bg})
		}
		segs = append(segs, ckSeg{t: name, fg: fg, bg: bg, attrs: attrs})
		if bg != nil {
			segs = append(segs, ckSeg{t: tail + " ", fg: tailFg, bg: bg})
		} else {
			segs = append(segs, ckSeg{t: tail, fg: tailFg})
		}
		if p.id != "" {
			ln.hit = "peer:" + p.id
		}
	}
	ln.segs = segs
	ln.label = true
	return ln
}

// ckTalkBody is a message under its card's line: the subject in bold, then
// the text as Markdown behind a bar in ink. A long message shows its first
// lines until opened.
func (m *OS) ckTalkBody(l ckLook, subject, text, fold string, ink color.Color, width int, open bool) []ckLine {
	pal := l.pal
	const indent = 4
	bodyW := max(width-indent-4, 8)
	var body []ckLine
	if s := printableTitle(subject); s != "" {
		body = append(body, ckLine{segs: []ckSeg{{t: ckTrunc(s, bodyW), fg: pal.Fg, attrs: ckBold}}})
	}
	text = strings.TrimSpace(text)
	if text != "" {
		body = append(body, m.ckMarkdownFold(l, text, bodyW, strings.TrimPrefix(fold, "fold:"), open)...)
	}
	more := 0
	if !open && len(body) > ckMailShown+1 {
		more = len(body) - ckMailShown
		body = body[:ckMailShown]
	}
	bar := "▎"
	if l.ascii {
		bar = "|"
	}
	out := make([]ckLine, 0, len(body)+1)
	for _, ln := range body {
		ln.indent += indent
		ln.bar, ln.barInk = bar, ink
		ln.bgAt = max(ln.bgAt, indent)
		ckShiftBar(&ln, indent-2)
		if ln.hit == "" {
			ln.hit = fold
		}
		out = append(out, ln)
	}
	if more > 0 {
		out = append(out, ckLine{indent: indent, hit: fold, segs: []ckSeg{{t: plural.Count(more, "more line") + "   ", fg: pal.FgMute},
			{t: m.inboxKeyOr(config.ActionInboxExpand, "e"), fg: pal.AccentBright, attrs: ckBold}, {t: " expand all", fg: pal.FgMute}}})
	}
	return out
}

// ckShiftBar moves a line's bar from the first cell to column at, by
// putting it in front of the line's text.
func ckShiftBar(ln *ckLine, at int) {
	if ln.bar == "" || at <= 0 {
		return
	}
	bar := ckSeg{t: ln.bar, fg: ln.barInk}
	ln.bar = ""
	if ln.raw != "" {
		// A drawn line keeps its own start; the bar goes in the first cell.
		ln.bar = bar.t
		return
	}
	pad := ln.indent - at - 1
	ln.indent = at
	ln.segs = append([]ckSeg{bar, {t: strings.Repeat(" ", max(pad, 1))}}, ln.segs...)
}

// ckTalkSig is what an entry's message card depends on beyond the entry and
// its result: the agents it names, the ring and the pill cursor on it.
func (m *OS) ckTalkSig(e sidebarAgentEntry, t *agentThread, en threadEntry) uint64 {
	h := fnv.New64a()
	h.Write([]byte(strconv.FormatUint(m.AgentMail.Gen, 10)))
	if strings.HasPrefix(t.pill, "p:"+en.ID+"/") {
		h.Write([]byte(t.pill))
	}
	for _, c := range en.Calls {
		h.Write([]byte("\x00" + c.To))
	}
	h.Write([]byte(strconv.FormatUint(m.ckPeersSig(), 10)))
	return h.Sum64()
}

// ckPeersSig hashes the agents a card can name: each listed agent's key,
// name, harness and state, and how many the cockpit has seen.
func (m *OS) ckPeersSig() uint64 {
	h := fnv.New64a()
	for _, k := range m.Inbox.ck.agents {
		h.Write([]byte("\x00" + agentKey(k) + "\x00" + k.Title + "\x00" + k.Harness + "\x00" + sidebarKinState(k)))
	}
	h.Write([]byte(strconv.Itoa(len(m.Inbox.ck.seen))))
	return h.Sum64()
}

// ckThreadTalks reports whether a thread has a message card.
func ckThreadTalks(t *agentThread) bool {
	for _, en := range t.entries {
		if len(en.Calls) > 0 {
			return true
		}
	}
	return false
}

// ckEntryPeers are the peers an entry's message card names, in the order
// it draws them, each with its stop id.
func (m *OS) ckEntryPeers(e sidebarAgentEntry, en, res threadEntry) []ckPeer {
	if len(en.Calls) == 0 {
		return nil
	}
	_, peers := m.ckTalkCardPeers(m.cockpitLook(), e, &agentThread{}, en, res, 80, true)
	return peers
}

// ckThreadPeers are the peer stops of the thread of e, by entry id.
func (m *OS) ckThreadPeers(e sidebarAgentEntry, t *agentThread) map[string][]ckPeer {
	var out map[string][]ckPeer
	results := map[string]threadEntry{}
	for _, en := range t.entries {
		if en.Kind == "tool_result" && en.ToolID != "" {
			results[en.ToolID] = en
		}
	}
	for _, en := range t.entries {
		if en.Kind != "tool_call" || len(en.Calls) == 0 {
			continue
		}
		if p := m.ckEntryPeers(e, en, results[en.ToolID]); len(p) > 0 {
			if out == nil {
				out = map[string][]ckPeer{}
			}
			out[en.ID] = p
		}
	}
	return out
}

// ckPeerTarget is the peer a stop on the thread of e names. ok is false for
// a stop the thread no longer has.
func (m *OS) ckPeerTarget(e sidebarAgentEntry, t *agentThread, id string) (ckPeer, bool) {
	rest, _ := strings.CutPrefix(id, "p:")
	entryID, _, _ := strings.Cut(rest, "/")
	for _, p := range m.ckThreadPeers(e, t)[entryID] {
		if p.id == id {
			return p, true
		}
	}
	return ckPeer{}, false
}

// ckPeerGo selects the agent a peer stop names, and leaves the thread it
// came from on the jump list. A closed agent, or one the fleet does not
// show, is said so.
func (m *OS) ckPeerGo(e sidebarAgentEntry, t *agentThread, id string) tea.Cmd {
	p, ok := m.ckPeerTarget(e, t, id)
	if !ok {
		t.pill = ""
		m.cockpitTouch()
		return nil
	}
	if p.key == "" {
		if p.closed {
			m.ShowNotification(p.name+" is closed. Its thread is gone.", "info", m.Settings.NotificationDuration)
		} else {
			m.ShowNotification("The agents view does not list "+p.name+".", "info", m.Settings.NotificationDuration)
		}
		return nil
	}
	jump := ckJump{key: agentKey(e), up: t.follow.Up, pill: t.pill}
	cmd, found := m.cockpitOpenAgentOK(p.key)
	if !found {
		m.ShowNotification(p.name+" is not in the list now. Clear the filter or the search to see it.", "info", m.Settings.NotificationDuration)
		return nil
	}
	ck := &m.Inbox.ck
	ck.jumps = append(ck.jumps, jump)
	if len(ck.jumps) > ckJumpsMax {
		ck.jumps = slices.Delete(ck.jumps, 0, len(ck.jumps)-ckJumpsMax)
	}
	return cmd
}

// CockpitJumpBack goes back to the thread the last jump from a message card
// left, at the place it was read. It reports false when there is none.
func (m *OS) CockpitJumpBack() (tea.Cmd, bool) {
	ck := &m.Inbox.ck
	for len(ck.jumps) > 0 {
		j := ck.jumps[len(ck.jumps)-1]
		ck.jumps = ck.jumps[:len(ck.jumps)-1]
		cmd, ok := m.cockpitOpenAgentOK(j.key)
		if !ok {
			// That agent closed or is filtered out: the one before it.
			continue
		}
		if t := m.Inbox.threads.byKey[j.key]; t != nil {
			t.follow.Scroll(j.up - t.follow.Up)
			t.pill = j.pill
			t.gen++
		}
		m.cockpitTouch()
		return cmd, true
	}
	return nil, false
}

// cockpitOpenAgentOK is cockpitOpenAgent, and whether the fleet lists the
// agent.
func (m *OS) cockpitOpenAgentOK(key string) (tea.Cmd, bool) {
	ck := &m.Inbox.ck
	for _, r := range m.cockpitRows() {
		if r.agent >= 0 && r.agent < len(ck.agents) && r.cursorable() && agentKey(ck.agents[r.agent]) == key {
			return m.cockpitOpenAgent(key), true
		}
	}
	return nil, false
}

// cockpitPeerClick handles a click on the other agent's name on a message
// card: it selects that agent.
func (m *OS) cockpitPeerClick(id string) tea.Cmd {
	e, ok := m.cockpitAgent()
	t := m.cockpitThread()
	if !ok || t == nil {
		return nil
	}
	t.pill = id
	return m.ckPeerGo(e, t, id)
}

// ckJumpHints is the footer's hint for the back key while the jump list
// holds a thread.
func (m *OS) ckJumpHints() []overlay.Hint {
	if len(m.Inbox.ck.jumps) == 0 {
		return nil
	}
	return m.keyHints(config.ActionInboxJumpBack, "back")
}
