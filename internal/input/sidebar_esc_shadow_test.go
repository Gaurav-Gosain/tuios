package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// railWithSidebarBinds is railOS with the sidebar section replaced, so a test
// can ask what a user's own rail bindings do.
func railWithSidebarBinds(t *testing.T, sidebar map[string][]string) *app.OS {
	t.Helper()
	withSidebarGlobals(t, "left")
	o := osWithBindings(t, func(kb *config.KeybindingsConfig) {
		kb.Sidebar = sidebar
	})
	o.Width, o.Height = 120, 40
	o.CurrentWorkspace = 1
	o.Windows = []*terminal.Window{{ID: "w1", X: 0, Y: 0, Width: 120, Height: 39, Workspace: 1}}
	o.FocusedWindow = 0
	o.EnterSidebarFocus()
	return o
}

// TestRailEscDoesNotShadowAConfiguredBinding is the shadow bug in its smallest
// form: esc used to be checked ahead of the rail's registry lookup, so a user
// who put esc on a rail action of their own had it silently overridden. A
// binding the user wrote has to win, or the config is decoration.
func TestRailEscDoesNotShadowAConfiguredBinding(t *testing.T) {
	o := railWithSidebarBinds(t, map[string][]string{
		"cursor_down": {"esc"},
		"exit":        {"q"},
	})
	o, _ = HandleSidebarKey(press("esc"), o)
	if !o.SidebarFocused {
		t.Error("esc left the rail even though the config bound it to cursor_down")
	}
}

// TestRailEscStillEscapesWhenUnbound keeps the other half. The rail swallows
// keys with no binding, so an esc that resolves to nothing must still get the
// keyboard back to the panes rather than trapping it.
func TestRailEscStillEscapesWhenUnbound(t *testing.T) {
	o := railWithSidebarBinds(t, map[string][]string{"exit": {"q"}})
	o, _ = HandleSidebarKey(press("esc"), o)
	if o.SidebarFocused {
		t.Error("an unbound esc left the keyboard trapped in the rail")
	}
}

// TestRailEscExitsByItsDefaultBinding pins that the ordinary path is the
// registry one: esc is in the default exit binding, so it leaves through the
// lookup and never reaches the fallback.
func TestRailEscExitsByItsDefaultBinding(t *testing.T) {
	cfg := config.DefaultConfig()
	if got := config.NewKeybindRegistry(cfg).GetSidebarAction("esc"); got != "exit" {
		t.Fatalf("default sidebar esc = %q, want exit", got)
	}
}
