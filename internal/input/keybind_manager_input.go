package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleKeybindManagerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.KeybindArmed() {
		o.KeybindCapture(msg.Keystroke())
		return o, nil
	}

	if listKey(o, msg.String(), o.KeybindTab != app.KeybindTabBindings, listPage, o.KeybindMove) {
		return o, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		o.CloseKeybindManager()
		return o, nil
	case "tab":
		o.KeybindStepTab(1)
		return o, nil
	case "shift+tab":
		o.KeybindStepTab(-1)
		return o, nil
	case "ctrl+r":
		if b, ok := o.KeybindSelectedBinding(); ok {
			o.KeybindArmFor(b.Section, b.Action)
		} else {
			o.KeybindArm()
		}
		return o, nil
	case "enter":
		return o, o.KeybindCommitBinding()
	case "ctrl+d":
		switch o.KeybindTab {
		case app.KeybindTabRecord:
			return o, o.KeybindFreeCapturedKey()
		case app.KeybindTabConflicts:
			return o, o.KeybindResolveSelectedConflict()
		}
		return o, o.KeybindUnbindSelected()
	case "ctrl+x":
		return o, o.KeybindFreeSelectedKey()
	}

	if o.KeybindTab == app.KeybindTabBindings {
		return handleKeybindFilterInput(msg, o)
	}

	switch msg.String() {
	case "q":
		o.CloseKeybindManager()
	case "l", "right", "]":
		o.KeybindStepTab(1)
	case "h", "left", "[":
		o.KeybindStepTab(-1)
	case "/":
		o.KeybindSetTab(app.KeybindTabBindings)
	}
	return o, nil
}

func handleKeybindFilterInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch keyStr := msg.String(); keyStr {
	case "left":
		o.KeybindStepTab(-1)
	case "right":
		o.KeybindStepTab(1)
	default:
		o.KeybindQueryKey(keyStr, msg.Text)
	}
	return o, nil
}
