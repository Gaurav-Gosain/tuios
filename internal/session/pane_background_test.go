package session

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/capture"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// The daemon's screenshot verb draws a pane on the ground the session's
// appearance.pane_background paints, read from the session's own options the
// way every other screenshot setting is.
func TestDaemonScreenshotUsesThePaneBackground(t *testing.T) {
	custom := shot.RGB(0x12, 0x34, 0x56)
	cases := []struct {
		name    string
		set     bool
		value   string
		painted bool
	}{
		{name: "unset", painted: false},
		{name: "off", set: true, value: "off", painted: false},
		{name: "colour", set: true, value: "#123456", painted: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sess := newTestSession(t)
			if tc.set {
				sess.SetOption("appearance.pane_background", tc.value)
			}
			settings, _ := (&Daemon{}).screenshotSettings(sess, "")
			palette, _ := capture.Palette(settings.ThemeID)
			palette = capture.WithPaneBackground(palette, settings.PaneBackground, settings.ThemeID)

			em := vt.New(20, 3)
			_, _ = em.Write([]byte("\x1b[41mRED\x1b[0m plain"))
			grid := gridOf(em, palette, 0, false)
			if grid == nil {
				t.Fatal("no grid")
			}
			if got := grid.BG == custom; got != tc.painted {
				t.Errorf("grid ground %v, painted=%v, want painted=%v", grid.BG, got, tc.painted)
			}
			if c := grid.Cells[0][4]; !c.BGDefault {
				t.Errorf("a default-background cell was resolved to a colour of its own: %+v", c)
			}
			if c := grid.Cells[0][0]; c.BGDefault || c.BG == custom {
				t.Errorf("the program's red cell became %+v", c)
			}
		})
	}
}
