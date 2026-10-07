package guibridge

import (
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The user's keys, for a renderer that resolves them itself: the leader and
// the prefix tables run in the renderer's own key handler, and every key ends
// as a tuios action name it sends back with the action command, or as a gui_
// action it runs itself.

// Keybinds is the keybinds event.
type Keybinds struct {
	// Leader is the key that starts a prefix, such as "ctrl+b".
	Leader string `json:"leader"`
	// RepeatMS is how long a repeatable prefix key stays live after it ran,
	// in milliseconds. Zero turns repeat off.
	RepeatMS int `json:"repeat_ms"`
	// Scopes are the keyboard contexts, in the order the list shows them.
	Scopes []Scope `json:"scopes"`
	// Bindings are the keys that run, one per key: no shadowed key and no
	// action without a key. The renderer builds its key tables from these.
	Bindings []Binding `json:"bindings"`
	// Prefixes maps each action that opens a sub-prefix to the scope its
	// keys are read from: prefix_window opens "prefix.window".
	Prefixes map[string]string `json:"prefixes"`
	// Menus are the which-key menus by prefix: "" is the leader's own menu,
	// and "window", "workspace" and the rest are the sub-prefixes.
	Menus map[string][]MenuGroup `json:"menus"`
	// Rows are the rows of `tuios keybinds list --json`, for a list that shows
	// every key, including the fixed ones and the actions with no key.
	Rows []app.KeybindRow `json:"rows"`
	// GUIActions describes the gui_ actions, which only the renderer runs.
	GUIActions map[string]string `json:"gui_actions"`
}

// Scope is one keyboard context. See config.Scope.
type Scope struct {
	ID string `json:"id"`
	// Name is what the scope is called on screen.
	Name string `json:"name"`
	// Chord is what is pressed to reach the scope, such as "ctrl+b t".
	Chord string `json:"chord,omitempty"`
}

// Binding is one key that runs one action in one scope.
type Binding struct {
	Scope string `json:"scope"`
	// Action is a tuios action name, a gui_ action, or for a
	// [[keybindings.command]] entry the action that runs its command.
	Action string `json:"action"`
	// Key is the key as written in config.toml, with no chord.
	Key string `json:"key"`
	// Press is the whole thing to press, chord included.
	Press string `json:"press"`
	// Description says what the key does.
	Description string `json:"description"`
	// Command is set for a [[keybindings.command]] entry.
	Command bool `json:"command,omitempty"`
}

// MenuGroup is a titled section of a which-key menu.
type MenuGroup struct {
	Title string     `json:"title,omitempty"`
	Rows  []MenuItem `json:"rows"`
}

// MenuItem is one line of a which-key menu.
type MenuItem struct {
	// Key is the label for the keys, such as "1-9" or "h/l".
	Key         string `json:"key"`
	Description string `json:"description"`
	Action      string `json:"action"`
	// Submenu marks a key that opens another prefix menu.
	Submenu bool `json:"submenu,omitempty"`
}

// prefixScopes maps the actions that open a sub-prefix to its scope.
var prefixScopes = map[string]string{
	"prefix_window":    config.ScopePrefixWindow,
	"prefix_minimize":  config.ScopePrefixMinimize,
	"prefix_workspace": config.ScopePrefixWorkspce,
	"prefix_debug":     config.ScopePrefixDebug,
	"prefix_tape":      config.ScopePrefixTape,
	"prefix_layout":    config.ScopePrefixLayout,
}

// menuNames are the which-key menus, by the name PrefixMenuGroups takes.
var menuNames = []string{"", "window", "minimize", "workspace", "debug", "tape", "layout"}

// exportKeybinds builds the keybinds event from the model's registry and
// config, so it shows what this session's keys do now.
func exportKeybinds(o *app.OS) *Keybinds {
	reg := o.KeybindRegistry
	cfg := o.UserConfig
	if reg == nil || cfg == nil {
		cfg = config.DefaultConfig()
		reg = config.NewKeybindRegistry(cfg)
	} else {
		cfg = reg.GetConfig()
	}
	leader := cfg.Keybindings.LeaderKey
	if leader == "" {
		leader = config.DefaultLeaderKey
	}
	kb := &Keybinds{
		Leader:     leader,
		RepeatMS:   o.Settings.PrefixRepeatTime,
		Prefixes:   prefixScopes,
		Menus:      map[string][]MenuGroup{},
		Rows:       app.KeybindRows(cfg),
		GUIActions: config.GUIActionDescriptions,
	}
	for _, s := range config.Scopes(leader) {
		kb.Scopes = append(kb.Scopes, Scope{ID: s.ID, Name: s.Name, Chord: s.Chord})
	}
	for _, b := range reg.Bindings() {
		if b.Unbound || b.Shadowed || b.Press == "" {
			continue
		}
		kb.Bindings = append(kb.Bindings, Binding{
			Scope: b.Scope, Action: b.Action, Key: b.Key, Press: b.Press,
			Description: config.ScopedDescription(b.Scope, b.Action),
			Command:     b.Section == config.SectionCommand,
		})
		if b.Section == config.SectionCommand {
			kb.Bindings[len(kb.Bindings)-1].Description = b.Desc
		}
	}
	st := config.MenuState{Daemon: true, Minimized: -1}
	for _, name := range menuNames {
		var groups []MenuGroup
		for _, g := range config.PrefixMenuGroups(reg, name, st) {
			mg := MenuGroup{Title: g.Title, Rows: []MenuItem{}}
			for _, k := range g.Bindings {
				mg.Rows = append(mg.Rows, MenuItem{Key: k.Key, Description: k.Description, Action: k.Action, Submenu: k.Submenu})
			}
			groups = append(groups, mg)
		}
		kb.Menus[name] = groups
	}
	return kb
}
