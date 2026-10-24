package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestScrollbackBrowserFollowsBorderStyle checks that the scrollback browser
// and its help modal draw their frames in appearance.border_style, as the
// dialogs do.
//
//   - double: the browser's corners are ╔ ╗ ╚ ╝ with ═ and ║ edges, and so are
//     the help modal's that ? opens over it.
//   - hidden: both keep the rounded ╭ ╮ ╰ ╯. Neither has a fill, so its frame is
//     its only edge, and a frame that is a surface's only edge falls back
//     rather than vanishing, as the 16-colour palette's does in
//     TestOverlayBordersFollowBorderStyle/hidden-16.
//
// How this could pass wrongly, written down first:
//   - The panes' borders take the same style, so a ╔ anywhere on screen proves
//     nothing. Each frame is found from its own text: the browser's top-left
//     corner is two rows above and three cells left of its title, and the help
//     modal's is two rows above its title and three cells left of its dismiss
//     line, two rows above its bottom edge.
//   - The right and bottom edges are found as the last frame glyph along the
//     row under the top edge and down the left edge, whatever glyph that is,
//     so a frame in the wrong style is still found and reported glyph by glyph
//     instead of failing as a browser that never opened.
//   - Under hidden the panes draw no border, so a rounded corner on screen is
//     the browser's own.
func TestScrollbackBrowserFollowsBorderStyle(t *testing.T) {
	art := artifactDir(t)
	rounded := overlayFrame{"╭", "╮", "╰", "╯", "─", "│"}
	for _, tc := range []struct {
		name string
		want overlayFrame
	}{
		{"double", overlayFrame{"╔", "╗", "╚", "╝", "═", "║"}},
		{"hidden", rounded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term, _ := startOverlaySession(t, "border_style = \""+tc.name+"\"\n", truecolor)
			showScrollbackBrowser(t, term)
			browser := waitFramedBox(t, term, "the scrollback browser", func(s tuitest.Screen) (int, int) {
				y := rowWith(s, browserTitle)
				if y < 0 || rowWith(s, "> echo two") < 0 {
					return -1, -1
				}
				return rowCol(s, y, browserTitle) - 3, y - 2
			})
			saveArtifact(t, term, art, tc.name+"-scrollback-browser")
			wantFrame(t, term, "the scrollback browser under "+tc.name, browser, tc.want)

			sendKeys(t, term, "?")
			help := waitFramedBox(t, term, "the browser's help modal", func(s tuitest.Screen) (int, int) {
				dy, ty := rowWith(s, browserHelpDismiss), rowWith(s, "Keys")
				if dy < 0 || ty < 0 {
					return -1, -1
				}
				return rowCol(s, dy, browserHelpDismiss) - 3, ty - 2
			})
			saveArtifact(t, term, art, tc.name+"-scrollback-browser-help")
			if y := rowWith(term.Screen(), browserHelpDismiss); y != help.y1-2 {
				t.Fatalf("the help modal's dismiss line is on row %d, want two rows above its bottom edge at %+v:\n%s", y, help, term.Snapshot())
			}
			wantFrame(t, term, "the browser's help modal under "+tc.name, help, tc.want)

			sendKeys(t, term, "?")
			if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), browserHelpDismiss) }, uiTimeout); err != nil {
				t.Fatalf("? did not close the help modal: %v\n%s", err, term.Snapshot())
			}
			closeScrollbackBrowser(t, term)
			alive(t, term, "after the browser under "+tc.name)
		})
	}
}

// browserHelpDismiss is the last line of the browser's help modal.
const browserHelpDismiss = "Press ? or esc to close"

// waitFramedBox waits until topLeft finds a frame's top-left corner from the
// surface's own text, and a frame glyph sits there, then finds the right edge
// as the last frame glyph along the row under the top edge and the bottom edge
// as the last frame glyph down the left edge.
func waitFramedBox(t *testing.T, term *tuitest.Terminal, what string, topLeft func(tuitest.Screen) (int, int)) overlayBox {
	t.Helper()
	isFrame := func(c string) bool { return c != "" && c != " " && strings.Contains(frameGlyphs, c) }
	var b overlayBox
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		x0, y0 := topLeft(s)
		if x0 < 0 || y0 < 0 || !isFrame(s.Cell(x0, y0).Content) {
			return false
		}
		cols, rows := s.Size()
		b = overlayBox{x0: x0, y0: y0}
		for x := x0 + 1; x < cols; x++ {
			if isFrame(s.Cell(x, y0+1).Content) {
				b.x1 = x
			}
		}
		for y := y0 + 1; y < rows; y++ {
			if isFrame(s.Cell(x0, y).Content) {
				b.y1 = y
			}
		}
		return b.x1 > x0 && b.y1 > y0+1
	}, uiTimeout); err != nil {
		t.Fatalf("%s never opened with a frame found from its text: %v\n%s", what, err, term.Snapshot())
	}
	return b
}
