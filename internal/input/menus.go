package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// declaredMenu is the menu called name in the running config.
func declaredMenu(o *app.OS, name string) (config.MenuConfig, bool) {
	if o.KeybindRegistry == nil || o.KeybindRegistry.GetConfig() == nil {
		return config.MenuConfig{}, false
	}
	return o.KeybindRegistry.GetConfig().Keybindings.Menu(name)
}

// handleOpenMenu builds the handler of an open_menu:NAME action. Like every
// sub-prefix gate it keeps the prefix active, so the next key goes to the
// menu rather than the pane. A name no [keybindings.menus.NAME] table
// declares opens nothing; validation warns about it.
func handleOpenMenu(action string) ActionHandler {
	name, _ := config.MenuNameOf(action)
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		if _, ok := declaredMenu(o, name); !ok {
			return o, nil
		}
		o.Menu = name
		o.PrefixActive = true
		o.LastPrefixTime = time.Now()
		return o, nil
	}
}

// HandleMenuCommand handles the key in a declared menu (see
// config.MenuConfig). A key the menu binds runs its action, which may open
// another menu: that one wins. A sticky menu stays open after a bound key
// runs, so h h h resizes three steps.
//
// A key the menu does not bind closes it. esc and ctrl+c close it and nothing
// else, in a sticky menu too. Any other key a sticky menu does not bind is
// passed to the pane in terminal mode, as tmux's -r does, so the letter typed
// after resizing is not lost. A menu that is not sticky drops the key, as
// every other prefix does.
func HandleMenuCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	name := o.Menu
	o.Menu = ""
	o.PrefixActive = false
	menu, ok := declaredMenu(o, name)
	if !ok {
		return o, nil
	}
	action := lookupAction(o, msg, func(key string) string { return o.KeybindRegistry.GetMenuAction(name, key) })
	m, cmd, ran := dispatchAction(action, msg, o)
	if ran {
		if menu.Sticky && m.Menu == "" && !m.PrefixActive {
			m.Menu = name
			m.PrefixActive = true
		}
		return m, cmd
	}
	if k := msg.String(); k == "esc" || k == "ctrl+c" {
		return o, nil
	}
	if menu.Sticky && o.Mode == app.TerminalMode {
		forwardKeyToFocusedWindow(msg, o)
	}
	return o, nil
}
