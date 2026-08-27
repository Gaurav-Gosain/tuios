package input

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// railOS builds an OS with the rail focused and one pane, routed through the
// real registry so the help the test reads is the help a user would get.
func railOS(t *testing.T) *app.OS {
	t.Helper()
	withSidebarGlobals(t, "left")
	o := osWithBindings(t, func(*config.KeybindingsConfig) {})
	o.Width, o.Height = 120, 40
	o.CurrentWorkspace = 1
	o.Windows = []*terminal.Window{{ID: "w1", X: 0, Y: 0, Width: 120, Height: 39, Workspace: 1}}
	o.FocusedWindow = 0
	o.EnterSidebarFocus()
	return o
}

// railHelpCategory is the help section the rail's key opens on.
func railHelpCategory(t *testing.T, o *app.OS) app.HelpCategory {
	t.Helper()
	cats := app.GetHelpCategories(o.KeybindRegistry, &o.Settings)
	if o.HelpCategory < 0 || o.HelpCategory >= len(cats) {
		t.Fatalf("HelpCategory = %d, outside the %d sections", o.HelpCategory, len(cats))
	}
	return cats[o.HelpCategory]
}

// TestRailHelpKeyOpensHelpOnTheRailSection is the discoverability the rail was
// missing: it binds a scope's worth of keys and, from inside it, nothing said
// what they were.
func TestRailHelpKeyOpensHelpOnTheRailSection(t *testing.T) {
	o := railOS(t)
	if o.ShowHelp {
		t.Fatal("help was already open")
	}

	o, _ = HandleKeyPress(press("?"), o)

	if !o.ShowHelp {
		t.Fatal("? in the rail did not open the help overlay")
	}
	if got := railHelpCategory(t, o).Name; got != app.HelpCategorySidebar {
		t.Errorf("help opened on the %q section, want %q", got, app.HelpCategorySidebar)
	}
	if o.SidebarFocused != true {
		t.Error("opening help dropped the rail's focus; closing it should return there")
	}
}

// TestRailHelpListsWhatTheRailBinds pins the help to the registry rather than to
// a copy of it: every row that names a rail action shows exactly the keys that
// action resolves to, so a rebind moves the help with it.
func TestRailHelpListsWhatTheRailBinds(t *testing.T) {
	o := railOS(t)
	o, _ = HandleKeyPress(press("?"), o)

	listed := map[string]bool{}
	for _, b := range railHelpCategory(t, o).Bindings {
		if b.Action == "" {
			continue
		}
		if want := o.KeybindRegistry.GetSidebarKeys(b.Action); want != nil {
			if !slices.Equal(b.Keys, want) {
				t.Errorf("help lists %s as %v, the rail binds %v", b.Action, b.Keys, want)
			}
			listed[b.Action] = true
		}
	}

	// help itself must be among them, or the key that opened this panel is the
	// one thing the panel does not mention.
	if !listed[sidebarActHelp] {
		t.Error("the rail's help section does not list its own help key")
	}
	if !listed[sidebarActExit] {
		t.Error("the rail's help section does not list how to leave the rail")
	}
}

// TestRailHelpKeyIsRebindable covers the config path: the key is a binding like
// any other, not a literal in the handler.
func TestRailHelpKeyIsRebindable(t *testing.T) {
	withSidebarGlobals(t, "left")
	o := osWithBindings(t, func(k *config.KeybindingsConfig) {
		k.Sidebar[sidebarActHelp] = []string{"F1"}
	})
	o.EnterSidebarFocus()

	if o2, _ := HandleKeyPress(press("F1"), o); !o2.ShowHelp {
		t.Error("the rebound key did not open help")
	}

	o3 := osWithBindings(t, func(k *config.KeybindingsConfig) {
		k.Sidebar[sidebarActHelp] = []string{"F1"}
	})
	o3.EnterSidebarFocus()
	if o4, _ := HandleKeyPress(press("?"), o3); o4.ShowHelp {
		t.Error("the replaced default ? still opened help")
	}
}

// TestRailHelpKeyResolvesInAConfigPredatingIt is the hazard a rail keybind has
// hit before: a config file written before a key existed left it dead, and the
// rail swallows what it does not bind, so a dead key here is a key that does
// nothing at all.
func TestRailHelpKeyResolvesInAConfigPredatingIt(t *testing.T) {
	withSidebarGlobals(t, "left")
	cfg := legacyConfig(t, "[keybindings]\nleader_key = \"ctrl+b\"\n\n"+
		"[keybindings.sidebar]\ncursor_down = [\"j\"]\ncursor_up = [\"k\"]\n")

	o := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.EnterSidebarFocus()

	o, _ = HandleKeyPress(press("?"), o)
	if !o.ShowHelp {
		t.Fatal("? was dead in a config written before the rail bound it")
	}
	// The user's own two keys survive the fill.
	if got := cfg.Keybindings.Sidebar[sidebarActCursorDown]; !slices.Equal(got, []string{"j"}) {
		t.Errorf("cursor_down = %v, the user's config said [j]", got)
	}
}

// TestHelpOwnsTheKeyboardOverTheRail guards the routing order the rail's help
// key depends on. The rail swallows what it does not bind, so with it still in
// front the overlay's own keys would never reach it.
func TestHelpOwnsTheKeyboardOverTheRail(t *testing.T) {
	o := railOS(t)
	o, _ = HandleKeyPress(press("?"), o)

	o, _ = HandleKeyPress(press("?"), o)
	if o.ShowHelp {
		t.Error("? did not close the help opened from the rail")
	}
	if !o.SidebarFocused {
		t.Error("closing help left the rail, which is not what closing a panel does")
	}
}
