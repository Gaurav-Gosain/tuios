package app

import (
	"image/color"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The backgrounds beyond the pane's: the desktop, the window chrome, the dock
// and the rail, and appearance.background, which sets them all. Each test
// composes the same frame with the option off and on and compares them cell
// by cell, which is the whole of the contract: a cell that had no background
// takes the surface's, a cell that had one keeps it, and a foreground that
// was set is kept.

const surfaceHex = "#2a1b3d"

var surfaceRGBA = color.RGBA{R: 0x2a, G: 0x1b, B: 0x3d, A: 0xff}

// Every background off has to cost nothing per frame beyond the comparisons
// that find it off: no allocation.
func TestBackgroundsOffAllocateNothing(t *testing.T) {
	if raceEnabled {
		// A colour setting is checked by config.IsHexColor, a regexp match,
		// and regexp keeps its matchers in a sync.Pool that -race empties at
		// random. The cached path allocates once a run there and never here.
		t.Skip("allocation counts are not meaningful under -race")
	}
	withTheme(t, "catppuccin_mocha")
	m := paneBgOS(t, "")
	if n := testing.AllocsPerRun(100, func() {
		g := m.frameGrounds()
		_ = g.any()
	}); n != 0 {
		t.Errorf("resolving every background off allocates %.0f times", n)
	}
	m.Settings.Background = surfaceHex
	_ = m.frameGrounds()
	if n := testing.AllocsPerRun(100, func() { _ = m.frameGrounds() }); n != 0 {
		t.Errorf("resolving cached backgrounds allocates %.0f times", n)
	}
	canvas := &frameCanvas{Buffer: *uv.NewBuffer(80, 24)}
	canvas.ClearTo(ground{})
	if n := testing.AllocsPerRun(100, func() { canvas.ClearTo(ground{}) }); n != 0 {
		t.Errorf("clearing the canvas with the desktop off allocates %.0f times", n)
	}
	desk := m.surfaceGround(surfaceDesktop)
	canvas.ClearTo(desk)
	if n := testing.AllocsPerRun(100, func() { canvas.ClearTo(desk) }); n != 0 {
		t.Errorf("clearing the canvas to a cached desktop ground allocates %.0f times", n)
	}
}

// A program in a pane asks what it is drawn on. The pane's emulator answers
// with the painted ground once the frame is composed, and with its own answer
// again when the paint is off. The daemon's emulators are told through the
// state this client pushes, and that is checked here too.
func TestPaneBackgroundAnswersOSC11(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	win := paneBgWindow(t, "osc-11", 2, 2, 40, 10)
	m := paneBgOS(t, "", win)

	ask := func(q string) string {
		t.Helper()
		win.WriteOutput([]byte(q))
		got := make(chan string, 1)
		go func() {
			buf := make([]byte, 256)
			n, _ := win.Terminal.Read(buf)
			got <- string(buf[:n])
		}()
		select {
		case s := <-got:
			return s
		case <-time.After(2 * time.Second):
			t.Fatal("the pane's emulator did not answer")
		}
		return ""
	}
	_ = m.composeFrame()
	before := ask("\x1b]11;?\x1b\\")
	if state := m.BuildSessionState(); state.PaneReportBg != "" || state.PaneReportFg != "" {
		t.Errorf("with the pane background off the state carries %q/%q", state.PaneReportBg, state.PaneReportFg)
	}

	m.Settings.Background = paneBgHex
	_ = m.composeFrame()
	if got := ask("\x1b]11;?\x1b\\"); !strings.Contains(got, "rgb:1212/3434/5656") {
		t.Errorf("OSC 11 answered %q, want the painted rgb:1212/3434/5656", got)
	}
	wantFg := colorHex(resolveGround(paneBgHex, theme.CurrentThemeID()).fg)
	r, g, b := wantFg[1:3], wantFg[3:5], wantFg[5:7]
	if got := ask("\x1b]10;?\x1b\\"); !strings.Contains(got, "rgb:"+r+r+"/"+g+g+"/"+b+b) {
		t.Errorf("OSC 10 answered %q, want the ink the pane gives default text, %s", got, wantFg)
	}
	state := m.BuildSessionState()
	if state.PaneReportBg != paneBgHex || state.PaneReportFg != wantFg {
		t.Errorf("the state pushed to the daemon carries %q/%q, want %s/%s", state.PaneReportBg, state.PaneReportFg, paneBgHex, wantFg)
	}

	// The pane's own off wins over the default for every surface.
	m.Settings.PaneBackground = config.BackgroundOff
	_ = m.composeFrame()
	if got := ask("\x1b]11;?\x1b\\"); got != before {
		t.Errorf("with the pane background off again OSC 11 answered %q, want the original %q", got, before)
	}
}
