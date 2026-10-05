package app

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// A link chip is a jump a pane hands its viewer as chrome: the daemon paints
// one line on the pane's bottom row, the client draws it over whatever the
// guest last blitted there, and a click on it jumps. The chip lives on the
// daemon's state (see session.WindowState.LinkChip), so every attached client
// shows it and a guest's redraw cannot take it.

// LinkChipRect is where a pane's chip was drawn on the last frame: the bottom
// row of the pane's content, which the chip line owns while it is up.
type LinkChipRect struct {
	X0, X1, Y int
}

// Contains reports whether a press at (x, y) landed on the chip.
func (r LinkChipRect) Contains(x, y int) bool {
	return y == r.Y && x >= r.X0 && x < r.X1
}

// linkChipRect is the hit rect for the chip line drawn on w's bottom content
// row. The input tests plant chips through SetLinkChipForTest with this, so a
// click and a frame always agree on where the chip is.
func linkChipRect(w *terminal.Window) LinkChipRect {
	off := w.BorderOffset()
	x := w.X + off
	return LinkChipRect{X0: x, X1: x + w.ContentWidth(), Y: w.Y + off + w.ContentHeight() - 1}
}

// noteLinkChip adopts a chip from a state sync, nil clearing the pane's.
func (m *OS) noteLinkChip(windowID string, chip *session.LinkChip) {
	if chip == nil {
		delete(m.linkChips, windowID)
		return
	}
	if m.linkChips == nil {
		m.linkChips = make(map[string]*session.LinkChip)
	}
	m.linkChips[windowID] = chip
}

// LinkChipClick returns the chip drawn at (x, y) and the pane that drew it.
func (m *OS) LinkChipClick(x, y int) (windowID string, chip *session.LinkChip, ok bool) {
	for id, r := range m.linkChipRects {
		if r.Contains(x, y) {
			return id, m.linkChips[id], true
		}
	}
	return "", nil, false
}

// clearLinkChip takes a chip down here and on the daemon, which is what the
// other attached clients read it from. The daemon call is fire-and-forget: the
// chip is gone here already, and a failure leaves it up on the peers until one
// of them repaints it or the pane paints again.
func (m *OS) clearLinkChip(windowID string) {
	m.noteLinkChip(windowID, nil)
	delete(m.linkChipRects, windowID)
	dial := m.verbDialer()
	params := map[string]any{
		"session": m.SessionName,
		"window":  windowID,
		"label":   "",
	}
	go func() {
		client, err := dial()
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		_, _ = client.Call("paint-link", params)
	}()
}

// linkChipJump runs a click on the chip of windowID: jump to the chip's
// target, then take the chip down. The jump records its origin, so ctrl+b u
// comes back here.
func (m *OS) LinkChipJump(windowID string, chip *session.LinkChip) tea.Cmd {
	m.jumpToNotifTarget(NotifTarget{WindowID: chip.Target})
	m.clearLinkChip(windowID)
	return nil
}

// resetLinkChipRects drops the previous frame's chip rects. The chip layer is
// drawn every frame the scrollbar is not cached, so a chip that is gone, moved
// or clipped stops being clickable with it.
func (m *OS) resetLinkChipRects() {
	clear(m.linkChipRects)
}

// renderLinkChipLayers draws one layer per chip: a single muted line across
// the pane's bottom content row, over the guest's cells.
func (m *OS) renderLinkChipLayers(rightClip int) []*lipgloss.Layer {
	if len(m.linkChips) == 0 {
		return nil
	}
	pal := theme.GroundUI()
	var layers []*lipgloss.Layer
	for _, w := range m.Windows {
		chip := m.linkChips[w.ID]
		if chip == nil {
			continue
		}
		off := w.BorderOffset()
		x := w.X + off
		width := w.ContentWidth()
		y := w.Y + off + w.ContentHeight() - 1
		if width <= 0 || x >= rightClip {
			continue
		}
		base := lipgloss.NewStyle()
		ground := m.terminalBg()
		if g := m.paneGround(); g.on() {
			ground = g.bg
			base = base.Background(g.bg)
		}
		hint := pal.FgMute
		if theme.ContrastRatio(hint, ground) < theme.ContrastFloor {
			hint = pal.FgDim
		}
		line := base.Foreground(pal.FgDim).Render(chip.Label) +
			base.Foreground(hint).Render("  click to jump")
		if lipgloss.Width(line) > width {
			line = base.Foreground(pal.FgDim).Render(chip.Label)
		}
		layers = append(layers, lipgloss.NewLayer(line).X(x).Y(y).
			Z(windowLayerZ(w, false)+1).ID("link-chip-"+w.ID))
		if m.linkChipRects == nil {
			m.linkChipRects = make(map[string]LinkChipRect)
		}
		m.linkChipRects[w.ID] = linkChipRect(w)
	}
	return layers
}

// SetLinkChipForTest plants a chip on a pane and records its hit rect, so the
// input tests, which live in another package, can put a chip under the
// pointer without building a frame.
func (m *OS) SetLinkChipForTest(w *terminal.Window, chip *session.LinkChip) {
	m.noteLinkChip(w.ID, chip)
	if chip == nil {
		delete(m.linkChipRects, w.ID)
		return
	}
	if m.linkChipRects == nil {
		m.linkChipRects = make(map[string]LinkChipRect)
	}
	m.linkChipRects[w.ID] = linkChipRect(w)
}

// LinkChipForTest reads a pane's chip back, nil when it has none.
func (m *OS) LinkChipForTest(windowID string) *session.LinkChip {
	return m.linkChips[windowID]
}
