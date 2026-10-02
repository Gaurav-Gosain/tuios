package app

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The message view is the one place a message is read in full.
//
// A message reaches the person on two surfaces that both have to cut it: the
// dock's block, which is one row and at most NotificationMaxWidth cells, and a
// row of the log viewer, which is one line of a list. Neither can grow without
// covering the panes, so both hand the whole message to this panel instead: a
// click on a cut message in the dock, enter on a log row, and prefix+N for the
// newest message all open it. The text is wrapped, every line of it can be
// scrolled to, and y puts it on the clipboard.

// overlayKindMessage is the message view's overlay kind and layer id.
const overlayKindMessage = "message"

// messageViewWidth is the panel's preferred inner width: wide enough that a
// path or a command line wraps a few times rather than many.
const messageViewWidth = 76

// maxRecentMessages bounds the messages prefix+N can reopen. Only the newest
// is reachable from the key, so there is no reason to keep many.
const maxRecentMessages = 32

// messageEntry is one message as the view shows it.
type messageEntry struct {
	// ID is the notification's, empty for a log line. It keeps a message from
	// being remembered twice.
	ID    string
	Text  string
	Level string // a notification type ("error") or a log level ("ERROR")
	Time  time.Time
	// Source names where the message came from: a pane's title, or "tuios"
	// for a message the app raised itself.
	Source string
	// Target is the pane or thread the message is about, nil when there is
	// none. Enter in the view goes there.
	Target *NotifTarget
}

// messageViewState is the open view. Runtime only.
type messageViewState struct {
	open   bool
	entry  messageEntry
	scroll int
}

// MessageViewOpen reports whether the message view is on screen.
func (m *OS) MessageViewOpen() bool { return m.msgView.open }

// openMessageView shows one message in full.
func (m *OS) openMessageView(e messageEntry) {
	m.msgView = messageViewState{open: true, entry: e}
}

// CloseMessageView takes the view off the screen.
func (m *OS) CloseMessageView() { m.msgView = messageViewState{} }

// OpenLastMessage opens the newest message the dock showed, whether it is
// still on the dock or has gone. It reports false when no message was shown
// yet.
func (m *OS) OpenLastMessage() bool {
	if len(m.recentMessages) == 0 {
		return false
	}
	m.openMessageView(m.recentMessages[len(m.recentMessages)-1])
	return true
}

// rememberMessage keeps a shown message for prefix+N.
func (m *OS) rememberMessage(e messageEntry) {
	if e.ID != "" && slices.ContainsFunc(m.recentMessages, func(r messageEntry) bool { return r.ID == e.ID }) {
		return
	}
	m.recentMessages = append(m.recentMessages, e)
	if len(m.recentMessages) > maxRecentMessages {
		m.recentMessages = m.recentMessages[len(m.recentMessages)-maxRecentMessages:]
	}
}

// notificationEntry is a live notification as the view shows it.
func (m *OS) notificationEntry(n Notification) messageEntry {
	return messageEntry{
		ID:     n.ID,
		Text:   n.Message,
		Level:  n.Type,
		Time:   n.StartTime,
		Source: n.Source,
		Target: n.Target,
	}
}

// logEntry is a log line as the view shows it.
func logEntry(l LogMessage) messageEntry {
	return messageEntry{Text: l.Message, Level: l.Level, Time: l.Time, Source: l.Source}
}

// notifSourceName names where a message came from, resolved when the message
// is raised so a pane closed since still has its name.
func (m *OS) notifSourceName(t *NotifTarget) string {
	if t == nil {
		return ""
	}
	if t.Thread != 0 {
		return "Agent mail"
	}
	if w := m.windowByID(t.WindowID); w != nil {
		if name := printableTitle(m.railTitleShown(w)); name != "" {
			return name
		}
		return "A pane"
	}
	if t.Host != "" {
		return t.Host
	}
	return "A pane"
}

// messageGoesSomewhere reports whether enter in the view has a place to go. A
// clipboard ask is left out: it is allowed only by a click on the dock, so a
// key pressed from habit cannot give a pane the clipboard.
func (e messageEntry) goesSomewhere() bool {
	return e.Target != nil && e.Target.ClipboardAsk == 0
}

// messageLevelWord is the level as a word, the same for a notification type and
// a log level.
func messageLevelWord(level string) string {
	switch strings.ToLower(level) {
	case "error":
		return "Error"
	case "warning", "warn":
		return "Warning"
	case "success":
		return "Success"
	default:
		return "Info"
	}
}

// messageLevelColor is the level's ink: the tokens the log viewer and the dock
// read by.
func messageLevelColor(level string, pal overlay.Palette) color.Color {
	switch strings.ToLower(level) {
	case "error":
		return pal.Warn
	case "warning", "warn":
		return pal.Warning
	default:
		return pal.Success
	}
}

// wrapMessage breaks a message onto lines of at most width cells. A line break
// in the message stays a line break.
func wrapMessage(text string, width int) []string {
	var out []string
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		out = append(out, wrapPlain(strings.ReplaceAll(line, "\t", "    "), width)...)
	}
	return out
}

// Caps on the text a message carries. A notification is a pane's to write, so
// its text is held to the size the daemon holds a pane's notification to. A
// log line can be an error with a stack in it, so it gets more room.
const (
	notifTextCap = 512
	logTextCap   = 4096
)

// capText trims s to n bytes on a rune boundary, and ends a trimmed text with
// the ellipsis so the cut shows.
func capText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + overlay.Ellipsis()
}

// wrapCacheSize is how many wrapped texts are kept. The view, the hover label
// and the log viewer's detail each wrap one text per frame.
const wrapCacheSize = 4

// wrapCacheEntry is one wrapped text.
type wrapCacheEntry struct {
	text  string
	width int
	lines []string
}

// wrapCached is wrapMessage for a text drawn on every frame: it wraps a text at
// a width once and keeps the lines until other texts push it out.
func (m *OS) wrapCached(text string, width int) []string {
	for _, e := range m.wrapCache {
		if e.lines != nil && e.width == width && e.text == text {
			return e.lines
		}
	}
	lines := wrapMessage(text, width)
	m.wrapCache[m.wrapCacheNext] = wrapCacheEntry{text: text, width: width, lines: lines}
	m.wrapCacheNext = (m.wrapCacheNext + 1) % wrapCacheSize
	return lines
}

// messageViewHints is the view's footer.
func (m *OS) messageViewHints() []overlay.Hint {
	hints := []overlay.Hint{
		{Key: "j/k", Label: "scroll"},
		{Key: "y", Label: "copy"},
	}
	if m.msgView.entry.goesSomewhere() {
		hints = append(hints, overlay.Hint{Key: "enter", Label: "go to source"})
	}
	return append(hints, overlay.Hint{Key: "esc", Label: "close"})
}

// messageViewExtra is the body lines that are not message text: the line that
// says when and from where, and the rule under it. A message that scrolls
// spends one more on the line that says where in it the view is.
const messageViewExtra = 2

// messageViewLayout is the view's fitted width, the message wrapped to it, how
// many of its lines fit, and the largest scroll offset. The keys, the wheel and
// the renderer all measure through here, so the range that scrolls is the range
// that is drawn.
func (m *OS) messageViewLayout() (width int, lines []string, rows, maxScroll int, hints []overlay.Hint) {
	width = m.panelWidth(messageViewWidth)
	lines = m.wrapCached(m.msgView.entry.Text, max(width-2, 1))
	hints = m.messageViewHints()
	rows, fitted := m.panelBody(len(lines), messageViewExtra, width, nil, hints)
	if len(lines) > rows {
		rows, fitted = m.panelBody(len(lines), messageViewExtra+1, width, nil, hints)
	}
	return width, lines, rows, max(len(lines)-rows, 0), fitted
}

// MessageViewScroll moves the view by delta lines.
func (m *OS) MessageViewScroll(delta int) {
	_, _, _, maxScroll, _ := m.messageViewLayout()
	m.msgView.scroll = max(0, min(m.msgView.scroll+delta, maxScroll))
}

// MessageViewPage moves the view by half a page, down when dir is positive.
func (m *OS) MessageViewPage(dir int) {
	_, _, rows, _, _ := m.messageViewLayout()
	m.MessageViewScroll(dir * max(rows/2, 1))
}

// MessageViewTop and MessageViewBottom jump to the first and last line.
func (m *OS) MessageViewTop() { m.msgView.scroll = 0 }

// MessageViewBottom jumps to the last line.
func (m *OS) MessageViewBottom() {
	_, _, _, maxScroll, _ := m.messageViewLayout()
	m.msgView.scroll = maxScroll
}

// CopyMessageView puts the message on the clipboard.
func (m *OS) CopyMessageView() tea.Cmd {
	text := m.msgView.entry.Text
	m.ShowNotification("Copied the message.", "success", m.Settings.NotificationDuration)
	return tea.SetClipboard(text)
}

// MessageViewActivate goes to where the message came from and closes the view.
// It reports false when the message has no source to go to.
func (m *OS) MessageViewActivate() bool {
	e := m.msgView.entry
	if !e.goesSomewhere() {
		return false
	}
	m.CloseMessageView()
	m.jumpToNotifTarget(*e.Target)
	return true
}

// renderMessageView draws the view as an overlay panel.
func (m *OS) renderMessageView(layers []*lipgloss.Layer) []*lipgloss.Layer {
	if !m.msgView.open {
		return layers
	}
	pal := theme.UI()
	bg := pal.Surface
	width, lines, rows, maxScroll, hints := m.messageViewLayout()
	m.msgView.scroll = max(0, min(m.msgView.scroll, maxScroll))
	e := m.msgView.entry

	source := e.Source
	if source == "" {
		source = "tuios"
	}
	head := overlay.Style(bg).Foreground(pal.FgDim).Render(e.Time.Format("15:04:05")+"  ") +
		overlay.Style(bg).Foreground(messageLevelColor(e.Level, pal)).Render(messageLevelWord(e.Level)) +
		overlay.Style(bg).Foreground(pal.FgDim).Render("  from "+source)
	body := []string{clipStyled(head, width), overlay.Rule(width, bg, pal)}

	end := min(m.msgView.scroll+rows, len(lines))
	for _, l := range lines[m.msgView.scroll:end] {
		body = append(body, overlay.Style(bg).Foreground(pal.Fg).Render(" "+l))
	}
	for range rows - (end - m.msgView.scroll) {
		body = append(body, overlay.Style(bg).Render(" "))
	}
	if maxScroll > 0 {
		body = append(body, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).
			Render(fmt.Sprintf("  Lines %d to %d of %d", m.msgView.scroll+1, end, len(lines))))
	}

	panel := overlay.Panel{
		Title: "message",
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	return m.placeOverlayPanel(layers, overlayKindMessage, content, geo, nil)
}
