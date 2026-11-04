package main

import (
	"runtime"

	"github.com/egoist/mygo/ui"
)

// The command palette's keys.
//
// The palette opens on the tuios config's palette keys while tuios keys are
// on: [keybindings.global] command_palette (Ctrl+P by default), as in the
// TUI, and [keybindings.gui] command_palette (Ctrl+Shift+P, which a GUI on
// macOS reads as Cmd+P). The menu's Command palette item (palette.open)
// opens it too, and on macOS Cmd+Shift+P, as in editors. Cmd+K is not one:
// it clears the terminal on macOS, and Ctrl+K is the shell's on Linux.

// keyChord is a key with the modifiers held for it.
type keyChord struct {
	mods ui.Modifiers
	key  ui.Key
}

// paletteChords reads the palette keys of one section of the tuios
// keymap. gui says the section is [keybindings.gui], whose Ctrl+Shift a
// GUI on macOS reads as Cmd.
func paletteChords(keys []string, gui bool) []keyChord {
	var out []keyChord
	for _, k := range keys {
		mods, key, ok := leaderChord(normKey(k))
		if !ok || mods == 0 {
			continue // a bare key would type nowhere else
		}
		if gui && runtime.GOOS == "darwin" && mods&(ui.Ctrl|ui.Shift) == ui.Ctrl|ui.Shift {
			mods = mods&^(ui.Ctrl|ui.Shift) | ui.Super
		}
		out = append(out, keyChord{mods, key})
	}
	return out
}

// paletteShortcuts opens the palette on its keys. The window's shortcuts
// see a key before the terminal does, and take only these chords: every
// other key reaches the pane.
func (a *App) paletteShortcuts(c *ui.Context) {
	open := false
	if a.keysOn() && !a.tk.live() {
		for _, k := range a.tk.palette {
			open = c.Shortcut(k.mods, k.key) || open
		}
	}
	if runtime.GOOS == "darwin" {
		open = c.Shortcut(ui.Super|ui.Shift, ui.KeyP) || open
	}
	if open {
		a.openPalette()
	}
}
