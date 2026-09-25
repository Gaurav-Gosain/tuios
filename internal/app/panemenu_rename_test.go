package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestPaneMenuOffersRenameWithTitlesHidden is the fourth copy of the guard that
// vetoed a rename with no title bar. The dialog is centred and draws its own
// frame, so a dimmed row here was the menu refusing an action that works.
func TestPaneMenuOffersRenameWithTitlesHidden(t *testing.T) {
	prevPos, prevBar := config.Global.WindowTitlePosition, config.Global.SidebarEnabled
	config.Global.WindowTitlePosition, config.Global.SidebarEnabled = "hidden", false
	t.Cleanup(func() { config.Global.WindowTitlePosition, config.Global.SidebarEnabled = prevPos, prevBar })

	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)

	_, items := m.paneMenu(0)
	for _, it := range items {
		if it.Action == "rename_window" {
			if it.Dim {
				t.Error("the pane menu dimmed Rename because the title bar is hidden")
			}
			return
		}
	}
	t.Fatal("the pane menu has no rename row at all")
}
