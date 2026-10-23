package session

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
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
//
// A hook takes a notice on a lease: the mail it describes counts as announced
// until leaseUntil, and becomes announced for good only when the hook says it
// wrote the line (commitNotice). A hook that missed its deadline printed
// nothing, and its lease runs out, so the next hook announces the mail again.
type noticeMark struct {
	id      uint64
	session string
	// lease is the token of the open lease, 0 for none. leaseTo is the id
	// it covers.
	lease      uint64
	leaseTo    uint64
	leaseUntil time.Time
}

// noticeLease is how long a hook's notice counts as announced before the
// hook says it wrote it. It is longer than a hook's whole run.
const noticeLease = 3 * time.Second

// effective is the newest id that counts as announced at now.
func (m noticeMark) effective(now time.Time) uint64 {
	if m.lease != 0 && now.Before(m.leaseUntil) {
		return max(m.id, m.leaseTo)
	}
	return m.id
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
// not yet announced, and marks everything it reports as announced: for good,
// or with lease until the caller commits it with the token it returns. With
// reset it announces whatever is unread. The check and the mark are one
// step, so two hooks that run at once announce a message once.
func (b *agentBus) takeNotice(session, inbox string, reset, lease bool) (mailSummary, bool, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.unreadSummaryLocked(session, inbox)
	if s.unread == 0 {
		return s, false, 0
	}
	if b.noticed == nil {
		b.noticed = map[string]noticeMark{}
	}
	now := time.Now()
	m := b.noticed[inbox]
	if !reset && s.newest <= m.effective(now) {
		return s, false, 0
	}
	m.session = session
	if !lease {
		m.id = max(m.id, s.newest)
		b.noticed[inbox] = m
		return s, true, 0
	}
	b.leaseSeq++
	m.lease, m.leaseTo, m.leaseUntil = b.leaseSeq, s.newest, now.Add(noticeLease)
	b.noticed[inbox] = m
	return s, true, m.lease
}

// commitNotice makes the lease with token announced for good. It reports
// false when the lease ran out or another took its place.
func (b *agentBus) commitNotice(inbox string, token uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	m, ok := b.noticed[inbox]
	if !ok || token == 0 || m.lease != token {
		return false
	}
	m.id = max(m.id, m.leaseTo)
	m.lease, m.leaseTo, m.leaseUntil = 0, 0, time.Time{}
	b.noticed[inbox] = m
	return true
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
//
// A hook names its harness, conversation and process, and the call is held to
// the guard a report from that hook would be: a nested harness, another
// conversation's process, or a harness outside the pane gets no notice and the
// reason. A hook asks with lease and commits once it wrote the line.
func (d *Daemon) verbAgentMailNotice(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session        string `json:"session"`
		Window         string `json:"window"`
		Reset          bool   `json:"reset"`
		Lease          bool   `json:"lease"`
		Commit         uint64 `json:"commit"`
		Harness        string `json:"harness"`
		AgentSessionID string `json:"agent_session_id"`
		HarnessPID     int    `json:"harness_pid"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Window == "" {
		return nil, invalidParam("window", "window is required: name the pane whose mail to announce, normally $TUIOS_PANE_ID")
	}
	sess, target, verr := d.reportTarget(cs, p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	windowID := state.Windows[idx].ID
	out := map[string]any{
		"type":      "agent_mail_notice",
		"session":   sess.Name(),
		"window_id": windowID,
	}
	if p.Commit != 0 {
		out["committed"] = d.agents.commitNotice(windowID, p.Commit)
		return out, nil
	}
	if p.Harness != "" {
		_, _, reason, err := sess.activityReportGuard(windowID, AgentReport{
			Harness:      p.Harness,
			SessionID:    p.AgentSessionID,
			HarnessPID:   p.HarnessPID,
			SharedServer: d.agentMatcher.registry.SharedServer(p.Harness),
			foreignPane:  d.harnessOutsidePane(cs, sess, windowID, p.HarnessPID),
		})
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		if reason != "" {
			out["notice"], out["reason"] = "", reason
			return out, nil
		}
	}
	sum, fresh, token := d.agents.takeNotice(sess.Name(), windowID, p.Reset, p.Lease)
	notice := ""
	if fresh {
		notice = mailNoticeText(sum, sess.Name(), windowID)
	}
	out["unread"] = sum.unread
	out["notice"] = notice
	if token != 0 {
		out["lease"] = token
	}
	return out, nil
}

// unannounced reports the inbox's unread mail and the newest message id
// announced to it. Mail newer than mark was never announced. It marks nothing.
func (b *agentBus) unannounced(session, inbox string) (mailSummary, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.unreadSummaryLocked(session, inbox), b.noticed[inbox].id
}

// markNoticed records that the inbox's mail up to id was announced. It never
// moves a mark back.
func (b *agentBus) markNoticed(session, inbox string, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.noticed == nil {
		b.noticed = map[string]noticeMark{}
	}
	if b.noticed[inbox].id < id {
		b.noticed[inbox] = noticeMark{id: id, session: session}
	}
}
