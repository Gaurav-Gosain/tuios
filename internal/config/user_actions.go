package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// User actions: the [[actions]] entries, one thing a person can bind to a key,
// put in a menu or run from the CLI.
//
//	[[actions]]
//	name = "open-in-editor"
//	label = "Open folder in editor"
//	key = "prefix+alt+e"
//	run = ["code", "{pane.cwd}"]
//	in = "silent"
//
// An entry is the action "user:<name>". Its body is one of:
//
//   - action: a built-in action, which runs as its key would;
//   - run: a command as an argument list, in a popup, a pane, a scratch pane
//     or silently (in), the [[keybindings.command]] rules for where it runs;
//   - plugin: a herdr plugin action, as type = "plugin_action" names it.
//
// A placeholder such as {pane.cwd} fills from the target when the action
// runs. It is replaced in one pass, so text a placeholder brings in (a
// selection, a pane title) is never read for placeholders itself, and it is
// never given to a shell: an argument stays one argument. A shell runs only
// when the person writes one into run, and then a placeholder may not stand
// in the shell's script: pass it as an argument after the script and read it
// as $1.
//
// A [[keybindings.command]] entry is a user action too: user:NAME runs the
// entry called NAME when no [[actions]] entry has that name, and tuios
// actions list shows both.
//
// User actions are read from config.toml only. They are not options, so
// set-option, which any pane may call, cannot add or change one: an action a
// pane could write would run as the person at the next key press.

// UserActionPrefix starts the action name of every user action.
const UserActionPrefix = "user:"

// The places a command action runs, the [[keybindings.command]] types.
const (
	UserActionInPopup   = CommandTypePopup
	UserActionInPane    = CommandTypePane
	UserActionInScratch = CommandTypeScratch
	UserActionInSilent  = "silent"
)

// UserActionIns is every value in accepts.
var UserActionIns = []string{UserActionInPopup, UserActionInPane, UserActionInScratch, UserActionInSilent}

// The targets an action is offered on. A1 runs every action on the focused
// pane; the menus of later changes offer an action only on its targets.
var UserActionTargets = []string{"pane", "session", "workspace", "rail_row", "card", "file", "selection", "desktop"}

// UserActionPlaceholders are the placeholders a run argument may hold.
var UserActionPlaceholders = []string{
	"pane.id", "pane.cwd", "pane.title", "pane.program", "session", "workspace",
	"selection", "host", "card.id", "card.title", "file.path", "agent.window",
}

// UserAction is one [[actions]] entry.
type UserAction struct {
	// Name is the action's name: lowercase letters, digits, dash and
	// underscore. The action is user:Name.
	Name string `toml:"name"`
	// Label names the action in menus, the palette and the CLI. The name
	// when empty.
	Label string `toml:"label,omitempty"`
	// Icon is a glyph name for menus. Optional.
	Icon string `toml:"icon,omitempty"`
	// Key binds the action, written as a [[keybindings.command]] key:
	// "prefix+..." acts after the leader, any other key in window and
	// terminal mode. Optional.
	Key string `toml:"key,omitempty"`
	// Targets are where the action is offered. Empty means a pane.
	Targets []string `toml:"targets,omitempty"`
	// Trusted lets an action that sends text to an agent and submits it run
	// without asking first.
	Trusted bool `toml:"trusted,omitempty"`

	// Action is a built-in action to run.
	Action string `toml:"action,omitempty"`
	// Run is a command and its arguments, with placeholders.
	Run []string `toml:"run,omitempty"`
	// In is where Run runs: popup (the default), pane, scratch or silent.
	In string `toml:"in,omitempty"`
	// Width and Height size a popup or scratch, as [[keybindings.command]]
	// does.
	Width  string `toml:"width,omitempty"`
	Height string `toml:"height,omitempty"`
	// Plugin is a herdr plugin action, bare or qualified with the plugin id.
	Plugin string `toml:"plugin,omitempty"`
}

// ActionName is the entry's action name.
func (a UserAction) ActionName() string { return UserActionPrefix + a.Name }

// DisplayLabel is the label, or the name.
func (a UserAction) DisplayLabel() string {
	if l := strings.TrimSpace(a.Label); l != "" {
		return l
	}
	return a.Name
}

// ResolvedIn is In with its default.
func (a UserAction) ResolvedIn() string {
	if in := strings.ToLower(strings.TrimSpace(a.In)); in != "" {
		return in
	}
	return UserActionInPopup
}

// Body is what the action runs: "action", "run" or "plugin", or "" when it
// has none.
func (a UserAction) Body() string {
	switch {
	case a.Action != "":
		return "action"
	case len(a.Run) > 0:
		return "run"
	case a.Plugin != "":
		return "plugin"
	}
	return ""
}

// AsCommand is the [[keybindings.command]] entry a run or plugin action runs
// as: the same popup, pane, scratch and plugin paths, and the same key rules.
// Command is empty for a run action: its argv is built from Run.
func (a UserAction) AsCommand() CommandBinding {
	c := CommandBinding{Key: a.Key, Name: a.Name, Description: a.DisplayLabel(), Width: a.Width, Height: a.Height}
	switch {
	case a.Plugin != "":
		c.Type, c.Command = CommandTypePluginAction, a.Plugin
	default:
		c.Type = a.ResolvedIn()
		if c.Type == UserActionInSilent {
			c.Type = CommandTypeShell
		}
	}
	return c
}

// userActionNameRE is a name an action may have.
var userActionNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// placeholderRE finds {name} in an argument.
var placeholderRE = regexp.MustCompile(`\{([a-z][a-z.]*)\}`)

// shells are the programs whose -c script a placeholder may not stand in.
var shells = []string{"sh", "bash", "zsh", "dash", "ksh", "fish", "nu", "pwsh", "powershell", "cmd", "cmd.exe"}

// userActionProblem is what is wrong with a, "" when nothing is. builtin
// reports whether a name is a built-in action.
func userActionProblem(a UserAction, normalizer *KeyNormalizer, builtin func(string) bool) string {
	if !userActionNameRE.MatchString(a.Name) {
		return fmt.Sprintf("The name %q is not valid. Use lowercase letters, digits, dash and underscore, for example name = \"open-in-editor\".", a.Name)
	}
	bodies := 0
	for _, set := range []bool{a.Action != "", len(a.Run) > 0, a.Plugin != ""} {
		if set {
			bodies++
		}
	}
	switch {
	case bodies == 0:
		return "The action has nothing to run. Add one of action, run or plugin."
	case bodies > 1:
		return "The action has more than one of action, run and plugin. Keep one."
	}
	if a.Action != "" {
		if strings.HasPrefix(a.Action, UserActionPrefix) || strings.HasPrefix(a.Action, CommandActionPrefix) {
			return fmt.Sprintf("The action %q is a user action. Name a built-in action.", a.Action)
		}
		if builtin != nil && !builtin(a.Action) {
			return fmt.Sprintf("The action %q is not a built-in action. tuios keybinds list shows the names.", a.Action)
		}
	}
	if len(a.Run) > 0 {
		if strings.TrimSpace(a.Run[0]) == "" {
			return "The first item of run is empty. Put the program first, for example run = [\"code\", \"{pane.cwd}\"]."
		}
		if strings.Contains(a.Run[0], "=") || placeholderRE.MatchString(a.Run[0]) {
			return "The first item of run is the program. It cannot hold = or a placeholder."
		}
		if !slices.Contains(UserActionIns, a.ResolvedIn()) {
			return fmt.Sprintf("The value in = %q is not known. Use popup, pane, scratch or silent.", a.In)
		}
		if msg := runPlaceholderProblem(a.Run); msg != "" {
			return msg
		}
	} else if a.In != "" {
		return "The value in is only for run. Remove it."
	}
	for _, t := range a.Targets {
		if !slices.Contains(UserActionTargets, t) {
			return fmt.Sprintf("The target %q is not known. Use %s.", t, strings.Join(UserActionTargets, ", "))
		}
	}
	key := ""
	if strings.TrimSpace(a.Key) != "" {
		key = a.AsCommand().BareKey()
	}
	return keyAndBoxProblem(normalizer, key, a.Width, a.Height)
}

// runPlaceholderProblem refuses an unknown placeholder, and a placeholder in
// the script of a shell's -c.
func runPlaceholderProblem(run []string) string {
	shell := slices.Contains(shells, strings.ToLower(filepath.Base(run[0])))
	for i, arg := range run {
		for _, m := range placeholderRE.FindAllStringSubmatch(arg, -1) {
			if !slices.Contains(UserActionPlaceholders, m[1]) {
				return fmt.Sprintf("The placeholder {%s} is not known. Use one of {%s}.", m[1], strings.Join(UserActionPlaceholders, "}, {"))
			}
			if shell && i > 0 && (run[i-1] == "-c" || strings.EqualFold(run[i-1], "/c")) {
				return fmt.Sprintf("The placeholder {%s} is in a shell script. A shell would read its text as code. Put {%s} after the script and use \"$1\" in the script.", m[1], m[1])
			}
		}
	}
	return ""
}

// ExpandRun fills the placeholders of run from facts, in one pass: a value
// is never read for placeholders. A placeholder with no value is an error
// that names it, so an action never runs with an argument left empty by
// mistake.
func ExpandRun(run []string, facts map[string]string) ([]string, error) {
	out := make([]string, len(run))
	for i, arg := range run {
		var missing string
		out[i] = placeholderRE.ReplaceAllStringFunc(arg, func(m string) string {
			key := m[1 : len(m)-1]
			v, ok := facts[key]
			if !ok || v == "" {
				if missing == "" {
					missing = key
				}
				return ""
			}
			return v
		})
		if missing != "" {
			return nil, errfmt.Errorf("this action needs {%s}, and the target has none", missing)
		}
	}
	return out, nil
}

// UserActions returns the [[actions]] entries tuios uses: every valid entry,
// a second one with the same name left out. builtin reports whether a name
// is a built-in action; nil accepts any.
func (c *UserConfig) UserActions(builtin func(string) bool) []UserAction {
	if len(c.Actions) == 0 {
		return nil
	}
	normalizer := NewKeyNormalizer()
	seen := map[string]bool{}
	out := make([]UserAction, 0, len(c.Actions))
	for _, a := range c.Actions {
		if userActionProblem(a, normalizer, builtin) != "" || seen[a.Name] {
			continue
		}
		seen[a.Name] = true
		out = append(out, a)
	}
	return out
}

// UserActionFor is the entry behind a user:NAME action, if one exists.
func (c *UserConfig) UserActionFor(action string) (UserAction, bool) {
	name, ok := strings.CutPrefix(action, UserActionPrefix)
	if !ok {
		return UserAction{}, false
	}
	for _, a := range c.UserActions(nil) {
		if a.Name == name {
			return a, true
		}
	}
	return UserAction{}, false
}

// validateUserActions warns about every [[actions]] entry tuios leaves out,
// and about one whose name a [[keybindings.command]] entry has too.
func validateUserActions(cfg *UserConfig, result *ValidationResult) {
	normalizer := NewKeyNormalizer()
	seen := map[string]bool{}
	commands := map[string]bool{}
	for _, c := range cfg.Keybindings.Commands() {
		commands[c.ResolvedName()] = true
	}
	for i, a := range cfg.Actions {
		field := fmt.Sprintf("actions[%d]", i+1)
		if msg := userActionProblem(a, normalizer, IsBuiltinAction); msg != "" {
			result.Warnings = append(result.Warnings, ValidationError{Field: field, Key: a.Name, Message: msg + " tuios ignores this action."})
			continue
		}
		if seen[a.Name] {
			result.Warnings = append(result.Warnings, ValidationError{Field: field, Key: a.Name,
				Message: fmt.Sprintf("Another action has the name %q. tuios uses the first one and ignores this one.", a.Name)})
			continue
		}
		seen[a.Name] = true
		if commands[a.Name] {
			result.Warnings = append(result.Warnings, ValidationError{Field: field, Key: a.Name,
				Message: fmt.Sprintf("A [[keybindings.command]] entry has the name %q too. user:%s runs this action, and command:%s runs the command entry.", a.Name, a.Name, a.Name)})
		}
	}
}

// IsBuiltinAction reports whether name is an action of the registry.
func IsBuiltinAction(name string) bool {
	_, ok := LookupAction(name)
	return ok
}

// keyedEntry is a [[keybindings.command]] entry or a keyed [[actions]] entry
// as the key map and the binding report read it: the command entries first,
// in file order, then the user actions.
type keyedEntry struct {
	section, key, action, desc, report string
}

// keyedEntries is every entry with a key that runs a command or a user
// action.
func (c *UserConfig) keyedEntries() []keyedEntry {
	var out []keyedEntry
	for _, cb := range c.Keybindings.Commands() {
		out = append(out, keyedEntry{section: cb.Section(), key: cb.BareKey(), action: cb.Action(), desc: cb.Label(), report: SectionCommand})
	}
	for _, a := range c.UserActions(IsBuiltinAction) {
		if strings.TrimSpace(a.Key) == "" {
			continue
		}
		cb := a.AsCommand()
		out = append(out, keyedEntry{section: cb.Section(), key: cb.BareKey(), action: a.ActionName(), desc: a.DisplayLabel(), report: SectionAction})
	}
	return out
}
