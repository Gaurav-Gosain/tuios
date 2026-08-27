package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// An underline says a run of cells is a link. It does not say where the link
// goes, and for an OSC 8 hyperlink the two have nothing to do with each other:
// the words on the screen are whatever the program chose, and the address is
// hidden behind them. A label that does not name the target would therefore be
// a feature that asks the user to click and find out.
//
// So the label names the target and says what to press. It is the whole of the
// feature's discoverability: nothing else on screen mentions the shortcut, and
// a hover that only underlines teaches nobody that a click would do anything.
//
// It appears with the underline rather than after a delay. The tooltip surfaces
// wait 350 ms because crossing a control on the way somewhere else should pop
// nothing; a link is different in that crossing one is already drawing the
// underline, so a delayed label would be a second thing arriving late to
// explain the first.

// linkLabelHint is what the label says about acting on the run. It is the only
// place in the interface that names the gesture.
const linkLabelHint = "shift+click to open"

// linkLabelMax bounds the address. A URL can be thousands of characters and the
// label is one row on a screen that is not; the end is trimmed rather than the
// middle, so the scheme and the host, which are what tell a user whether they
// trust it, always survive.
const linkLabelMax = 72

// renderLinkLabel composes the label for the run under the pointer, or nil when
// there is none.
func (m *OS) renderLinkLabel() *lipgloss.Layer {
	link, ok := m.HoveredLink()
	if !ok || link.URL == "" {
		return nil
	}

	pal := theme.UI()
	renderW := m.GetRenderWidth()

	body := overlay.Truncate(sanitizeLinkText(link.URL), linkLabelMax)
	text := body + "  " + linkLabelHint
	label := tooltipLabel(text, max(renderW-2, 1), pal)

	// The label sits one row under the pointer so it never covers the run it is
	// naming, and flips above when there is no room below. tooltipLayer clamps
	// it to the screen either way, which is what keeps a link near the right
	// edge from pushing its own label off it.
	x, y := m.LastMouseX, m.LastMouseY+1
	if y >= m.GetTopMargin()+m.GetUsableHeight() {
		y = m.LastMouseY - 1
	}
	return tooltipLayer(label, x, y, renderW, "link-label")
}

// sanitizeLinkText makes an address safe to draw.
//
// The string came from a program's own output, so it may carry anything: an
// escape sequence in it would be interpreted by the terminal drawing the label
// rather than shown, and a control byte would move the cursor. The set stripped
// here is the same one the desktop notification path strips, and for the same
// reason.
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
