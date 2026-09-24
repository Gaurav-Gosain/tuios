package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// navKey builds the key a person presses for a movement name.
func navKey(name string) tea.KeyPressMsg {
	switch name {
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "G":
		return tea.KeyPressMsg{Code: 'g', ShiftedCode: 'G', Mod: tea.ModShift, Text: "G"}
	}
	return press(name)
}

func listKeysOS(t *testing.T) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Settings.WrapLists = true
	o.Width, o.Height = 120, 40
	return o
}

// TestEveryListTakesTheSameMovementKeys drives each list overlay through its
// real input handler: home and end reach the ends, the page keys move a page
// and stop at the end rather than wrapping, and up on the first row wraps.
func TestEveryListTakesTheSameMovementKeys(t *testing.T) {
	type list struct {
		name   string
		open   func(o *app.OS)
		handle func(tea.KeyPressMsg, *app.OS) (*app.OS, tea.Cmd)
		cursor func(o *app.OS) int
		n      func(o *app.OS) int
		// letters is whether the list reads g and G as well.
		letters bool
	}
	lists := []list{
		{
			name:    "settings",
			open:    func(o *app.OS) { o.OpenSettings() },
			handle:  handleSettingsInput,
			cursor:  func(o *app.OS) int { return o.SettingsSelected },
			n:       func(o *app.OS) int { return o.SettingsRowCount() },
			letters: true,
		},
		{
			name: "command palette",
			open: func(o *app.OS) {
				o.ShowCommandPalette = true
				o.PaletteItems = []app.CommandPaletteItem{{Name: "one"}, {Name: "two"}, {Name: "three"}, {Name: "four"}}
			},
			handle: handleCommandPaletteInput,
			cursor: func(o *app.OS) int { return o.CommandPaletteSelected },
			n:      func(*app.OS) int { return 4 },
		},
		{
			name: "quit menu",
			open: func(o *app.OS) {
				o.QuitMenuItems = []app.QuitMenuItem{{Label: "a"}, {Label: "b"}, {Label: "c"}}
			},
			handle:  handleQuitMenuKey,
			cursor:  func(o *app.OS) int { return o.QuitMenuSelected },
			n:       func(*app.OS) int { return 3 },
			letters: true,
		},
		{
			name:    "dock editor",
			open:    func(o *app.OS) { o.OpenDockEditor() },
			handle:  handleDockEditorInput,
			cursor:  func(o *app.OS) int { return o.DockEditorSelected },
			n:       nil,
			letters: true,
		},
	}
	for _, l := range lists {
		t.Run(l.name, func(t *testing.T) {
			o := listKeysOS(t)
			l.open(o)

			o, _ = l.handle(navKey("end"), o)
			last := l.cursor(o)
			if l.n != nil && last != l.n(o)-1 {
				t.Errorf("end went to %d, want the last row %d", last, l.n(o)-1)
			}
			o, _ = l.handle(navKey("home"), o)
			first := l.cursor(o)
			if first >= last {
				t.Fatalf("home went to %d, which is not above end's %d", first, last)
			}
			o, _ = l.handle(navKey("pgup"), o)
			if got := l.cursor(o); got != first {
				t.Errorf("page up from the first row went to %d; a page must not wrap", got)
			}
			o, _ = l.handle(navKey("up"), o)
			if got := l.cursor(o); got != last {
				t.Errorf("up on the first row went to %d, want the last %d", got, last)
			}
			o, _ = l.handle(navKey("pgdown"), o)
			if got := l.cursor(o); got != last {
				t.Errorf("page down from the last row went to %d; a page must not wrap", got)
			}
			if l.letters {
				o, _ = l.handle(press("g"), o)
				if got := l.cursor(o); got != first {
					t.Errorf("g went to %d, want the first row %d", got, first)
				}
				o, _ = l.handle(navKey("G"), o)
				if got := l.cursor(o); got != last {
					t.Errorf("G went to %d, want the last row %d", got, last)
				}
			}
		})
	}
}

// TestAFilteredListKeepsItsLetters: g in a list with a filter is a letter of
// the query, not a jump.
func TestAFilteredListKeepsItsLetters(t *testing.T) {
	o := listKeysOS(t)
	o.ShowCommandPalette = true
	o.PaletteItems = []app.CommandPaletteItem{{Name: "one"}, {Name: "gone"}}
	o, _ = handleCommandPaletteInput(press("g"), o)
	if o.CommandPaletteQuery != "g" {
		t.Errorf("g in the palette left the query %q, want it typed", o.CommandPaletteQuery)
	}
}
