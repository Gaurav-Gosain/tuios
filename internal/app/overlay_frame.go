package app

import (
	"fmt"
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// An overlay's frame is drawn the way a pane's border is: in the focused
// pane's border colour, and on whatever is behind the overlay rather than on
// the overlay's own surface, so a corner glyph does not sit in a shaded square.
//
// A cell a layer leaves with no background does not show the cell beneath it:
// the compositor copies a layer's cells over the canvas whole, so such a cell
// takes the terminal's default background, or the desktop's ground when that is
// painted. The frame cells therefore carry seeThroughBg, which the compositor
// replaces with the background of the cell it lands on. The marked cells are
// found when a layer is parsed, and only in a layer whose string carries the
// marker, so a layer without one pays a substring search when it is parsed and
// nothing when it is copied. A layer that shows a program's output is never
// looked at: a program may paint the marker's colour itself, and that cell is
// the program's to colour.

// seeThroughBg marks a cell whose background is the cell beneath it. It never
// reaches the terminal: the compositor replaces it on every cell it marks.
var seeThroughBg color.Color = color.RGBA{R: 0x01, G: 0xfe, B: 0x02, A: 0xff} //tuios:allow-color a marker the compositor replaces, never drawn

// syncOverlayFrame pushes the colours overlay frames are drawn in: the
// focused pane's border colour, which is what the panes show for the focused
// thing, and the see-through ground.
func (m *OS) syncOverlayFrame() {
	focused := m.GetFocusedWindow()
	overlay.SetFrameColors(overlay.FrameColors{
		Ink:    m.focusedBorderColor(m.focusedStateBorder(focused)),
		Ground: seeThroughBg,
	})
}

// isSeeThrough reports whether c is seeThroughBg, as the parser read it back.
func isSeeThrough(c color.Color) bool {
	if isNilColor(c) {
		return false
	}
	r, g, b, a := c.RGBA()
	wr, wg, wb, wa := seeThroughBg.RGBA()
	return r == wr && g == wg && b == wb && a == wa
}

// seeThroughCell is one marked cell of a parsed layer, relative to the layer.
type seeThroughCell struct{ x, y int }

// seeThroughSGR is the marker as a layer's string carries it, so a layer
// without it, which is every pane, is passed over without a look at its cells.
var seeThroughSGR = func() string {
	r, g, b, _ := seeThroughBg.RGBA()
	return fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, b>>8)
}()

// findSeeThrough records the marked cells of a parsed layer w by h, parsed from
// content.
func findSeeThrough(content string, lines []uv.Line, w, h int, out []seeThroughCell) []seeThroughCell {
	out = out[:0]
	if !strings.Contains(content, seeThroughSGR) {
		return out
	}
	for y := range min(h, len(lines)) {
		line := lines[y]
		for x := range min(w, len(line)) {
			if isSeeThrough(line[x].Style.Bg) {
				out = append(out, seeThroughCell{x, y})
			}
		}
	}
	return out
}

// groundsUnder reads the background each marked cell will land on, before the
// layer is drawn over it. A cell off the canvas reads nil.
func (cl *cellLayer) groundsUnder(canvas *frameCanvas, x, y int) []color.Color {
	cl.under = cl.under[:0]
	for _, c := range cl.see {
		cx, cy := x+c.x, y+c.y
		var bg color.Color
		if cy >= 0 && cy < len(canvas.Lines) && cx >= 0 && cx < len(canvas.Lines[cy]) {
			bg = canvas.Lines[cy][cx].Style.Bg
		}
		cl.under = append(cl.under, bg)
	}
	return cl.under
}

// showThrough gives each marked cell, now drawn, the background read by
// groundsUnder.
func (cl *cellLayer) showThrough(canvas *frameCanvas, x, y int, under []color.Color) {
	for i, c := range cl.see {
		cx, cy := x+c.x, y+c.y
		if cy >= 0 && cy < len(canvas.Lines) && cx >= 0 && cx < len(canvas.Lines[cy]) {
			canvas.Lines[cy][cx].Style.Bg = under[i]
		}
	}
}

// showsProgramOutput reports whether the layer with this id draws a pane's
// output: the pane itself, its picture-in-picture view, or the scrollback
// browser.
func (m *OS) showsProgramOutput(id string) bool {
	return id == pipLayerID || id == "scrollback-browser" || m.windowByID(id) != nil
}
