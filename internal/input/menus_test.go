package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// toggle_mouse flips OS.MouseOff and needs no window, so it is the action
// these menus run: what a key did is read off one boolean.
const menuConfig = `
[keybindings]
leader_key = "ctrl+b"

[keybindings.prefix_mode]
"open_menu:tools" = ["U"]
"open_menu:resize" = ["R"]

[keybindings.menus.tools.keys]
toggle_mouse = ["m"]
"open_menu:deep" = ["d"]

[keybindings.menus.deep.keys]
toggle_mouse = ["x"]

[keybindings.menus.resize]
sticky = true

[keybindings.menus.resize.keys]
toggle_mouse = ["h"]
`

func menuPane(t *testing.T, mode app.Mode) (*app.OS, *capturePty) {
	t.Helper()
	o, pty := osWithFocusedPane(t, legacyConfig(t, menuConfig), mode)
	return o, pty
}

func pressKeys(o *app.OS, keys ...string) *app.OS {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		if k == "leader" {
			msg = tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}
		} else {
			msg = press(k)
		}
		o, _ = HandleKeyPress(msg, o)
	}
	return o
}

// A menu nests as deep as the config does: leader, U, d, x reaches a menu two
// below the leader, from either mode.
func TestDeclaredMenusNest(t *testing.T) {
	for _, mode := range []app.Mode{app.TerminalMode, app.WindowManagementMode} {
		o, _ := menuPane(t, mode)
		o = pressKeys(o, "leader", "U")
		if o.Menu != "tools" {
			t.Fatalf("mode %v: leader U opened %q, want tools", mode, o.Menu)
		}
		o = pressKeys(o, "d")
		if o.Menu != "deep" || !o.PrefixActive {
			t.Fatalf("mode %v: leader U d left menu=%q prefix=%v, want deep and armed", mode, o.Menu, o.PrefixActive)
		}
		o = pressKeys(o, "x")
		if !o.MouseOff {
			t.Errorf("mode %v: leader U d x did not run the action", mode)
		}
		if o.Menu != "" || o.PrefixActive {
			t.Errorf("mode %v: a non-sticky menu stayed open after a key: menu=%q prefix=%v", mode, o.Menu, o.PrefixActive)
		}
	}
}

// A key lives in its own menu: tools' m does nothing in the deep menu, and the
// unbound key is not typed into the pane.
func TestMenuKeyIsLookedUpInItsOwnMenuOnly(t *testing.T) {
	o, pty := menuPane(t, app.TerminalMode)
	o = pressKeys(o, "leader", "U", "d", "m")
	if o.MouseOff {
		t.Error("m, bound in tools, ran from the deep menu")
	}
	if len(pty.got) != 0 {
		t.Errorf("a key unbound in a non-sticky menu reached the pane: %q", pty.got)
	}
}

// tmux -r: the menu stays open for repeated keys; the key that is not bound
// closes it and, in terminal mode, is the pane's.
func TestStickyMenuRepeatsThenPassesTheClosingKeyThrough(t *testing.T) {
	o, pty := menuPane(t, app.TerminalMode)
	o = pressKeys(o, "leader", "R", "h")
	if !o.MouseOff || o.Menu != "resize" || !o.PrefixActive {
		t.Fatalf("after one h: mouseOff=%v menu=%q prefix=%v, want it run and the menu still open", o.MouseOff, o.Menu, o.PrefixActive)
	}
	o = pressKeys(o, "h")
	if o.MouseOff || o.Menu != "resize" {
		t.Fatalf("after two h: mouseOff=%v menu=%q, want the action run twice and the menu open", o.MouseOff, o.Menu)
	}
	o = pressKeys(o, "q")
	if o.Menu != "" || o.PrefixActive {
		t.Errorf("an unbound key left the menu open: menu=%q prefix=%v", o.Menu, o.PrefixActive)
	}
	if string(pty.got) != "q" {
		t.Errorf("the pane got %q, want the closing key q", pty.got)
	}
}

// esc leaves a sticky menu and is not typed into the pane.
func TestEscLeavesAStickyMenuWithoutTypingIt(t *testing.T) {
	o, pty := menuPane(t, app.TerminalMode)
	o = pressKeys(o, "leader", "R", "h", "esc")
	if o.Menu != "" || o.PrefixActive {
		t.Errorf("esc left the menu open: menu=%q prefix=%v", o.Menu, o.PrefixActive)
	}
	if len(pty.got) != 0 {
		t.Errorf("esc reached the pane: %q", pty.got)
	}
}

// A sticky menu in window mode has no pane to hand the key to.
func TestStickyMenuInWindowModeDropsTheClosingKey(t *testing.T) {
	o, pty := menuPane(t, app.WindowManagementMode)
	o = pressKeys(o, "leader", "R", "h", "q")
	if o.Menu != "" || len(pty.got) != 0 {
		t.Errorf("menu=%q, pane got %q; want the menu closed and nothing typed", o.Menu, pty.got)
	}
}
