package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func helpSection(t *testing.T, name string) HelpCategory {
	t.Helper()
	reg := config.NewKeybindRegistry(config.DefaultConfig())
	for _, cat := range GetHelpCategories(reg, &config.Global) {
		if cat.Name == name {
			return cat
		}
	}
	t.Fatalf("the help overlay has no %q section", name)
	return HelpCategory{}
}

// TestHelpDocumentsTheRailScope checks every key the rail's keyboard scope binds
// is in the help overlay. The scope swallows keys it does not recognise, so a
// binding nobody documented is a key that silently does nothing to the user.
//
// new_session and new_window are the deliberate exceptions: they are bound but
// only notify that they are not available yet, so listing them would be a lie.
func TestHelpDocumentsTheRailScope(t *testing.T) {
	cat := helpSection(t, "Sidebar")

	documented := map[string]bool{}
	for _, b := range cat.Bindings {
		for _, k := range b.Keys {
			documented[k] = true
		}
	}

	for action, keys := range config.DefaultConfig().Keybindings.Sidebar {
		switch {
		case action == "new_session" || action == "new_window":
			continue
		case strings.HasPrefix(action, "jump_"):
			// Listed as one range row rather than nine rows of the same sentence.
			continue
		}
		covered := false
		for _, k := range keys {
			if documented[k] {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("the rail binds %q to %v and the help overlay lists none of them", action, keys)
		}
	}
}

// TestGestureRowsFitTheHelpPanel checks the hand-written gesture rows fit the
// panel's columns at its preferred width. A row wider than the key column has
// its extra key combos dropped, and a description wider than what is left is
// truncated, both of which happen silently. Only the gesture sections are
// checked: the rest take their text from config.ActionDescriptions.
func TestGestureRowsFitTheHelpPanel(t *testing.T) {
	pal := theme.UI()

	for _, name := range []string{"Mouse", "Sidebar"} {
		cat := helpSection(t, name)

		keyColW := 0
		for _, b := range cat.Bindings {
			keyColW = max(keyColW, lipgloss.Width(overlay.KeyBadges(b.Keys, pal.Surface, pal)))
		}
		if keyColW > helpKeyColMax {
			t.Errorf("%s: the widest key column is %d cells, the panel caps it at %d",
				name, keyColW, helpKeyColMax)
			keyColW = helpKeyColMax
		}

		descMax := helpPanelInnerWidth - keyColW - 2
		for _, b := range cat.Bindings {
			if w := lipgloss.Width(b.Description); w > descMax {
				t.Errorf("%s: %v is described in %d cells, the column holds %d: %q",
					name, b.Keys, w, descMax, b.Description)
			}
		}
	}
}

// TestHelpSectionsHaveTabLabels checks a new section cannot ship without a short
// tab label. Without one the strip falls back to the full category name, which
// is what pushes the tabs onto a second row.
func TestHelpSectionsHaveTabLabels(t *testing.T) {
	reg := config.NewKeybindRegistry(config.DefaultConfig())
	for _, cat := range GetHelpCategories(reg, &config.Global) {
		if _, ok := helpTabNames[cat.Name]; !ok {
			t.Errorf("section %q has no entry in helpTabNames", cat.Name)
		}
	}
}
