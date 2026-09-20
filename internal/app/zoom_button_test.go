package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A tiled pane's title bar used to carry two controls: there was nothing a
// maximize could mean when the tiler owns the rectangle. There is now, because
// zoom means it, and a tiled pane is exactly where a zoom is worth reaching
// for.

// TestATiledBarCarriesTheZoomControl pins that the third control is drawn and
// recorded, so it can be pressed.
func TestATiledBarCarriesTheZoomControl(t *testing.T) {
	for _, style := range config.WindowButtonStyles {
		withButtonStyle(t, style, func() {
			withTiledZoom(t, true, func() {
				m, wins := zoomPeekOS(t)
				m.Settings = config.Global
				_, rects := drawTopBorder(t, m, wins[0], true)

				var found bool
				for _, r := range rects {
					if r.Action == WindowButtonZoom {
						found = true
					}
				}
				if !found {
					t.Errorf("%s: a tiled bar recorded no zoom control, so there is nothing to press", style)
				}
			})
		})
	}
}

// TestTheTiledZoomControlCanBeTurnedOff pins the setting, for anyone who would
// rather a tiled bar carried two.
func TestTheTiledZoomControlCanBeTurnedOff(t *testing.T) {
	withTiledZoom(t, false, func() {
		m, wins := zoomPeekOS(t)
		m.Settings = config.Global
		_, rects := drawTopBorder(t, m, wins[0], true)

		for _, r := range rects {
			if r.Action == WindowButtonZoom {
				t.Error("a tiled bar recorded a zoom control with the setting off")
			}
		}
	})
}

// TestAFloatingBarIsUnchanged pins that the setting is about tiled panes only.
// A floating window's maximize has always been there and is not a zoom.
func TestAFloatingBarIsUnchanged(t *testing.T) {
	withTiledZoom(t, false, func() {
		m, wins := zoomPeekOS(t)
		m.Settings = config.Global
		_, rects := drawTopBorder(t, m, wins[0], false)

		var found bool
		for _, r := range rects {
			if r.Action == WindowButtonZoom {
				found = true
			}
		}
		if !found {
			t.Error("a floating bar lost its maximize control")
		}
	})
}
