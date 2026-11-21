package app

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	sidebarMarqueeGap = 4
)

// SidebarMarqueeActive reports whether a row is currently scrolling, so the
// update loop keeps the render tick at normal rate. It reflects the last frame:
// an empty key means the hovered row (if any) fits and the tick may idle.
func (m *OS) SidebarMarqueeActive() bool {
	return m.SidebarMarqueeKey != ""
}

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

func (m *OS) marqueePause() time.Duration {
	return time.Duration(m.Settings.MarqueePauseMS) * time.Millisecond
}

func (m *OS) marqueeStep() time.Duration {
	ms := m.Settings.MarqueeStepMS
	if ms <= 0 {
		ms = config.MarqueeStepMSDefault
	}
	return time.Duration(ms) * time.Millisecond
}
