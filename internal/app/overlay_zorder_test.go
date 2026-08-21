package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestEveryOverlayKindHasAPlaceInTheStack is the guard for the bug the colour
// picker found: "accent" was in the set of overlays that can be open and not in
// the order they are stacked in, so it never entered the z-order at all and sat
// on the base index. Two panels on the base index tie, and the tie went to
// whichever was recorded first, which is how clicks inside the picker landed on
// the settings row behind it.
func TestEveryOverlayKindHasAPlaceInTheStack(t *testing.T) {
	inOrder := map[string]bool{}
	for _, k := range overlayKindOrder {
		if inOrder[k] {
			t.Errorf("%q appears twice in overlayKindOrder", k)
		}
		inOrder[k] = true
	}

	// Every flag on, so openOverlayKinds names every kind there is.
	m := &OS{
		ShowHelp: true, ShowCommandPalette: true, ShowSessionSwitcher: true,
		ShowWorkspaceSwitcher: true, ShowLayoutPicker: true, ShowAggregateView: true,
		ShowSettings: true, ShowThemePicker: true, ShowAccentPicker: true,
		ShowQuitMenu: true, ShowSessionClose: true,
	}
	for kind := range m.openOverlayKinds() {
		if !inOrder[kind] {
			t.Errorf("%q can be open but is not in overlayKindOrder, so it never gets a z-index", kind)
		}
	}
}

// TestThePickerOutranksThePanelThatOpenedIt is the same bug stated as the
// behaviour that broke: a colour row opens the picker over the settings panel,
// and the picker has to be the one a click inside it reaches.
func TestThePickerOutranksThePanelThatOpenedIt(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig(), Width: 150, Height: 45})
	m.OpenSettings()
	m.reconcileOverlayZOrder()
	focusSetting(t, m, "Appearance", "Focused border color")
	m.SettingsActivate()
	m.reconcileOverlayZOrder()

	if !m.ShowAccentPicker {
		t.Fatal("the picker did not open")
	}
	if got, want := m.overlayZ("accent"), m.overlayZ("settings"); got <= want {
		t.Errorf("the picker sits at z %d and the settings panel at %d; a click inside the picker would reach the panel behind it", got, want)
	}
}
