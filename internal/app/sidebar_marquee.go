package app

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// A hovered sidebar row whose title overflows its columns scrolls the full text
// past the row so the hidden tail can be read without a resize. Only the hovered
// row animates, driven off the render tick: the scroll position is a pure
// function of wall time since the hover landed, so the same frame that draws the
// row also advances it, and nothing scrolls while nothing overflowing is hovered.

const (
	// sidebarMarqueeGap is the blank run between the tail and the wrapped head,
	// so the text reads as a loop rather than a jump.
	sidebarMarqueeGap = 4
)

// SidebarMarqueeActive reports whether a row is currently scrolling, so the
// update loop keeps the render tick at normal rate. It reflects the last frame:
// an empty key means the hovered row (if any) fits and the tick may idle.
func (m *OS) SidebarMarqueeActive() bool {
	return m.SidebarMarqueeKey != ""
}

// sidebarMarquee renders the plain title s into avail cells. A hovered row whose
// text overflows scrolls; every other case is a plain truncation. key is the
// row's identity so hover moving to another row restarts the cycle, and marking
// it seen keeps SidebarMarqueeKey alive only while this row still draws hovered.
func (m *OS) sidebarMarquee(key, s string, avail int, hovered bool) string {
	if avail < 1 {
		avail = 1
	}
	if !hovered || !m.Settings.SidebarMarquee || lipgloss.Width(s) <= avail {
		return overlay.Truncate(s, avail)
	}

	m.sidebarMarqueeSeen = true
	if key != m.SidebarMarqueeKey {
		m.SidebarMarqueeKey = key
		m.SidebarMarqueeStart = time.Now()
	}

	shift := 0
	if e := time.Since(m.SidebarMarqueeStart) - m.marqueePause(); e > 0 {
		cycle := lipgloss.Width(s) + sidebarMarqueeGap
		shift = int(e/m.marqueeStep()) % cycle
	}
	return sidebarMarqueeWindow(s, avail, shift)
}

// sidebarMarqueeWindow returns exactly avail cells of s scrolled shift cells to
// the left, looping through a gap. Two copies guarantee enough tail for any
// shift inside one cycle.
func sidebarMarqueeWindow(s string, avail, shift int) string {
	loop := s + strings.Repeat(" ", sidebarMarqueeGap)
	loop += loop
	runes := []rune(loop)
	i, w := 0, 0
	for i < len(runes) && w < shift {
		w += lipgloss.Width(string(runes[i]))
		i++
	}
	return lipgloss.NewStyle().MaxWidth(avail).Render(string(runes[i:]))
}

// marqueePause is how long a hovered row holds still when hover first lands, so
// a glance at a row is legible before it starts moving
// (appearance.sidebar.marquee_pause_ms).
func (m *OS) marqueePause() time.Duration {
	return time.Duration(m.Settings.MarqueePauseMS) * time.Millisecond
}

// marqueeStep is how long each one-cell step is held; larger is slower
// (appearance.sidebar.marquee_step_ms).
func (m *OS) marqueeStep() time.Duration {
	ms := m.Settings.MarqueeStepMS
	if ms <= 0 {
		ms = config.MarqueeStepMSDefault
	}
	return time.Duration(ms) * time.Millisecond
}
