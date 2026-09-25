package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// wmPress runs one key through window mode, the way a real keystroke arrives.
func wmPress(o *app.OS, key string) (*app.OS, tea.Cmd) {
	return HandleWindowManagementModeKey(press(key), o)
}

// TestSectionEditorTakesKeysBeforeTheRailDoes is the routing claim.
//
// The editor draws over the settings panel, which draws over the rail, and each
// of those has its own use for j and k. An open editor has to be asked first or
// its arrows would move the rail's cursor underneath it.
//
// Negative control, confirmed red: remove the ShowSectionEditor branch from
// HandleWindowManagementModeKey. The keystroke falls through and the editor's
// selection does not move.
func TestSectionEditorTakesKeysBeforeTheRailDoes(t *testing.T) {
	prev := config.Global.SidebarSections
	config.Global.SidebarSections = config.SidebarDefaultSections
	t.Cleanup(func() { config.Global.SidebarSections = prev })

	o := app.NewOS(app.OSOptions{UserConfig: config.DefaultConfig(), Width: 120, Height: 40})
	o.ConfigReadOnly = true
	o.OpenSectionEditor()
	first := o.SectionEditorSelected

	if _, _ = wmPress(o, "j"); o.SectionEditorSelected == first {
		t.Errorf("j did not reach the editor; the selection is still %d", first)
	}

	// And esc closes it rather than leaving window mode.
	o, _ = wmPress(o, "esc")
	if o.ShowSectionEditor {
		t.Error("esc did not reach the editor")
	}
}
