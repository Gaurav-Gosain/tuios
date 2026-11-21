package session

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

const mailNoticeMaxSenders = 3

const mailNoticeMaxName = 24

type noticeMark struct {
	id         uint64
	session    string
	lease      uint64
	leaseTo    uint64
	leaseUntil time.Time
}

const noticeLease = 3 * time.Second

func (m noticeMark) effective(now time.Time) uint64 {
	if m.lease != 0 && now.Before(m.leaseUntil) {
		return max(m.id, m.leaseTo)
	}
	return m.id
}

type mailSummary struct {
	unread  int
	senders []string
	more    int
	newest  uint64
}

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

func mailReadCommand(session, window string) string {
	s := `"$TUIOS_SESSION"`
	if shellSafeSession(session) {
		s = session
	}
	return "tuios read-agent-messages -s " + s + " -w " + shortWindowID(window) + " --unread"
}

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

func (b *agentBus) unannounced(session, inbox string) (mailSummary, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.unreadSummaryLocked(session, inbox), b.noticed[inbox].effective(time.Now())
}

func (b *agentBus) claimNotice(session, inbox string, id, was uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.noticed == nil {
		b.noticed = map[string]noticeMark{}
	}
	m := b.noticed[inbox]
	if m.effective(time.Now()) != was {
		return false
	}
	m.session = session
	m.id = max(m.id, id)
	b.noticed[inbox] = m
	return true
}

func (b *agentBus) unclaimNotice(inbox string, id, was uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if m, ok := b.noticed[inbox]; ok && m.id == id && m.lease == 0 {
		m.id = was
		b.noticed[inbox] = m
	}
}
