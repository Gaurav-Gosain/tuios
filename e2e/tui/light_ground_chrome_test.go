package tuie2e

import (
	"image/color"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuitest"
)

// latteGround is catppuccin_latte's background, the ground the rail and the
// dock are drawn on under that theme, since neither paints one of its own.
var latteGround = color.RGBA{R: 0xef, G: 0xf1, B: 0xf5, A: 0xff}

// inkAt is the foreground of the cell at col, row as a colour, and false when
// the cell carries no 24-bit colour to measure.
func inkAt(s tuitest.Screen, col, row int) (color.Color, bool) {
	c := s.Cell(col, row).Fg
	if c.Kind != tuitest.ColorRGB {
		return nil, false
	}
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: 0xff}, true
}

// textAt finds want on screen, right of column from, and returns its row and
// column.
func textAt(s tuitest.Screen, want string, from int) (row, col int, ok bool) {
	_, rows := s.Size()
	for r := range rows {
		runes := []rune(s.Line(r))
		if from >= len(runes) {
			continue
		}
		tail := string(runes[from:])
		if i := strings.Index(tail, want); i >= 0 {
			return r, from + len([]rune(tail[:i])), true
		}
	}
	return 0, 0, false
}

// TestLightThemeRailAndDockAreReadable attaches under catppuccin_latte, a
// light theme, with the shipped looks: the rail on the right and the dock on
// top. Neither paints a ground, so their text is written on the theme's own
// near-white background. The rail's session names and the dock's notice were
// drawn in the dark ramp's near-white inks there, at about 1.1:1, which is
// text nobody can read. Each is measured against the ground here and has to
// clear the text floor. The frame is saved under artifactDir.
//
// How this could pass wrongly, written down first:
//   - A name could be matched in the dock or a pane instead of the rail, so
//     the rail's names are looked for right of the rail's divider only.
//   - A cell left in the terminal's default colour cannot be measured, and it
//     reads by definition, so the check needs the cells to carry a colour:
//     the client is told the terminal has truecolor, and a default cell on the
//     measured text fails as "not measured" rather than passing.
//   - The frame could be read before the dock's notice is drawn, and a
//     missing notice would then be skipped. Each probe fails when its text
//     is not on screen.
//
// Negative control: with GroundUI returning UI unchanged, all three probes
// measure 1.09:1 and fail.
func TestLightThemeRailAndDockAreReadable(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[appearance]\ntheme = \"catppuccin_latte\"\n")
	for _, name := range []string{"e2e-lite", "e2e-quill"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-lite"}, shippedLooks: true,
		env: []string{"COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1 && strings.Contains(s.Text(), "e2e-quill")
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached with the rail listing both sessions: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	s := term.Screen()
	saveArtifact(t, term, artifactDir(t), "light-rail-and-dock")

	railX := railHeaderColumn(s)
	if railX < 0 {
		t.Fatalf("no rail on screen\n%s", term.Snapshot())
	}
	for _, probe := range []struct {
		what, text string
		from       int
	}{
		{"the rail's attached session", "e2e-lite", railX},
		{"the rail's other session", "e2e-quill", railX},
		{"the dock's notice", "Window management mode", 0},
	} {
		row, col, ok := textAt(s, probe.text, probe.from)
		if !ok {
			t.Fatalf("%s (%q) is not on screen\n%s", probe.what, probe.text, term.Snapshot())
		}
		ink, measured := inkAt(s, col, row)
		if !measured {
			t.Fatalf("%s at (%d,%d) carries no colour to measure\n%s", probe.what, col, row, term.SnapshotStyled())
		}
		if ratio := overlay.ContrastRatio(ink, latteGround); ratio < overlay.ContrastFloor {
			t.Errorf("%s at (%d,%d) is %v on the theme's ground, %.2f:1, under the %.1f:1 floor",
				probe.what, col, row, ink, ratio, overlay.ContrastFloor)
		}
	}
}
