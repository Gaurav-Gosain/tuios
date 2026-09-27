package tuie2e

import (
	"image/color"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The screen behind a modal overlay is dimmed (appearance.modal_dim), and the
// overlay itself is not. This drives the real binary at each colour depth, on
// a dark terminal with no theme and under a light theme, and reads the cells
// the terminal was sent.
//
// A pane prints three words: one in a truecolor ink, one in a 256-colour cube
// ink and one in ANSI red. With the palette open each has to come back quieter
// than it was, in the way the depth can draw it:
//
//   - truecolor and 256: a colour tuios can read channels off (the truecolor
//     and cube inks, and under a theme the ANSI slot, which the theme defines)
//     moves toward its ground: darker on the dark terminal, lighter under the
//     light theme, whose scrim pulls toward its light surface. At 256 the new
//     colour is written as another index, measured on the xterm table.
//   - an ANSI slot with no theme is the user's own colour, which tuios cannot
//     blend, and it goes faint instead.
//   - 16 colours: every word goes faint and keeps its colour.
//
// Then modal_dim is set to 0 with the palette still open. The words come back
// exactly as they were before it opened, and every cell of the palette is the
// same as it was with the dim on: the dim never reached the overlay. Closing
// the palette also puts the words back.
//
// How this could pass wrongly, written down first:
//   - The palette might not be open when the cells are read: each read waits
//     for the palette's title, and the geometry comes from its own search rule.
//   - The words could be found in the echoed command line rather than the
//     output: the markers are built by the shell, so the command line does
//     not contain them.
//   - A cell could differ for another reason (the cursor, a blink): the words
//     sit on a line of their own, away from the prompt, and the comparison
//     with the dim off is made on the same open palette, not a reopened one.
//   - set-config might never reach the client, leaving the second read equal
//     to the first by accident: the second read waits until the words are
//     back to their undimmed cells, and fails if they never are.
//
// Every read is saved under artifactDir as text, styled text and a PNG drawn
// by tuios's own renderer.
func TestModalDimsTheScreenBehind(t *testing.T) {
	for _, look := range chromeLooks {
		for _, d := range chromeDepths {
			t.Run(look.name+"-"+d.name, func(t *testing.T) {
				modalDimRun(t, look.theme, d)
			})
		}
	}
}

// dimWords are the three words the pane prints, each ending in x so the
// command line that prints them (which spells the stem and the x apart) never
// contains one.
var dimWords = []string{"DIMTRUx", "DIMIDXx", "DIMANSIx"}

const dimCommand = `clear; printf '\n\033[38;2;200;150;90m%s\033[m \033[38;5;208m%s\033[m \033[31m%s\033[m\n\n' "$(echo DIMTRU)x" "$(echo DIMIDX)x" "$(echo DIMANSI)x"`

func modalDimRun(t *testing.T, themeName string, d chromeDepth) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	cfg := "[startup]\ntiled = true\n"
	if themeName != "" {
		cfg += "[appearance]\ntheme = \"" + themeName + "\"\n"
	}
	writeConfig(t, base, cfg)
	if out, err := tuiosCLI(t, base, "new", "e2e-dim", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-dim"}, shippedLooks: true, env: d.env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	runInShell(t, term, dimCommand, "DIMANSIx", shellTimeout)
	windowManagementMode(t, term)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v", err)
	}

	dir := artifactDir(t)
	host := hostPalette(t, themeName)
	before := term.Screen()
	saveArtifact(t, term, dir, "before")
	savePNG(t, before, host, dir, "before")
	words := findDimWords(t, term, before)
	railRow, railCol := railNameCell(t, term, before)

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("open the palette: %v", err)
	}
	waitPaletteOpen(t, term, "for the dim")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled with the palette open: %v", err)
	}
	p := findPalette(t, term)
	dimmed := term.Screen()
	saveArtifact(t, term, dir, "palette")
	savePNG(t, dimmed, host, dir, "palette")

	for i, at := range words {
		was, now := before.Cell(at[0], at[1]), dimmed.Cell(at[0], at[1])
		checkDimmed(t, term, dimWords[i], was, now, d.name, themeName)
	}
	// The rail is behind the palette too: its text is quieter or faint.
	if was, now := before.Cell(railCol, railRow), dimmed.Cell(railCol, railRow); !now.Faint && !quieter(now.Fg, was.Fg, themeName != "") {
		t.Errorf("the rail's session name at (%d,%d) was not dimmed: %+v then %+v\n%s",
			railCol, railRow, was, now, term.SnapshotStyled())
	}

	// The same open palette with the dim off: the words come back as they
	// were, and not one palette cell moves.
	if out, err := tuiosCLI(t, base, "set-config", "appearance.modal_dim", "0"); err != nil {
		t.Fatalf("set modal_dim 0: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		for _, at := range words {
			if s.Cell(at[0], at[1]) != before.Cell(at[0], at[1]) {
				return false
			}
		}
		return strings.Contains(s.Text(), paletteTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("with modal_dim 0 the words never came back undimmed: %v\n%s", err, term.SnapshotStyled())
	}
	plain := term.Screen()
	saveArtifact(t, term, dir, "palette-nodim")
	savePNG(t, plain, host, dir, "palette-nodim")
	for y := p.titleRow - 1; y <= paletteBottom(plain, p); y++ {
		for x := p.left; x < p.right; x++ {
			if a, b := dimmed.Cell(x, y), plain.Cell(x, y); a != b {
				t.Fatalf("palette cell (%d,%d) is %+v under the dim and %+v without it: the dim reached the overlay\n%s",
					x, y, a, b, term.SnapshotStyled())
			}
		}
	}

	// Back on, and closed: the screen is as it was before the palette.
	if out, err := tuiosCLI(t, base, "set-config", "appearance.modal_dim", "30"); err != nil {
		t.Fatalf("set modal_dim 30: %v\n%s", err, out)
	}
	closePalette(t, term, "after the dim checks")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		for _, at := range words {
			if s.Cell(at[0], at[1]) != before.Cell(at[0], at[1]) {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("the words stayed dimmed after the palette closed: %v\n%s", err, term.SnapshotStyled())
	}
	saveArtifact(t, term, dir, "closed")
}

// paletteBottom is the palette's last row. findPalette reads it off the
// panel's ground, which a 16-colour panel does not paint, so there the frame's
// bottom corner is what says where the panel ends.
func paletteBottom(s tuitest.Screen, p palettePanel) int {
	_, rows := s.Size()
	for y := p.selRow; y < rows; y++ {
		if s.Cell(p.left, y).Content == "╰" {
			return y
		}
	}
	return p.bottomRow
}

// findDimWords returns the first cell of each word the pane printed.
func findDimWords(t *testing.T, term *tuitest.Terminal, s tuitest.Screen) [][2]int {
	t.Helper()
	out := make([][2]int, len(dimWords))
	for i, w := range dimWords {
		row, col, ok := textAt(s, w, 0)
		if !ok {
			t.Fatalf("the pane never printed %s\n%s", w, term.Snapshot())
		}
		out[i] = [2]int{col, row}
	}
	return out
}

// railNameCell is the first cell of the attached session's name on the rail.
func railNameCell(t *testing.T, term *tuitest.Terminal, s tuitest.Screen) (row, col int) {
	t.Helper()
	railX := railHeaderColumn(s)
	if railX < 0 {
		t.Fatalf("no rail on screen\n%s", term.Snapshot())
	}
	row, col, ok := textAt(s, "e2e-dim", railX)
	if !ok {
		t.Fatalf("the session is not on the rail\n%s", term.Snapshot())
	}
	return row, col
}

// checkDimmed holds one word's first cell to what its depth can draw.
func checkDimmed(t *testing.T, term *tuitest.Terminal, word string, was, now tuitest.Cell, depth, themeName string) {
	t.Helper()
	ansiNoTheme := word == "DIMANSIx" && themeName == ""
	switch {
	case depth == "16", ansiNoTheme:
		if !now.Faint {
			t.Errorf("%s at %s colours is %+v under the palette, want faint\n%s", word, depth, now, term.SnapshotStyled())
		}
		if now.Fg != was.Fg {
			t.Errorf("%s changed colour at %s colours: %+v to %+v; a colour the terminal owns cannot be blended\n%s",
				word, depth, was.Fg, now.Fg, term.SnapshotStyled())
		}
	default:
		// On a dark theme the scrim pulls text toward black; on a light one
		// it pulls it toward the light surface. Either way the text sits
		// closer to its ground than it did.
		if !quieter(now.Fg, was.Fg, themeName != "") {
			t.Errorf("%s at %s colours is %+v under the palette and was %+v: not moved toward its ground\n%s",
				word, depth, now.Fg, was.Fg, term.SnapshotStyled())
		}
	}
}

// quieter reports whether text moved toward its ground under the scrim:
// darker on the dark terminal, lighter under the light theme, whose scrim
// pulls toward the light surface.
func quieter(now, was tuitest.Color, light bool) bool {
	if light {
		return darker(was, now)
	}
	return darker(now, was)
}

// darker reports whether a is a darker colour than b, measured as the host
// paints them: through the xterm table for an index.
func darker(a, b tuitest.Color) bool {
	if a.Kind == tuitest.ColorDefault || b.Kind == tuitest.ColorDefault {
		return false
	}
	return luma(tuiColor(a)) < luma(tuiColor(b))
}

func luma(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)
}
