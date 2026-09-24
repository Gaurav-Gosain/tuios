package app

import (
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
)

// A screenshot of a pane is drawn on the pane's ground. With a colour set,
// that is the colour, both in the grid (a default-background cell resolves to
// it) and in the file the capture writes; a cell the program coloured keeps
// its own.
func TestScreenshotOfAPaneCarriesThePaneBackground(t *testing.T) {
	withTheme(t, "")
	cases := []struct {
		setting string
		want    bool
	}{
		{config.PaneBackgroundOff, false},
		{paneBgHex, true},
	}
	for _, tc := range cases {
		t.Run(tc.setting, func(t *testing.T) {
			m := shotOS(t)
			m.Settings.PaneBackground = tc.setting
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
