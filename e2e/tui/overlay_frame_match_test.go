package tuie2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The colours the frame-match test configures: the focused pane border's, the
// ground behind pane content, and the overlay frame colour the option sets.
// None is anything a theme or a panel surface would land on by chance.
var (
	matchBorder  = tuitest.Color{Kind: tuitest.ColorRGB, R: 0xff, G: 0x88, B: 0x00}
	matchPaneBg  = tuitest.Color{Kind: tuitest.ColorRGB, R: 0x20, G: 0x30, B: 0x40}
	matchOverlay = tuitest.Color{Kind: tuitest.ColorRGB, R: 0x00, G: 0xc0, B: 0xff}
)

// TestOverlayFramesMatchPaneBorders checks that a framed overlay looks like a
// pane window: its frame drawn in the colour the focused pane's border is
// drawn in, and the frame and everything inside it on the pane's ground, so
// there is no shaded band between the frame line and the overlay's content.
//
//   - panel: the command palette, framed by frame_panels.
//   - dialog: the rename dialog.
//   - overlay-border-color: both again with overlay_border_color set, whose
//     colour the frames take while the focused pane's border keeps its own.
//
// For each, the four corners, a cell of the top edge and a cell of the left
// edge must carry the frame ink and pane_background as their ground, and so
// must a blank padding cell inside the frame and a cell under body text that
// no row highlight covers. The one pane is tiled to fill the screen, so every
// cell an overlay covers is pane content, and the cell just outside the frame
// must carry pane_background too.
//
// How this could pass wrongly: a frame drawn on the overlay's shade would
// carry the shade, and so would an interior left on it, which is what the
// interior probes catch; a frame left on the default background would take
// the terminal's own, not the pane's; and the focused border's ink is read off
// a pane border cell outside the overlay on the same screen, so it is the
// colour the panes actually show and not only the one the config names. The
// frame is found from the overlay's own title and hints, not by searching for
// the colours, and the body cell from its text.
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
		wantFrameColours(t, term, art, "frame-match-palette", "the command palette", box, matchBorder, paletteInterior)
		closePalette(t, term, "after the frame-match check")
		alive(t, term, "after the palette frame-match check")
	})

	t.Run("dialog", func(t *testing.T) {
		term := startOverlayBorders(t, appearance, truecolor)
		enableTiling(t, term)
		box := openRenameFrame(t, term)
		saveArtifact(t, term, art, "frame-match-rename")
		wantFrameColours(t, term, art, "frame-match-rename", "the rename dialog", box, matchBorder, renameInterior)
		wantFrameRowsOnPane(t, term, art, "frame-match-rename", box)
		closeRename(t, term)
		alive(t, term, "after the rename frame-match check")
	})

	t.Run("overlay-border-color", func(t *testing.T) {
		term := startOverlayBorders(t, appearance+"overlay_border_color = \"#00c0ff\"\n", truecolor)
		enableTiling(t, term)
		box := openPaletteFrameByCorners(t, term, "╮", "╰")
		saveArtifact(t, term, art, "frame-colour-palette")
		wantFrameColours(t, term, art, "frame-colour-palette", "the command palette", box, matchOverlay, paletteInterior)
		closePalette(t, term, "after the frame colour check")

		box = openRenameFrame(t, term)
		saveArtifact(t, term, art, "frame-colour-rename")
		wantFrameColours(t, term, art, "frame-colour-rename", "the rename dialog", box, matchOverlay, renameInterior)
		closeRename(t, term)
		alive(t, term, "after the frame colour check")
	})
}

// cellProbe is one named cell a check reads.
type cellProbe struct {
	name string
	x, y int
}

// paletteInterior is the command palette's interior probes: the padding cell
// right of the left edge on the blank row under the title, and the first cell
// of a command row the cursor is not on.
func paletteInterior(s tuitest.Screen, b overlayBox) []cellProbe {
	body := cellProbe{"body text", -1, -1}
	for y := b.y0 + 1; y < b.y1; y++ {
		if x := rowCol(s, y, "New window"); x > b.x0 && x < b.x1 {
			body.x, body.y = x, y
			break
		}
	}
	return []cellProbe{{"padding", b.x0 + 1, b.y0 + 2}, body}
}

// renameInterior is the rename dialog's interior probes: the padding cell right
// of the left edge on the field row, and the sigil that fronts the field.
func renameInterior(_ tuitest.Screen, b overlayBox) []cellProbe {
	return []cellProbe{{"padding", b.x0 + 1, b.y0 + 1}, {"body text", b.x0 + 2, b.y0 + 1}}
}

// wantFrameColours fails unless every frame cell of b checked carries ink on
// the pane's ground, the interior cells interior names and the cell just
// outside the frame are on the pane's ground too, and a pane border cell
// outside b carries the focused border's colour. It writes what it read beside
// the screen artifacts.
func wantFrameColours(t *testing.T, term *tuitest.Terminal, art, name, what string, b overlayBox, ink tuitest.Color, interior func(tuitest.Screen, overlayBox) []cellProbe) {
	t.Helper()
	frame := []cellProbe{
		{"top-left", b.x0, b.y0}, {"top-right", b.x1, b.y0},
		{"bottom-left", b.x0, b.y1}, {"bottom-right", b.x1, b.y1},
		{"top", b.x0 + 1, b.y0}, {"left", b.x0, b.y0 + 1},
	}
	var report string
	err := term.WaitFor(func(s tuitest.Screen) bool {
		var r strings.Builder
		ok := true
		for _, p := range frame {
			c := s.Cell(p.x, p.y)
			fmt.Fprintf(&r, "%-12s (%d,%d) %q fg=%+v bg=%+v\n", p.name, p.x, p.y, c.Content, c.Fg, c.Bg)
			if c.Fg != ink || c.Bg != matchPaneBg {
				ok = false
			}
		}
		grounds := append([]cellProbe{{"outside", b.x0 - 1, b.y0 + 1}}, interior(s, b)...)
		for _, p := range grounds {
			if p.x < 0 {
				fmt.Fprintf(&r, "%-12s not found\n", p.name)
				ok = false
				continue
			}
			c := s.Cell(p.x, p.y)
			fmt.Fprintf(&r, "%-12s (%d,%d) %q bg=%+v\n", p.name, p.x, p.y, c.Content, c.Bg)
			if c.Bg != matchPaneBg {
				ok = false
			}
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
		t.Fatalf("%s at %+v does not read as a pane window (want frame fg=%+v, every ground %+v, a pane border fg=%+v):\n%s\n%s",
			what, b, ink, matchPaneBg, matchBorder, report, term.Snapshot())
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
// border's colour: the focused pane's own border.
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
