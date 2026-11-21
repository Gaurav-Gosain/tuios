package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type captureHit struct {
	Index          int
	X0, Y0, X1, Y1 int
}

const (
	captureMarqueeZ = config.ZIndexContextMenu + 1
	captureHintZ    = captureMarqueeZ + 1
)

func (m *OS) renderCaptureMode() []*lipgloss.Layer {
	m.captureHits = m.captureHits[:0]
	if !m.Capture.Active {
		return nil
	}
	for _, idx := range m.captureVisibleWindows() {
		r, ok := m.paneOnScreen(m.Windows[idx])
		if !ok {
			continue
		}
		m.captureHits = append(m.captureHits, captureHit{
			Index: idx, X0: r.Min.X, Y0: r.Min.Y, X1: r.Max.X, Y1: r.Max.Y,
		})
	}
	return m.captureLayers()
}

func (m *OS) captureOccluders() []cellRect {
	if !m.Capture.Active {
		return nil
	}
	var out []cellRect
	for _, l := range m.captureLayers() {
		if l.Width() > 0 && l.Height() > 0 {
			out = append(out, cellRect{l.GetX(), l.GetY(), l.Width(), l.Height()})
		}
	}
	return out
}

func (m *OS) captureLayers() []*lipgloss.Layer {
	pal := theme.UI()
	var layers []*lipgloss.Layer

	strip := m.captureHintStrip(pal)
	if strip != "" {
		layers = append(layers, lipgloss.NewLayer(strip).
			X(max(0, (m.GetRenderWidth()-lipgloss.Width(strip))/2)).Y(0).
			Z(captureHintZ).ID("capture-hints"))
	}

	if !m.Capture.Dragging && m.Capture.Hover >= 0 && m.Capture.Hover < len(m.Windows) {
		if w := m.Windows[m.Capture.Hover]; w != nil {
			if r, ok := m.paneOnScreen(w); ok {
				layers = append(layers, m.captureOutline(pal, r.Min.X, r.Min.Y, r.Dx(), r.Dy(), "")...)
			}
		}
	}

	if m.Capture.Dragging {
		x0, y0, x1, y1 := m.Capture.rect()
		chip := fmt.Sprintf("%dx%d", x1-x0, y1-y0)
		layers = append(layers, m.captureOutline(pal, x0, y0, x1-x0, y1-y0, chip)...)
	}
	return layers
}

func (m *OS) captureHintStrip(pal overlay.Palette) string {
	if m.Capture.OverOverlay {
		hints := []overlay.Hint{
			{Key: "drag", Label: "select region"},
			{Key: "click", Label: "full screen"},
			{Key: "esc", Label: "cancel"},
		}
		if m.Capture.Keyboard {
			hints = []overlay.Hint{
				{Key: overlay.EnterKey(), Label: "full screen"},
				{Key: "esc", Label: "cancel"},
			}
		}
		return captureHints(pal, hints)
	}
	hints := []overlay.Hint{
		{Key: "click", Label: "capture window"},
		{Key: "drag", Label: "select region"},
		{Key: "f", Label: "full screen"},
		{Key: "esc", Label: "cancel"},
	}
	if m.Capture.Keyboard {
		hints = []overlay.Hint{
			{Key: overlay.EnterKey(), Label: "capture window"},
			{Key: "tab", Label: "next window"},
			{Key: "f", Label: "full screen"},
			{Key: "esc", Label: "cancel"},
		}
	}
	return captureHints(pal, hints)
}

func captureHints(pal overlay.Palette, hints []overlay.Hint) string {
	key := lipgloss.NewStyle().Background(pal.Card).Foreground(pal.AccentBright).Bold(true)
	label := lipgloss.NewStyle().Background(pal.Card).Foreground(pal.Fg)
	sep := lipgloss.NewStyle().Background(pal.Card).Foreground(pal.FgMute).Render(" · ")

	var parts []string
	for _, h := range hints {
		parts = append(parts, key.Render(h.Key)+label.Render(": "+h.Label))
	}
	pad := lipgloss.NewStyle().Background(pal.Card).Render(" ")
	return pad + strings.Join(parts, sep) + pad
}

func (m *OS) captureOutline(pal overlay.Palette, x, y, w, h int, chip string) []*lipgloss.Layer {
	if w < 1 || h < 1 {
		return nil
	}
	g := m.Settings.GetBorderForStyle()
	ink := lipgloss.NewStyle().Foreground(pal.AccentBright).Bold(true)
	z := captureMarqueeZ

	layer := func(body string, lx, ly int, id string) *lipgloss.Layer {
		return lipgloss.NewLayer(ink.Render(body)).X(lx).Y(ly).Z(z).ID(id)
	}

	inner := max(0, w-2)
	top := g.TopLeft + strings.Repeat(orSpace(g.Top), inner) + g.TopRight
	layers := []*lipgloss.Layer{layer(top, x, y, "capture-marquee-top")}

	if h > 1 {
		bottomFill := strings.Repeat(orSpace(g.Bottom), inner)
		if chip != "" && len([]rune(chip))+2 <= inner {
			runes := []rune(bottomFill)
			label := []rune(" " + chip + " ")
			copy(runes[len(runes)-len(label):], label)
			bottomFill = string(runes)
		}
		bottom := g.BottomLeft + bottomFill + g.BottomRight
		layers = append(layers, layer(bottom, x, y+h-1, "capture-marquee-bottom"))
	}

	if h > 2 && w > 1 {
		side := strings.TrimSuffix(strings.Repeat(orSpace(g.Left)+"\n", h-2), "\n")
		layers = append(layers, layer(side, x, y+1, "capture-marquee-left"))
		right := strings.TrimSuffix(strings.Repeat(orSpace(g.Right)+"\n", h-2), "\n")
		layers = append(layers, layer(right, x+w-1, y+1, "capture-marquee-right"))
	}
	return layers
}

func orSpace(s string) string {
	if s == "" {
		return " "
	}
	return s
}

// CaptureWindowAt returns the window index whose recorded rectangle contains
// the cell, or -1. It reads what the renderer drew, never a fresh layout.
func (m *OS) CaptureWindowAt(x, y int) int {
	top, topZ := -1, -1
	for _, h := range m.captureHits {
		if x < h.X0 || x >= h.X1 || y < h.Y0 || y >= h.Y1 {
			continue
		}
		w := m.Windows[h.Index]
		if w != nil && HitZ(w) >= topZ {
			top, topZ = h.Index, HitZ(w)
		}
	}
	return top
}
