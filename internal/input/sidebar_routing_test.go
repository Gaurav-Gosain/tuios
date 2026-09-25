package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// withSidebarGlobals turns the sidebar on for a test and restores the previous
// configuration afterwards.
func withSidebarGlobals(t *testing.T, pos string) {
	t.Helper()
	pe, pp, pw := config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth
	config.Global.SidebarEnabled = true
	config.Global.SidebarPosition = pos
	config.Global.SidebarWidth = config.SidebarDefaultWidth
	t.Cleanup(func() {
		config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth = pe, pp, pw
	})
}
