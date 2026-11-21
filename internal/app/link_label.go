package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func linkLabelHint(s *config.Settings) string {
	switch s.LinkClick {
	case config.LinkClickOff:
		return ""
	case config.LinkClickShift:
		return "shift+click to open"
	case config.LinkClickCtrl:
		return "ctrl+click to open"
	}
	return "ctrl+click to open"
}

func linkLabelHintFor(rawURL string, s *config.Settings) string {
	if target, ok := parseTuiosLink(rawURL); ok {
		verb := "jumps to that window"
		if target.close {
			verb = "asks to close that window"
		}
		if hint := linkLabelHint(s); hint != "" {
			gesture := strings.TrimSuffix(hint, " to open")
			return gesture + " " + verb
		}
		return ""
	}
	return linkLabelHint(s)
}

const linkLabelMax = 72

func (m *OS) renderLinkLabel() *lipgloss.Layer {
	link, ok := m.HoveredLink()
	if !ok || link.URL == "" {
		return nil
	}

	pal := theme.UI()
	renderW := m.GetRenderWidth()

	addr, hint := linkDisplayAddress(link.URL), linkLabelHintFor(link.URL, &m.Settings)
	if p, ok := fileLinkLabel(link.URL); ok {
		addr, hint = p, strings.Replace(hint, " to open", " to preview", 1)
	}
	text := overlay.Truncate(sanitizeLinkText(addr), linkLabelMax)
	if hint != "" {
		text += "  " + hint
	}
	label := tooltipLabel(text, max(renderW-2, 1), pal)

	return m.pointerLabelLayer(label, "link-label")
}

func sanitizeLinkText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
