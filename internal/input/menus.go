package input

import (
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
		o.OpenMenu(name)
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
	origin := o.MenuState().Origin
	k := startMenuKey(o)
	menu, ok := declaredMenu(o, name)
	if !ok || k.leaves(msg) {
		return o, nil
	}
	action := lookupAction(o, msg, func(key string) string { return o.KeybindRegistry.GetMenuAction(name, key) })
	if k.refuses(o, action) {
		k.unbound(msg, o)
		return o, nil
	}
	if origin.Set {
		return handleOverlayMenuKey(msg, o, name, menu, origin, action)
	}
	m, cmd, ran := dispatchAction(action, msg, o)
	if ran {
		if menu.Sticky && m.Menu == "" && !m.PrefixActive {
			m.HoldMenu(name)
		} else {
			// Lock mode holds a menu open after a repeatable action too.
			k.ran(m, action)
		}
		return m, cmd
	}
	if s := msg.String(); s == "esc" || s == "ctrl+c" {
		return o, nil
	}
	if k.unbound(msg, o) {
		return o, nil
	}
	if menu.Sticky && o.Mode == app.TerminalMode {
		forwardKeyToFocusedWindow(msg, o)
	}
	return o, nil
}

// handleOverlayMenuKey is HandleMenuCommand for a menu opened over an
// overlay (issue #1590). The menu is closed when this runs. A key that opens
// another declared menu opens it over the same overlay. Any other action the
// menu binds is handed back to the overlay: its key handler runs once with
// the key, and its tableAction returns that action in place of the key's own
// (keychords.go). The hand-back is cleared after that one call whatever it
// did, also when the overlay closed or never read its table, so it can never
// answer a later key. A sticky menu opens again after, while the overlay is
// still up. esc, ctrl+c and a key the menu does not bind close it and do
// nothing else.
func handleOverlayMenuKey(msg tea.KeyPressMsg, o *app.OS, name string, menu config.MenuConfig, origin app.MenuOrigin, action string) (*app.OS, tea.Cmd) {
	if next, ok := config.MenuNameOf(action); ok {
		if _, declared := declaredMenu(o, next); declared {
			o.OpenMenuFrom(next, origin.Table, false)
		}
		return o, nil
	}
	if action == "" {
		return o, nil
	}
	o.MenuAction = app.MenuHandback{Set: true, Table: origin.Table, Action: action}
	m, cmd := routeKey(msg, o)
	m.MenuAction = app.MenuHandback{}
	o.MenuAction = app.MenuHandback{}
	if menu.Sticky && m.Menu == "" && !m.PrefixActive && overlayOpen(m, origin.Table) {
		m.OpenMenuFrom(name, origin.Table, true)
	}
	return m, cmd
}

// overlayOpen reports whether the overlay that table belongs to is still up,
// for a sticky menu over it. A table this does not know counts as up.
func overlayOpen(o *app.OS, table config.Table) bool {
	switch table {
	case config.TableInbox, config.TableInboxPeek:
		return o.ShowInbox
	case config.TableMail:
		return o.ShowAgentMail
	case config.TableTransfers:
		return o.TransfersOpen()
	case config.TableSidebar, config.TableSidebarFiles, config.TableSidebarAgents:
		return o.SidebarFocused
	case config.TableReview:
		return o.ReviewOpen()
	case config.TableQuickLook, config.TableQuickLookInside:
		return o.QuickLookOpen()
	}
	return true
}
