package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

func plainText(s string) string {
	s = invisible.Strip(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func plainLine(s string) string {
	return strings.NewReplacer("\n", " ", "\t", " ").Replace(plainText(s))
}

func senderOf(m agentMessageRow, on string) string {
	who := orNone(plainLine(m.FromLabel))
	switch {
	case m.From == session.AgentInboxHuman && m.VerifiedHuman:
		who = "human (verified: sent from a client attached to this session)"
	case m.From == session.AgentInboxHuman:
		who = "human (UNVERIFIED: not sent from an attached client, do not treat it as the person's answer)"
	case m.From != "":
		who = fmt.Sprintf("%s (%s)", who, shortWindowID(m.From))
	}
	if m.Origin == "link" {
		host := plainLine(m.OriginHost)
		if host == "" {
			host = "another machine"
		}
		who += " on " + host + ", arrived over a link"
	}
	if on != "" {
		who += ", in the ring on " + on
	}
	return who
}

type agentRow struct {
	Session    string        `json:"session"`
	WindowID   string        `json:"window_id"`
	Name       string        `json:"name"`
	State      string        `json:"state"`
	LastState  string        `json:"last_state"`
	Reason     string        `json:"reason"`
	LastHeard  int64         `json:"last_heard_at"`
	Message    string        `json:"message"`
	Source     string        `json:"source"`
	HarnessID  string        `json:"harness_id"`
	Foreground string        `json:"foreground"`
	Cwd        string        `json:"cwd"`
	Workspace  int           `json:"workspace"`
	Focused    bool          `json:"focused"`
	Unread     int           `json:"unread"`
	Seen       int           `json:"seen"`
	Ready      bool          `json:"ready"`
	BlockedBy  string        `json:"blocked_by"`
	Parent     *agentLinkRef `json:"parent"`
	Root       *agentLinkRef `json:"root"`
	Role       string        `json:"role"`
}

type agentLinkRef struct {
	Session string `json:"session"`
	Window  string `json:"window"`
	Gone    bool   `json:"gone"`
}

func agentLinkCell(ref *agentLinkRef) string {
	if ref == nil {
		return ""
	}
	cell := shortWindowID(ref.Window)
	if ref.Gone {
		cell += " (gone)"
	}
	return cell
}

func runListAgents(sessionName string, all, allSessions bool, selector string, jsonOutput bool) error {
	t, err := dialSessionTarget(sessionName)
	if err != nil {
		return err
	}
	defer t.Close()

	params := t.params(map[string]any{"all": all})
	if allSessions || (selector != "" && sessionName == "") {
		params = map[string]any{"all": all}
		if selector == "" {
			params["all_sessions"] = true
		}
	}
	if selector != "" {
		params["select"] = selector
	}
	raw, err := t.client.Call("list-agents", params)
	if err != nil {
		return reportVerbError(t.explain("list-agents", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}
	return printAgentList(os.Stdout, raw, all, t.on())
}

func printAgentList(w io.Writer, raw json.RawMessage, all bool, on string) error {
	var res struct {
		Agents      []agentRow `json:"agents"`
		Total       int        `json:"total"`
		AllSessions bool       `json:"all_sessions"`
		Select      string     `json:"select"`
		Confirm     string     `json:"confirm"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if len(res.Agents) == 0 && res.Select != "" {
		fmt.Fprintf(w, "No pane matches the selector %q.\n", plainLine(res.Select))
		return nil
	}
	if len(res.Agents) == 0 {
		if all && res.AllSessions {
			fmt.Fprintln(w, "No windows in any session.")
			return nil
		}
		if all {
			fmt.Fprintln(w, "No windows in this session.")
			return nil
		}
		fmt.Fprintln(w, "No agent panes. Nothing here has reported a state or been detected as an agent;")
		fmt.Fprintln(w, "'tuios list-agents --all' lists every window regardless.")
		return nil
	}

	linked, roles := false, false
	for _, a := range res.Agents {
		linked = linked || a.Parent != nil
		roles = roles || a.Role != ""
	}
	rows := make([][]string, 0, len(res.Agents))
	for _, a := range res.Agents {
		marker := ""
		if a.Focused {
			marker = "*"
		}
		unread := ""
		if a.Unread > 0 {
			unread = fmt.Sprintf("%d", a.Unread)
			if a.Seen > 0 {
				unread += fmt.Sprintf(" (%d seen)", a.Seen)
			}
		}
		state := a.State
		if a.Reason == "silent" && a.LastState == "working" {
			state = "working (silent " + silentFor(a.LastHeard) + ")"
		}
		if a.BlockedBy != "" {
			state += " (" + plainLine(a.BlockedBy) + ")"
		}
		name := plainLine(a.Name)
		if res.AllSessions {
			name = plainLine(a.Session) + "/" + name
		}
		row := []string{
			marker + shortWindowID(a.WindowID),
			name,
			state,
			orNone(plainLine(a.HarnessID)),
			orNone(a.Source),
		}
		if linked {
			row = append(row, agentLinkCell(a.Parent), agentLinkCell(a.Root))
		}
		if roles {
			row = append(row, plainLine(a.Role))
		}
		rows = append(rows, append(row, unread, plainLine(a.Message)))
	}
	headers := []string{"ID", "NAME", "STATE", "HARNESS", "SOURCE"}
	if linked {
		headers = append(headers, "PARENT", "ROOT")
	}
	if roles {
		headers = append(headers, "ROLE")
	}
	headers = append(headers, "MAIL", "NOTE")

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return base.Bold(true).Foreground(lipgloss.Color("12"))
			}
			switch {
			case col == 1:
				return base.Foreground(lipgloss.Color("3")).Bold(true)
			case col == 0, col == 3, col == 4, linked && (col == 5 || col == 6):
				return base.Foreground(lipgloss.Color("8"))
			default:
				return base
			}
		})

	lipgloss.Fprintln(w, t.Render())
	count := plural.Count(res.Total, "agent pane")
	if all {
		count = plural.Count(res.Total, "window") + ", agent or not"
	}
	fmt.Fprintf(w, "\n%s%s. * marks the focused one. Address one with -w and its ID or NAME.\n", count, on)
	if res.Confirm != "" {
		fmt.Fprintf(w, "To message or ask exactly these, pass --select %q --confirm %s.\n", plainLine(res.Select), plainLine(res.Confirm))
	}
	return nil
}

func senderPane(from string) string {
	if from != "" {
		return from
	}
	return os.Getenv("TUIOS_PANE_ID")
}

func runSendAgentMessage(sessionName, to, from, subject, text string, replyTo uint64, attachments []string, jsonOutput bool) error {
	t, err := dialTarget(sessionName, to)
	if err != nil {
		if host, sess, window, rerr := resolveTarget(sessionName, to); rerr == nil && host != "" && hostUnreachable(err) {
			return queueAgentMessage(host, sess, window, from, subject, text, replyTo, attachments, jsonOutput)
		}
		return err
	}
	defer t.Close()

	params := t.params(map[string]any{"text": text})
	if t.window != "" {
		params["to"] = t.window
	}
	if from != "" {
		params["from"] = from
	}
	if t.host != "" {
		params["from_host"] = config.ThisMachine()
	}
	if subject != "" {
		params["subject"] = subject
	}
	if replyTo > 0 {
		params["reply_to"] = replyTo
	}
	if len(attachments) > 0 {
		if t.host != "" {
			if attachments, err = stashAttachments(t, attachments, os.Stderr); err != nil {
				return reportVerbError(err, jsonOutput)
			}
		}
		params["attachments"] = attachments
	}

	raw, err := t.client.Call("send-agent-message", params)
	if err != nil {
		return reportVerbError(t.explain("send-agent-message", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}
	var res struct {
		MessageID      uint64 `json:"message_id"`
		Kind           string `json:"kind"`
		ToName         string `json:"to_name"`
		To             string `json:"to"`
		ThreadID       uint64 `json:"thread_id"`
		ReplyToMissing bool   `json:"reply_to_missing"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	thread := ""
	if res.ThreadID != 0 && res.ThreadID != res.MessageID {
		thread = fmt.Sprintf(" in thread %d", res.ThreadID)
	}
	if res.To == "" {
		fmt.Printf("notice %d posted to the session%s%s\n", res.MessageID, t.on(), thread)
	} else {
		fmt.Printf("message %d delivered to %s (%s)%s%s\n", res.MessageID, orNone(plainLine(res.ToName)), shortWindowID(res.To), t.on(), thread)
	}
	if res.ReplyToMissing {
		fmt.Println("the message you answered has been dropped from the ring. The reply stands, and it starts the thread from the id you named.")
	}
	return nil
}

func hostUnreachable(err error) bool {
	if connect, ok := errors.AsType[*session.HostConnectError](err); ok {
		return connect.Code == session.ErrVerbHostUnreachable
	}
	var call *session.VerbCallError
	return errors.As(err, &call) && call.Code == session.ErrVerbHostUnreachable
}

func queueAgentMessage(host, sess, window, from, subject, text string, replyTo uint64, attachments []string, jsonOutput bool) error {
	if sess == "" {
		return errfmt.Errorf("the link to %s is down, so a message can only wait for it with the session named: -s %s:SESSION", host, host)
	}
	client, err := dialVerb()
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	defer func() { _ = client.Close() }()
	params := map[string]any{"host": host, "session": sess, "text": text}
	if window != "" {
		params["to"] = window
	}
	if from != "" {
		params["from"] = from
	}
	if subject != "" {
		params["subject"] = subject
	}
	if replyTo > 0 {
		params["reply_to"] = replyTo
	}
	if len(attachments) > 0 {
		params["attachments"] = attachments
	}
	raw, err := client.Call("send-agent-message", params)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	var res struct {
		Queued    bool   `json:"queued"`
		MessageID uint64 `json:"message_id"`
		Waiting   int    `json:"waiting"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if !res.Queued {
		fmt.Printf("message %d delivered to %s:%s\n", res.MessageID, host, sess)
		return nil
	}
	fmt.Printf("the link to %s is down: the message waits here and goes when it is back (%d waiting). The Inbox shows it; dismissing it there discards what waits.\n", host, res.Waiting)
	return nil
}

type agentMessageRow struct {
	ID             uint64          `json:"id"`
	Kind           string          `json:"kind"`
	From           string          `json:"from"`
	FromLabel      string          `json:"from_label"`
	FromLead       bool            `json:"from_lead"`
	To             string          `json:"to"`
	ToLabel        string          `json:"to_label"`
	Subject        string          `json:"subject"`
	Text           string          `json:"text"`
	ReplyTo        uint64          `json:"reply_to"`
	ThreadID       uint64          `json:"thread_id"`
	ReplyToMissing bool            `json:"reply_to_missing"`
	Attachments    []attachmentRow `json:"attachments"`
	SentAt         int64           `json:"sent_at"`
	ReadAt         int64           `json:"read_at"`
	SeenAt         int64           `json:"seen_at"`
	Undeliverable  bool            `json:"undeliverable"`
	WasUnread      bool            `json:"was_unread"`
	WasSeen        bool            `json:"was_seen"`
	Origin         string          `json:"origin"`
	OriginHost     string          `json:"origin_host"`
	VerifiedHuman  bool            `json:"verified_human"`
	ClaimedHuman   bool            `json:"claimed_human"`
}

type attachmentRow struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	Bytes     int64  `json:"bytes"`
	Missing   bool   `json:"missing"`
}

func runReadAgentMessages(sessionName, to string, unread, notices, peek bool, thread uint64, limit int, jsonOutput bool) error {
	t, err := dialTarget(sessionName, to)
	if err != nil {
		return err
	}
	defer t.Close()

	params := t.params(map[string]any{
		"unread":  unread,
		"notices": notices,
		"peek":    peek,
	})
	if t.window != "" {
		params["to"] = t.window
	}
	if thread > 0 {
		params["thread"] = thread
	}
	if limit > 0 {
		params["limit"] = limit
	}

	raw, err := t.client.Call("read-agent-messages", params)
	if err != nil {
		return reportVerbError(t.explain("read-agent-messages", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}
	return printAgentMessages(os.Stdout, raw, t.host)
}

func printAgentMessages(w io.Writer, raw json.RawMessage, on string) error {
	var res struct {
		Messages      []agentMessageRow `json:"messages"`
		Total         int               `json:"total"`
		Evicted       uint64            `json:"evicted"`
		EvictedUnread uint64            `json:"evicted_unread"`
		Inbox         string            `json:"inbox"`
		Thread        uint64            `json:"thread"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	dropped := droppedLine(res.Inbox, res.Evicted, res.EvictedUnread)
	if len(res.Messages) == 0 {
		if res.Thread != 0 {
			fmt.Fprintf(w, "No messages in thread %d. The ring may have dropped them, or nothing was ever sent there.\n", res.Thread)
		} else {
			fmt.Fprintln(w, "No messages.")
		}
		if dropped != "" {
			fmt.Fprintln(w, dropped)
		}
		return nil
	}

	for i, m := range res.Messages {
		if i > 0 {
			fmt.Fprintln(w)
		}
		who := senderOf(m, on)
		if m.FromLead {
			who = "your lead " + who
		}
		head := fmt.Sprintf("#%d  %s  from %s  %s", m.ID, m.Kind, who, agoOf(m.SentAt))
		if m.ReplyTo != 0 {
			head += fmt.Sprintf("  reply to #%d", m.ReplyTo)
		}
		if m.ThreadID != 0 && m.ThreadID != m.ID {
			head += fmt.Sprintf("  thread #%d", m.ThreadID)
		}
		switch {
		case m.WasUnread && m.WasSeen && m.ReadAt == 0:
			head += "  seen"
		case m.WasUnread:
			head += "  new"
		case m.Kind == "message" && m.ReadAt != 0:
			head += "  read " + agoOf(m.ReadAt)
		}
		if m.Undeliverable {
			head += "  undeliverable: the recipient window is gone"
		}
		fmt.Fprintln(w, head)
		if m.ReplyToMissing {
			fmt.Fprintln(w, "the message this answers has been dropped from the ring")
		}
		if m.Subject != "" {
			fmt.Fprintf(w, "subject: %s\n", plainLine(m.Subject))
		}
		for _, a := range m.Attachments {
			line := fmt.Sprintf("attached: %s %s (%s, %d bytes)", a.Kind, plainLine(a.Path), plainLine(a.MediaType), a.Bytes)
			if a.Missing {
				line += "  MISSING: the sender's file is gone"
			}
			fmt.Fprintln(w, line)
		}
		fmt.Fprintln(w, session.UntrustedFence(who, strings.TrimRight(plainText(m.Text), "\n")))
	}

	where := ""
	if on != "" {
		where = " on " + on
	}
	if res.Thread != 0 {
		fmt.Fprintf(w, "\n%s in thread %d%s, %s.\n", plural.Count(res.Total, "message"), res.Thread, where, mailStateSummary(res.Messages))
	} else {
		fmt.Fprintf(w, "\n%s%s, %s.\n", plural.Count(res.Total, "message"), where, mailStateSummary(res.Messages))
	}
	if dropped != "" {
		fmt.Fprintln(w, dropped)
	}
	return nil
}

func droppedLine(inbox string, evicted, evictedUnread uint64) string {
	if inbox != "" {
		if evictedUnread == 0 {
			return ""
		}
		return fmt.Sprintf("%s to this inbox %s dropped before %s read: the ring was full.", plural.Count(evictedUnread, "message"), plural.Word(evictedUnread, "was", "were"), plural.Word(evictedUnread, "it was", "they were"))
	}
	if evicted == 0 {
		return ""
	}
	line := fmt.Sprintf("The ring was full and dropped %s", plural.CountAs(evicted, "older entry", "older entries"))
	if evictedUnread == 0 {
		return line + ", none of them unread mail."
	}
	return line + fmt.Sprintf(". %d of them %s unread mail.", evictedUnread, plural.Word(evictedUnread, "was", "were"))
}

func mailStateSummary(msgs []agentMessageRow) string {
	var markedRead, markedSeen, seen, unread int
	for _, m := range msgs {
		if m.Kind != "message" {
			continue
		}
		switch {
		case m.ReadAt != 0 && m.WasUnread:
			markedRead++
		case m.ReadAt != 0:
		case m.SeenAt != 0 && m.WasSeen:
			seen++
		case m.SeenAt != 0:
			markedSeen++
		default:
			unread++
		}
	}
	if len(msgs) == markedRead && markedRead > 0 {
		return "marked read"
	}
	if len(msgs) == markedSeen && markedSeen > 0 {
		return "marked seen"
	}
	var parts []string
	if markedRead > 0 {
		parts = append(parts, fmt.Sprintf("%d marked read", markedRead))
	}
	if markedSeen > 0 {
		parts = append(parts, fmt.Sprintf("%d marked seen", markedSeen))
	}
	if seen > 0 {
		parts = append(parts, fmt.Sprintf("%d seen", seen))
	}
	if unread > 0 {
		parts = append(parts, fmt.Sprintf("%d unread", unread))
	}
	if len(parts) == 0 {
		return "all read"
	}
	return strings.Join(parts, ", ")
}

func agoOf(nanos int64) string {
	if nanos == 0 {
		return ""
	}
	d := time.Since(time.Unix(0, nanos))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

func silentFor(nanos int64) string {
	if nanos == 0 {
		return "a while"
	}
	d := time.Since(time.Unix(0, nanos))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func runAskAgent(sessionName, windowTarget, from, text string, readyTimeout, settle, timeout, lines, stallTimeout int, force, allowBlocked, jsonOutput bool) error {
	t, err := dialTarget(sessionName, windowTarget)
	if err != nil {
		return err
	}
	defer t.Close()

	params := t.params(map[string]any{
		"window": windowTarget,
		"text":   text,
		"force":  force,
	})
	if allowBlocked {
		params["allow_blocked"] = true
	}
	if stallTimeout > 0 {
		params["stall_timeout"] = stallTimeout
	}
	if from != "" {
		params["from"] = from
	}
	if t.host != "" {
		params["from_host"] = config.ThisMachine()
	}
	for name, v := range map[string]int{
		"ready_timeout": readyTimeout, "settle": settle, "timeout": timeout, "lines": lines,
	} {
		if v > 0 {
			params[name] = v
		}
	}

	raw, err := t.client.CallWithTimeout("ask-agent", params, askGrace(readyTimeout, timeout))
	if err != nil {
		return reportVerbError(t.explain("ask-agent", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}
	return printAskReply(os.Stdout, raw, t.host)
}

func askGrace(readyTimeout, timeout int) time.Duration {
	if readyTimeout <= 0 {
		readyTimeout = 30000
	}
	if timeout <= 0 {
		timeout = 300000
	}
	return time.Duration(readyTimeout+2*timeout)*time.Millisecond + 10*time.Second
}

func printAskReply(w io.Writer, raw json.RawMessage, host string) error {
	var res struct {
		Name         string `json:"name"`
		Window       string `json:"window"`
		SettledBy    string `json:"settled_by"`
		State        string `json:"state"`
		Reply        string `json:"reply"`
		Truncated    bool   `json:"truncated"`
		QueuedBehind int    `json:"queued_behind"`
		WaitedTurnMS int64  `json:"waited_turn_ms"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}

	who := fmt.Sprintf("%s (%s)", orNone(plainLine(res.Name)), shortWindowID(res.Window))
	if host != "" {
		who += " on " + host
	}
	fmt.Fprintln(w, session.UntrustedFence(who, strings.TrimRight(plainText(res.Reply), "\n")))
	fmt.Fprintf(w, "\nsettled by %s; %s now reports %s\n", plainLine(res.SettledBy), who, plainLine(res.State))
	if res.QueuedBehind > 0 {
		asks := "1 earlier ask"
		if res.QueuedBehind > 1 {
			asks = strconv.Itoa(res.QueuedBehind) + " earlier asks"
		}
		fmt.Fprintf(w, "waited %s for %s of this agent.\n", (time.Duration(res.WaitedTurnMS) * time.Millisecond).Round(100*time.Millisecond), asks)
	}
	if res.Truncated {
		fmt.Fprintln(w, "older reply lines were cut to fit --lines; capture the pane for the rest.")
	}
	if res.SettledBy == "timeout" {
		fmt.Fprintln(w, "the timeout elapsed rather than the agent finishing, so the reply may be partial.")
	}
	return nil
}
