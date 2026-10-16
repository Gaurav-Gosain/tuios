package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleThemeEditorInput handles a key while the theme editor is open. The
// editor is modal: a key it does not use is dropped, not passed to the pane.
func handleThemeEditorInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	if value, typing := o.ThemeEditorTyping(); typing {
		switch key {
		case "enter":
			o.ThemeEditorCommitTyping()
		case "esc":
			o.ThemeEditorEscape()
		case "tab":
			o.ThemeEditorComplete()
		default:
			if changed, _ := editFilterQuery(msg, &value, true); changed {
				o.ThemeEditorTyped(value)
			}
		}
		return o, nil
	}
	if o.ThemeEditorHidden() {
		// Hidden, the sheet waits for space or esc. Every other key is used up,
		// so a key meant for the editor never reaches a pane.
		if key == "space" || key == "esc" {
			o.ThemeEditorToggleHidden()
		}
		return o, nil
	}
	if listKey(o, key, true, listPage, o.ThemeEditorMove) {
		return o, o.ThemeEditorFlash()
	}
	switch key {
	case "esc", "q":
		o.ThemeEditorEscape()
	case "enter":
		o.ThemeEditorPick()
	case "e":
		o.ThemeEditorStartTyping()
	case "x":
		o.ThemeEditorReset()
	case "f":
		o.ThemeEditorFix()
	case "u":
		o.ThemeEditorUndo()
	case "ctrl+s":
		return o, o.ThemeEditorSave()
	case "space":
		o.ThemeEditorToggleHidden()
	case "tab", "right", "l", "]":
		o.ThemeEditorTab(1)
	case "shift+tab", "left", "h", "[":
		o.ThemeEditorTab(-1)
	}
	return o, nil
}
