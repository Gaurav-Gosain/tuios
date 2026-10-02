package app

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The pointer on the dock's message block holds the message there.
//
// A message burns down on a timer the person did not start, and a long one is
// still being read when the timer runs out. While the pointer is on the block
// the clock is stopped: the burn holds, nothing expires, and when the pointer
// leaves, every live message gets back the time it was held for. A message the
// block had to cut also shows a label above the dock with the first lines of
// it, the way a clipped workspace pill says its full name.

// notifTooltipLines is the most lines of a message the label shows. Past that
// it is the message view's job, which a click on the block opens.
const notifTooltipLines = 4

// notifMaxHold is the longest the pointer holds the messages. A pointer left on
// the block, or a terminal that lost focus without saying so, must not keep a
// message up for ever.
const notifMaxHold = 60 * time.Second

// notifPaused reports whether the pointer is holding the live messages. With
// none left there is nothing to hold, and the hold is dropped, so a message
// that arrives later starts on the wall clock and not on a stale hold. A hold
// past notifMaxHold ends here.
func (m *OS) notifPaused() bool {
	if m.notifHoverAt.IsZero() {
		return false
	}
	if len(m.Notifications) == 0 {
		m.notifHoverAt = time.Time{}
		return false
	}
	if time.Since(m.notifHoverAt) >= notifMaxHold {
		m.notifHoverEnd()
		return false
	}
	return true
}

// notifNow is the clock the live messages age by: the wall clock, or the
// moment the pointer came onto the block while it is there.
func (m *OS) notifNow() time.Time {
	if m.notifPaused() {
		return m.notifHoverAt
	}
	return time.Now()
}

// NotifHoldEnd ends the hold for a reason other than the pointer leaving: a
// key press, or the terminal losing focus. The hold does not start again until
// the pointer has left the block and come back.
func (m *OS) NotifHoldEnd() {
	if m.notifHoverAt.IsZero() {
		return
	}
	m.notifHoverEnd()
	m.notifHoldSpent = true
}

// notifHoverEnd lets the clock run again and credits each live message with
// the time it was held: from the later of the hold's start and its own, so a
// message that arrived during the hold gets only the time it was up, and never
// more than notifMaxHold.
func (m *OS) notifHoverEnd() {
	if m.notifHoverAt.IsZero() {
		return
	}
	end := time.Now()
	if limit := m.notifHoverAt.Add(notifMaxHold); end.After(limit) {
		end = limit
	}
	for i := range m.Notifications {
		from := m.notifHoverAt
		if start := m.Notifications[i].StartTime; start.After(from) {
			from = start
		}
		if held := end.Sub(from); held > 0 {
			m.Notifications[i].StartTime = m.Notifications[i].StartTime.Add(held)
		}
	}
	m.notifHoverAt = time.Time{}
	m.notifHoldSpent = true
	if m.Tooltip.Source == tooltipDockNotif {
		m.tooltipClear()
	}
}

// notifBlockAt reports whether screen (x, y) is on the message block the last
// frame drew.
func (m *OS) notifBlockAt(x, y int) bool {
	z := m.notifHit
	return z.Active && len(m.Notifications) > 0 && y == z.Y && x >= z.X0 && x < z.X1
}

// DockNotifHoverAt holds the live messages while the pointer is on the block,
// and arms the label for a cut one. It reports whether the pointer is on the
// block. Like the other dock hovers it does not consume the motion.
func (m *OS) DockNotifHoverAt(x, y int) bool {
	x, y = m.ScreenPoint(x, y)
	on := m.notifBlockAt(x, y)
	switch {
	case !on:
		m.notifHoverEnd()
		m.notifHoldSpent = false
	case !m.notifPaused() && !m.notifHoldSpent:
		m.notifHoverAt = time.Now()
	}
	if on && m.notifHit.Cut && !m.notifHoldSpent {
		m.tooltipTrack(tooltipDockNotif, 0)
	} else if m.Tooltip.Source == tooltipDockNotif {
		m.tooltipClear()
	}
	return on
}

// notifHoverChangesAt reports whether a motion to (x, y) changes the hold or
// the label, for the motion filter. See dockHoverChangesAt.
func (m *OS) notifHoverChangesAt(x, y int) bool {
	on := m.notifBlockAt(x, y)
	paused := m.notifPaused()
	switch {
	case !on && (paused || m.notifHoldSpent):
		return true
	case on && !paused && !m.notifHoldSpent:
		return true
	}
	wantLabel := on && m.notifHit.Cut && !m.notifHoldSpent && m.tooltipsEnabled(tooltipDockNotif)
	return wantLabel != (m.Tooltip.Source == tooltipDockNotif)
}

// renderDockNotifTooltip shows the first lines of a cut message above the
// block, wrapped to the block's width cap, and how to read the rest.
func (m *OS) renderDockNotifTooltip() *lipgloss.Layer {
	if !m.tooltipVisible(tooltipDockNotif) {
		return nil
	}
	m.Tooltip.Shown = true
	if !m.notifHit.Active || !m.notifHit.Cut || len(m.Notifications) == 0 {
		return nil
	}
	msg := m.Notifications[len(m.Notifications)-1]

	pal := theme.UI()
	renderW := m.GetRenderWidth()
	width := max(min(notifBudget(renderW), renderW-2*tooltipPad), 8)
	textW := width - 2*tooltipPad

	lines := m.wrapCached(msg.Message, textW)
	if len(lines) > notifTooltipLines {
		lines = append([]string(nil), lines[:notifTooltipLines]...)
		last := lines[notifTooltipLines-1]
		ell := overlay.Ellipsis()
		lines[notifTooltipLines-1] = strings.TrimRight(truncateToWidth(last, textW-lipgloss.Width(ell)), " ") + ell
	}

	pad := strings.Repeat(" ", tooltipPad)
	text := lipgloss.NewStyle().Background(pal.Surface).Foreground(pal.Fg)
	dim := lipgloss.NewStyle().Background(pal.Surface).Foreground(pal.FgDim)
	rows := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		rows = append(rows, text.Render(pad+l+strings.Repeat(" ", max(textW-lipgloss.Width(l), 0))+pad))
	}
	hint := "Click to show the full message."
	if isClipboardAsk(msg.Target) {
		hint = "Click to allow it."
	}
	hint = overlay.Truncate(hint, textW)
	rows = append(rows, dim.Render(pad+hint+strings.Repeat(" ", max(textW-lipgloss.Width(hint), 0))+pad))

	label := strings.Join(rows, "\n")
	y := m.notifHit.Y - len(rows)
	if m.Settings.DockbarPosition == "top" {
		y = m.notifHit.Y + 1
	}
	return tooltipLayer(label, m.notifHit.X0, y, renderW, "dock-notif-tooltip")
}
