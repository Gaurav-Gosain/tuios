package tuie2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The colours the frame-match test configures: the focused pane border's, and
// the ground behind pane content. Neither is anything a theme or a panel
// surface would land on by chance.
var (
	matchBorder = tuitest.Color{Kind: tuitest.ColorRGB, R: 0xff, G: 0x88, B: 0x00}
	matchPaneBg = tuitest.Color{Kind: tuitest.ColorRGB, R: 0x20, G: 0x30, B: 0x40}
)

// TestOverlayFramesMatchPaneBorders checks that an overlay's frame looks like
// a pane's border: drawn in the colour the focused pane's border is drawn in,
// and on whatever is behind the overlay rather than on the overlay's own
// surface, so a corner does not sit in a shaded square.
//
//   - panel: the command palette, framed by frame_panels.
//   - dialog: the rename dialog.
//
// For each, the four corners, a cell of the top edge and a cell of the left
// edge must carry the configured border_focused_color as their ink and the
// configured pane_background as their ground: the one pane is tiled to fill
// the screen, so every cell an overlay covers is pane content, and the cell
// just outside the frame must carry pane_background too.
//
// How this could pass wrongly: a frame on the panel's surface would carry the
// surface colour, so the ground is also checked against a cell inside the
// frame, which must differ; a frame left on the default background would take
// the terminal's own, not the pane's; and the ink is checked against a pane
// border cell read off the same screen, outside the overlay, so it is the
// colour the panes actually show and not only the one the config names. The
// frame is found from the overlay's own title and hints, not by searching for
// the colours.
func TestOverlayFramesMatchPaneBorders(t *testing.T) {
	art := artifactDir(t)
	const appearance = "frame_panels = true\n" +
		"border_focused_color = \"#ff8800\"\n" +
		"pane_background = \"#203040\"\n"

	t.Run("panel", func(t *testing.T) {
		term := startOverlayBorders(t, appearance, truecolor)
		enableTiling(t, term)
		box := openPaletteFrameByCorners(t, term, "╮", "╰")
		saveArtifact(t, term, art, "frame-match-palette")
		wantFrameColours(t, term, art, "frame-match-palette", "the command palette's frame", box)
		closePalette(t, term, "after the frame-match check")
		alive(t, term, "after the palette frame-match check")
	})

	t.Run("dialog", func(t *testing.T) {
		term := startOverlayBorders(t, appearance, truecolor)
		enableTiling(t, term)
		box := openRenameFrame(t, term)
		saveArtifact(t, term, art, "frame-match-rename")
		wantFrameColours(t, term, art, "frame-match-rename", "the rename dialog's frame", box)
		wantFrameRowsOnPane(t, term, art, "frame-match-rename", box)
		closeRename(t, term)
		alive(t, term, "after the rename frame-match check")
	})
}

// wantFrameColours fails unless every frame cell of b checked carries the
// focused border's ink on the pane's ground, the cell just inside the frame is
// on a different ground, and a pane border cell outside b carries the same
// ink. It writes what it read beside the screen artifacts.
func wantFrameColours(t *testing.T, term *tuitest.Terminal, art, name, what string, b overlayBox) {
	t.Helper()
	type probe struct {
		name string
		x, y int
	}
	probes := []probe{
		{"top-left", b.x0, b.y0}, {"top-right", b.x1, b.y0},
		{"bottom-left", b.x0, b.y1}, {"bottom-right", b.x1, b.y1},
		{"top", b.x0 + 1, b.y0}, {"left", b.x0, b.y0 + 1},
	}
	var report string
	err := term.WaitFor(func(s tuitest.Screen) bool {
		var r strings.Builder
		ok := true
		for _, p := range probes {
			c := s.Cell(p.x, p.y)
			fmt.Fprintf(&r, "%-12s (%d,%d) %q fg=%+v bg=%+v\n", p.name, p.x, p.y, c.Content, c.Fg, c.Bg)
			if c.Fg != matchBorder || c.Bg != matchPaneBg {
				ok = false
			}
		}
		outside := s.Cell(b.x0-1, b.y0+1)
		fmt.Fprintf(&r, "%-12s (%d,%d) %q bg=%+v\n", "outside", b.x0-1, b.y0+1, outside.Content, outside.Bg)
		if outside.Bg != matchPaneBg {
			ok = false
		}
		inside := s.Cell(b.x0+1, b.y0+1)
		fmt.Fprintf(&r, "%-12s (%d,%d) %q bg=%+v\n", "inside", b.x0+1, b.y0+1, inside.Content, inside.Bg)
		if inside.Bg == matchPaneBg {
			ok = false
		}
		bx, by, found := paneBorderCell(s, b)
		fmt.Fprintf(&r, "%-12s (%d,%d) found=%v\n", "pane border", bx, by, found)
		if !found {
			ok = false
		}
		report = r.String()
		return ok
	}, uiTimeout)
	saveOverlayText(t, art, name+"-colours", report)
	if err != nil {
		t.Fatalf("%s at %+v does not match the focused pane's border (want fg=%+v bg=%+v, a different bg inside):\n%s\n%s",
			what, b, matchBorder, matchPaneBg, report, term.Snapshot())
	}
}

// wantFrameRowsOnPane fails unless every cell of b's top and bottom rows sits
// on the pane's ground: the dialog's title and its hints ride in those rows,
// and a label on the dialog's own surface would be a shaded patch on an
// unshaded line. The title row must hold a letter, so a frame drawn with no
// title cannot pass for one whose title was checked.
func wantFrameRowsOnPane(t *testing.T, term *tuitest.Terminal, art, name string, b overlayBox) {
	t.Helper()
	var report string
	err := term.WaitFor(func(s tuitest.Screen) bool {
		var r strings.Builder
		ok, letters := true, 0
		for _, y := range []int{b.y0, b.y1} {
			for x := b.x0; x <= b.x1; x++ {
				c := s.Cell(x, y)
				if c.Content != "" && !strings.Contains(frameGlyphs+" ", c.Content) {
					letters++
				}
				if c.Bg != matchPaneBg {
					fmt.Fprintf(&r, "(%d,%d) %q bg=%+v\n", x, y, c.Content, c.Bg)
					ok = false
				}
			}
		}
		fmt.Fprintf(&r, "label cells: %d\n", letters)
		report = r.String()
		return ok && letters > 0
	}, uiTimeout)
	saveOverlayText(t, art, name+"-label-grounds", report)
	if err != nil {
		t.Fatalf("the dialog's title or hints at %+v are not on the pane's ground %+v:\n%s\n%s", b, matchPaneBg, report, term.Snapshot())
	}
}

// paneBorderCell finds a box-drawing cell outside b drawn in the focused
// border's ink: the focused pane's own border, which the frame is held to.
func paneBorderCell(s tuitest.Screen, b overlayBox) (int, int, bool) {
	cols, rows := s.Size()
	for y := range rows {
		for x := range cols {
			if x >= b.x0 && x <= b.x1 && y >= b.y0 && y <= b.y1 {
				continue
			}
			c := s.Cell(x, y)
			if c.Fg == matchBorder && c.Content != "" && strings.Contains(frameGlyphs, c.Content) {
				return x, y, true
			}
		}
	}
	return -1, -1, false
}
