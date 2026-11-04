package main

import (
	"runtime"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/egoist/mygo/ui"
)

// Keys become tea.KeyPressMsg and go to the model, which routes them as the
// terminal client routes a key from its terminal: the prefix key, keybinds,
// copy mode and overlays first, then the focused pane, encoded for the
// keyboard mode the pane's program asked for (legacy, modifyOtherKeys or the
// kitty protocol). So every key tuios has works here with no table of its
// own.
//
// Text comes as InputText, typed or committed by an input method, and goes
// as one key per character with its text. A key that types text is left to
// the text that follows it.

var namedKeys = map[ui.Key]rune{
	ui.KeyEnter: tea.KeyEnter, ui.KeyEscape: tea.KeyEscape, ui.KeyBackspace: tea.KeyBackspace,
	ui.KeyTab: tea.KeyTab, ui.KeySpace: tea.KeySpace, ui.KeyDelete: tea.KeyDelete,
	ui.KeyInsert: tea.KeyInsert, ui.KeyHome: tea.KeyHome, ui.KeyEnd: tea.KeyEnd,
	ui.KeyPageUp: tea.KeyPgUp, ui.KeyPageDown: tea.KeyPgDown,
	ui.KeyLeft: tea.KeyLeft, ui.KeyRight: tea.KeyRight, ui.KeyUp: tea.KeyUp, ui.KeyDown: tea.KeyDown,
	ui.KeyF1: tea.KeyF1, ui.KeyF2: tea.KeyF2, ui.KeyF3: tea.KeyF3, ui.KeyF4: tea.KeyF4,
	ui.KeyF5: tea.KeyF5, ui.KeyF6: tea.KeyF6, ui.KeyF7: tea.KeyF7, ui.KeyF8: tea.KeyF8,
	ui.KeyF9: tea.KeyF9, ui.KeyF10: tea.KeyF10, ui.KeyF11: tea.KeyF11, ui.KeyF12: tea.KeyF12,
}

var punctKeys = map[ui.Key]rune{
	ui.KeyMinus: '-', ui.KeyEqual: '=', ui.KeyComma: ',', ui.KeyPeriod: '.', ui.KeySlash: '/',
	ui.KeySemicolon: ';', ui.KeyQuote: '\'', ui.KeyBracketLeft: '[', ui.KeyBracketRight: ']',
	ui.KeyBackslash: '\\', ui.KeyBackquote: '`',
}

func teaMods(m ui.Modifiers) tea.KeyMod {
	var k tea.KeyMod
	if m&ui.Shift != 0 {
		k |= tea.ModShift
	}
	if m&ui.Ctrl != 0 {
		k |= tea.ModCtrl
	}
	if m&ui.Alt != 0 {
		k |= tea.ModAlt
	}
	if m&ui.Super != 0 {
		k |= tea.ModSuper
	}
	return k
}

// keyCode is the code of a key that types a character, or 0.
func keyCode(k ui.Key) rune {
	switch {
	case k >= ui.KeyA && k <= ui.KeyZ:
		return 'a' + rune(k-ui.KeyA)
	case k >= ui.Key0 && k <= ui.Key9:
		return '0' + rune(k-ui.Key0)
	}
	return punctKeys[k]
}

// keyMsg turns a key down into the key the model takes. ok is false for a key
// whose text follows as InputText, and for keys guios keeps for itself.
func keyMsg(ev ui.InputEvent) (tea.KeyPressMsg, bool) {
	mods := teaMods(ev.Mods)
	if code, ok := namedKeys[ev.Key]; ok {
		if ev.Key == ui.KeySpace && ev.Mods&(ui.Ctrl|ui.Alt) == 0 {
			return tea.KeyPressMsg{}, false // text follows
		}
		return tea.KeyPressMsg{Code: code, Mod: mods}, true
	}
	code := keyCode(ev.Key)
	if code == 0 {
		return tea.KeyPressMsg{}, false
	}
	// Command chords are the app's on macOS (the menus take them).
	if runtime.GOOS == "darwin" && ev.Mods&ui.Super != 0 {
		return tea.KeyPressMsg{}, false
	}
	// On macOS Option types characters, which come as text.
	if runtime.GOOS == "darwin" && ev.Mods&ui.Alt != 0 && ev.Mods&ui.Ctrl == 0 {
		return tea.KeyPressMsg{}, false
	}
	if ev.Mods&(ui.Ctrl|ui.Alt) == 0 {
		return tea.KeyPressMsg{}, false // text follows
	}
	return tea.KeyPressMsg{Code: code, Mod: mods}, true
}

// textKeys turns typed text into keys, one per character.
func textKeys(s string) []tea.KeyPressMsg {
	out := make([]tea.KeyPressMsg, 0, utf8.RuneCountInString(s))
	for _, cl := range splitClusters(s) {
		r, _ := utf8.DecodeRuneInString(cl)
		out = append(out, tea.KeyPressMsg{Code: r, Text: cl})
	}
	return out
}
