package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// inboxWidth is the Inbox overlay's preferred inner width: a row carries a
// name, a summary, the session and the wait.
const inboxWidth = 72

// inboxEmptyLines is the empty state: what the Inbox holds, so an empty one
// says what would appear here.
var inboxEmptyLines = []string{
	"Nothing is waiting for you.",
	"Agents that block on an approval or a question, write to you, error,",
	"finish a turn you have not looked at, or can resume after a restart",
	"show up here, from every session.",
}

// renderInbox renders the Inbox overlay: each kind under a heading in words,
// oldest first, with how long each item has waited. Nothing is said by colour
// alone: the heading names the kind, the row names the session and the wait.
func (m *OS) renderInbox() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.Inbox
	if st.Peek != nil {
		return m.renderInboxPeek(st.Peek, time.Now())
	}
	title := "Inbox"
	if st.Filter != "" {
		title += ": " + inboxGroupTitle(st.Filter)
	}
	if !st.Live {
		title += " (not connected)"
	}
	// The key after dismiss is what answers the selected item: y resumes a
	// conversation, r replies to mail and to anything else.
	answer := overlay.Hint{Key: "r", Label: "reply"}
	if it, ok := m.inboxSelected(); ok && it.Kind == session.AttentionResume {
		answer = overlay.Hint{Key: "y", Label: "resume"}
	} else if ok && it.HeldID != 0 {
		answer = overlay.Hint{Key: "p", Label: "pass on"}
	}
	hints := []overlay.Hint{
		{Key: overlay.EnterKey(), Label: "go"},
		{Key: "space", Label: "peek"},
		{Key: "d", Label: "dismiss"},
		answer,
		{Key: "f", Label: "filter"},
		{Key: "m", Label: "mailbox"},
		{Key: "esc", Label: "close"},
	}
	var detailFor func(int) []string
	selected, ok := m.inboxSelected()
	held := ok && selected.Kind == session.AttentionApproval && selected.RequestID != ""
	if held {
		hints = inboxApprovalHints(selected)
		// The row cuts the line to fit, so the held prompt is shown whole
		// under the list, with what always adds beside its key. The keys
		// only answer the item under the cursor, which is the one shown.
		detailFor = func(width int) []string { return inboxApprovalDetail(selected, width) }
	}
	m.noteInboxShown(selected, held && inboxShowsWhole(selected), time.Now())
	rows := m.inboxRows()
	if len(rows) == 0 {
		lines := inboxEmptyLines
		switch {
		case st.Unsupported:
			lines = []string{"This daemon has no Inbox.", "Restart it with a newer tuios: tuios kill-server"}
		case st.Filter == session.AttentionMail:
			lines = []string{"No unread mail for you.", "m opens the mailbox, with every thread between agents too."}
		case st.Filter != "":
			lines = []string{"Nothing under " + inboxGroupTitle(st.Filter) + ".", "f shows the next kind, and then all of them."}
		case !m.IsDaemonSession:
			lines = []string{"The Inbox needs the daemon.", "", "Start a daemon session with: tuios new"}
		}
		return m.simpleOverlayPanel("", title, lines, []overlay.Hint{{Key: "f", Label: "filter"}, {Key: "m", Label: "mailbox"}, {Key: "esc", Label: "close"}})
	}
	now := time.Now()
	return m.renderListOverlay(listOverlay{
		Title:      title,
		Width:      inboxWidth,
		MaxVisible: 14,
		Count:      len(rows),
		Selected:   st.Selected,
		Scroll:     &st.Scroll,
		Hints:      hints,
		DetailFor:  detailFor,
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			r := rows[i]
			if r.item == nil {
				return m.inboxHeadingRow(r, rowBg, pal, width)
			}
			return m.inboxItemRow(*r.item, selected, rowBg, pal, width, now)
		},
	})
}

// inboxHeadingRow draws a group heading: the kind in words and how many.
func (m *OS) inboxHeadingRow(r inboxRow, bg color.Color, pal overlay.Palette, width int) string {
	text := r.heading + " " + strconv.Itoa(r.count)
	return overlay.Style(bg).Foreground(pal.FgMute).Bold(true).Render(overlay.Truncate(text, width))
}

// inboxItemRow draws one item: its kind's mark, who, what it says, and on the
// right the session and how long it has waited.
func (m *OS) inboxItemRow(it session.AttentionItem, selected bool, bg color.Color, pal overlay.Palette, width int, now time.Time) string {
	sep := " · "
	if overlay.UseASCII() {
		sep = " . "
	}
	// An item from a machine whose link is down is what that machine said
	// last. It says so in words, when it was last heard from, and is drawn in
	// the muted ink so the eye passes over it; the words carry it without
	// colour.
	when := inboxWait(it.Since, now)
	if it.Stale {
		when = inboxSeen(it.SeenAt, now)
	}
	right := overlay.Style(bg).Foreground(pal.FgMute).Render(inboxWhere(it)+sep) +
		overlay.Style(bg).Foreground(pal.FgDim).Render(when)

	glyph := inboxKindGlyph(it.Kind) + " "
	who := inboxWho(it)
	if it.Count > 1 {
		who += " (" + strconv.Itoa(it.Count) + ")"
	}
	summary := printableTitle(it.Summary)
	if summary == "" {
		summary = inboxKindWords(it)
	}
	if keys := inboxAnswerKeys(it); keys != "" {
		// Said in text, so a held approval reads as answerable here without
		// colour: the keys that answer it, in front of what it asks.
		summary = "[" + keys + "] " + summary
	}
	if it.HeldID != 0 {
		// Mail another machine sent an agent here, held for the person by
		// the link policy. Said in words: who it was for, and the key.
		summary = "[held for " + printableTitle(it.HeldFor) + ", p passes on] " + summary
	}

	avail := max(width-lipgloss.Width(right)-lipgloss.Width(glyph)-4, 1)
	whoW := min(lipgloss.Width(who), avail)
	summaryW := max(avail-whoW-2, 0)

	whoColor := pal.FgDim
	if selected {
		whoColor = pal.Fg
	}
	glyphColor := inboxKindColor(it.Kind, pal)
	if it.Stale {
		whoColor, glyphColor = pal.FgMute, pal.FgMute
	}
	left := overlay.Style(bg).Foreground(glyphColor).Render(glyph) +
		overlay.Style(bg).Foreground(whoColor).Bold(true).Render(overlay.Truncate(who, whoW))
	if summaryW >= 2 {
		left += overlay.Style(bg).Foreground(pal.FgDim).Render("  " + overlay.Truncate(summary, summaryW))
	}
	return listRowSpans(width, listRowMarker(selected), left, right, bg, pal)
}

// inboxPeekMaxLines is how many prompt lines the peek shows at most. A prompt
// longer than the screen allows keeps its bottom, where the question and the
// options are.
const inboxPeekMaxLines = 14

// renderInboxPeek draws the peek: who waits and for how long, the prompt as
// the pane shows it behind a bar that marks it as the pane's text, its
// options, and the keys that answer it. What the peek says is in words; the
// colours only repeat it.
func (m *OS) renderInboxPeek(p *inboxPeek, now time.Time) (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Surface
	width := m.panelWidth(inboxWidth)
	textW := max(width-2, 1)
	it := p.Item
	pk := p.Peek

	var body []string
	add := func(ink color.Color, s string) {
		for _, l := range wrapPlain(s, textW) {
			body = append(body, overlay.Style(bg).Foreground(ink).Render("  "+l))
		}
	}

	since := it.Since
	if pk != nil && pk.Blocked && pk.StateAt > 0 {
		since = pk.StateAt
	}
	what := inboxKindWords(it)
	if pk != nil && !pk.Blocked {
		what = "is " + strings.ReplaceAll(pk.State, "_", " ") + " now, not waiting on a prompt"
	}
	add(pal.Fg, inboxWho(it)+" in "+inboxWhere(it)+" "+what+sepWord()+"waited "+inboxWait(since, now))
	if p.Note != "" {
		add(pal.Warning, p.Note)
	}

	bar := "│ "
	if overlay.UseASCII() {
		bar = "| "
	}
	hints := []overlay.Hint{}
	switch {
	case pk == nil && p.Loading:
		body = append(body, "")
		add(pal.FgDim, "Reading the prompt...")
	case pk == nil:
	case !pk.Found:
		body = append(body, "")
		add(pal.FgDim, capitalize(printableTitle(pk.Reason))+".")
	default:
		body = append(body, "")
		lines := pk.Lines
		if len(lines) > inboxPeekMaxLines {
			lines = lines[len(lines)-inboxPeekMaxLines:]
		}
		for _, l := range lines {
			body = append(body, overlay.Style(bg).Foreground(pal.FgMute).Render("  "+bar)+
				overlay.Style(bg).Foreground(pal.FgDim).Render(overlay.Truncate(printableRunes(l), max(textW-2, 1))))
		}
		if len(pk.Options) > 0 {
			body = append(body, "")
			for _, o := range pk.Options {
				key := strconv.Itoa(o.N)
				body = append(body, overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("  "+key)+
					overlay.Style(bg).Foreground(pal.Fg).Render("  "+overlay.Truncate(printableTitle(o.Label), max(textW-len(key)-2, 1))))
			}
		}
		if !pk.Answerable {
			body = append(body, "")
			add(pal.FgDim, capitalize(printableTitle(pk.Reason))+". Enter goes to the pane.")
		}
		hints = inboxPeekHints(pk)
	}

	if p.Composing {
		body = append(body, "")
		add(pal.Fg, "Answer: "+printableRunes(p.Draft)+"_")
		hints = []overlay.Hint{{Key: overlay.EnterKey(), Label: "send"}, {Key: "esc", Label: "cancel"}}
	}
	switch {
	case p.Sending:
		body = append(body, "")
		add(pal.FgDim, "Answering, and waiting for the pane to move on...")
	case p.Loading && pk != nil:
		body = append(body, "")
		add(pal.FgDim, "Reading the prompt again...")
	}
	if p.Err != "" {
		body = append(body, "")
		add(pal.Warn, p.Err)
	}
	if !p.Composing {
		hints = append(hints,
			overlay.Hint{Key: overlay.EnterKey(), Label: "go to pane"},
			overlay.Hint{Key: "r", Label: "read again"},
			overlay.Hint{Key: "esc", Label: "back"})
	}

	title := "Prompt"
	if pk != nil && pk.Kind != "" {
		title = inboxGroupTitle(inboxKindForPrompt(pk.Kind))
		title = strings.TrimSuffix(title, "s")
	}
	panel := overlay.Panel{
		Glyph: inboxKindGlyph(it.Kind),
		Title: title + ": " + inboxWho(it),
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	return content, geo, nil
}

// inboxKindForPrompt is the Inbox kind a prompt kind reads as.
func inboxKindForPrompt(kind string) string {
	if kind == harness.PromptKindQuestion {
		return session.AttentionQuestion
	}
	return session.AttentionApproval
}

// sepWord is the separator between two clauses of a peek's first line.
func sepWord() string {
	if overlay.UseASCII() {
		return ", "
	}
	return " · "
}

// inboxPeekHints are the keys that answer a peeked prompt: only the answers
// the prompt takes now are offered.
func inboxPeekHints(pk *session.PromptPeek) []overlay.Hint {
	var hints []overlay.Hint
	if pk.Offers(harness.ActionChoose) && len(pk.Options) > 0 {
		key := strconv.Itoa(pk.Options[0].N)
		if last := pk.Options[len(pk.Options)-1].N; last != pk.Options[0].N {
			key += "-" + strconv.Itoa(min(last, 9))
		}
		hints = append(hints, overlay.Hint{Key: key, Label: "choose"})
	}
	if pk.Offers(harness.ActionApprove) {
		hints = append(hints, overlay.Hint{Key: "a", Label: "approve"})
	}
	if pk.Offers(harness.ActionApproveAlways) {
		hints = append(hints, overlay.Hint{Key: "A", Label: "always"})
	}
	if pk.Offers(harness.ActionDeny) {
		hints = append(hints, overlay.Hint{Key: "d", Label: "deny"})
	}
	if pk.Offers(harness.ActionText) {
		hints = append(hints, overlay.Hint{Key: "tab", Label: "type"})
	}
	return hints
}

// inboxAnswerOrder is each decision's key, in the order of the harness's own
// menu: yes, yes and do not ask again, no.
var inboxAnswerOrder = []struct{ key, decision, label string }{
	{"1", session.ApprovalOnce, "allow"},
	{"2", session.ApprovalAlways, "always"},
	{"3", session.ApprovalDeny, "deny"},
}

// inboxAnswerKeys is the keys that answer a held approval, such as "1/2/3",
// or empty for an item the Inbox is not holding.
func inboxAnswerKeys(it session.AttentionItem) string {
	if it.RequestID == "" {
		return ""
	}
	var keys []string
	for _, a := range inboxAnswerOrder {
		if slices.Contains(it.Options, a.decision) {
			keys = append(keys, a.key)
		}
	}
	return strings.Join(keys, "/")
}

// inboxApprovalDetail is the held approval under the cursor in full: its whole
// line, wrapped, and for always the rules it adds from now on. The daemon only
// holds a prompt whose line is the whole request, so this is everything the
// answer approves.
func inboxApprovalDetail(it session.AttentionItem, width int) []string {
	width = max(width-2, 8)
	if !inboxShowsWhole(it) {
		return wrapPlain("  This prompt has characters this terminal cannot show, so it is not answered here. Enter answers it in the pane.", width)
	}
	var lines []string
	for _, l := range wrapPlain(printableTitle(it.Summary), width) {
		lines = append(lines, "  "+l)
	}
	if slices.Contains(it.Options, session.ApprovalAlways) && len(it.AlwaysScope) > 0 {
		for _, l := range wrapPlain("2 (always) also allows from now on:", width) {
			lines = append(lines, "  "+l)
		}
		for _, rule := range it.AlwaysScope {
			for _, l := range wrapPlain(printableTitle(rule), width-2) {
				lines = append(lines, "    "+l)
			}
		}
	}
	return lines
}

// inboxShowsWhole reports whether this client draws a held approval's line
// and rules exactly as they are. A character it would leave out, such as one
// an ASCII-only terminal cannot draw, would make the line read as something
// it does not say, so such a prompt is answered in the pane.
func inboxShowsWhole(it session.AttentionItem) bool {
	for _, line := range append([]string{it.Summary}, it.AlwaysScope...) {
		if printableTitle(line) != line || strings.IndexFunc(line, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
			return false
		}
	}
	return true
}

// inboxApprovalHints are the hints for a held approval under the cursor: its
// answers first, then going to the pane, which hands the prompt back there.
func inboxApprovalHints(it session.AttentionItem) []overlay.Hint {
	var hints []overlay.Hint
	for _, a := range inboxAnswerOrder {
		if slices.Contains(it.Options, a.decision) {
			hints = append(hints, overlay.Hint{Key: a.key, Label: a.label})
		}
	}
	return append(hints,
		overlay.Hint{Key: overlay.EnterKey(), Label: "answer in pane"},
		overlay.Hint{Key: "d", Label: "dismiss"},
		overlay.Hint{Key: "esc", Label: "close"},
	)
}

// inboxKindColor is the ink of a kind's mark, the same the rail gives the
// state behind it.
func inboxKindColor(kind string, pal overlay.Palette) color.Color {
	switch kind {
	case session.AttentionMail, session.AttentionResume:
		return pal.AccentBright
	case session.AttentionFinished:
		return pal.Success
	}
	return sidebarSeverityColor(inboxAlertState(kind), pal)
}
