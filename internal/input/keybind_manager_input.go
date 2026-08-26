package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleKeybindManagerInput handles keyboard input while the keybind manager is
// open.
//
// The recorder makes this handler different from every other overlay's: while
// it is armed there are no commands at all, Esc included, because a key that
// still meant something is a key the recorder cannot record. That is safe only
// because arming is one-shot, so the press that gets captured is also the press
// that hands the keyboard back.
//
// The Bindings tab has a filter and the other three do not, so they run
// different grammars: a tab with a filter cannot also spend letters on
// movement, or typing "close" would step through the tabs on its l. Record is
// on ctrl+r everywhere rather than on r on some tabs and not others.
func handleKeybindManagerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.KeybindArmed() {
		// Keystroke() rather than String(): it is the spelling config.toml uses,
		// so what the recorder shows is what a user would write in the file, and
		// what it writes is what the registry will look up.
		o.KeybindCapture(msg.Keystroke())
		return o, nil
	}

	// Keys that mean the same thing on every tab.
	switch msg.String() {
	case "esc", "ctrl+c":
		o.CloseKeybindManager()
		return o, nil
	case "up", "ctrl+p":
		o.KeybindMove(-1)
		return o, nil
	case "down", "ctrl+n":
		o.KeybindMove(1)
		return o, nil
	case "pgup":
		o.KeybindMove(-10)
		return o, nil
	case "pgdown":
		o.KeybindMove(10)
		return o, nil
	case "tab":
		o.KeybindStepTab(1)
		return o, nil
	case "shift+tab":
		o.KeybindStepTab(-1)
		return o, nil
	case "ctrl+r":
		// Armed from a selected binding, the recorder knows what the key would
		// be bound to. From anywhere else it is inspect-only.
		if b, ok := o.KeybindSelectedBinding(); ok {
			o.KeybindArmFor(b.Section, b.Action)
		} else {
			o.KeybindArm()
		}
		return o, nil
	case "enter":
		return o, o.KeybindCommitBinding()
	case "ctrl+d":
		// Narrow on the list, wide on the recorder. On the Bindings tab there is
		// a row under the cursor naming one action, so that is what is unbound.
		// On the Record tab the only thing named is the key itself, and the
		// question the recorder was opened to answer is whether tuios takes it,
		// so freeing it everywhere is what the answer is for.
		if o.KeybindTab == app.KeybindTabRecord {
			return o, o.KeybindFreeCapturedKey()
		}
		return o, o.KeybindUnbindSelected()
	case "ctrl+x":
		return o, o.KeybindFreeSelectedKey()
	}

	if o.KeybindTab == app.KeybindTabBindings {
		return handleKeybindFilterInput(msg, o)
	}

	// The tabs without a filter can spend letters on movement.
	switch msg.String() {
	case "q":
		o.CloseKeybindManager()
	case "k":
		o.KeybindMove(-1)
	case "j":
		o.KeybindMove(1)
	case "l", "right", "]":
		o.KeybindStepTab(1)
	case "h", "left", "[":
		o.KeybindStepTab(-1)
	case "/":
		o.KeybindSetTab(app.KeybindTabBindings)
	}
	return o, nil
}

// handleKeybindFilterInput is the Bindings tab's grammar: everything printable
// is filter text, and movement is on the arrows.
func handleKeybindFilterInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch keyStr := msg.String(); keyStr {
	case "left":
		o.KeybindStepTab(-1)
	case "right":
		o.KeybindStepTab(1)
	case "backspace":
		if q := o.KeybindQuery(); q != "" {
			o.KeybindSetQuery(q[:len(q)-1])
		}
	case "ctrl+u":
		o.KeybindSetQuery("")
	default:
		if keyStr == "space" {
			o.KeybindSetQuery(o.KeybindQuery() + " ")
		} else if msg.Text != "" {
			o.KeybindSetQuery(o.KeybindQuery() + msg.Text)
		}
	}
	return o, nil
}
