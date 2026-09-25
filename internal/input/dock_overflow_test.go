package input

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestDockOverflowMarkerOpensTheListOfWhatItHides is the other half of giving
// the marker a rectangle: the click has somewhere to go.
//
// The entries that did not fit are only reachable by mouse through the panel
// that lists every window, so that is what the marker opens.
func TestDockOverflowMarkerOpensTheListOfWhatItHides(t *testing.T) {
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
	o.Width, o.Height = 80, 30
	o.EffectiveWidth, o.EffectiveHeight = 80, 30
	o.CurrentWorkspace, o.FocusedWindow = 1, -1
	for i := range 8 {
		o.Windows = append(o.Windows, &terminal.Window{
			ID: fmt.Sprintf("m%d", i), CustomName: fmt.Sprintf("min%d", i),
			Workspace: 1, Minimized: true, MinimizeOrder: int64(i + 1),
		})
	}
	_ = o.View()

	y := o.GetDockbarContentYPosition()
	x := -1
	for c := range o.Width {
		if o.DockOverflowAt(c, y) {
			x = c
			break
		}
	}
	if x < 0 {
		t.Fatal("eight minimized panes on an 80-column dock drew no overflow marker")
	}

	o, cmd := handleMouseClick(clickAt(x, y), o)
	if !o.ShowAggregateView {
		t.Error("clicking the marker did not open the list of windows it stands for")
	}
	if cmd != nil {
		t.Error("clicking the marker returned a command; it only opens a panel")
	}
}
