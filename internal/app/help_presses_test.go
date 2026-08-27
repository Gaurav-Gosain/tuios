package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The help overlay tells a reader what to press. It listed the bare key from
// the config, which is the right answer for the keymap and the wrong one for a
// human as soon as an action lives behind a chord.

// helpBindingFor returns the help entry for an action, across every category.
func helpBindingFor(t *testing.T, r *config.KeybindRegistry, action string) (HelpBinding, bool) {
	t.Helper()
	for _, cat := range GetHelpCategories(r, &config.Global) {
		for _, b := range cat.Bindings {
			if b.Action == action {
				return b, true
			}
		}
	}
	return HelpBinding{}, false
}

// TestHelpShowsTheWholeChord. Corner snapping is reached with the layout
// prefix, and listing its key as "1" would tell the reader that 1 snaps a
// window when 1 selects one.
//
// Negative control, run and confirmed failing: put registry.GetKeys back in
// generateCategoryBindings and this fails with Keys=["1"].
func TestHelpShowsTheWholeChord(t *testing.T) {
	cfg := config.DefaultConfig()
	r := config.NewKeybindRegistry(cfg)

	b, ok := helpBindingFor(t, r, "snap_corner_1")
	if !ok {
		t.Fatal("corner snapping vanished from the help overlay")
	}
	if slices.Contains(b.Keys, "1") {
		t.Errorf("the help offers a bare %q for snap_corner_1, which selects a window", "1")
	}
	joined := strings.Join(b.Keys, " ")
	for _, want := range []string{cfg.Keybindings.LeaderKey, "L", "1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the help shows %v for snap_corner_1, which does not name %q", b.Keys, want)
		}
	}
}

// TestHelpLeavesPlainBindingsAlone, so the change costs nothing for the great
// majority of actions, which have no chord.
//
// select_window_1 is not in this table, though it is the action whose key
// corner snapping was shadowing. The help overlay's categories do not list
// select_window_N at all, so there is nothing here to leave alone. That is a
// gap in the help, and a pre-existing one: window selection has never had a
// row. It is left as it was found rather than fixed alongside a keybind
// change.
//
// Negative control: prepend a chord unconditionally and this fails.
func TestHelpLeavesPlainBindingsAlone(t *testing.T) {
	r := config.NewKeybindRegistry(config.DefaultConfig())
	for action, want := range map[string]string{
		"new_window":  "n",
		"toggle_zoom": "z",
		"snap_left":   "h",
	} {
		b, ok := helpBindingFor(t, r, action)
		if !ok {
			t.Errorf("%s is missing from the help overlay", action)
			continue
		}
		if !slices.Contains(b.Keys, want) {
			t.Errorf("the help shows %v for %s, want it to include %q", b.Keys, action, want)
		}
	}
}

// TestHelpDoesNotOfferADeadBinding. A key another action already took does not
// run, and the help exists to say what to press.
//
// toggle_zoom rather than select_window_1, which this case used to name. The
// help categories do not list select_window_N at all, so helpBindingFor came
// back empty and the assertion never ran: the test passed with the filter and
// without it. The premise is asserted first now, so a category list that stops
// covering the action fails loudly instead of quietly proving nothing.
//
// Negative control, run and confirmed failing: drop the b.Shadowed test from
// PressesByAction and the help offers "z" for toggle_zoom, which never fires.
func TestHelpDoesNotOfferADeadBinding(t *testing.T) {
	cfg := config.DefaultConfig()
	// layout is merged into the window-mode keymap after window_management, so
	// snap_left takes z and toggle_zoom's only binding goes dark.
	cfg.Keybindings.Layout["snap_left"] = []string{"z"}
	r := config.NewKeybindRegistry(cfg)

	// The premise: the help really does list this action, and the shadowing is
	// real. Both are checked because either one being false would make the
	// assertion below vacuous.
	if _, ok := helpBindingFor(t, config.NewKeybindRegistry(config.DefaultConfig()), "toggle_zoom"); !ok {
		t.Fatal("toggle_zoom is in no help category, so this case would prove nothing")
	}
	if got := r.GetAction("z"); got != "snap_left" {
		t.Fatalf("z runs %q, so toggle_zoom is not shadowed and this case would prove nothing", got)
	}

	if b, ok := helpBindingFor(t, r, "toggle_zoom"); ok && slices.Contains(b.Keys, "z") {
		t.Error("the help offers z for toggle_zoom, which snap_left already took")
	}
	// And the action that does run still says so.
	b, ok := helpBindingFor(t, r, "snap_left")
	if !ok {
		t.Fatal("snap_left vanished from the help overlay")
	}
	if !slices.Contains(b.Keys, "z") {
		t.Errorf("the help shows %v for snap_left, which actually runs on z", b.Keys)
	}
}
