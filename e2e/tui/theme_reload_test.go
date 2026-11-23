package tuie2e

import (
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestAReloadWithoutAThemeTurnsTheThemeOff: a pane prints red (SGR 31) under
// theme = "dracula", and the host gets the theme's red as a true colour. The
// config file then loses its theme line. The reload turns the theme off: the
// pane border leaves the theme's colour, and red text printed after the
// reload reaches the host as a palette slot, as on a start with no theme.
//
// Negative control: make ApplyAppearanceConfig skip theme.Initialize for an
// empty theme name again. The theme stays on, and the wait for the border to
// change fails.
func TestAReloadWithoutAThemeTurnsTheThemeOff(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\ntheme = \"dracula\"\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	artifacts := artifactDir(t)

	// find is where marker is on the screen. The shell builds each marker, so
	// the command line does not hold it.
	find := func(s tuitest.Screen, marker string) (x, y int) {
		_, rows := s.Size()
		for y := range rows {
			if x := navFind(s, y, marker, 0); x >= 0 {
				return x, y
			}
		}
		return -1, -1
	}
	red := func(marker string) tuitest.Color {
		t.Helper()
		runInShell(t, term, `printf '\033[31m%s\033[0m\n' "RED-$((4400+`+marker[len(marker)-2:]+`))"`, marker, shellTimeout)
		var ink tuitest.Color
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			x, y := find(s, marker)
			ink = s.Cell(x, y).Fg
			return x >= 0
		}, uiTimeout); err != nil {
			t.Fatalf("%s is not on the screen: %v\n%s", marker, err, term.Snapshot())
		}
		return ink
	}

	if ink := red("RED-4417"); ink.Kind != tuitest.ColorRGB {
		t.Fatalf("under the dracula theme the red text is not a true colour: the ink is %+v\n%s", ink, term.Snapshot())
	}
	// The pane's left border, on the row of the first marker.
	bx, by := find(term.Screen(), "RED-4417")
	bx--
	border := term.Screen().Cell(bx, by)
	if border.Content != "│" {
		t.Fatalf("the cell left of RED-4417 is %q, not the pane border\n%s", border.Content, term.Snapshot())
	}
	saveArtifact(t, term, artifacts, "dracula")

	saveConfigLikeAnEditor(t, base, "[appearance]\n")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return s.Cell(bx, by).Fg != border.Fg
	}, configWatchTimeout); err != nil {
		t.Fatalf("the reload without a theme left the border in the theme's colour %+v: %v\n%s", border.Fg, err, term.Snapshot())
	}
	if ink := red("RED-4418"); !navIsSlot(ink, 1, 9) {
		t.Fatalf("after the reload without a theme, red text is %+v, want palette slot 1 or 9\n%s", ink, term.Snapshot())
	}
	saveArtifact(t, term, artifacts, "no-theme")
}
