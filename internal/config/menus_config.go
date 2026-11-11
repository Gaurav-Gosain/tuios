package config

import (
	"fmt"
	"slices"
	"strings"
)

// The right-click menus a person changes: the [menus.NAME] tables.
//
//	[menus.pane]
//	add = ["user:open-in-editor", { menu = "tools" }]
//	after = "toggle_zoom"
//	remove = ["screenshot_window"]
//
//	[menus.tools]
//	title = "Tools"
//	items = ["user:deploy", "---", "user:restart-node"]
//
// A table named after a target (MenuTargets) changes that target's menu: items
// replaces its rows, or add puts rows after the row whose action is after (at
// the end when empty) and remove takes rows out. Any other name is a submenu,
// which a { menu = "NAME" } row opens. A row is an action name (a built-in
// action, user:NAME), "---" for a separator, or { menu = "NAME" }.
//
// The shipped menus are data in internal/app (contextmenu_rows.go): a person's
// table changes them without copying them.

// MenuTargets are the menus a [menus.NAME] table changes by name.
var MenuTargets = []string{"pane", "selection", "desktop", "dock", "dock_item", "workspace", "session", "repo", "machine", "file"}

// MenuSeparator is the row that draws a separator.
const MenuSeparator = "---"

// MenuRowPrefix starts the action of a row that opens a submenu.
const MenuRowPrefix = "menu:"

// MenuSpec is one [menus.NAME] table.
type MenuSpec struct {
	// Title heads a submenu. A target's menu keeps its own title.
	Title string `toml:"title,omitempty"`
	// Items replaces the menu's rows.
	Items []any `toml:"items,omitempty"`
	// Add puts rows after the row After names, or at the end.
	Add   []any  `toml:"add,omitempty"`
	After string `toml:"after,omitempty"`
	// Remove takes out the rows with these actions.
	Remove []string `toml:"remove,omitempty"`
}

// MenuEntry is one row a person wrote: an action, a separator or a submenu.
type MenuEntry struct {
	Action string
	Menu   string
	Sep    bool
}

// Row is the entry as a menu row's action: the action, MenuSeparator, or
// menu:NAME.
func (e MenuEntry) Row() string {
	switch {
	case e.Sep:
		return MenuSeparator
	case e.Menu != "":
		return MenuRowPrefix + e.Menu
	}
	return e.Action
}

// menuEntries reads a list of rows. A row it cannot read is reported and
// left out.
func menuEntries(list []any) ([]MenuEntry, []string) {
	var out []MenuEntry
	var bad []string
	for i, v := range list {
		switch v := v.(type) {
		case string:
			s := strings.TrimSpace(v)
			switch {
			case s == MenuSeparator:
				out = append(out, MenuEntry{Sep: true})
			case s == "":
				bad = append(bad, fmt.Sprintf("row %d is empty", i+1))
			default:
				out = append(out, MenuEntry{Action: s})
			}
		case map[string]any:
			name, _ := v["menu"].(string)
			if name == "" || len(v) != 1 {
				bad = append(bad, fmt.Sprintf("row %d is a table that is not { menu = \"NAME\" }", i+1))
				continue
			}
			out = append(out, MenuEntry{Menu: name})
		default:
			bad = append(bad, fmt.Sprintf("row %d is not an action name, \"---\" or { menu = \"NAME\" }", i+1))
		}
	}
	return out, bad
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

// IsMenuTarget reports whether name is a target's menu rather than a submenu.
func IsMenuTarget(name string) bool { return slices.Contains(MenuTargets, name) }

// validateMenusConfig warns about a [menus.NAME] table tuios reads only in
// part: a row it cannot read, an action it does not know, a submenu that is
// not declared, items and add together, after or remove on a submenu. A
// submenu that opens itself again needs no warning: it stops eight deep.
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
	for name, spec := range cfg.Menus {
		if !validMenuName(name) {
			warn(name, fmt.Sprintf("The menu name %q is not valid. Use lowercase letters, digits, dash and underscore.", name))
			continue
		}
		if spec.Items != nil && spec.Add != nil {
			warn(name, "The menu has items and add. items replaces the rows, so tuios ignores add.")
		}
		if !IsMenuTarget(name) && (spec.After != "" || spec.Remove != nil) {
			warn(name, "A submenu has no shipped rows, so after and remove do nothing. Use items.")
		}
		for _, list := range [][]any{spec.Items, spec.Add} {
			entries, bad := menuEntries(list)
			for _, b := range bad {
				warn(name, "The "+b+". tuios leaves it out.")
			}
			for _, e := range entries {
				switch {
				case e.Menu != "" && IsMenuTarget(e.Menu):
					warn(name, fmt.Sprintf("The row { menu = %q } names a target's menu. A submenu needs a name of its own.", e.Menu))
				case e.Menu != "":
					if _, ok := cfg.Menus[e.Menu]; !ok {
						warn(name, fmt.Sprintf("The submenu %q is not declared. Add a [menus.%s] table with items.", e.Menu, e.Menu))
					}
				case e.Action != "" && !known(e.Action):
					warn(name, fmt.Sprintf("The action %q is not known. tuios leaves the row out. tuios actions list and tuios keybinds list show the names.", e.Action))
				}
			}
		}
	}
}
