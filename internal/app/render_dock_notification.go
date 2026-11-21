package app

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type notifBlock struct {
	Text     string
	Rule     string
	Width    int
	DismissW int
	drawn    notifDrawn
	Cut      bool
	Choices  []notifSpan
}

type notifSpan struct{ X0, X1 int }

type notifDrawn struct {
	id        string
	queued    int
	lit, span int
}

func (m *OS) notifBurnMoved() bool {
	s, ok := m.notifStatus()
	if !ok {
		return false
	}
	d := m.notifDrawn
	return s.msg.ID != d.id || s.queued != d.queued ||
		m.notifLit(s.frac, d.span) != d.lit
}

func (m *OS) notifNextMove(now time.Time) (time.Duration, bool) {
	s, ok := m.notifStatus()
	if !ok {
		return 0, false
	}
	d := m.notifDrawn
	if s.msg.ID != d.id || s.queued != d.queued || d.span <= 0 ||
		m.notifLit(s.frac, d.span) != d.lit {
		return 0, false
	}
	next := time.Duration(math.MaxInt64)
	if m.notifPaused() {
		return m.notifHoverAt.Add(notifMaxHold).Sub(now), true
	}
	for _, n := range m.Notifications {
		if !n.Sticky {
			next = min(next, n.StartTime.Add(n.Duration).Sub(now))
		}
	}
	if top := s.msg; !top.Sticky && top.Duration > 0 && d.lit > 1 {
		frac := (float64(d.lit) - 0.5) / float64(d.span)
		at := top.StartTime.Add(time.Duration((1 - frac) * float64(top.Duration)))
		next = min(next, at.Sub(now))
	}
	return next, true
}

type notifStatus struct {
	msg    Notification
	queued int
	worst  string
	frac   float64
}

func notifSeverityRank(notifType string) int {
	switch notifType {
	case "error":
		return 3
	case "warning", "warn":
		return 2
	case "success":
		return 1
	default:
		return 0
	}
}

func notifLevelRank(level string) int {
	switch level {
	case config.NotificationLevelError:
		return notifSeverityRank("error")
	case config.NotificationLevelWarning:
		return notifSeverityRank("warning")
	default:
		return 0
	}
}

func notifGlyph(notifType string, s *config.Settings) string {
	if s.NerdFontsOff() {
		switch notifType {
		case "error":
			return config.NotificationIconError
		case "warning", "warn":
			return config.NotificationIconWarning
		case "success":
			return config.NotificationIconSuccess
		default:
			return config.NotificationIconInfo
		}
	}
	switch notifType {
	case "error":
		return config.NotificationGlyphError
	case "warning", "warn":
		return config.NotificationGlyphWarning
	case "success":
		return config.NotificationGlyphSuccess
	default:
		return config.NotificationGlyphInfo
	}
}

func notifCap(notifType string, s *config.Settings) string {
	switch notifType {
	case "error":
		return s.GetNotificationCap(config.NotificationCapHeavy)
	case "warning", "warn":
		return s.GetNotificationCap(config.NotificationCapMedium)
	default:
		return s.GetNotificationCap(config.NotificationCapLight)
	}
}

func notifRuleStroke(notifType string, s *config.Settings) string {
	if notifSeverityRank(notifType) >= 2 {
		return s.GetNotificationRule(config.NotificationRuleHeavy)
	}
	return s.GetNotificationRule(config.NotificationRuleLight)
}

func (m *OS) notifStatus() (notifStatus, bool) {
	if len(m.Notifications) == 0 {
		return notifStatus{}, false
	}

	now := m.notifNow()
	s := notifStatus{
		msg:    m.Notifications[len(m.Notifications)-1],
		queued: len(m.Notifications) - 1,
		frac:   1,
	}

	for _, q := range m.Notifications[:len(m.Notifications)-1] {
		if notifSeverityRank(q.Type) > notifSeverityRank(s.worst) {
			s.worst = q.Type
		}
	}

	if !s.msg.Sticky && s.msg.Duration > 0 {
		left := 1 - float64(now.Sub(s.msg.StartTime))/float64(s.msg.Duration)
		s.frac = min(max(left, 0), 1)
	}
	return s, true
}

func notifBudget(renderWidth int) int {
	budget := min(renderWidth-config.NotificationDockReserve, config.NotificationMaxWidth)
	if budget < config.NotificationMinWidth {
		budget = max(renderWidth-4, 8)
	}
	return budget
}

func (s notifStatus) notifMeta(pal overlay.Palette) string {
	bg := pal.Canvas
	dim := theme.Readable(pal.FgDim, bg)

	var parts []string
	if s.msg.Sticky {
		parts = append(parts, lipgloss.NewStyle().Foreground(dim).Render("esc"))
	}
	if s.queued > 0 {
		col := dim
		if notifSeverityRank(s.worst) > notifSeverityRank(s.msg.Type) {
			col = theme.Readable(theme.NotificationSeverity(s.worst), bg)
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(col).Render(fmt.Sprintf("+%d", s.queued)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, "  ")
}

const notifChromeWidth = 7

func (m *OS) renderNotificationBlock(renderWidth, avail int) (notifBlock, bool) {
	s, ok := m.notifStatus()
	if !ok {
		return notifBlock{}, false
	}

	ground := m.groundUI()
	bg := ground.Canvas
	sev := theme.ReadableAt(theme.NotificationSeverity(s.msg.Type), bg, theme.MarkFloor)
	inked := lipgloss.NewStyle().Foreground(sev)

	lead := inked.Render(notifCap(s.msg.Type, &m.Settings))
	if m.Settings.NotificationsPlain() {
		lead = " "
	}
	mark := inked.Render(" " + notifGlyph(s.msg.Type, &m.Settings))
	if glyph, fg := agentMark(s.msg.AgentState, false, 0, ground); glyph != "" {
		mark = lipgloss.NewStyle().Foreground(theme.ReadableAt(fg, bg, theme.MarkFloor)).Render(" " + glyph)
	}

	chromeW := notifChromeWidth + max(lipgloss.Width(mark)-2, 0)

	budget := notifBudget(renderWidth)
	if avail > 0 {
		budget = min(budget, max(avail-1, chromeW))
	}

	meta := s.notifMeta(ground)
	if chromeW+lipgloss.Width(meta) > budget {
		bare := s
		bare.msg.Sticky = false
		meta = bare.notifMeta(ground)
	}
	if chromeW+lipgloss.Width(meta) > budget {
		meta = ""
	}

	room := budget - chromeW - lipgloss.Width(meta)

	offer := isMachineOffer(s.msg.Target)
	var choiceText []string
	if offer {
		fg := theme.Readable(ground.Fg, bg)
		for i, label := range offerChoices {
			st := lipgloss.NewStyle().Foreground(fg).Underline(true)
			if i == offerAdd {
				st = st.Foreground(theme.Readable(ground.Accent, bg)).Bold(true)
			}
			choiceText = append(choiceText, st.Render(label))
			room -= 2 + lipgloss.Width(label)
		}
	}

	cut := lipgloss.Width(s.msg.Message) > room
	more := ""
	if cut && !offer && room-notifMoreWidth >= notifMoreMinText {
		room -= notifMoreWidth
		more = "  " + lipgloss.NewStyle().Foreground(theme.Readable(ground.FgDim, bg)).Render(notifMoreLabel)
	}

	text := notifFit(s.msg.Message, room)
	bodyStyle := lipgloss.NewStyle().Foreground(theme.Readable(ground.Fg, bg))
	if (s.msg.Target != nil || cut) && !offer {
		bodyStyle = bodyStyle.Underline(true)
	}
	body := "  " + bodyStyle.Render(text) + more
	var choices []notifSpan
	if offer {
		x := lipgloss.Width(lead + mark + body)
		for i, c := range choiceText {
			w := lipgloss.Width(offerChoices[i])
			choices = append(choices, notifSpan{X0: x + 2, X1: x + 2 + w})
			x += 2 + w
			body += "  " + c
		}
	}

	block := lead + mark + body + meta + "  "
	width := lipgloss.Width(block)

	lit := m.notifLit(s.frac, width)
	return notifBlock{
		Text:     block,
		Rule:     notifBurnRule(s, width, lit, &m.Settings, m.railRule()),
		Width:    width,
		DismissW: lipgloss.Width(meta) + 2,
		Cut:      cut && !offer,
		Choices:  choices,
		drawn: notifDrawn{
			id:     s.msg.ID,
			queued: s.queued,
			lit:    lit,
			span:   width,
		},
	}, true
}

const (
	notifMoreLabel   = "more"
	notifMoreWidth   = 2 + len(notifMoreLabel)
	notifMoreMinText = 12
)

func notifFit(message string, room int) string {
	if room <= 0 {
		return ""
	}
	if lipgloss.Width(message) <= room {
		return message
	}
	ell := overlay.Ellipsis()
	ellW := lipgloss.Width(ell)
	if room <= ellW {
		return truncateToWidth(ell, room)
	}
	return notifWordCut(truncateToWidth(message, room-ellW), message) + ell
}

func notifWordCut(cut, message string) string {
	if len(cut) < len(message) && strings.HasPrefix(message, cut) && message[len(cut)] != ' ' {
		if i := strings.LastIndexByte(cut, ' '); i > 0 && 2*lipgloss.Width(cut[:i]) >= lipgloss.Width(cut) {
			cut = cut[:i]
		}
	}
	return strings.TrimRight(cut, " ,;:")
}

func notifBurnRule(s notifStatus, span, lit int, set *config.Settings, rule color.Color) string {
	if span <= 0 {
		return ""
	}

	burnt := lipgloss.NewStyle().Foreground(theme.NotificationSeverity(s.msg.Type)).
		Render(strings.Repeat(notifRuleStroke(s.msg.Type, set), lit))
	rest := lipgloss.NewStyle().Foreground(rule).
		Render(strings.Repeat(set.GetWindowSeparatorChar(), span-lit))
	return burnt + rest
}

func (m *OS) notifLit(frac float64, span int) int {
	if m.Settings.NotificationsPlain() {
		return 0
	}
	return notifLitSpan(frac, span)
}

func notifLitSpan(frac float64, span int) int {
	if span <= 0 {
		return 0
	}
	lit := int(float64(span)*frac + 0.5)
	if frac > 0 && lit < 1 {
		lit = 1
	}
	return min(max(lit, 0), span)
}
