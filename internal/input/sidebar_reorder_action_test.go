package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestTheRailsReorderActionsAreDispatchable guards the machine header's menu.
//
// Its two rows carry the action IDs "reorder_up" and "reorder_down" and
// nothing else, and a menu row is handed straight to this dispatcher. Until
// the header had a menu, those two were dispatched by the rail's own key
// switch alone and were not registered here, so a row naming them would have
// been a row that did nothing when clicked. The rail's own e2e test clicks the
// row; this is the guard that catches the registration going away.
func TestTheRailsReorderActionsAreDispatchable(t *testing.T) {
	d := GetDispatcher()
	for _, action := range []string{"reorder_up", "reorder_down"} {
		if !d.HasAction(action) {
			t.Errorf("ASSERTION: the dispatcher does not have %q, so a machine menu row naming it does nothing", action)
		}
		if _, ok := config.ActionDescriptions[action]; !ok {
			t.Errorf("ASSERTION: %q has no description, so its menu row shows no key hint", action)
		}
	}
}
