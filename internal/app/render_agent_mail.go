//go:build !slim

package app

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// agentMailWidth is the mail overlay's preferred inner width. Wider than the
// session switcher because a row carries two names and a subject.
const agentMailWidth = 66

// agentMailRows is how many conversation lines the thread view prefers to
// show. The screen height cuts it down like every other panel.
const agentMailRows = 16

// agentMailEmptyRows is the height of the empty mailbox's body: the rows its
// list would have, so opening it empty and then receiving mail does not
// resize the panel.
const agentMailEmptyRows = 10

// agentMailEmptyLines is the empty state: what this is, and what makes
// something appear here. It is shared with the test that pins it.
var agentMailEmptyLines = []string{
	"No mail.",
	"Agents leave messages here with tuios send-agent-message.",
	"An agent writes to you with: tuios send-agent-message -w human",
}

// agentMailLinkGlyph is the mark a row wears when a message in it arrived
// from another machine. It replaces the kind's mark, because where the mail
// came from matters more than what kind it is.
func agentMailLinkGlyph() string {
	if overlay.UseASCII() {
		return "~"
	}
	return "⇄"
}

// agentMailWho is how a thread row names who wrote to whom. The kind is said
// in words rather than with a mark of its own: the marks it used ("!" and "?"
// in ASCII) were already needs-you and unknown, and a notice with no sender
// read as "someone -> all".
func agentMailWho(th agentMailThread) string {
	arrow := " → "
	if overlay.UseASCII() {
		arrow = " -> "
	}
	switch th.Kind {
	case "ask":
		return th.From + " asks " + th.To
	case "notice":
		if th.Anonymous {
			return "Notice to " + th.To
		}
		return "Notice from " + th.From + " to " + th.To
	}
	return th.From + arrow + th.To
}

// renderAgentMail renders the mail overlay: the list of threads, or the open
// thread, on the shared overlay grammar.
func (m *OS) renderAgentMail() (string, overlay.Geometry, []overlayRowHit) {
	if !m.IsDaemonSession || m.DaemonClient == nil {
		return m.simpleOverlayPanel("Mail",
			[]string{"Mail needs the daemon.", "", "Start a daemon session with: tuios new"},
			[]overlay.Hint{{Key: "esc", Label: "close"}})
	}
	st := &m.AgentMail
	if st.Thread != 0 {
		return m.renderAgentMailThread()
	}
	if st.Picking {
		return m.renderAgentMailPicker()
	}
	if st.Composing && st.ComposeTo != "" {
		return m.renderAgentMailCompose()
	}

	title := "Mail"
	if st.Inbox != "" {
		title = "Mail: " + m.agentMailWindowName(st.Inbox)
	}
	if st.Evicted > 0 {
		title += " (" + strconv.FormatUint(st.Evicted, 10) + " older dropped)"
	}

	threads := m.agentMailThreads()
	now := time.Now()
	if len(threads) == 0 && !st.Loading {
		lines := agentMailEmptyLines
		if st.Inbox != "" {
			lines = []string{"No mail for " + m.agentMailWindowName(st.Inbox) + ".", agentMailEmptyLines[1]}
		}
		if st.Error != "" {
			lines = append(append([]string{}, lines...), "", st.Error)
		}
		close := overlay.Hint{Key: "esc", Label: "close"}
		hints := append(m.keyHints(config.ActionMailNew, "new message"), close)
		return m.emptyPanel(title, agentMailWidth, agentMailEmptyRows, lines[0], lines[1:], close, hints)
	}
	if len(threads) > 0 {
		st.Selected = clampInt(st.Selected, 0, len(threads)-1)
	}
	return m.renderListOverlay(listOverlay{
		Title:      title,
		Width:      agentMailWidth,
		MaxVisible: 10,
		Count:      len(threads),
		Selected:   st.Selected,
		Scroll:     &st.Scroll,
		EmptyMsg:   "Reading mail",
		Pending:    st.Loading && !overlay.ShowLoading(st.LoadingSince, now),
		Hints:      m.keyHints(config.ActionMailOpen, "open", config.ActionMailNew, "new", config.ActionMailBack, "close"),
		DetailFor:  m.agentMailErrorLines,
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			return m.agentMailThreadRow(threads[i], selected, rowBg, pal, width, now)
		},
	})
}

// agentMailErrorLines is the last failure, under the list of threads.
func (m *OS) agentMailErrorLines(width int) []string {
	if m.AgentMail.Error == "" {
		return nil
	}
	pal := theme.UI()
	var out []string
	for _, l := range wrapPlain(m.AgentMail.Error, max(width, 1)) {
		out = append(out, overlay.Style(pal.Surface).Foreground(pal.Warn).Render(l))
	}
	return out
}

// renderAgentMailPicker lists the agents of this session a new message can
// go to. Enter chooses one and opens the message line.
func (m *OS) renderAgentMailPicker() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.AgentMail
	targets := m.agentMailComposeTargets()
	if len(targets) > 0 {
		st.PickSelected = clampInt(st.PickSelected, 0, len(targets)-1)
	}
	return m.renderListOverlay(listOverlay{
		Title:      "New message: choose an agent",
		Width:      agentMailWidth,
		MaxVisible: 10,
		Count:      len(targets),
		Selected:   st.PickSelected,
		Scroll:     &st.PickScroll,
		EmptyMsg:   "No agent runs in this session.",
		Hints:      m.keyHints(config.ActionMailOpen, "write", config.ActionMailBack, "back"),
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			w := targets[i]
			right := overlay.Style(rowBg).Foreground(pal.FgMute).Render(w.AgentState)
			labelColor := pal.FgDim
			if selected {
				labelColor = pal.Fg
			}
			// The short id tells apart two panes with the same title.
			left := overlay.Style(rowBg).Foreground(labelColor).Render(m.agentMailWindowName(w.ID)) +
				overlay.Style(rowBg).Foreground(pal.FgMute).Render("  "+shortWindowLabel(w.ID))
			return listRowSpans(width, listRowMarker(selected), left, right, rowBg, pal)
		},
	})
}

// renderAgentMailCompose draws the message line for a new message to one
// agent. The message starts a thread of its own.
func (m *OS) renderAgentMailCompose() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.AgentMail
	pal := theme.UI()
	bg := pal.Surface
	width := m.panelWidth(agentMailWidth)
	mute := overlay.Style(bg).Foreground(pal.FgMute)
	name := m.agentMailWindowName(st.ComposeTo)

	lines := []string{
		mute.Render(overlay.Truncate("  The message goes to "+name+" from you, in a new thread.", width)),
		mute.Render(overlay.Truncate("  "+name+" reads it with read-agent-messages.", width)),
	}
	hints := []overlay.Hint{{Key: overlay.EnterKey(), Label: "send"}, {Key: "esc", Label: "cancel"}}
	extra := 2
	if st.Error != "" {
		extra++
	}
	rows, hints := m.panelBody(len(lines)+1, extra, width, nil, hints)
	body := append([]string{}, lines[:min(len(lines), rows)]...)
	for len(body) < rows {
		body = append(body, "")
	}
	body = append(body, overlay.Rule(width, bg, pal), m.agentMailPromptLine("message: ", width))
	if st.Error != "" {
		body = append(body, overlay.Style(bg).Foreground(pal.Warn).Render(overlay.Truncate(st.Error, width)))
	}
	panel := overlay.Panel{
		Title: overlay.Truncate("New message to "+name, max(width-4, 4)),
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	return content, geo, nil
}

// agentMailPromptLine is the line a draft is typed on: the prompt, the end of
// the draft that fits, and the cursor. The prompt says when the draft is being
// sent, and when keys from outside the keyboard touched it.
func (m *OS) agentMailPromptLine(prompt string, width int) string {
	st := &m.AgentMail
	pal := theme.UI()
	bg := pal.Surface
	if st.Sending {
		prompt = "sending: "
	}
	// Said in words, not only in colour: a draft that keys routed from
	// outside touched is sent as a claim, not as the person's answer.
	if st.DraftAutomated {
		prompt = "automated " + prompt
	}
	return overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil()) +
		overlay.Style(bg).Foreground(pal.FgMute).Render(prompt) +
		overlay.Style(bg).Foreground(pal.Fg).Render(agentMailDraftTail(st.Draft, width-lipgloss.Width(prompt)-4)) +
		overlay.Cursor(" ", bg, pal.Fg)
}

// agentMailWindowName is what to call a window the overlay was opened for.
func (m *OS) agentMailWindowName(windowID string) string {
	if windowID == session.AgentInboxHuman {
		return "you"
	}
	if w := m.windowByID(windowID); w != nil {
		if name := printableTitle(m.railTitleShown(w)); name != "" {
			return agentMailNotThePerson(windowID, name)
		}
	}
	return shortWindowLabel(windowID)
}

// agentMailThreadRow draws one conversation: its kind, who to whom, the
// subject, and on the right how many messages and how old the newest is. A
// thread with mail waiting for the person is bold; one with a message the
// person has not seen carries "new".
func (m *OS) agentMailThreadRow(th agentMailThread, selected bool, rowBg color.Color, pal overlay.Palette, width int, now time.Time) string {
	right := overlay.Style(rowBg).Foreground(pal.FgMute).Render(strconv.Itoa(th.Count) + " · " + agentMailAge(th.LastAt, now))
	switch {
	case th.Unread:
		right = overlay.Style(rowBg).Foreground(pal.AccentBright).Bold(true).Render("unread  ") + right
	case th.New:
		right = overlay.Style(rowBg).Foreground(pal.Accent).Render("new  ") + right
	}

	who := agentMailWho(th)
	glyph := sidebarMailGlyph() + " "
	if th.Link {
		glyph = agentMailLinkGlyph() + " "
	}
	// The thread id is the one the CLI prints and takes (read-agent-messages
	// --thread), so the person can name a conversation to an agent.
	id := agentMailThreadLabel(th.ID) + " "

	avail := max(width-lipgloss.Width(right)-lipgloss.Width(glyph)-lipgloss.Width(id)-4, 1)
	whoW := min(lipgloss.Width(who), avail)
	subjectW := max(avail-whoW-2, 0)

	labelColor := pal.FgDim
	if selected || th.Unread {
		labelColor = pal.Fg
	}
	left := overlay.Style(rowBg).Foreground(pal.FgMute).Render(glyph+id) +
		overlay.Style(rowBg).Foreground(labelColor).Bold(th.Unread).Render(overlay.Truncate(who, whoW))
	if subjectW >= 2 && th.Subject != "" {
		left += overlay.Style(rowBg).Foreground(pal.FgDim).Render("  " + overlay.Truncate(th.Subject, subjectW))
	}
	return listRowSpans(width, listRowMarker(selected), left, right, rowBg, pal)
}

// agentMailFenceOpen is the fence's opening line for a body from who, fitted
// to width. A line too long breaks after the sender's name, so each half still
// reads as a whole: who wrote it, then what it is.
func agentMailFenceOpen(who string, width int) []string {
	line := fmt.Sprintf(session.UntrustedOpen, who)
	if lipgloss.Width(line) <= width {
		return []string{line}
	}
	// Cut on the known suffix, not the first ": ": the name is the sender's
	// and can hold ": " itself.
	head, ok := strings.CutSuffix(line, session.UntrustedOpenSuffix)
	if !ok {
		return wrapPlain(line, width)
	}
	return append(wrapPlain(head+":", width), wrapPlain(strings.TrimPrefix(session.UntrustedOpenSuffix, ": "), width)...)
}

// agentMailGutter is the mark every body line starts with, in the glyph set
// the client draws in.
func agentMailGutter() string {
	if overlay.UseASCII() {
		return session.UntrustedGutterASCII
	}
	return session.UntrustedGutter
}

// agentMailFenceBody is a message body as the lines drawn after the gutter,
// each at most width minus the gutter wide. A line the panel wraps keeps the
// gutter on every part, so no part of the body starts a screen line the way a
// line outside the fence does.
func agentMailFenceBody(body string, width int) []string {
	avail := max(width-lipgloss.Width(agentMailGutter()), 1)
	var out []string
	for _, raw := range session.UntrustedBodyLines(body, "") {
		out = append(out, wrapPlain(printableTitle(raw), avail)...)
	}
	return out
}

// agentMailThreadLabel is how a thread id reads on screen: "#12", the way
// read-agent-messages prints it.
func agentMailThreadLabel(id uint64) string {
	return "#" + strconv.FormatUint(id, 10)
}

// renderAgentMailThread draws one conversation oldest first, each message
// under a line naming who wrote it to whom and how long ago, with the reply
// line at the foot while one is being written.
func (m *OS) renderAgentMailThread() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.AgentMail
	pal := theme.UI()
	bg := pal.Surface
	width := m.panelWidth(agentMailWidth)
	now := time.Now()
	msgs := m.agentMailThreadMessages(st.Thread)

	arrow := " → "
	if overlay.UseASCII() {
		arrow = " -> "
	}
	mute := overlay.Style(bg).Foreground(pal.FgMute)
	dim := overlay.Style(bg).Foreground(pal.FgDim)
	strong := overlay.Style(bg).Foreground(pal.Fg).Bold(true)
	caution := overlay.Style(bg).Foreground(pal.Warning)

	var lines []string
	title := "Mail"
	for i, mm := range msgs {
		if i == 0 {
			title = "Mail " + agentMailThreadLabel(st.Thread) + ": " + agentMailSummary(mm)
		}
		if i > 0 {
			lines = append(lines, "")
		}
		who := agentMailSender(mm) + arrow + agentMailName(mm.To, mm.ToLabel, true)
		if mm.Kind == "ask" {
			who = agentMailSender(mm) + " asked " + agentMailName(mm.To, mm.ToLabel, true)
		}
		age := agentMailAge(mm.SentAt, now)
		if mm.Kind == "message" && mm.To == session.AgentInboxHuman && mm.ReadAt == 0 {
			age = "unread · " + age
		}
		gap := max(width-lipgloss.Width(who)-lipgloss.Width(age)-1, 1)
		lines = append(lines, strong.Render(overlay.Truncate(who, max(width-lipgloss.Width(age)-2, 1)))+
			mute.Render(strings.Repeat(" ", gap)+age))
		if agentMailFromLink(mm) {
			// Said in words under the header, not only in the name: a
			// message from another machine was written by a program this
			// machine's owner does not run.
			lines = append(lines, caution.Render(overlay.Truncate("  "+agentMailLinkGlyph()+" from "+agentMailOriginHost(mm)+", over a link. Written on another machine.", width)))
		}
		if mm.Subject != "" {
			for _, l := range wrapPlain(printableTitle(mm.Subject), width-2) {
				lines = append(lines, dim.Render("  "+l))
			}
		}
		body := strings.TrimRight(mm.Text, "\n")
		if mm.Kind == "ask" && body == "" {
			body = "(the pane printed nothing)"
		}
		// The body sits inside the fence the CLI prints, so the person reads
		// the same frame an agent reading the same mail does: what is inside
		// was written by another program.
		for _, l := range agentMailFenceOpen(agentMailSender(mm), width-2) {
			lines = append(lines, mute.Render("  "+l))
		}
		gutter := agentMailGutter()
		for _, l := range agentMailFenceBody(body, width-2) {
			lines = append(lines, mute.Render("  "+gutter)+dim.Render(l))
		}
		lines = append(lines, mute.Render("  "+session.UntrustedClose))
		if mm.Kind == "ask" && mm.SettledBy != "" {
			lines = append(lines, mute.Render("  settled by "+mm.SettledBy))
		}
		for _, att := range mm.Attachments {
			note := ""
			if att.Missing {
				note = " (missing)"
			}
			lines = append(lines, mute.Render(overlay.Truncate("  + "+att.Path+note, width)))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, mute.Render("  This thread is empty."))
	}

	var hints []overlay.Hint
	extra := 0
	if st.Composing {
		hints = []overlay.Hint{{Key: overlay.EnterKey(), Label: "send"}, {Key: "esc", Label: "cancel"}}
		extra += 2
	} else {
		hints = m.keyHints(config.ActionMailReply, "reply", config.ActionMailFocusPane, "open pane")
		if down, up := m.inboxKey(config.ActionMailDown), m.inboxKey(config.ActionMailUp); down != "" && up != "" {
			hints = append(hints, overlay.Hint{Key: down + "/" + up, Label: "scroll"})
		}
		hints = append(hints, m.keyHints(config.ActionMailBack, "back")...)
	}
	if st.Error != "" {
		extra++
	}
	// A short conversation gets a short panel; a long one scrolls inside the
	// rows the screen can hold.
	rows, hints := m.panelBody(clampInt(len(lines), 6, agentMailRows), extra, width, nil, hints)
	st.Scroll = clampInt(st.Scroll, 0, max(len(lines)-rows, 0))
	end := min(st.Scroll+rows, len(lines))
	body := append([]string{}, lines[st.Scroll:end]...)
	for len(body) < rows {
		body = append(body, "")
	}
	if st.Composing {
		body = append(body, overlay.Rule(width, bg, pal), m.agentMailPromptLine("reply: ", width))
	}
	if st.Error != "" {
		body = append(body, overlay.Style(bg).Foreground(pal.Warn).Render(overlay.Truncate(st.Error, width)))
	}

	panel := overlay.Panel{
		Title: overlay.Truncate(title, max(width-4, 4)),
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	return content, geo, nil
}

// agentMailDraftTail is the end of the draft that fits beside the prompt, so
// the cursor stays on screen while a long reply is typed.
func agentMailDraftTail(draft string, avail int) string {
	if avail < 1 {
		return ""
	}
	r := []rune(draft)
	if len(r) <= avail {
		return draft
	}
	return string(r[len(r)-avail:])
}
