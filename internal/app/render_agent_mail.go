package app

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
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

// agentMailGlyph is the mark a row wears for its kind.
func agentMailGlyph(kind string) string {
	if overlay.UseASCII() {
		switch kind {
		case "notice":
			return "!"
		case "ask":
			return "?"
		default:
			return "@"
		}
	}
	switch kind {
	case "notice":
		return "◆"
	case "ask":
		return "?"
	default:
		return "✉"
	}
}

// renderAgentMail renders the mail overlay: the list of threads, or the open
// thread, on the shared overlay grammar.
func (m *OS) renderAgentMail() (string, overlay.Geometry, []overlayRowHit) {
	if !m.IsDaemonSession || m.DaemonClient == nil {
		return m.simpleOverlayPanel("", "Mail",
			[]string{"Mail needs the daemon.", "", "Start a daemon session with: tuios new"},
			[]overlay.Hint{{Key: "esc", Label: "close"}})
	}
	st := &m.AgentMail
	if st.Thread != 0 {
		return m.renderAgentMailThread()
	}

	title := "Mail"
	if st.Inbox != "" {
		title = "Mail: " + m.agentMailWindowName(st.Inbox)
	}
	if st.Evicted > 0 {
		title += " (" + strconv.FormatUint(st.Evicted, 10) + " older dropped)"
	}

	threads := m.agentMailThreads()
	if len(threads) == 0 && !st.Loading {
		lines := agentMailEmptyLines
		if st.Inbox != "" {
			lines = []string{"No mail for " + m.agentMailWindowName(st.Inbox) + ".", agentMailEmptyLines[1]}
		}
		if st.Error != "" {
			lines = append(append([]string{}, lines...), "", st.Error)
		}
		return m.simpleOverlayPanel("", title, lines, []overlay.Hint{{Key: "esc", Label: "close"}})
	}
	if len(threads) > 0 {
		st.Selected = clampInt(st.Selected, 0, len(threads)-1)
	}
	now := time.Now()
	return m.renderListOverlay(listOverlay{
		Title:      title,
		Width:      agentMailWidth,
		MaxVisible: 10,
		Count:      len(threads),
		Selected:   st.Selected,
		Scroll:     &st.Scroll,
		EmptyMsg:   "Reading mail",
		Hints: []overlay.Hint{
			{Key: overlay.EnterKey(), Label: "open"},
			{Key: "esc", Label: "close"},
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			return m.agentMailThreadRow(threads[i], selected, rowBg, pal, width, now)
		},
	})
}

// agentMailWindowName is what to call a window the overlay was opened for.
func (m *OS) agentMailWindowName(windowID string) string {
	if windowID == session.AgentInboxHuman {
		return "you"
	}
	if w := m.windowByID(windowID); w != nil {
		if name := printableTitle(m.railTitleShown(w)); name != "" {
			return name
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

	arrow := " → "
	if overlay.UseASCII() {
		arrow = " -> "
	}
	who := th.From + arrow + th.To
	glyph := agentMailGlyph(th.Kind) + " "
	if th.Link {
		glyph = agentMailLinkGlyph() + " "
	}

	avail := max(width-lipgloss.Width(right)-lipgloss.Width(glyph)-4, 1)
	whoW := min(lipgloss.Width(who), avail)
	subjectW := max(avail-whoW-2, 0)

	labelColor := pal.FgDim
	if selected || th.Unread {
		labelColor = pal.Fg
	}
	left := overlay.Style(rowBg).Foreground(pal.FgMute).Render(glyph) +
		overlay.Style(rowBg).Foreground(labelColor).Bold(th.Unread).Render(overlay.Truncate(who, whoW))
	if subjectW >= 2 && th.Subject != "" {
		left += overlay.Style(rowBg).Foreground(pal.FgDim).Render("  " + overlay.Truncate(th.Subject, subjectW))
	}
	return listRowSpans(width, listRowMarker(selected), left, right, rowBg, pal)
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
			title = "Mail: " + agentMailSummary(mm)
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
		for _, raw := range strings.Split(body, "\n") {
			for _, l := range wrapPlain(printableTitle(raw), width-2) {
				lines = append(lines, dim.Render("  "+l))
			}
		}
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
		hints = []overlay.Hint{
			{Key: "r", Label: "reply"},
			{Key: "o", Label: "open pane"},
			{Key: "j/k", Label: "scroll"},
			{Key: "esc", Label: "back"},
		}
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
		prompt := "reply: "
		if st.Sending {
			prompt = "sending: "
		}
		body = append(body, overlay.Rule(width, bg, pal),
			overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil())+
				mute.Render(prompt)+
				overlay.Style(bg).Foreground(pal.Fg).Render(agentMailDraftTail(st.Draft, width-lipgloss.Width(prompt)-4))+
				overlay.Cursor(" ", bg, pal.Fg))
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
