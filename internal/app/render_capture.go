package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Capture mode's drawing: a hint strip along the top, a highlight around the
// window under the pointer, and a marquee while a region is being dragged.
//
// All three are cells. There is no graphics tier here at all, deliberately: a
// selection has to be visible on a plain xterm or the mode is unusable there,
// and the marquee is the same shapes the rest of tuios draws its chrome from,
// so a riced glyph set carries into it.
//
// The rectangles the hover highlight draws are recorded here as they are
// drawn, in captureHits, and the click handler reads them. Nothing recomputes
// a layout in a handler.

// captureHit is one window rectangle capture mode drew a highlight around.
type captureHit struct {
	Index          int
	X0, Y0, X1, Y1 int
}

// renderCaptureMode returns the layers capture mode adds. It records its own
// hit rectangles as it goes.
func (m *OS) renderCaptureMode() []*lipgloss.Layer {
	m.captureHits = m.captureHits[:0]
	if !m.Capture.Active {
		return nil
	}
	pal := theme.UI()
	var layers []*lipgloss.Layer

	// The hint strip. It says what this mode can do, and it says different
	// things for a pointer and for a keyboard, because on a mouse-less entry
	// the drag is not reachable and offering it would be a lie.
	strip := m.captureHintStrip(pal)
	if strip != "" {
		layers = append(layers, lipgloss.NewLayer(strip).
			X(max(0, (m.GetRenderWidth()-lipgloss.Width(strip))/2)).Y(0).
			Z(config.ZIndexContextMenu+1).ID("capture-hints"))
	}

	// The window under the pointer or the keyboard cursor, lifted so "click
	// captures this" is visible before the click.
	for _, idx := range m.captureVisibleWindows() {
		w := m.Windows[idx]
		m.captureHits = append(m.captureHits, captureHit{
			Index: idx, X0: w.X, Y0: w.Y, X1: w.X + w.Width, Y1: w.Y + w.Height,
		})
	}
	if !m.Capture.Dragging && m.Capture.Hover >= 0 && m.Capture.Hover < len(m.Windows) {
		if w := m.Windows[m.Capture.Hover]; w != nil {
			layers = append(layers, m.captureOutline(pal, w.X, w.Y, w.Width, w.Height, "")...)
		}
	}

	if m.Capture.Dragging {
		x0, y0, x1, y1 := m.Capture.rect()
		chip := fmt.Sprintf("%dx%d", x1-x0, y1-y0)
		layers = append(layers, m.captureOutline(pal, x0, y0, x1-x0, y1-y0, chip)...)
	}
	return layers
}

// captureHintStrip is the one-line instruction bar, in whichkey styling.
func (m *OS) captureHintStrip(pal overlay.Palette) string {
	hints := []overlay.Hint{
		{Key: "click", Label: "capture window"},
		{Key: "drag", Label: "select region"},
		{Key: "f", Label: "full screen"},
		{Key: "esc", Label: "cancel"},
	}
	if m.Capture.Keyboard {
		hints = []overlay.Hint{
			{Key: "enter", Label: "capture window"},
			{Key: "tab", Label: "next window"},
			{Key: "f", Label: "full screen"},
			{Key: "esc", Label: "cancel"},
		}
	}
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

// captureOutline draws a rectangle in the user's own border shapes, in the
// accent, with an optional size chip on the bottom edge.
//
// It returns four layers, not one. A lipgloss layer is opaque, so a single
// block with spaces down its middle would blank the very content the user is
// aiming at; four edge layers leave the interior showing through. Dimming
// outside the selection instead would need a translucency lipgloss does not
// have, and costs one full cache invalidation per mode change, so it is left
// out with the cost written down rather than faked.
func (m *OS) captureOutline(pal overlay.Palette, x, y, w, h int, chip string) []*lipgloss.Layer {
	if w < 1 || h < 1 {
		return nil
	}
	// The same resolver every window border goes through: it honours the
	// glyph set, the border style and ascii-only mode, so a riced session's
	// marquee is drawn in its own strokes rather than in a set spelled out
	// here.
	g := config.GetBorderForStyle()
	ink := lipgloss.NewStyle().Foreground(pal.AccentBright).Bold(true)
	z := config.ZIndexContextMenu + 1

	layer := func(body string, lx, ly int, id string) *lipgloss.Layer {
		return lipgloss.NewLayer(ink.Render(body)).X(lx).Y(ly).Z(z).ID(id)
	}

	inner := max(0, w-2)
	top := g.TopLeft + strings.Repeat(orSpace(g.Top), inner) + g.TopRight
	layers := []*lipgloss.Layer{layer(top, x, y, "capture-marquee-top")}

	if h > 1 {
		bottomFill := strings.Repeat(orSpace(g.Bottom), inner)
		if chip != "" && len([]rune(chip))+2 <= inner {
			// The size readout sits on the bottom edge, the macOS crosshair
			// readout translated to cell units.
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

// orSpace substitutes a space for a border glyph a style leaves empty, so a
// hidden border does not collapse the marquee to nothing.
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
		if w != nil && w.Z > topZ {
			top, topZ = h.Index, w.Z
		}
	}
	return top
}
