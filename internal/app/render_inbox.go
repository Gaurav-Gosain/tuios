package app

import (
	"image/color"
	"strconv"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// inboxWidth is the Inbox overlay's preferred inner width: a row carries a
// name, a summary, the session and the wait.
const inboxWidth = 72

// inboxEmptyLines is the empty state: what the Inbox holds, so an empty one
// says what would appear here.
var inboxEmptyLines = []string{
	"Nothing is waiting for you.",
	"Agents that block on an approval or a question, write to you, error or",
	"finish a turn you have not looked at show up here, from every session.",
}

// renderInbox renders the Inbox overlay: each kind under a heading in words,
// oldest first, with how long each item has waited. Nothing is said by colour
// alone: the heading names the kind, the row names the session and the wait.
func (m *OS) renderInbox() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.Inbox
	title := "Inbox"
	if st.Filter != "" {
		title += ": " + inboxGroupTitle(st.Filter)
	}
	if !st.Live {
		title += " (not connected)"
	}
	hints := []overlay.Hint{
		{Key: overlay.EnterKey(), Label: "go"},
		{Key: "d", Label: "dismiss"},
		{Key: "r", Label: "reply"},
		{Key: "f", Label: "filter"},
		{Key: "m", Label: "mailbox"},
		{Key: "esc", Label: "close"},
	}
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
	right := overlay.Style(bg).Foreground(pal.FgMute).Render(inboxWhere(it)+sep) +
		overlay.Style(bg).Foreground(pal.FgDim).Render(inboxWait(it.Since, now))

	glyph := inboxKindGlyph(it.Kind) + " "
	who := inboxWho(it)
	if it.Count > 1 {
		who += " (" + strconv.Itoa(it.Count) + ")"
	}
	summary := printableTitle(it.Summary)
	if summary == "" {
		summary = inboxKindWords(it)
	}

	avail := max(width-lipgloss.Width(right)-lipgloss.Width(glyph)-4, 1)
	whoW := min(lipgloss.Width(who), avail)
	summaryW := max(avail-whoW-2, 0)

	whoColor := pal.FgDim
	if selected {
		whoColor = pal.Fg
	}
	left := overlay.Style(bg).Foreground(inboxKindColor(it.Kind, pal)).Render(glyph) +
		overlay.Style(bg).Foreground(whoColor).Bold(true).Render(overlay.Truncate(who, whoW))
	if summaryW >= 2 {
		left += overlay.Style(bg).Foreground(pal.FgDim).Render("  " + overlay.Truncate(summary, summaryW))
	}
	return listRowSpans(width, listRowMarker(selected), left, right, bg, pal)
}

// inboxKindColor is the ink of a kind's mark, the same the rail gives the
// state behind it.
func inboxKindColor(kind string, pal overlay.Palette) color.Color {
	switch kind {
	case session.AttentionMail:
		return pal.AccentBright
	case session.AttentionFinished:
		return pal.Success
	}
	return sidebarSeverityColor(inboxAlertState(kind), pal)
}
