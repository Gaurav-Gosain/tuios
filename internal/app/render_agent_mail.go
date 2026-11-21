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
)

const agentMailWidth = 66

const agentMailRows = 16

const agentMailEmptyRows = 10

var agentMailEmptyLines = []string{
	"No mail.",
	"Agents leave messages here with tuios send-agent-message.",
	"An agent writes to you with: tuios send-agent-message -w human",
}

func agentMailLinkGlyph() string {
	if overlay.UseASCII() {
		return "~"
	}
	return "⇄"
}

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
	if st.Inbox == "" && st.Evicted > 0 {
		dropped := strconv.FormatUint(st.Evicted, 10) + " older dropped"
		if st.EvictedUnread > 0 {
			dropped += ", " + strconv.FormatUint(st.EvictedUnread, 10) + " unread"
		}
		title += " (" + dropped + ")"
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
		st.List.Cursor = clampInt(st.List.Cursor, 0, len(threads)-1)
	}
	return m.renderListOverlay(listOverlay{
		Title:      title,
		Width:      agentMailWidth,
		MaxVisible: 10,
		Count:      len(threads),
		Selected:   st.List.Cursor,
		Scroll:     &st.List.Scroll,
		EmptyMsg:   "Reading mail",
		Pending:    st.Loading && !overlay.ShowLoading(st.LoadingSince, now),
		Hints:      m.keyHints(config.ActionMailOpen, "open", config.ActionMailNew, "new", config.ActionMailBack, "close"),
		DetailFor:  m.agentMailErrorLines,
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			return m.agentMailThreadRow(threads[i], selected, rowBg, pal, width, now)
		},
	})
}

func (m *OS) agentMailErrorLines(width int) []string {
	if m.AgentMail.Error == "" {
		return nil
	}
	pal := m.overlayUI()
	var out []string
	for _, l := range wrapPlain(m.AgentMail.Error, max(width, 1)) {
		out = append(out, overlay.Style(pal.Surface).Foreground(pal.Warn).Render(l))
	}
	return out
}

func (m *OS) renderAgentMailPicker() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.AgentMail
	targets := m.agentMailComposeTargets()
	if len(targets) > 0 {
		st.Pick.Cursor = clampInt(st.Pick.Cursor, 0, len(targets)-1)
	}
	return m.renderListOverlay(listOverlay{
		Title:      "New message: choose an agent",
		Width:      agentMailWidth,
		MaxVisible: 10,
		Count:      len(targets),
		Selected:   st.Pick.Cursor,
		Scroll:     &st.Pick.Scroll,
		EmptyMsg:   "No agent runs in this session.",
		Hints:      m.keyHints(config.ActionMailOpen, "write", config.ActionMailBack, "back"),
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			w := targets[i]
			right := overlay.Style(rowBg).Foreground(pal.FgMute).Render(w.AgentState)
			labelColor := pal.FgDim
			if selected {
				labelColor = pal.Fg
			}
			left := overlay.Style(rowBg).Foreground(labelColor).Render(m.agentMailWindowName(w.ID)) +
				overlay.Style(rowBg).Foreground(pal.FgMute).Render("  "+shortWindowLabel(w.ID))
			return listRowSpans(width, listRowMarker(selected), left, right, rowBg, pal)
		},
	})
}

func (m *OS) renderAgentMailCompose() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.AgentMail
	pal := m.overlayUI()
	bg := pal.Surface
	width := m.panelWidth(agentMailWidth)
	mute := overlay.Style(bg).Foreground(pal.FgMute)
	name := m.agentMailWindowName(st.ComposeTo)

	lines := []string{
		mute.Render(overlay.Truncate("  The message goes to "+name+" from you, in a new thread.", width)),
		mute.Render(overlay.Truncate("  "+name+" reads it with read-agent-messages.", width)),
	}
	hints := []overlay.Hint{{Key: overlay.EnterKey(), Label: "send"}, {Key: "esc", Label: "cancel"}}
	if st.Comp != nil {
		hints = composerHints(st.Comp.Hints(overlay.UseASCII()))
	}
	draft := m.agentMailDraftLines("message: ", width, hints)
	extra := 1 + len(draft)
	if st.Error != "" {
		extra++
	}
	rows, hints := m.panelBody(len(lines)+1, extra, width, nil, hints)
	body := append([]string{}, lines[:min(len(lines), rows)]...)
	for len(body) < rows {
		body = append(body, "")
	}
	body = append(body, overlay.Rule(width, bg, pal))
	body = append(body, draft...)
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
	m.agentMailDraftAt(geo, rows)
	return content, geo, nil
}

func (m *OS) agentMailDraftLines(prompt string, width int, hints []overlay.Hint) []string {
	st := &m.AgentMail
	pal := m.overlayUI()
	bg := pal.Surface
	if st.Sending {
		prompt = "sending: "
	}
	if st.draftAutomated() {
		prompt = "automated " + prompt
	}
	out := []string{overlay.Fill(overlay.Style(bg).Foreground(pal.FgMute).Render(strings.TrimSpace(prompt)), width, bg)}
	if st.Comp == nil {
		return out
	}
	if rh := m.panelRoomHeight(); rh > 0 {
		room := rh - panelChrome(0, width, nil, hints) - minPanelRows - 2
		if st.Error != "" {
			room--
		}
		st.Comp.SetRoom(max(room, 2))
	}
	st.Comp.SetMaxRows(m.composerRows(agentMailDraftRows))
	return append(out, st.Comp.Render(max(width, 8), composerStyles(pal), bg)...)
}

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

func (m *OS) agentMailThreadRow(th agentMailThread, selected bool, rowBg color.Color, pal overlay.Palette, width int, now time.Time) string {
	right := overlay.Style(rowBg).Foreground(pal.FgMute).Render(strconv.Itoa(th.Count) + " · " + agentMailAge(th.LastAt, now))
	switch {
	case th.Unread && !th.Untouched:
		right = overlay.Style(rowBg).Foreground(pal.AccentBright).Bold(true).Render("seen  ") + right
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

func agentMailFenceOpen(who string, width int) []string {
	line := fmt.Sprintf(session.UntrustedOpen, who)
	if lipgloss.Width(line) <= width {
		return []string{line}
	}
	head, ok := strings.CutSuffix(line, session.UntrustedOpenSuffix)
	if !ok {
		return wrapPlain(line, width)
	}
	return append(wrapPlain(head+":", width), wrapPlain(strings.TrimPrefix(session.UntrustedOpenSuffix, ": "), width)...)
}

func agentMailGutter() string {
	if overlay.UseASCII() {
		return session.UntrustedGutterASCII
	}
	return session.UntrustedGutter
}

func agentMailFenceBody(body string, width int) []string {
	avail := max(width-lipgloss.Width(agentMailGutter()), 1)
	var out []string
	for _, raw := range session.UntrustedBodyLines(body, "") {
		out = append(out, wrapPlain(printableTitle(raw), avail)...)
	}
	return out
}

func agentMailThreadLabel(id uint64) string {
	return "#" + strconv.FormatUint(id, 10)
}

func (m *OS) renderAgentMailThread() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.AgentMail
	pal := m.overlayUI()
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
			if mm.SeenAt != 0 {
				age = "seen · " + age
			} else {
				age = "unread · " + age
			}
		}
		gap := max(width-lipgloss.Width(who)-lipgloss.Width(age)-1, 1)
		lines = append(lines, strong.Render(overlay.Truncate(who, max(width-lipgloss.Width(age)-2, 1)))+
			mute.Render(strings.Repeat(" ", gap)+age))
		if agentMailFromLink(mm) {
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
	var draft []string
	if st.Composing {
		hints = []overlay.Hint{{Key: overlay.EnterKey(), Label: "send"}, {Key: "esc", Label: "cancel"}}
		if st.Comp != nil {
			hints = composerHints(st.Comp.Hints(overlay.UseASCII()))
		}
		draft = m.agentMailDraftLines("reply: ", width, hints)
		extra += 1 + len(draft)
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
	rows, hints := m.panelBody(clampInt(len(lines), 6, agentMailRows), extra, width, nil, hints)
	st.ThreadScroll = clampInt(st.ThreadScroll, 0, max(len(lines)-rows, 0))
	end := min(st.ThreadScroll+rows, len(lines))
	body := append([]string{}, lines[st.ThreadScroll:end]...)
	for len(body) < rows {
		body = append(body, "")
	}
	if st.Composing {
		body = append(body, overlay.Rule(width, bg, pal))
		body = append(body, draft...)
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
	if st.Composing {
		m.agentMailDraftAt(geo, rows)
	}
	return content, geo, nil
}

func (m *OS) agentMailDraftAt(geo overlay.Geometry, rows int) {
	at := &m.AgentMail.wires.at
	at.x, at.y, at.ok = geo.BodyX, geo.BodyY+rows+2, true
	at.drawn, at.top = true, 0
}
