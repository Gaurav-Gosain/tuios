package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleSettingsInput handles keyboard input while the settings overlay is open.
// Changes apply live and are persisted by the OS as they are made.
func handleSettingsInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SettingsEditActive() {
		return handleSettingsEditInput(msg, o)
	}
	if o.SettingsSearchOpen() {
		return handleSettingsSearchInput(msg, o)
	}
	if listKey(msg.String(), true, o.SettingsPageRows(), o.SettingsMove) {
		return o, nil
	}
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		o.CloseSettings()
	case "left", "h":
		return o, o.SettingsAdjust(-1)
	case "right", "l":
		return o, o.SettingsAdjust(1)
	case "enter", "space":
		return o, o.SettingsActivate()
	case "tab", "]":
		o.SettingsNextCategory()
	case "shift+tab", "[":
		o.SettingsPrevCategory()
	case "/":
		o.SettingsSearchStart("")
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// The first nine tabs by number, in the order the strip draws them.
		o.SettingsSetCategory(int(msg.String()[0] - '1'))
	default:
		// A letter no key on this page answers to starts a search with it, so
		// a person can type the name of the setting they came for. The letters
		// the page does use keep their meaning; / always opens the search.
		if startsSettingsSearch(msg) {
			o.SettingsSearchStart(msg.Text)
		}
	}
	return o, nil
}

// startsSettingsSearch reports whether a key typed on the tab view begins a
// search: a single letter carrying text and no modifier beyond shift. The keys
// the tab view already answers to (h, j, k, l, g, G, q) never reach here,
// because the switch above takes them first.
func startsSettingsSearch(msg tea.KeyPressMsg) bool {
	if msg.Mod&^tea.ModShift != 0 {
		return false
	}
	r := []rune(msg.Text)
	if len(r) != 1 {
		return false
	}
	return (r[0] >= 'a' && r[0] <= 'z') || (r[0] >= 'A' && r[0] <= 'Z')
}

// handleSettingsSearchInput handles keys while the search line is open. Every
// printable key is part of the query. The list keys move through the results,
// the left and right arrows change the row under the cursor and enter
// activates it, all in place, exactly as they would on its own tab. Tab goes to
// the row on its own tab. Esc closes the search and puts the tab view back
// where it was; the next esc closes the panel.
func handleSettingsSearchInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	if listKey(key, false, o.SettingsPageRows(), o.SettingsMove) {
		return o, nil
	}
	switch key {
	case "esc":
		o.SettingsSearchClose()
	case "ctrl+c":
		o.CloseSettings()
	case "enter":
		return o, o.SettingsActivate()
	case "left":
		return o, o.SettingsAdjust(-1)
	case "right":
		return o, o.SettingsAdjust(1)
	case "tab":
		o.SettingsSearchJump()
	case "backspace":
		o.SettingsSearchBackspace()
	case "ctrl+u":
		o.SettingsSearchSetQuery("")
	default:
		q := o.SettingsSearchQuery()
		if changed, _ := editFilterQuery(msg, &q, false); changed {
			o.SettingsSearchSetQuery(q)
		}
	}
	return o, nil
}

// handleSettingsEditInput handles keystrokes while a text setting is being
// edited inline. Enter commits, Esc cancels, and printable input is appended to
// the buffer.
func handleSettingsEditInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	number := o.SettingsEditingNumber()
	switch msg.String() {
	case "esc":
		o.SettingsEditCancel()
	case "enter":
		return o, o.SettingsEditCommit()
	case "backspace":
		o.SettingsEditBackspace()
	case "ctrl+u":
		o.SettingsEditClear()
	case "left", "right", "up", "down":
		// The slider half of the number editor. A text field has nothing for
		// these to do, so they are left alone there.
		if !number {
			return o, nil
		}
		dir := 1
		if msg.String() == "left" || msg.String() == "down" {
			dir = -1
		}
		o.SettingsEditNumberSlide(dir)
	default:
		switch {
		case number:
			// Digits and a leading minus only. A number editor that accepts
			// letters is one that refuses the value on Enter for a reason the
			// user cannot see while typing it.
			if t := msg.Text; t != "" && isNumberEntry(t) {
				o.SettingsEditAppend(t)
			}
		case msg.String() == "space":
			o.SettingsEditAppend(" ")
		case msg.Text != "":
			o.SettingsEditAppend(msg.Text)
		}
	}
	return o, nil
}

// isNumberEntry reports whether typed text belongs in a number field.
func isNumberEntry(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}
