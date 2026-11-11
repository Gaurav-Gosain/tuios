package app

import (
	"image/color"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The log viewer is a list of entries with a cursor, drawn by the shared list
// overlay so it scrolls, highlights and takes the mouse the way every other
// list does. A row is one line and cuts a long entry, so the entry under the
// cursor is also shown wrapped under the list, and enter opens it in the
// message view.

// overlayKindLogs is the log viewer's overlay kind and layer id.
const overlayKindLogs = "logs"

// logViewerWidth is the viewer's preferred inner width.
const logViewerWidth = 80

// logDetailLines is how many lines of the selected entry the viewer shows
// under the list. The count is fixed, so the list does not change height as
// the cursor moves.
const logDetailLines = 3

// logViewerHints is the footer the log viewer advertises.
func logViewerHints() []overlay.Hint {
	return []overlay.Hint{
		{Key: "j/k", Label: "move"},
		{Key: "enter", Label: "show all"},
		{Key: "E", Label: "copy errors"},
		{Key: "A", Label: "copy all"},
		{Key: "q", Label: "close"},
	}
}

// logViewerList is the viewer as a list overlay. The key handler, the wheel and
// the renderer all measure through it, so the rows that scroll are the rows
// that are drawn.
func (m *OS) logViewerList() listOverlay {
	n := len(m.LogMessages)
	return listOverlay{
		Title:      "logs",
		Width:      logViewerWidth,
		MaxVisible: max(n, minPanelRows),
		Count:      n,
		Selected:   m.logList.Cursor,
		Scroll:     &m.logList.Scroll,
		EmptyMsg:   "The log is empty.",
		EmptyHint:  overlay.Hint{Key: "q", Label: "close"},
		Hints:      logViewerHints(),
		RenderRow:  m.logViewerRow,
		DetailFor: func(width int) []string {
			if n == 0 {
				return nil
			}
			return m.logDetail(m.LogMessages[max(0, min(m.logList.Cursor, n-1))].Message, width)
		},
	}
}

// logViewerRow draws one entry: the time, the level, and as much of the message
// as the row holds.
func (m *OS) logViewerRow(i int, _ bool, rowBg color.Color, pal overlay.Palette, width int) string {
	msg := m.LogMessages[i]
	// The severity tokens the rest of the app reads by, so a log line says the
	// same thing a message in the dock does.
	level := msg.Level
	line := overlay.Style(rowBg).Foreground(pal.FgDim).Render(" "+msg.Time.Format("15:04:05")+" ") +
		overlay.Style(rowBg).Foreground(messageLevelColor(level, pal)).Render("["+level+"] ")
	used := 1 + 9 + len(level) + 3
	text := strings.Join(strings.Fields(msg.Message), " ")
	return line + overlay.Style(rowBg).Foreground(pal.Fg).Render(overlay.Truncate(text, max(width-used, 1)))
}

// logDetail is the selected entry wrapped to the panel, in exactly
// logDetailLines lines. An entry longer than that ends in an ellipsis, and
// enter shows the rest.
func (m *OS) logDetail(text string, width int) []string {
	textW := max(width-2, 1)
	wrapped := m.wrapCached(text, textW)
	// A copy: the wrapped lines are the cache's, and these are edited below.
	lines := append([]string(nil), wrapped[:min(len(wrapped), logDetailLines)]...)
	if len(wrapped) > logDetailLines {
		ell := overlay.Ellipsis()
		last := truncateToWidth(lines[logDetailLines-1], max(textW-len([]rune(ell)), 1))
		lines[logDetailLines-1] = strings.TrimRight(last, " ") + ell
	}
	for len(lines) < logDetailLines {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = " " + l
	}
	return lines
}

// logViewerRows is how many entries the viewer shows at once.
func (m *OS) logViewerRows() int {
	_, rows, _ := m.listOverlayLayout(m.logViewerList())
	return rows
}

// LogViewerCursor is the log entry under the viewer's cursor.
func (m *OS) LogViewerCursor() int { return m.logList.Cursor }

// LogViewerScroll is the first log entry the viewer draws.
func (m *OS) LogViewerScroll() int { return m.logList.Scroll }

// LogViewerMove moves the viewer's cursor by delta entries.
func (m *OS) LogViewerMove(delta int) {
	m.logList.Move(delta, len(m.LogMessages), m.logViewerRows(), m.listWraps(), nil)
}

// LogViewerPage is the rows a page key moves the cursor by.
func (m *OS) LogViewerPage() int { return max(m.logViewerRows()/2, 1) }

// LogViewerOpenSelected opens the entry under the cursor in the message view.
func (m *OS) LogViewerOpenSelected() {
	if len(m.LogMessages) == 0 {
		return
	}
	m.logList.Cursor = max(0, min(m.logList.Cursor, len(m.LogMessages)-1))
	m.openMessageView(logEntry(m.LogMessages[m.logList.Cursor]))
}

// CloseLogViewer closes the viewer.
func (m *OS) CloseLogViewer() {
	m.ShowLogs = false
}
