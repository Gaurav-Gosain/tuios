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
	for _, cat := range GetHelpCategories(reg) {
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

// TestHelpDocumentsTheEntryPointsToTheRail checks the three ways into the rail
// are all findable from the Sidebar section, since a scope you cannot reach is
// worse than one you cannot read.
func TestHelpDocumentsTheEntryPointsToTheRail(t *testing.T) {
	cat := helpSection(t, "Sidebar")
	want := []string{"s", config.LeaderKey + ", o", config.LeaderKey + ", b"}

	for _, key := range want {
		found := false
		for _, b := range cat.Bindings {
			for _, k := range b.Keys {
				if k == key {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("the Sidebar section never mentions %q", key)
		}
	}
}

// TestHelpDocumentsTheMouse checks the gesture sections describe the buttons a
// user can actually press. help.go documented no gesture at all until this, so
// the assertion is on the gestures being present and described, not on wording.
func TestHelpDocumentsTheMouse(t *testing.T) {
	var rows []HelpBinding
	rows = append(rows, helpSection(t, "Mouse").Bindings...)
	rows = append(rows, helpSection(t, "Sidebar").Bindings...)

	joined := ""
	for _, b := range rows {
		if b.Description == "" {
			t.Errorf("gesture %v has no description", b.Keys)
		}
		joined += strings.Join(b.Keys, " ") + "\n"
	}

	for _, gesture := range []string{
		"ctrl+shift+click",         // multi-select
		"ctrl+drag",                // move a pane
		"drag pane border",         // tiled divider / floating edge
		"right-drag",               // corner resize
		"right-click",              // pane menu without a modifier
		"ctrl/shift + right-click", // pane menu over a mouse-aware app
		"wheel",                    // scrollback and the rail
		"click a row",              // rail: switch / focus
		"drag a session",           // rail: reorder
		"drag the rail edge",       // rail: resize
		"hover a clipped row",
	} {
		if !strings.Contains(joined, gesture) {
			t.Errorf("the help overlay documents no %q gesture", gesture)
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
	for _, cat := range GetHelpCategories(reg) {
		if _, ok := helpTabNames[cat.Name]; !ok {
			t.Errorf("section %q has no entry in helpTabNames", cat.Name)
		}
	}
}
