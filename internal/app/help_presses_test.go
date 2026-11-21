package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

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
	cfg.Keybindings.Layout["snap_left"] = []string{"z"}
	r := config.NewKeybindRegistry(cfg)

	if _, ok := helpBindingFor(t, config.NewKeybindRegistry(config.DefaultConfig()), "toggle_zoom"); !ok {
		t.Fatal("toggle_zoom is in no help category, so this case would prove nothing")
	}
	if got := r.GetAction("z"); got != "snap_left" {
		t.Fatalf("z runs %q, so toggle_zoom is not shadowed and this case would prove nothing", got)
	}

	if b, ok := helpBindingFor(t, r, "toggle_zoom"); ok && slices.Contains(b.Keys, "z") {
		t.Error("the help offers z for toggle_zoom, which snap_left already took")
	}
	b, ok := helpBindingFor(t, r, "snap_left")
	if !ok {
		t.Fatal("snap_left vanished from the help overlay")
	}
	if !slices.Contains(b.Keys, "z") {
		t.Errorf("the help shows %v for snap_left, which actually runs on z", b.Keys)
	}
}
