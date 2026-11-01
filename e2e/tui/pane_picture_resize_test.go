package tuie2e

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestPanePictureSurvivesAResize shows a kitty picture from a shell in a pane,
// once, and resizes the terminal. Nothing in the pane sends the picture again, so
// it has to stay on the host. A resize must not send an ED 2, which takes every
// picture and its data off the host, and the picture must be placed again after
// each resize, as a host that loses rows scrolls its screen and the placement
// goes with it. The real pixels were checked in a kitty under Xvfb: see
// NEGATIVE_CONTROLS.md.
//
// How this could pass wrongly: the stream's placement says nothing about the
// host's pixels. This test holds the two rules that kitty's and ghostty's own
// cores were replayed against. It does not stand in for the Xvfb run.
func TestPanePictureSurvivesAResize(t *testing.T) {
	out := &lockedBuffer{}
	term, _ := start(t, startOpts{cols: 140, rows: 40, out: out,
		env: []string{"TUIOS_KITTY_GRAPHICS=1", "TUIOS_CELL_SIZE=10x20", "TERM=xterm-kitty", "COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")
	enterTerminalMode(t, term)
	runInShell(t, term, `printf '\033_Ga=T,q=2,f=100;`+cellBoundPNG(t)+`\033\\'; echo; echo dr""awn`, "drawn", uiTimeout)
	place := regexp.MustCompile(`\x1b_Ga=p,i=\d+,p=\d+`)
	for _, size := range [][2]int{{160, 40}, {140, 40}, {140, 50}, {140, 40}, {120, 30}, {140, 40}} {
		mark := len(out.String())
		if err := term.Resize(size[0], size[1]); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitFor(func(tuitest.Screen) bool {
			s := out.String()[mark:]
			return strings.Contains(s, hostClear) && place.MatchString(s)
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: a resize to %v did not place the picture again: %v", size, err)
		}
		if strings.Contains(out.String()[mark:], "\x1b[2J") {
			t.Errorf("ASSERTION: a resize to %v sent an ED 2 to a host with kitty graphics", size)
		}
	}
	alive(t, term, "after the resizes")
}
