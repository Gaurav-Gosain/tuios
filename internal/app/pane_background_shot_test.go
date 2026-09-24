package app

import (
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
)

// A screen or region capture reads the composed frame, so it carries every
// painted surface as it is drawn: with appearance.background set there is no
// cell left on the capture's default ground, and a surface turned off on its
// own keeps its default cells.
func TestScreenAndRegionCapturesCarryEverySurface(t *testing.T) {
	withTheme(t, "")
	m := shotOS(t)
	m.Settings.DockbarPosition = "bottom"
	m.Settings.SidebarEnabled = true
	m.Settings.SidebarPosition = "left"
	m.Settings.Background = surfaceHex
	want := shot.RGB(0x2a, 0x1b, 0x3d)

	g := m.composedGrid(0, 0, m.GetRenderWidth(), m.GetRenderHeight())
	if g == nil {
		t.Fatal("no screen capture")
	}
	painted := 0
	for y, row := range g.Cells {
		for x, c := range row {
			if c.Width == 0 {
				continue
			}
			if c.BGDefault {
				t.Fatalf("screen capture: (%d,%d) %q is on the default ground with every surface painted", x, y, c.Cluster)
			}
			if c.BG == want {
				painted++
			}
		}
	}
	if painted == 0 {
		t.Fatal("screen capture: no cell carries the painted ground")
	}

	// A region over the dock with the dock turned off on its own: its default
	// cells stay default, and the region beside it keeps the paint.
	m.Settings.DockBackground = config.BackgroundOff
	_, dockY := m.renderDockString()
	region := m.composedGrid(0, dockY, m.GetRenderWidth(), dockY+config.DockHeight)
	if region == nil {
		t.Fatal("no region capture")
	}
	sawDefault := false
	for _, row := range region.Cells {
		for _, c := range row {
			if c.BG == want && !c.BGDefault {
				t.Fatalf("region capture: a dock cell carries the painted ground with the dock off: %+v", c)
			}
			sawDefault = sawDefault || c.BGDefault
		}
	}
	if !sawDefault {
		t.Error("region capture: the dock has no default cell to prove anything with")
	}
}

// A screenshot of a pane is drawn on the pane's ground. With a colour set,
// that is the colour, both in the grid (a default-background cell resolves to
// it) and in the file the capture writes; a cell the program coloured keeps
// its own.
func TestScreenshotOfAPaneCarriesThePaneBackground(t *testing.T) {
	withTheme(t, "")
	cases := []struct {
		name, setting, all string
		want               bool
	}{
		{name: "off", setting: config.PaneBackgroundOff},
		{name: "colour", setting: paneBgHex, want: true},
		// The default for every surface reaches a pane not set on its own,
		// and a pane's own off keeps the capture bare.
		{name: "follows all", all: paneBgHex, want: true},
		{name: "own off over all", setting: config.PaneBackgroundOff, all: paneBgHex},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := shotOS(t)
			m.Settings.PaneBackground = tc.setting
			m.Settings.Background = tc.all
			m.UserConfig.Screenshot.Format = string(shot.FormatSVG)
			win := m.Windows[0]
			win.WriteOutput([]byte("\x1b[41mRED\x1b[0m plain"))

			cmd := m.ScreenshotWindow(0)
			if cmd == nil {
				t.Fatal("no capture was started")
			}
			msg := cmd().(screenshotResultMsg)
			if msg.err != nil {
				t.Fatalf("capture failed: %v", msg.err)
			}
			if got := msg.grid.BG == paneBgRGBA; got != tc.want {
				t.Errorf("grid ground %v, painted=%v, want painted=%v", msg.grid.BG, got, tc.want)
			}
			// "RED" is the program's own red and keeps it either way.
			if red := msg.grid.Cells[0][0]; red.BGDefault || red.BG == paneBgRGBA {
				t.Errorf("the program's red cell became %+v", red)
			}
			data, err := os.ReadFile(msg.path)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(strings.ToLower(string(data)), paneBgHex); got != tc.want {
				t.Errorf("the SVG mentions %s: %v, want %v", paneBgHex, got, tc.want)
			}
		})
	}
}
