package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
)

// Menus: one table for every menu a person declares, [menus.NAME].
//
//	[menus.tools]
//	title = "Tools"
//	items = [
//	  { action = "user:deploy", key = "d" },
//	  { action = "split_vertical", key = "v", label = "Side by side" },
//	  "---",
//	  { menu = "git", key = "g" },
//	]
//
//	[menus.pane]
//	add = ["user:open-in-editor", { menu = "tools" }]
//	after = "toggle_zoom"
//	remove = ["screenshot_window"]
//
// A menu is a named list of rows. A row is an action name, "---" for a
// separator, or a table with action or menu (a submenu), and optional key,
// label and when. open_menu:NAME opens a menu: from a key it is the which-key
// panel of the rows that have a key, and as a row of a right-click menu it is
// that menu in the right-click menu's place. sticky keeps the which-key panel
// open after a key runs.
//
// The names in MenuTargets are reserved: they are the right-click menus tuios
// ships (internal/app/contextmenu_rows.go). Their table changes the shipped
// rows: items replaces them, or add puts rows after the row after names (at
// the end when empty) and remove takes rows out. A key on such a row is not
// read: a target's menu needs its target, so no key opens it.
//
// [keybindings.menus.NAME] with title, sticky and a keys table is the older
// spelling of a menu with keyed rows, read as it always was. The loader adds
// each [menus.NAME] with keyed rows to KeybindingsConfig.Menus, so the
// which-key code reads one map; a save leaves those out (withoutFoldedMenus).

// MenuTargets are the reserved menu names, the right-click menus tuios ships.
var MenuTargets = []string{"pane", "selection", "desktop", "dock", "dock_item", "workspace", "session", "repo", "machine", "file"}

// MenuSeparator is the row that draws a separator.
const MenuSeparator = "---"

// MenuSpec is one [menus.NAME] table.
type MenuSpec struct {
	// Title heads the menu. A target's menu keeps its own title.
	Title string `toml:"title,omitempty"`
	// Sticky keeps the which-key panel open after a key runs, until esc or a
	// key the menu does not bind (tmux's -r).
	Sticky bool `toml:"sticky,omitempty"`
	// Items are the menu's rows. On a target's menu they replace the
	// shipped rows.
	Items []any `toml:"items,omitempty"`
	// Add puts rows on a target's menu after the row After names, or at the
	// end.
	Add   []any  `toml:"add,omitempty"`
	After string `toml:"after,omitempty"`
	// Remove takes the rows with these actions off a target's menu.
	Remove []string `toml:"remove,omitempty"`
}

// MenuEntry is one row a person wrote: an action, a submenu or a separator,
// with the key that runs it in the which-key panel, its own label and when.
type MenuEntry struct {
	Action string
	Menu   string
	Label  string
	Keys   []string
	When   map[string]any
	Sep    bool
}

// RowAction is the action the row runs: the action, or open_menu:NAME.
func (e MenuEntry) RowAction() string {
	if e.Menu != "" {
		return OpenMenuAction(e.Menu)
	}
	return e.Action
}

// menuEntries reads a list of rows. A row it cannot read is reported and
// left out.
func menuEntries(list []any) ([]MenuEntry, []string) {
	var out []MenuEntry
	var bad []string
	for i, v := range list {
		e, problem := menuEntry(v)
		if problem != "" {
			bad = append(bad, fmt.Sprintf("row %d %s", i+1, problem))
			continue
		}
		out = append(out, e)
	}
	return out, bad
}

// menuEntry reads one row, or says why it cannot.
func menuEntry(v any) (MenuEntry, string) {
	switch v := v.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == MenuSeparator {
			return MenuEntry{Sep: true}, ""
		}
		if s == "" {
			return MenuEntry{}, "is empty"
		}
		if name, ok := MenuNameOf(s); ok {
			return MenuEntry{Menu: name}, ""
		}
		return MenuEntry{Action: s}, ""
	case map[string]any:
		var e MenuEntry
		for k, f := range v {
			var ok bool
			switch k {
			case "action":
				e.Action, ok = f.(string)
			case "menu":
				e.Menu, ok = f.(string)
			case "label":
				e.Label, ok = f.(string)
			case "key":
				e.Keys, ok = whenValues(f)
			case "when":
				e.When, ok = f.(map[string]any)
				if ok && whenProblem(e.When) != "" {
					return e, "has a when it cannot read: " + whenProblem(e.When)
				}
			}
			if !ok {
				return e, "is not a row: a row table takes action or menu as text, key as text or a list, label as text and when as a table"
			}
		}
		if (e.Action == "") == (e.Menu == "") {
			return e, "needs one of action and menu"
		}
		if name, ok := MenuNameOf(e.Action); ok {
			e.Action, e.Menu = "", name
		}
		return e, ""
	}
	return MenuEntry{}, "is not an action name, \"---\" or a table"
}

// MenuItems is the spec's items as rows, and whether it sets them.
func (s MenuSpec) MenuItems() ([]MenuEntry, bool) {
	if s.Items == nil {
		return nil, false
	}
	e, _ := menuEntries(s.Items)
	return e, true
}

// MenuAdd is the spec's add rows.
func (s MenuSpec) MenuAdd() []MenuEntry {
	e, _ := menuEntries(s.Add)
	return e
}

// IsMenuTarget reports whether name is a target's menu.
func IsMenuTarget(name string) bool { return slices.Contains(MenuTargets, name) }

// MenuRows is the menu name as rows, with its title, from [menus.NAME] or
// from the older [keybindings.menus.NAME]. A target's menu is not here: its
// rows start from the shipped ones.
func (c *UserConfig) MenuRows(name string) ([]MenuEntry, string, bool) {
	if IsMenuTarget(name) {
		return nil, "", false
	}
	if spec, ok := c.Menus[name]; ok {
		rows, _ := spec.MenuItems()
		return rows, strings.TrimSpace(spec.Title), true
	}
	old, ok := c.Keybindings.Menus[name]
	if !ok || c.foldedMenus[name] {
		return nil, "", false
	}
	return keyedRows(old.Keys), strings.TrimSpace(old.Title), true
}

// keyedRows is a keys table as rows: submenus first, then by key, as the
// which-key panel orders them. It sorts the action names, not the rows, so
// it reuses the string sort the binary has.
func keyedRows(keys map[string][]string) []MenuEntry {
	first := map[string]string{}
	var actions []string
	for action, k := range keys {
		for _, key := range k {
			if key = strings.TrimSpace(key); key != "" {
				first[action] = key
				actions = append(actions, action)
				break
			}
		}
	}
	rank := func(a string) string {
		menu := "1"
		if _, ok := MenuNameOf(a); ok {
			menu = "0"
		}
		return menu + strings.ToLower(first[a]) + "\x00" + a
	}
	slices.SortFunc(actions, func(a, b string) int { return strings.Compare(rank(a), rank(b)) })
	rows := make([]MenuEntry, 0, len(actions))
	for _, action := range actions {
		e := MenuEntry{Action: action, Keys: keys[action]}
		if name, ok := MenuNameOf(action); ok {
			e.Action, e.Menu = "", name
		}
		rows = append(rows, e)
	}
	return rows
}

// foldMenus adds each [menus.NAME] with keyed rows to the which-key menus,
// in the place of a [keybindings.menus.NAME] of the same name.
func (c *UserConfig) foldMenus() {
	kb := &c.Keybindings
	for _, name := range slices.Sorted(maps.Keys(c.Menus)) {
		if IsMenuTarget(name) || !validMenuName(name) {
			continue
		}
		spec := c.Menus[name]
		rows, _ := spec.MenuItems()
		keys := map[string][]string{}
		labels := map[string]string{}
		for _, e := range rows {
			if len(e.Keys) == 0 || e.Sep {
				continue
			}
			a := e.RowAction()
			keys[a] = append(keys[a], e.Keys...)
			label := e.Label
			if ua, ok := c.UserActionFor(a); ok && label == "" {
				label = ua.DisplayLabel()
			}
			if label = invisible.Strip(label); label != "" {
				labels[a] = label
			}
		}
		if len(keys) == 0 {
			continue
		}
		if kb.Menus == nil {
			kb.Menus = map[string]MenuConfig{}
		}
		if old, ok := kb.Menus[name]; ok {
			if c.shadowedMenus == nil {
				c.shadowedMenus = map[string]MenuConfig{}
			}
			c.shadowedMenus[name] = old
		}
		if c.foldedMenus == nil {
			c.foldedMenus = map[string]bool{}
		}
		c.foldedMenus[name] = true
		kb.Menus[name] = MenuConfig{Title: spec.Title, Sticky: spec.Sticky, Keys: keys, labels: labels}
	}
}

// withoutFoldedMenus is c as it was written: the which-key menus without
// the ones foldMenus made, and with the ones they took the place of.
func (c *UserConfig) withoutFoldedMenus() *UserConfig {
	if len(c.foldedMenus) == 0 {
		return c
	}
	out := *c
	menus := map[string]MenuConfig{}
	for name, m := range c.Keybindings.Menus {
		if !c.foldedMenus[name] {
			menus[name] = m
		}
	}
	maps.Copy(menus, c.shadowedMenus)
	if len(menus) == 0 {
		menus = nil
	}
	out.Keybindings.Menus = menus
	return &out
}

// validateMenusConfig warns about a [menus.NAME] table tuios reads only in
// part: a row it cannot read, an action it does not know, a submenu that is
// not declared, items and add together, add, after or remove on a menu that
// is not a target's, a key on a target's row, and a name both spellings
// declare. A submenu that opens itself again needs no warning: it stops
// eight deep.
func validateMenusConfig(cfg *UserConfig, result *ValidationResult) {
	known := func(action string) bool {
		if IsBuiltinAction(action) {
			return true
		}
		_, ok := cfg.UserActionFor(action)
		if !ok && strings.HasPrefix(action, UserActionPrefix) {
			_, ok = cfg.Keybindings.CommandFor(CommandActionPrefix + strings.TrimPrefix(action, UserActionPrefix))
		}
		return ok
	}
	warn := func(name, msg string) {
		result.Warnings = append(result.Warnings, ValidationError{Field: "menus." + name, Message: msg})
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Menus)) {
		spec := cfg.Menus[name]
		if !validMenuName(name) {
			warn(name, fmt.Sprintf("The menu name %q is not valid. Use lowercase letters, digits, dash and underscore.", name))
			continue
		}
		target := IsMenuTarget(name)
		if _, ok := cfg.shadowedMenus[name]; ok {
			warn(name, fmt.Sprintf("The menu %s is in [menus] and in [keybindings.menus]. tuios uses [menus.%s].", name, name))
		}
		if spec.Items != nil && spec.Add != nil {
			warn(name, "The menu has items and add. items replaces the rows, so tuios ignores add.")
		}
		if !target && (spec.Add != nil || spec.After != "" || spec.Remove != nil) {
			warn(name, "Only a target's menu has shipped rows, so add, after and remove do nothing here. Use items.")
		}
		for _, list := range [][]any{spec.Items, spec.Add} {
			entries, bad := menuEntries(list)
			for _, b := range bad {
				warn(name, "The "+b+". tuios leaves it out.")
			}
			for _, e := range entries {
				switch {
				case e.Menu != "" && IsMenuTarget(e.Menu):
					warn(name, fmt.Sprintf("The row opens the menu %q, which needs its target. A submenu needs a name of its own.", e.Menu))
				case e.Menu != "":
					if _, _, ok := cfg.MenuRows(e.Menu); !ok {
						warn(name, fmt.Sprintf("The submenu %q is not declared. Add a [menus.%s] table with items.", e.Menu, e.Menu))
					}
				case e.Action != "" && !known(e.Action):
					warn(name, fmt.Sprintf("The action %q is not known. tuios leaves the row out. tuios actions list and tuios keybinds list show the names.", e.Action))
				}
				if target && len(e.Keys) > 0 {
					warn(name, fmt.Sprintf("The key of %s is not read: a target's menu opens on its target, not from a key.", e.RowAction()))
				}
			}
		}
	}
}
