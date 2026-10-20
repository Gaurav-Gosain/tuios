package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// frameGlyphs is every box-drawing glyph a dialog or panel frame can be drawn
// with in the styles this test sets. A hidden frame draws none of them.
const frameGlyphs = "╭╮╰╯─│╔╗╚╝═║┌┐└┘━┃┏┓┗┛"

// overlayFrame is the frame glyphs read off the screen at the corners and
// edges of an overlay: tl, tr, bl and br are the four corners, top is the
// cell next to the top-left corner and side the cell under it.
type overlayFrame struct {
	tl, tr, bl, br, top, side string
}

func (f overlayFrame) String() string {
	return fmt.Sprintf("tl=%q tr=%q bl=%q br=%q top=%q side=%q", f.tl, f.tr, f.bl, f.br, f.top, f.side)
}

// TestOverlayBordersFollowBorderStyle checks that the small dialogs follow
// appearance.border_style, that panels stay unframed until
// appearance.frame_panels asks for a frame, and that a framed panel takes the
// same style.
//
//   - double: the rename dialog's corners are ╔ ╗ ╚ ╝, and the command palette
//     draws no frame.
//   - double with frame_panels: the palette's corners are ╔ ╗ ╚ ╝ too.
//   - hidden: the rename dialog draws no frame glyph, and its title and hints
//     are still on screen where the frame would carry them.
//   - block: the rename dialog falls back to the rounded ╭ ╮ ╰ ╯.
//   - hidden at 16 colours: the dialog still draws no frame, but the palette,
//     whose frame is its only edge on the terminal's own background, falls
//     back to rounded rather than vanishing.
//
// How this could pass wrongly: the panes' own borders take the same style, so
// a ╔ anywhere on screen proves nothing. Every glyph is read at a cell found
// from the overlay's own title: the dialog's top-left corner is three cells
// left of its title, the panel's is three cells left of its title chip's
// text, and the right edge is found from the dialog's hints, which end two
// cells inside it. The terminal is truecolor, because at 16 colours a panel
// frames itself and the unframed case would not be tested, except in the
// 16-colour case, which is about that self-frame.
func TestOverlayBordersFollowBorderStyle(t *testing.T) {
	art := artifactDir(t)

	t.Run("double", func(t *testing.T) {
		term := startOverlayBorders(t, "border_style = \"double\"\n", truecolor)
		dialog := openRenameFrame(t, term)
		saveArtifact(t, term, art, "double-rename")
		wantFrame(t, term, "the rename dialog under double", dialog, overlayFrame{"╔", "╗", "╚", "╝", "═", "║"})
		closeRename(t, term)

		panel := openPaletteFrame(t, term)
		saveArtifact(t, term, art, "double-palette")
		wantFrame(t, term, "the command palette without frame_panels", panel, overlayFrame{" ", " ", " ", " ", " ", " "})
		closePalette(t, term, "under double")
		alive(t, term, "under double")
	})

	t.Run("double-frame-panels", func(t *testing.T) {
		term := startOverlayBorders(t, "border_style = \"double\"\nframe_panels = true\n", truecolor)
		panel := openPaletteFrameByCorners(t, term, "╗", "╚")
		saveArtifact(t, term, art, "double-frame-panels-palette")
		wantFrame(t, term, "the command palette with frame_panels", panel, overlayFrame{"╔", "╗", "╚", "╝", "═", "║"})
		closePalette(t, term, "under frame_panels")

		dialog := openRenameFrame(t, term)
		saveArtifact(t, term, art, "double-frame-panels-rename")
		wantFrame(t, term, "the rename dialog with frame_panels", dialog, overlayFrame{"╔", "╗", "╚", "╝", "═", "║"})
		closeRename(t, term)
		alive(t, term, "under frame_panels")
	})

	t.Run("hidden", func(t *testing.T) {
		term := startOverlayBorders(t, "border_style = \"hidden\"\n", truecolor)
		dialog := openRenameFrame(t, term)
		saveArtifact(t, term, art, "hidden-rename")
		wantFrame(t, term, "the rename dialog under hidden", dialog, overlayFrame{" ", " ", " ", " ", " ", " "})
		s := term.Screen()
		var region strings.Builder
		for y := dialog.y0; y <= dialog.y1; y++ {
			for x := dialog.x0; x <= dialog.x1; x++ {
				region.WriteString(s.Cell(x, y).Content)
			}
			region.WriteString("\n")
		}
		if strings.ContainsAny(region.String(), frameGlyphs) {
			t.Fatalf("the hidden rename dialog drew a frame glyph:\n%s\n%s", region.String(), term.Snapshot())
		}
		for _, want := range []string{"rename", "save", "esc cancel"} {
			if !strings.Contains(region.String(), want) {
				t.Fatalf("the hidden rename dialog lost %q:\n%s\n%s", want, region.String(), term.Snapshot())
			}
		}
		saveOverlayText(t, art, "hidden-rename-region", region.String())
		closeRename(t, term)
		alive(t, term, "under hidden")
	})

	t.Run("block", func(t *testing.T) {
		term := startOverlayBorders(t, "border_style = \"block\"\n", truecolor)
		dialog := openRenameFrame(t, term)
		saveArtifact(t, term, art, "block-rename")
		wantFrame(t, term, "the rename dialog under block", dialog, overlayFrame{"╭", "╮", "╰", "╯", "─", "│"})
		closeRename(t, term)
		alive(t, term, "under block")
	})

	t.Run("hidden-16", func(t *testing.T) {
		term := startOverlayBorders(t, "border_style = \"hidden\"\n", sixteenColours)
		dialog := openRenameFrame(t, term)
		saveArtifact(t, term, art, "hidden-16-rename")
		wantFrame(t, term, "the rename dialog under hidden at 16 colours", dialog, overlayFrame{" ", " ", " ", " ", " ", " "})
		closeRename(t, term)

		panel := openPaletteFrameByCorners(t, term, "╮", "╰")
		saveArtifact(t, term, art, "hidden-16-palette")
		wantFrame(t, term, "the command palette under hidden at 16 colours", panel, overlayFrame{"╭", "╮", "╰", "╯", "─", "│"})
		closePalette(t, term, "under hidden at 16 colours")
		alive(t, term, "under hidden at 16 colours")
	})
}

// The colour depths the subtests run at, as TERM and COLORTERM select them.
var (
	truecolor      = []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
	sixteenColours = []string{"TERM=xterm", "COLORTERM="}
)

// startOverlayBorders attaches a client with env (its colour depth) to a fresh
// daemon whose config carries appearance, and settles it in window-management
// mode.
func startOverlayBorders(t *testing.T, appearance string, env []string) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[appearance]\n"+appearance)
	const session = "ovb"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create %s: %v: %s", session, err, out)
	}
	term := startIn(t, base, startOpts{
		args: []string{"attach", session},
		env:  env,
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	return term
}

// overlayBox is where an overlay's frame cells are: x0 and x1 are its left and
// right columns, y0 and y1 its top and bottom rows.
type overlayBox struct{ x0, x1, y0, y1 int }

// rowCol returns the column text starts at in row y, counting cells rather
// than runes, or -1.
func rowCol(s tuitest.Screen, y int, text string) int {
	cols, _ := s.Size()
	var row strings.Builder
	var at []int
	for x := range cols {
		c := s.Cell(x, y).Content
		if c == "" {
			continue
		}
		for range len(c) {
			at = append(at, x)
		}
		row.WriteString(c)
	}
	i := strings.Index(row.String(), text)
	if i < 0 {
		return -1
	}
	return at[i]
}

// renameBox finds the rename dialog: its title sits three cells right of the
// top-left corner, and its hint strip ends with "cancel", one pad cell and one
// rule cell inside the right edge, on the bottom border two rows down.
func renameBox(s tuitest.Screen) (overlayBox, bool) {
	_, rows := s.Size()
	for y := 0; y+2 < rows; y++ {
		tx := rowCol(s, y, " rename ")
		if tx < 0 {
			continue
		}
		hx := rowCol(s, y+2, "esc cancel")
		if hx < 0 {
			continue
		}
		return overlayBox{x0: tx - 2, x1: hx + len("esc cancel") + 2, y0: y, y1: y + 2}, true
	}
	return overlayBox{}, false
}

func openRenameFrame(t *testing.T, term *tuitest.Terminal) overlayBox {
	t.Helper()
	sendKeys(t, term, "r")
	var box overlayBox
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		var ok bool
		box, ok = renameBox(s)
		return ok
	}, uiTimeout); err != nil {
		t.Fatalf("the rename dialog never opened: %v\n%s", err, term.Snapshot())
	}
	return box
}

func closeRename(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !renameDialogUp(s) }, uiTimeout); err != nil {
		t.Fatalf("the rename dialog never closed: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
}

// openPaletteFrame opens the command palette and finds its frame cells. The
// title chip's text is one chip pad and two cells of panel padding right of
// the left edge, the top edge is the row above the chip, and the right edge
// and the bottom are where the panel's surface ends, read along the top row
// and down the padding column, which no row highlight reaches.
func openPaletteFrame(t *testing.T, term *tuitest.Terminal) overlayBox {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('p'))
	waitPaletteOpen(t, term, "to read its frame")
	s := term.Screen()
	cols, rows := s.Size()
	for y := 1; y < rows; y++ {
		tx := rowCol(s, y, paletteTitle)
		if tx < 0 {
			continue
		}
		x0, top := tx-3, y-1
		ground := s.Cell(x0+1, top).Bg
		x1 := x0 + 1
		for x1+1 < cols && s.Cell(x1+1, top).Bg == ground {
			x1++
		}
		y1 := y
		for y1+1 < rows && s.Cell(x0+1, y1+1).Bg == ground {
			y1++
		}
		return overlayBox{x0: x0, x1: x1, y0: top, y1: y1}
	}
	t.Fatalf("the command palette's title is not on screen:\n%s", term.Snapshot())
	return overlayBox{}
}

// openPaletteFrameByCorners is openPaletteFrame for a framed panel, whose
// frame cells sit on what is behind the panel rather than on its fill, so the
// panel's extent cannot be read off a fill. The left edge is found the same
// way, and the right edge and the bottom are the top-right corner tr and the
// bottom-left corner bl the test expects there: a missing corner leaves the
// box at the left edge alone, and wantFrame then reports what is drawn there.
func openPaletteFrameByCorners(t *testing.T, term *tuitest.Terminal, tr, bl string) overlayBox {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('p'))
	waitPaletteOpen(t, term, "to read its frame")
	s := term.Screen()
	cols, rows := s.Size()
	for y := 1; y < rows; y++ {
		tx := rowCol(s, y, paletteTitle)
		if tx < 0 {
			continue
		}
		b := overlayBox{x0: tx - 3, x1: tx - 3, y0: y - 1, y1: y - 1}
		for x := b.x0 + 1; x < cols; x++ {
			if s.Cell(x, b.y0).Content == tr {
				b.x1 = x
				break
			}
		}
		for yy := y; yy < rows; yy++ {
			if s.Cell(b.x0, yy).Content == bl {
				b.y1 = yy
				break
			}
		}
		return b
	}
	t.Fatalf("the command palette's title is not on screen:\n%s", term.Snapshot())
	return overlayBox{}
}

// wantFrame fails unless the overlay's corners and edges carry want.
func wantFrame(t *testing.T, term *tuitest.Terminal, what string, b overlayBox, want overlayFrame) {
	t.Helper()
	var got overlayFrame
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		got = overlayFrame{
			tl:   s.Cell(b.x0, b.y0).Content,
			tr:   s.Cell(b.x1, b.y0).Content,
			bl:   s.Cell(b.x0, b.y1).Content,
			br:   s.Cell(b.x1, b.y1).Content,
			top:  s.Cell(b.x0+1, b.y0).Content,
			side: s.Cell(b.x0, b.y0+1).Content,
		}
		return got == want
	}, uiTimeout); err != nil {
		t.Fatalf("%s at %+v: got %s, want %s\n%s", what, b, got, want, term.Snapshot())
	}
}

// saveOverlayText writes text into the artifact directory beside the frames.
func saveOverlayText(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(text), 0o644); err != nil {
		t.Errorf("save %s: %v", name, err)
	}
}
