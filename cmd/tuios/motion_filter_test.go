package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// filterOS builds a model with the rail on the left and one pane beside it.
func filterOS(t *testing.T) *app.OS {
	t.Helper()
	pe, pp, pw := config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth
	config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth = true, "left", 30
	prevFFM := config.FocusFollowsMouse
	config.FocusFollowsMouse = false
	t.Cleanup(func() {
		config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth = pe, pp, pw
		config.FocusFollowsMouse = prevFFM
	})

	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Width, o.Height = 120, 40
	o.EffectiveWidth, o.EffectiveHeight = 120, 40
	o.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "editor", X: 31, Y: 1, Width: 40, Height: 20, Workspace: 1},
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	return o
}

// TestMotionFilterPassesRailHover is the second gate the rail's hover has to
// clear. The view asks the host for all-motion tracking so hover has events to
// work with; this whitelist decides which of them reach Update at all, and a
// motion it drops is a hover that never happens. Terminal mode is pinned
// alongside window management because that is where hover looked broken.
func TestMotionFilterPassesRailHover(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode app.Mode
	}{
		{"window management", app.WindowManagementMode},
		{"terminal", app.TerminalMode},
	} {
		t.Run(mode.name, func(t *testing.T) {
			o := filterOS(t)
			o.Mode = mode.mode

			// Deep inside the rail band, well below the rows, where the footer
			// controls live.
			onRail := tea.MouseMotionMsg{X: 3, Y: 35}
			if filterMouseMotion(o, onRail) == nil {
				t.Error("motion over the rail was dropped; nothing downstream can hover")
			}

			// The pane keeps the CPU guard when nothing out there hovers. Link
			// hover is the one thing that does, so the guard is now conditional
			// on it rather than absolute: with appearance.links off, a plain
			// shell asked for no mouse mode, tuios draws no hover out there, and
			// that motion is noise exactly as it always was.
			prev := config.Links
			config.Links = config.LinksOff
			t.Cleanup(func() { config.Links = prev })

			offRail := tea.MouseMotionMsg{X: 50, Y: 10}
			if filterMouseMotion(o, offRail) != nil {
				t.Error("motion over a plain pane was passed; the guard is what keeps a mouse sweep cheap")
			}
		})
	}
}

// TestMotionFilterPassesTheBandExitEvent pins the event the rail's hover peek
// snaps back on. The peek owns no clock: it is cleared by the first motion that
// resolves off the sessions rows, and when the pointer leaves the band
// altogether the only motion that can carry that is the one extra event
// SidebarHoverActive keeps flowing. Drop it here and a preview outlives the
// pointer that made it, with nothing left to take it down.
func TestMotionFilterPassesTheBandExitEvent(t *testing.T) {
	o := filterOS(t)
	o.Mode = app.TerminalMode

	// Links off, so the rail's own clause is the only thing that can pass this
	// event and the assertions below are about it and nothing else.
	prev := config.Links
	config.Links = config.LinksOff
	t.Cleanup(func() { config.Links = prev })

	// The pointer is in the band and hovering, then steps out over a plain pane.
	o.SidebarHoverActive = true
	exit := tea.MouseMotionMsg{X: 50, Y: 10}
	if filterMouseMotion(o, exit) == nil {
		t.Fatal("the band-exit event was dropped; the peek and the hover highlight both outlive the pointer")
	}

	// And once it has been delivered the guard closes again: the handler clears
	// HoverActive, so the next event over the same pane is noise once more.
	o.SidebarHoverActive = false
	if filterMouseMotion(o, exit) != nil {
		t.Error("motion over a plain pane stayed whitelisted after the exit event")
	}
}

// TestMotionFilterPassesPaneContentForLinks is the other half of the clause the
// two tests above now qualify. A link under the pointer is drawn by the pane
// itself, so unlike every other hover in tuios its target is not a rectangle the
// chrome recorded, and the motion that crosses it has to be let through on the
// strength of the pane's content box alone.
//
// Negative control: with the links clause removed from filterMouseMotion, the
// first assertion fails, and so does the underline in a real session. With the
// cell-change guard removed the last assertion fails. NOT YET CONFIRMED RED.
//
// The clause's absence was observed at the level above, though: before it went
// in, TestMotionFilterPassesRailHover passed and no motion over a pane reached
// Update, which is the state this test's first assertion is written against.
func TestMotionFilterPassesPaneContentForLinks(t *testing.T) {
	o := filterOS(t)
	o.Mode = app.WindowManagementMode

	prev := config.Links
	t.Cleanup(func() { config.Links = prev })

	config.Links = config.LinksAll
	if filterMouseMotion(o, tea.MouseMotionMsg{X: 50, Y: 10}) == nil {
		t.Error("motion over pane content was dropped; no link can ever underline itself")
	}

	// Off is off: the guard the two tests above pin is restored exactly.
	config.Links = config.LinksOff
	if filterMouseMotion(o, tea.MouseMotionMsg{X: 50, Y: 10}) != nil {
		t.Error("appearance.links = off still passed pane motion")
	}

	// A pane's border is not its content, so the pointer resting on the frame
	// buys nothing. The pane above starts at X=31 with a border, so column 31 is
	// the border and column 32 the first content cell.
	config.Links = config.LinksAll
	if filterMouseMotion(o, tea.MouseMotionMsg{X: 31, Y: 10}) != nil {
		t.Error("motion on a pane's border was passed as content")
	}

	// And a motion that lands on the cell the pointer is already on resolves to
	// the run it is already showing, so it is dropped.
	o.LastMouseX, o.LastMouseY = 50, 10
	if filterMouseMotion(o, tea.MouseMotionMsg{X: 50, Y: 10}) != nil {
		t.Error("a motion that changed no cell was passed")
	}
}
