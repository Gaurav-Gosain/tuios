package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestRailScopeRoutesKeys checks the rail scope: s enters it, pane bindings do
// not fire while it is focused, and esc leaves it. Entering/leaving is what
// makes the scope a scope; the pane-key check is the guarantee it is exclusive.
func TestRailScopeRoutesKeys(t *testing.T) {
	prev := config.Global.SidebarEnabled
	config.Global.SidebarEnabled = true
	t.Cleanup(func() { config.Global.SidebarEnabled = prev })

	o := twoPaneOS(t)
	before := len(o.Windows)

	// s enters the rail (bound to focus_sidebar in window mode).
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 's', Text: "s"}, o)
	if !o.SidebarFocused {
		t.Fatal("s did not enter the rail scope")
	}

	// n is new_window on a pane; while the rail owns the keyboard it is the
	// rail's new_session, so it must not create a window. Standalone has no
	// daemon to create a session on either, which it says rather than doing.
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'n', Text: "n"}, o)
	if len(o.Windows) != before {
		t.Fatalf("n created a window while the rail was focused (windows %d -> %d)", before, len(o.Windows))
	}
	if len(o.Notifications) == 0 {
		t.Fatal("n in the rail did not reach new_session")
	}
	if msg := o.Notifications[len(o.Notifications)-1].Message; !strings.Contains(msg, "daemon") {
		t.Errorf("n in the rail said %q, not that sessions need the daemon", msg)
	}

	// esc leaves the rail rather than switching pane modes.
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEscape}, o)
	if o.SidebarFocused {
		t.Fatal("esc did not leave the rail scope")
	}
}
