package session

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Mail notices: the one line that tells an agent it has unread mail.
//
// No harness reads a tuios inbox on its own, and an agent taught to check one
// forgets after a few compactions. So the daemon says it for the agent, in two
// places. A hook of a harness that takes context from its hooks asks for the
// notice during a turn (agent-mail-notice, called by tuios agent-hook), and the
// harness adds the line to the agent's context.
//
// Each message is announced once. The bus keeps, per pane, the newest message
// id it announced, and a notice goes out only when an unread message is newer
// than that. A conversation that starts, resumes or compacts asks with reset:
// the agent no longer holds the earlier notice, so mail still unread is
// announced again.
//
// The line is written by tuios alone. It carries a count, the senders' names
// cut to a safe alphabet, and a command to run. It never carries a subject or a
// body: those are written by another program, and the line goes into the
// agent's context, where text is read as instructions. Mail stays data the
// recipient reads on purpose, fenced, with read-agent-messages.

// mailNoticeMaxSenders is how many senders a notice names before "and N more".
const mailNoticeMaxSenders = 3

// mailNoticeMaxName bounds one sender's name in a notice, in runes.
const mailNoticeMaxName = 24

// noticeMark is the newest message id announced to one pane, and the session
// the pane was in, so a session that ends takes its marks with it.
type noticeMark struct {
	id      uint64
	session string
}

// mailSummary is a pane's unread mail as a notice describes it.
type mailSummary struct {
	unread  int
	senders []string
	// more is how many senders are left out of senders.
	more int
	// newest is the id of the newest unread message.
	newest uint64
}

// unreadSummary counts the unread direct messages in one inbox and names their
// senders, newest sender first. Callers hold b.mu.
func (b *agentBus) unreadSummaryLocked(session, inbox string) mailSummary {
	var s mailSummary
	seen := map[string]bool{}
	var names []string
	msgs := b.box(session).msgs
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Kind != agentMsgDirect || m.To != inbox || m.ReadAt != 0 {
			continue
		}
		s.unread++
		s.newest = max(s.newest, m.ID)
		name := mailSenderName(*m)
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) > mailNoticeMaxSenders {
		s.more = len(names) - mailNoticeMaxSenders
		names = names[:mailNoticeMaxSenders]
	}
	s.senders = names
	return s
}

// takeNotice reports the inbox's unread mail and whether it holds a message
// not yet announced, and marks everything it reports as announced. With reset
// it announces whatever is unread. The check and the mark are one step, so two
// hooks that run at once announce a message once.
func (b *agentBus) takeNotice(session, inbox string, reset bool) (mailSummary, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.unreadSummaryLocked(session, inbox)
	if s.unread == 0 {
		return s, false
	}
	if b.noticed == nil {
		b.noticed = map[string]noticeMark{}
	}
	if !reset && s.newest <= b.noticed[inbox].id {
		return s, false
	}
	b.noticed[inbox] = noticeMark{id: s.newest, session: session}
	return s, true
}

// forgetNotices drops the marks of one pane, or with window empty of every
// pane in session.
func (b *agentBus) forgetNotices(session, window string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if window != "" {
		delete(b.noticed, window)
		return
	}
	for w, m := range b.noticed {
		if m.session == session {
			delete(b.noticed, w)
		}
	}
}

// mailSenderName is the name a notice gives a message's sender: the label it
// was sent with, cut to letters, digits and a few marks, or the sender's short
// window id. Every part is a claim or a pane title, which the sending program
// controls, so nothing outside that alphabet reaches the line.
func mailSenderName(m AgentMessage) string {
	name := safeNoticeName(m.FromLabel)
	if name == "" {
		name = safeNoticeName(shortWindowID(m.From))
	}
	if name == "" {
		name = "an unnamed sender"
	} else {
		name = `"` + name + `"`
	}
	if m.Origin == AgentOriginLink {
		host := safeNoticeName(m.OriginHost)
		if host == "" {
			host = "another machine"
		}
		name += " on " + host
	}
	return name
}

// safeNoticeName keeps the letters, digits, spaces and the marks . _ - : @ of
// s, joins runs of spaces, and cuts the result to mailNoticeMaxName runes.
func safeNoticeName(s string) string {
	var b strings.Builder
	n := 0
	space := false
	for _, r := range s {
		if n == mailNoticeMaxName {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-', r == ':', r == '@':
		case r == ' ' || r == '\t':
			space = b.Len() > 0
			continue
		default:
			continue
		}
		if space {
			b.WriteByte(' ')
			n++
			space = false
			if n == mailNoticeMaxName {
				break
			}
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// shellSafeSession reports whether a session name can go into a command line
// as it is: letters, digits, '.', '_' and '-', at most 64 bytes.
func shellSafeSession(name string) bool {
	if name == "" || len(name) > 64 || strings.HasPrefix(name, "-") {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// mailReadCommand is the command a notice tells the agent to run. A session
// name outside the safe alphabet is left to the agent's own TUIOS_SESSION.
func mailReadCommand(session, window string) string {
	s := `"$TUIOS_SESSION"`
	if shellSafeSession(session) {
		s = session
	}
	return "tuios read-agent-messages -s " + s + " -w " + shortWindowID(window) + " --unread"
}

// mailNoticeText is the notice line, with no line break.
func mailNoticeText(s mailSummary, session, window string) string {
	if s.unread == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("You have ")
	b.WriteString(strconv.Itoa(s.unread))
	if s.unread == 1 {
		b.WriteString(" unread tuios message")
	} else {
		b.WriteString(" unread tuios messages")
	}
	names := s.senders
	if s.more > 0 {
		names = append(names[:len(names):len(names)], strconv.Itoa(s.more)+" more")
	}
	if len(names) > 0 {
		b.WriteString(" from ")
		for i, n := range names {
			switch {
			case i == 0:
			case i == len(names)-1:
				b.WriteString(" and ")
			default:
				b.WriteString(", ")
			}
			b.WriteString(n)
		}
	}
	if s.unread == 1 {
		b.WriteString(". Read it with: ")
	} else {
		b.WriteString(". Read them with: ")
	}
	b.WriteString(mailReadCommand(session, window))
	return b.String()
}

// verbAgentMailNotice answers a pane's hook with the notice line for its
// unread mail, once per message. See the top of this file.
func (d *Daemon) verbAgentMailNotice(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Reset   bool   `json:"reset"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Window == "" {
		return nil, invalidParam("window", "window is required: name the pane whose mail to announce, normally $TUIOS_PANE_ID")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	target := state.Windows[idx]
	sum, fresh := d.agents.takeNotice(sess.Name(), target.ID, p.Reset)
	notice := ""
	if fresh {
		notice = mailNoticeText(sum, sess.Name(), target.ID)
	}
	return map[string]any{
		"type":      "agent_mail_notice",
		"session":   sess.Name(),
		"window_id": target.ID,
		"unread":    sum.unread,
		"notice":    notice,
	}, nil
}
