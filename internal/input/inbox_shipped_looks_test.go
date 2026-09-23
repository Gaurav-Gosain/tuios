package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestInboxClickOnTheShippedLooks drives the Inbox with the looks v0.8.0
// ships: the rail on, on the right, and the dock on top. The rest of this
// package runs with the looks from before it (see TestMain), where nothing
// sits beside the panel. Here the rail's band runs under the Inbox's right
// side on a narrow screen, and a click on an item row there has to reach the
// Inbox rather than the rail.
//
// The click lands on the row's last cell, inside the rail's band when the
// band reaches under the panel, and must go to that row's pane and close the
// Inbox, as enter does. Moving the rail's click ahead of the overlay's in
// handleMouseClick fails both sizes.
func TestInboxClickOnTheShippedLooks(t *testing.T) {
	prev := config.Global
	t.Cleanup(func() { config.Global = prev })

	underRail := 0
	for _, sz := range []struct{ w, h int }{{120, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", sz.w, sz.h), func(t *testing.T) {
			config.Global = config.DefaultSettings()
			o := inboxInputOS(t)
			o.Settings = config.DefaultSettings()
			o.Width, o.Height = sz.w, sz.h
			o.EffectiveWidth, o.EffectiveHeight = sz.w, sz.h
			if o.GetSidebarWidth() == 0 || o.GetLeftMargin() != 0 || o.GetTopMargin() != config.DockHeight {
				t.Fatalf("not the shipped looks: rail=%d left=%d top=%d",
					o.GetSidebarWidth(), o.GetLeftMargin(), o.GetTopMargin())
			}

			o = leader(o, press("i"))
			if !o.ShowInbox {
				t.Fatal("leader, i did not open the Inbox")
			}
			_ = o.View()
			var x, y int
			found := false
			for _, h := range o.OverlayHits {
				if h.Kind != "inbox" {
					continue
				}
				if h.OriginX < 0 || h.OriginY < 0 || h.OriginX+h.Geo.Width > sz.w || h.OriginY+h.Geo.Height > sz.h {
					t.Fatalf("the Inbox spans (%d,%d) %dx%d, off a %dx%d screen",
						h.OriginX, h.OriginY, h.Geo.Width, h.Geo.Height, sz.w, sz.h)
				}
				// Rows are Approvals, the approval for b, Questions, the
				// question for a. Pane a has the focus, so the approval is
				// the one whose click shows where it went.
				if len(h.Rows) != 4 {
					t.Fatalf("%d hit rows, want 4", len(h.Rows))
				}
				row := h.Rows[1]
				x, y = h.OriginX+row.Rect.X1-1, h.OriginY+row.Rect.Y0
				found = true
			}
			if !found {
				t.Fatal("the Inbox recorded no hit geometry")
			}
			if o.SidebarBandContains(x, y) {
				underRail++
			}

			o, _ = handleMouseClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}, o)
			if o.ShowInbox {
				t.Fatalf("a click on the approval's row at (%d,%d) left the Inbox open", x, y)
			}
			if w := o.GetFocusedWindow(); w == nil || w.ID != "b" {
				t.Errorf("a click on the approval's row did not go to pane b")
			}
		})
	}
	if underRail == 0 {
		t.Error("ASSERTION: no click landed in the rail's band, so the rail was never in the way")
	}
}
