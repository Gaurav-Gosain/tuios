package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestRailScopeRoutesKeys checks the rail scope: s enters it, pane bindings do
// not fire while it is focused, and esc leaves it. Entering/leaving is what
// makes the scope a scope; the pane-key check is the guarantee it is exclusive.
func TestRailScopeRoutesKeys(t *testing.T) {
	prev := config.SidebarEnabled
	config.SidebarEnabled = true
	t.Cleanup(func() { config.SidebarEnabled = prev })

	o := twoPaneOS(t)
	before := len(o.Windows)

	// s enters the rail (bound to focus_sidebar in window mode).
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 's', Text: "s"}, o)
	if !o.SidebarFocused {
		t.Fatal("s did not enter the rail scope")
	}

	// n is new_window on a pane; while the rail owns the keyboard it must not
	// create a window (it is the rail's new_session, a no-op-with-hint for now).
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'n', Text: "n"}, o)
	if len(o.Windows) != before {
		t.Fatalf("n created a window while the rail was focused (windows %d -> %d)", before, len(o.Windows))
	}

	// esc leaves the rail rather than switching pane modes.
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEscape}, o)
	if o.SidebarFocused {
		t.Fatal("esc did not leave the rail scope")
	}
}

// TestRailScopeSuppressesTerminalTyping checks that a key does not reach the PTY
// while the rail owns the keyboard, even though the client is in terminal mode
// (the rail is reachable from terminal mode via ctrl+b o).
func TestRailScopeSuppressesTerminalTyping(t *testing.T) {
	prev := config.SidebarEnabled
	config.SidebarEnabled = true
	t.Cleanup(func() { config.SidebarEnabled = prev })

	o := twoPaneOS(t)
	o.Mode = app.TerminalMode
	o.SidebarFocused = true

	// A plain letter would be forwarded to the shell in terminal mode; here it is
	// consumed by the rail. The assertion is that HandleKeyPress returns without
	// leaving the rail and without a panic on the nil-terminal windows.
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}, o)
	if !o.SidebarFocused {
		t.Fatal("a rail key dropped rail focus")
	}
	if o.Mode != app.TerminalMode {
		t.Fatal("a rail key changed the pane mode underneath")
	}
}
