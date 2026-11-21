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
)

const overlayKindMessage = "message"

const messageViewWidth = 76

const maxRecentMessages = 32

type messageEntry struct {
	ID     string
	Text   string
	Level  string
	Time   time.Time
	Source string
	Target *NotifTarget
}

type messageViewState struct {
	open   bool
	entry  messageEntry
	scroll int
}

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

func (m *OS) rememberMessage(e messageEntry) {
	if e.ID != "" && slices.ContainsFunc(m.recentMessages, func(r messageEntry) bool { return r.ID == e.ID }) {
		return
	}
	m.recentMessages = append(m.recentMessages, e)
	if len(m.recentMessages) > maxRecentMessages {
		m.recentMessages = m.recentMessages[len(m.recentMessages)-maxRecentMessages:]
	}
}

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

func logEntry(l LogMessage) messageEntry {
	return messageEntry{Text: l.Message, Level: l.Level, Time: l.Time, Source: l.Source}
}

func (m *OS) notifSourceName(t *NotifTarget) string {
	if t == nil {
		return ""
	}
	if t.Thread != 0 {
		return "Agent mail"
	}
	if t.AgentInstall != "" {
		return ""
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

func (e messageEntry) goesSomewhere() bool {
	return e.Target != nil && e.Target.ClipboardAsk == 0
}

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

func wrapMessage(text string, width int) []string {
	var out []string
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		out = append(out, wrapPlain(strings.ReplaceAll(line, "\t", "    "), width)...)
	}
	return out
}

const (
	notifTextCap = 512
	logTextCap   = 4096
)

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

const wrapCacheSize = 4

type wrapCacheEntry struct {
	text  string
	width int
	lines []string
}

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

func (m *OS) messageViewHints() []overlay.Hint {
	hints := []overlay.Hint{
		{Key: hintListMoveLetters, Label: "scroll"},
		{Key: "y", Label: "copy"},
	}
	if e := m.msgView.entry; e.goesSomewhere() {
		label := "go to source"
		if e.Target.AgentInstall != "" {
			label = "install"
		}
		hints = append(hints, overlay.Hint{Key: "enter", Label: label})
	}
	return append(hints, overlay.Hint{Key: "esc", Label: "close"})
}

const messageViewExtra = 2

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

func (m *OS) renderMessageView(layers []*lipgloss.Layer) []*lipgloss.Layer {
	if !m.msgView.open {
		return layers
	}
	pal := m.overlayUI()
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
