package app

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The message block that lives in the dock's right-hand end.
//
// The placement is the whole point: a message belongs to the bar and never
// covers a pane. The corner toast this replaces was drawn as a layer on top of
// the workspace, three rows tall, and landed exactly where a freshly split
// pane's first output goes.
//
// The block wears no fill. It used to sit on the chrome's Surface step, and
// that slab was the largest ink object in the bar while the words inside it
// were the information: structure drawn at the weight of content, the same
// shape of problem the rail's edges had. The bar's own ground does the job for
// free, and it does it better for the marks, which no longer have to be lifted
// toward the text colour to clear the mark floor on a raised grey: info blue
// measures 3.16:1 on the bare canvas against 2.20:1 on the old fill, so
// dropping the slab bought back the hue. What separates the message from the
// dock items beside it is that they are filled pills and it is not, plus the
// cap on its left and the lit rule above its span.
//
// The cap is a freestanding partial block in the severity's ink, two eighths
// for info and success, four for a warning, six for an error. The weight is a
// channel that survives greyscale and a theme with no contrast to spare, which
// colour alone does not, and the sliver doubles as the block's opening edge.
//
// Naming goes to the mark, a Nerd Font severity glyph in the severity colour
// rather than knocked out of a coloured chip, so the message itself is never
// on a coloured ground and stays as legible as the rest of the dock.
//
// Time is carried by the dock's own hairline, one row above: it lights in the
// severity colour across exactly the block's span and burns down from the right
// as the message ages. A sticky error lights it end to end and stops, so "not
// moving" is the affordance that it is waiting for you.

// notifBlock is a rendered message: the dock's right-hand block, and the run of
// hairline that goes above it. They are produced together because the rule's
// length is defined as the block's span, and the two drifting apart is exactly
// the sort of thing that only shows up on screen.
type notifBlock struct {
	Text  string // styled, drawn as the dock bar's right-hand block
	Rule  string // styled, replaces the right-hand end of the dock's hairline
	Width int    // display width of both
	// DismissW is how many of the trailing columns dismiss instead of
	// activating: the meta (the esc affordance and the queue counter) and the
	// bare columns past it. Everything to its left is the message itself,
	// which is what a click follows.
	DismissW int
}

// notifStatus is the reading of the live queue that the block is drawn from:
// which message wins, what is stacked behind it, and how much life it has left.
type notifStatus struct {
	msg    Notification
	queued int     // how many are waiting behind it
	worst  string  // the worst severity among those waiting
	frac   float64 // 1 at birth, 0 at death; a sticky message sits at 1
}

// notifSeverityRank orders the severities so the counter behind a message can
// report the worst thing queued rather than the most recent.
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

// notifGlyph is the severity mark. It is the one part of the block that is
// never truncated: a message cut down to nothing still says how bad it was.
func notifGlyph(notifType string) string {
	if config.UseASCIIOnly {
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

// notifCap is the weighted opening edge: two eighths, four eighths, six eighths.
//
// Two eighths apart rather than one. The first pass ran the three weights an
// eighth apart and the step from info to warning was invisible, because you
// never see two of them side by side to compare.
func notifCap(notifType string) string {
	switch notifType {
	case "error":
		return config.GetNotificationCap(config.NotificationCapHeavy)
	case "warning", "warn":
		return config.GetNotificationCap(config.NotificationCapMedium)
	default:
		return config.GetNotificationCap(config.NotificationCapLight)
	}
}

// notifRuleStroke escalates the hairline's weight with severity, so a burning
// info is no heavier than the rule it replaces and an error is a heavy line.
func notifRuleStroke(notifType string) string {
	if notifSeverityRank(notifType) >= 2 {
		return config.GetNotificationRule(config.NotificationRuleHeavy)
	}
	return config.GetNotificationRule(config.NotificationRuleLight)
}

// notifStatus reads the live queue. The newest message wins the block and the
// rest are reported as a count behind it.
func (m *OS) notifStatus() (notifStatus, bool) {
	if len(m.Notifications) == 0 {
		return notifStatus{}, false
	}

	now := time.Now()
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

// notifBudget is how many columns the message block may take.
//
// The dock's mode pill and workspace counts are never given up for a message,
// which is what the reserve is; past the cap a message that keeps growing stops
// being a status line and starts being a paragraph. On a dock too narrow to
// split at all the message takes what the screen has less a small margin, which
// is the only case where it may crowd the counts.
func notifBudget(renderWidth int) int {
	budget := min(renderWidth-config.NotificationDockReserve, config.NotificationMaxWidth)
	if budget < config.NotificationMinWidth {
		budget = max(renderWidth-4, 8)
	}
	return budget
}

// notifMeta draws the two things that must never be truncated: the esc
// affordance on a message waiting to be dismissed, and the count of whatever is
// queued behind this one.
//
// The counter takes the colour of the worst thing waiting, so an error that a
// later info pushed out of the block still says so from underneath it.
// Otherwise it is dim, because a queue of routine messages is not news.
func (s notifStatus) notifMeta() string {
	// Dim against the bar's own ground rather than a fixed grey. The grey it
	// used to be was picked for one background and measured under 4.5:1 on two
	// thirds of the themes.
	bg := theme.NotificationGround()
	dim := theme.Readable(theme.UI().FgDim, bg)

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

// notifChromeWidth is every column of the block that is not the message: the
// cap, the space and the mark, the two spaces before the text, and the two
// columns of bare bar past the block's end.
//
// Those last columns are not decoration. Without them the message runs flush
// into whatever holds the bar's right-hand end and stops reading as a block.
const notifChromeWidth = 7

// renderNotificationBlock builds the dock's message block and the run of
// hairline above it, or reports false when nothing is live.
//
// avail is the room the rest of the dock has actually left, which is the
// smaller of the two limits in play: the design's own budget says how wide a
// message should ever get, and avail says how wide it can get here. Pass 0 for
// avail to ask only what the design wants, which is what the layout pass does
// before the dock items have been measured.
//
// Width is a hard contract, not an aspiration: the returned Width is what the
// dock reserves, what the rule is drawn to, and what the block actually
// measures. The old renderer clamped the message with MaxWidth after the fact,
// which cut whatever happened to be last, and what was last was the block's
// own trailing columns.
func (m *OS) renderNotificationBlock(renderWidth, avail int) (notifBlock, bool) {
	s, ok := m.notifStatus()
	if !ok {
		return notifBlock{}, false
	}

	// The severity is the theme's, so it is measured against the bar's ground
	// before it is drawn on it. A mark carries its meaning in its weight as well
	// as its colour, so it is held to the mark floor and keeps more of its hue.
	bg := theme.NotificationGround()
	sev := theme.ReadableAt(theme.NotificationSeverity(s.msg.Type), bg, theme.MarkFloor)
	inked := lipgloss.NewStyle().Foreground(sev)

	// The cap is a freestanding sliver on the bare bar: its weight still reads
	// as two, four or six eighths of a cell of ink, and it is the block's whole
	// left edge now that there is no fill to open.
	lead := inked.Render(notifCap(s.msg.Type))
	mark := inked.Render(" " + notifGlyph(s.msg.Type))

	budget := notifBudget(renderWidth)
	if avail > 0 {
		// One column short of what is left, so the opening cap always has a
		// column of bar in front of it. At 80 columns with two minimized
		// windows in the dock the spacers collapse to nothing and the cap
		// butted straight against the dock items' truncation dots, which reads
		// as one run-on shape rather than as a message arriving in the bar.
		//
		// Never below the chrome, though: a budget that cannot hold the cap
		// and the mark would have the block quietly overrun it,
		// and a contract that is broken in the tight case is not a contract. A
		// dock with fewer columns than that free is already overfull, and the
		// bar's own one-screen backstop is what handles it.
		budget = min(budget, max(avail-1, notifChromeWidth))
	}

	// The meta gives way before the message does only when it has to: the
	// counter outranks the esc affordance, because a buried error is news and a
	// dismissal hint the user has seen before is not.
	meta := s.notifMeta()
	if notifChromeWidth+lipgloss.Width(meta) > budget {
		bare := s
		bare.msg.Sticky = false
		meta = bare.notifMeta()
	}
	if notifChromeWidth+lipgloss.Width(meta) > budget {
		meta = ""
	}

	room := budget - notifChromeWidth - lipgloss.Width(meta)
	text := notifFit(s.msg.Message, room)
	bodyStyle := lipgloss.NewStyle().Foreground(theme.Readable(theme.UI().Fg, bg))
	if s.msg.Target != nil {
		// Underline is the one link mark everyone reads without being taught,
		// costs no columns, and never appears on a message with nowhere to go,
		// so its absence says something too.
		bodyStyle = bodyStyle.Underline(true)
	}
	body := "  " + bodyStyle.Render(text)

	// The last two columns are bare bar: the gap that keeps the message off
	// whatever holds the right-hand end of the screen.
	block := lead + mark + body + meta + "  "
	width := lipgloss.Width(block)

	return notifBlock{
		Text:     block,
		Rule:     notifBurnRule(s, width),
		Width:    width,
		DismissW: lipgloss.Width(meta) + 2, // meta and the bar columns after it
	}, true
}

// notifFit truncates the message, and only the message. The severity mark, the
// esc affordance and the overflow counter are the parts you cannot afford to
// lose, so they are measured out of the room before this is called and this
// takes whatever is left, including nothing.
func notifFit(message string, room int) string {
	if room <= 0 {
		return ""
	}
	if lipgloss.Width(message) <= room {
		return message
	}
	// Below four columns there is no room for an ellipsis and a character of
	// message both, so the ellipsis alone says the message was cut.
	if room < 4 {
		return truncateToWidth("...", room)
	}
	return strings.TrimRight(truncateToWidth(message, room-3), " ") + "..."
}

// notifBurnRule lights the dock's hairline across the message's span and
// shortens it from the right as the message ages, so what is left of the rule
// is what is left of the message.
//
// A sticky message sits at full length and does not move. That is the
// affordance, and it is the reason the burn is on the rule rather than in a
// cell of its own: a line that has stopped is legible as stopped at a glance,
// where a single character that has stopped changing is not.
func notifBurnRule(s notifStatus, span int) string {
	if span <= 0 {
		return ""
	}

	lit := notifLitSpan(s.frac, span)

	burnt := lipgloss.NewStyle().Foreground(theme.NotificationSeverity(s.msg.Type)).
		Render(strings.Repeat(notifRuleStroke(s.msg.Type), lit))
	rest := lipgloss.NewStyle().Foreground(theme.RailRule()).
		Render(strings.Repeat(config.GetWindowSeparatorChar(), span-lit))
	return burnt + rest
}

// notifLitSpan is how many of the rule's cells are still lit at this fraction
// of a message's life.
//
// It is separate from the styling because for an info message the lit stroke
// and the unlit stroke are the same character, distinguished only by colour, so
// this is the only place the burn can be read as a number rather than looked
// at. A message with any life left keeps at least one lit cell: a rule that has
// gone completely dark while its message is still on the dock reads as a
// message that has already been dealt with.
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
