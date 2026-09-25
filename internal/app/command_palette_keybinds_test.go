package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// paletteOS is a model with a config and a registry, which is all the keybind
// rows need.
func paletteOS(t *testing.T) *OS {
	t.Helper()
	cfg := config.DefaultConfig()
	return &OS{Settings: config.Global, UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)}
}

// TestUnbindIsFindableByItsOwnWord. Someone who wants a key back searches for
// "unbind", not for "keybind manager".
//
// Negative control: delete the "Unbind a key" row and this fails.
func TestUnbindIsFindableByItsOwnWord(t *testing.T) {
	for _, word := range []string{"unbind", "rebind"} {
		hits := FilterCommandPalette(GetCommandPaletteItems(&config.Global), word)
		if len(hits) == 0 {
			t.Errorf("nothing in the palette answers to %q", word)
			continue
		}
		if hits[0].Action == nil {
			t.Errorf("the first hit for %q does nothing", word)
		}
	}
}

// TestActionRowsStayOutOfAnOrdinarySearch is why the rows sit behind a token.
// There are a few hundred of them and around twenty commands, and letting them
// into the default list would mean the palette no longer answers the question
// it was opened for.
//
// Negative control: drop the item.Keybind == keybinds partition from
// FilterCommandPalette and this fails: "window" alone returns dozens of action
// rows alongside the commands.
func TestActionRowsStayOutOfAnOrdinarySearch(t *testing.T) {
	m := paletteOS(t)
	m.PaletteKeybindItems = getKeybindPaletteItems(m)
	if len(m.PaletteKeybindItems) == 0 {
		t.Fatal("no action rows were built")
	}
	m.rebuildPaletteItems()

	for _, query := range []string{"", "window", "close", "keybind"} {
		for _, item := range FilterCommandPalette(m.allPaletteItems(), query) {
			if item.Keybind {
				t.Errorf("query %q returned the action row %q without the token", query, item.Name)
				break
			}
		}
	}
}

// TestTokenFindsAnActionByEitherName. A user who read config.toml searches for
// "prefix_close_window"; a user who read the overlay searches for "close". Both
// have to work, or the row is only findable by people who already know where it
// came from.
//
// Negative control: build the row's Name from the description alone and the
// action-name half fails.
func TestTokenFindsAnActionByEitherName(t *testing.T) {
	m := paletteOS(t)
	m.PaletteKeybindItems = getKeybindPaletteItems(m)
	m.rebuildPaletteItems()

	for _, query := range []string{"#prefix_close_window", "#close window"} {
		hits := FilterCommandPalette(m.allPaletteItems(), query)
		if len(hits) == 0 {
			t.Errorf("%q found nothing", query)
			continue
		}
		found := false
		for _, h := range hits {
			if !h.Keybind {
				t.Errorf("%q returned the non-action row %q", query, h.Name)
			}
			if strings.Contains(h.Name, "close_window") {
				found = true
			}
		}
		if !found {
			t.Errorf("%q did not find a close_window row; got %v", query, paletteNames(hits))
		}
	}
}

// TestABareTokenListsEveryAction, which is the answer to "what can I rebind".
//
// Negative control: make splitPaletteKeybinds require text after the token and
// this fails.
func TestABareTokenListsEveryAction(t *testing.T) {
	m := paletteOS(t)
	m.PaletteKeybindItems = getKeybindPaletteItems(m)
	m.rebuildPaletteItems()

	hits := FilterCommandPalette(m.allPaletteItems(), "#")
	if len(hits) != len(m.PaletteKeybindItems) {
		t.Errorf("a bare token returned %d rows, want all %d actions", len(hits), len(m.PaletteKeybindItems))
	}
}
