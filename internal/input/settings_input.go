package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleSettingsInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SettingsEditActive() {
		return handleSettingsEditInput(msg, o)
	}
	if o.SettingsSearchOpen() {
		return handleSettingsSearchInput(msg, o)
	}
	if listKey(o, msg.String(), true, o.SettingsPageRows(), o.SettingsMove) {
		return o, nil
	}
	switch msg.String() {
	case "esc", "q":
		if !o.SettingsBack() {
			o.CloseSettings()
		}
	case "ctrl+c":
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
	case "backspace", "delete":
		return o, o.SettingsResetSelected()
	case "ctrl+z":
		return o, o.SettingsUndo()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		o.SettingsSetCategory(int(msg.String()[0] - '1'))
	default:
		if startsSettingsSearch(msg) {
			o.SettingsSearchStart(msg.Text)
		}
	}
	return o, nil
}

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

func handleSettingsSearchInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	if listKey(o, key, false, o.SettingsPageRows(), o.SettingsMove) {
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
	case "delete":
		return o, o.SettingsResetSelected()
	case "ctrl+z":
		return o, o.SettingsUndo()
	case "backspace":
		o.SettingsSearchBackspace()
	case "ctrl+u":
		o.SettingsSearchSetQuery("")
	default:
		o.SettingsSearchKey(key, msg.Text)
	}
	return o, nil
}

func handleSettingsEditInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	number := o.SettingsEditingNumber()
	switch msg.String() {
	case "esc":
		o.SettingsEditCancel()
	case "enter":
		return o, o.SettingsEditCommit()
	case "up", "down":
		if number {
			dir := 1
			if msg.String() == "down" {
				dir = -1
			}
			o.SettingsEditNumberSlide(dir)
		}
	case "left", "right":
		if !number {
			o.SettingsEditKey(msg.String(), "")
			return o, nil
		}
		dir := 1
		if msg.String() == "left" || msg.String() == "down" {
			dir = -1
		}
		o.SettingsEditNumberSlide(dir)
	default:
		o.SettingsEditKey(msg.String(), msg.Text)
	}
	return o, nil
}
