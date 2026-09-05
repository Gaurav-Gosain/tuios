package input

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// This file answers one question the rest of the package cannot: does pressing
// the key a binding ships with reach the action the binding names?
//
// Every other test here enters below the router. It arms a state, calls the
// handler for that state, and checks what the handler did. That proves the
// handler works. It says nothing about whether any key reaches the handler, so
// a feature can be registered, bound, documented and completely dead with the
// package green. That has happened, more than once.
//
// So this table starts where the user does: HandleKeyPress, with the keys the
// default config binds. It asserts one thing and refuses to assert more, on
// purpose. It does not check what an action does. It checks that the action
// ran, through OS.NoteAction, the record every dispatch already keeps for a
// crash report. What an action does belongs to the tests that own it.

// bindingSection is one keybinding table plus the way the app makes it live: a
// mode, an optional leader chord, and a fixture with the state the section
// needs. Everything else about it comes from the default config.
type bindingSection struct {
	name string
	// binds is the section's table, read from the default config.
	binds map[string][]string
	// gate is the prefix_mode action whose key opens this section. "" when the
	// section answers without a chord.
	gate string
	// leader says whether the leader key comes first.
	leader bool
	// modes are the modes the app makes this section live in.
	modes []app.Mode
	// newOS builds a fixture with the state the section needs.
	newOS func(*testing.T) *app.OS
}

// reachOS is the plain fixture: the default config, a viewport and one pane.
func reachOS(t *testing.T) *app.OS {
	t.Helper()
	o := osWithBindings(t, func(*config.KeybindingsConfig) {})
	o.Width, o.Height = 120, 40
	o.EffectiveWidth, o.EffectiveHeight = 120, 40
	o.CurrentWorkspace = 1
	o.Windows = append(o.Windows, &terminal.Window{
		ID: "reach", X: 0, Y: 0, Width: 120, Height: 39, Workspace: o.CurrentWorkspace,
	})
	o.FocusedWindow = 0
	return o
}

// reachScriptOS is reachOS with a tape playing, which is the only state the
// script section answers in.
func reachScriptOS(t *testing.T) *app.OS {
	t.Helper()
	o := reachOS(t)
	o.ScriptMode = true
	return o
}

// reachFilesOS gives the rail the keyboard with the cursor on a file row, which
// is the only state the files section answers in.
func reachFilesOS(t *testing.T) *app.OS {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	return filesRailOS(t, dir, "report.txt")
}

// reachSections builds the table from the default config. Nothing here names a
// key or an action: a binding added to any of these sections is covered the
// moment it is added, and a binding removed stops being asserted. The only
// hand-written facts are the mode and the chord each section is reached
// through, which is what this file exists to pin.
func reachSections(t *testing.T) []bindingSection {
	t.Helper()
	k := config.DefaultConfig().Keybindings
	bothModes := []app.Mode{app.WindowManagementMode, app.TerminalMode}
	windowMode := []app.Mode{app.WindowManagementMode}

	// The main sections are flattened into one lookup and answered in window
	// mode, with no chord in front of them.
	main := func(name string, binds map[string][]string) bindingSection {
		return bindingSection{name: name, binds: binds, modes: windowMode, newOS: reachOS}
	}
	// A sub-prefix section is reached by the leader key and then the key bound
	// to the prefix_mode action that opens it. Both modes: the routing is
	// written twice, once in HandleKeyPress and once in HandleTerminalModeKey,
	// and a cut in either one is a dead chord for half the app.
	sub := func(name, gate string, binds map[string][]string) bindingSection {
		return bindingSection{name: name, binds: binds, gate: gate, leader: true, modes: bothModes, newOS: reachOS}
	}

	return []bindingSection{
		main("window_management", k.WindowManagement),
		main("workspaces", k.Workspaces),
		main("layout", k.Layout),
		main("mode_control", k.ModeControl),
		main("system", k.System),
		main("navigation", k.Navigation),
		main("restore_minimized", k.RestoreMinimized),

		{name: "prefix_mode", binds: k.PrefixMode, leader: true, modes: bothModes, newOS: reachOS},
		sub("window_prefix", "prefix_window", k.WindowPrefix),
		sub("minimize_prefix", "prefix_minimize", k.MinimizePrefix),
		sub("workspace_prefix", "prefix_workspace", k.WorkspacePrefix),
		sub("debug_prefix", "prefix_debug", k.DebugPrefix),
		sub("tape_prefix", "prefix_tape", k.TapePrefix),
		sub("layout_prefix", "prefix_layout", k.LayoutPrefix),

		// The terminal-mode section is the binds that answer while typing into a
		// shell. Two of them (the scroll pair) and the host paste are answered
		// inside HandleTerminalModeKey rather than by the dispatcher, so they
		// are only live there.
		{name: "terminal_mode", binds: k.TerminalMode, modes: []app.Mode{app.TerminalMode}, newOS: reachOS},
		// The global scope answers in both modes, which is the whole reason it
		// is a section of its own.
		{name: "global", binds: k.Global, modes: bothModes, newOS: reachOS},
		// The script scope is live only while a tape plays back.
		{name: "script", binds: k.Script, modes: bothModes, newOS: reachScriptOS},
		// The rail owns the keyboard in either mode while it has focus.
		{name: "sidebar", binds: k.Sidebar, modes: bothModes, newOS: func(t *testing.T) *app.OS {
			t.Helper()
			return railOS(t)
		}},
		// The files section is read first, and only while the cursor is on a row
		// of the listing.
		{name: "sidebar_files", binds: k.SidebarFiles, modes: windowMode, newOS: reachFilesOS},
	}
}

// TestEveryDefaultBindingReachesItsAction presses every key the default config
// binds, through the real entry point, and asserts the action ran.
//
// The failure it exists for is a break between the key and the handler: a route
// that was never wired, a switch case nobody added, a chord disconnected by an
// edit somewhere above it. None of those change a handler, so no test of a
// handler can see them, and the eight negative controls that mutate a handler
// all pass while the feature is dead.
func TestEveryDefaultBindingReachesItsAction(t *testing.T) {
	k := config.DefaultConfig().Keybindings
	for _, sec := range reachSections(t) {
		for _, mode := range sec.modes {
			for _, action := range sortedActions(sec.binds) {
				for _, key := range sec.binds[action] {
					name := sec.name + "/" + modeName(mode) + "/" + action + "/" + key
					t.Run(name, func(t *testing.T) {
						o := sec.newOS(t)
						o.Mode = mode
						if sec.leader {
							o = pressKey(t, o, k.LeaderKey)
						}
						if sec.gate != "" {
							o = pressKey(t, o, gateKey(t, k, sec.gate))
						}
						o = pressKey(t, o, key)
						got := o.RecentActions()
						if len(got) == 0 || got[len(got)-1] != action {
							t.Errorf("pressing %s did not reach %q (actions run: %v)",
								describeChord(k, sec, key), action, got)
						}
					})
				}
			}
		}
	}
}

// TestReachabilityTableCoversEveryBindingSection stops the table from going
// stale in the one way it can: a new section added to the config that nobody
// adds a row for here. The config struct is the list; this compares against it.
func TestReachabilityTableCoversEveryBindingSection(t *testing.T) {
	covered := map[string]bool{}
	for _, sec := range reachSections(t) {
		covered[sec.name] = true
	}
	typ := reflect.TypeOf(config.KeybindingsConfig{})
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.Map {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		if name == "" {
			t.Errorf("field %s has no toml name, so no section can be matched to it", f.Name)
			continue
		}
		if !covered[name] {
			t.Errorf("config section %q is bound by nothing in the reachability table: add a row to reachSections", name)
		}
		delete(covered, name)
	}
	for name := range covered {
		t.Errorf("the reachability table names section %q, which the config does not have", name)
	}
}

// actionsWithNoDefaultBinding is every action the dispatcher registers that the
// default config binds no key to, and the surface that reaches it instead.
//
// The list is asserted in both directions, so it cannot grow in silence. An
// action registered with no key and no note here fails; an action here that
// later gets a default key fails too, and moves into the table above.
var actionsWithNoDefaultBinding = map[string]string{
	// Context menu rows. The menu hands them to the same dispatcher.
	"clear_selection":       "context menu row",
	"kill_session":          "context menu row",
	"kill_session_next":     "context menu row",
	"kill_session_quit":     "context menu row",
	"paste_clipboard":       "context menu row",
	"rename_session":        "context menu row",
	"screenshot_window":     "context menu row",
	"set_accent":            "context menu row",
	"set_session_accent":    "context menu row",
	"workspace_pill_switch": "context menu row",
	"settings_sidebar":      "rail mouse row",

	// Debug and tape surfaces reached from their own prefix, whose actions are
	// separate names. These are the bodies both share.
	"toggle_logs":         "shared body, reached as debug_prefix_logs",
	"toggle_cache_stats":  "shared body, reached as debug_prefix_cache",
	"toggle_tape_manager": "shared body, reached as tape_prefix_manager",
	"stop_recording":      "shared body, reached as tape_prefix_stop",

	// Sizes the layout chord does not ship a key for. 50 to 90 are bound; the
	// small half is there for a user who wants it.
	"resize_width_10":  "user binding",
	"resize_width_20":  "user binding",
	"resize_width_30":  "user binding",
	"resize_width_40":  "user binding",
	"resize_height_10": "user binding",
	"resize_height_20": "user binding",
	"resize_height_30": "user binding",
	"resize_height_40": "user binding",
	// "," is open_settings, so this half of the master resize ships unbound.
	"resize_master_shrink_left": "user binding",

	// The scrolling layout's own verbs. No default key, no menu row.
	"scroll_focus_left":  "user binding",
	"scroll_focus_right": "user binding",
	"scroll_move_left":   "user binding",
	"scroll_move_right":  "user binding",
	"scroll_cycle_width": "user binding",
	"scroll_consume":     "user binding",
	"scroll_expel":       "user binding",

	// BSP has one split it does not ship a key for.
	"smart_split": "user binding",

	// Two of the three screenshot verbs. The picker is the one with a key.
	"screenshot":        "run-command verb, user binding",
	"screenshot_screen": "run-command verb, user binding",
}

// TestEveryRegisteredActionIsBoundOrListed pairs with the table above. Between
// them every registered action is either pressed through the real entry point
// or named here with the surface that reaches it. Neither list can grow by
// accident.
func TestEveryRegisteredActionIsBoundOrListed(t *testing.T) {
	bound := map[string]bool{}
	for _, sec := range reachSections(t) {
		for action := range sec.binds {
			bound[action] = true
		}
	}
	for action := range GetDispatcher().handlers {
		if bound[action] {
			if _, listed := actionsWithNoDefaultBinding[action]; listed {
				t.Errorf("%q has a default binding now: drop it from actionsWithNoDefaultBinding", action)
			}
			continue
		}
		if _, listed := actionsWithNoDefaultBinding[action]; !listed {
			t.Errorf("%q is registered, has no default key, and is not listed in "+
				"actionsWithNoDefaultBinding: say which surface reaches it, or bind it", action)
		}
	}
	for action := range actionsWithNoDefaultBinding {
		if !GetDispatcher().HasAction(action) {
			t.Errorf("actionsWithNoDefaultBinding names %q, which nothing registers", action)
		}
	}
}

// pressKey sends one binding spelling through the real entry point.
func pressKey(t *testing.T, o *app.OS, spec string) *app.OS {
	t.Helper()
	next, _ := HandleKeyPress(keyEventFor(t, spec), o)
	return next
}

// keyEventFor builds the key event a terminal sends for a binding spelling, and
// proves it: the event has to spell the binding back. A spelling this cannot
// build fails the test instead of being skipped, because a row the table cannot
// press is a row the table is not covering.
func keyEventFor(t *testing.T, spec string) tea.KeyPressMsg {
	t.Helper()
	var mod tea.KeyMod
	base := spec
	for {
		name, rest, ok := strings.Cut(base, "+")
		if !ok || rest == "" {
			break
		}
		bit, isMod := bindingMods[name]
		if !isMod {
			break
		}
		mod |= bit
		base = rest
	}
	msg := tea.KeyPressMsg{Mod: mod}
	if code, ok := namedBindingKeys[base]; ok {
		msg.Code = code
	} else {
		runes := []rune(base)
		if len(runes) != 1 {
			t.Fatalf("binding %q names a key this test cannot build: add %q to namedBindingKeys", spec, base)
		}
		msg.Code = runes[0]
		if mod == 0 {
			// An unmodified printable key arrives with the text it produced,
			// which is the spelling a binding on "|" or "R" matches.
			msg.Text = base
		}
	}
	if msg.String() != spec && msg.Keystroke() != spec {
		// Either the binding is spelled a way no key event spells itself, in
		// which case the binding is dead and the config is what to fix, or this
		// builder is missing a key name. Both are failures, and neither may be
		// skipped: a row the table cannot press is a row it is not covering.
		t.Fatalf("no key event spells %q (the nearest event spells itself %q): "+
			"either the binding is dead, or namedBindingKeys is missing a key",
			spec, msg.Keystroke())
	}
	return msg
}

var bindingMods = map[string]tea.KeyMod{
	"ctrl":  tea.ModCtrl,
	"alt":   tea.ModAlt,
	"shift": tea.ModShift,
	"meta":  tea.ModMeta,
	"hyper": tea.ModHyper,
	"super": tea.ModSuper,
}

var namedBindingKeys = map[string]rune{
	"esc":       tea.KeyEscape,
	"enter":     tea.KeyEnter,
	"tab":       tea.KeyTab,
	"space":     tea.KeySpace,
	"backspace": tea.KeyBackspace,
	"delete":    tea.KeyDelete,
	"insert":    tea.KeyInsert,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"left":      tea.KeyLeft,
	"right":     tea.KeyRight,
	"home":      tea.KeyHome,
	"end":       tea.KeyEnd,
	"pgup":      tea.KeyPgUp,
	"pgdown":    tea.KeyPgDown,
	"f1":        tea.KeyF1,
	"f2":        tea.KeyF2,
	"f3":        tea.KeyF3,
	"f4":        tea.KeyF4,
	"f5":        tea.KeyF5,
	"f6":        tea.KeyF6,
	"f7":        tea.KeyF7,
	"f8":        tea.KeyF8,
	"f9":        tea.KeyF9,
	"f10":       tea.KeyF10,
	"f11":       tea.KeyF11,
	"f12":       tea.KeyF12,
}

// gateKey is the key that opens a sub-prefix, read from the prefix_mode table
// so a rebind of the chord moves the whole section with it.
func gateKey(t *testing.T, k config.KeybindingsConfig, gate string) string {
	t.Helper()
	keys := k.PrefixMode[gate]
	if len(keys) == 0 {
		t.Fatalf("the prefix_mode section binds no key to %q, so its whole sub-section is unreachable", gate)
	}
	return keys[0]
}

func describeChord(k config.KeybindingsConfig, sec bindingSection, key string) string {
	parts := make([]string, 0, 3)
	if sec.leader {
		parts = append(parts, k.LeaderKey)
	}
	if sec.gate != "" {
		parts = append(parts, k.PrefixMode[sec.gate][0])
	}
	return strings.Join(append(parts, key), " ")
}

func sortedActions(binds map[string][]string) []string {
	out := make([]string, 0, len(binds))
	for a := range binds {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func modeName(m app.Mode) string {
	if m == app.TerminalMode {
		return "terminal"
	}
	return "window"
}

// TestEveryDescribedActionIsHandled holds the description catalogue to the
// code that runs actions. A description for an action nothing handles is a row
// in help, which-key and the keybind manager that names something no key can
// run: toggle_showkeys and prefix_logs sat there while the live actions were
// debug_prefix_showkeys and debug_prefix_logs.
//
// Both sides come from the code. The catalogue is config.ActionDescriptions.
// The handled set is the dispatcher's table plus every action this package
// names by string in a handler that takes it by section instead: the terminal
// mode's own keys, the script keys, the global binds. The hold layer's held
// key is read through its constant.
func TestEveryDescribedActionIsHandled(t *testing.T) {
	handled := map[string]bool{app.HoldModeAction: true}
	d := GetDispatcher()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil {
					handled[v] = true
				}
			}
			return true
		})
	}
	for action := range config.ActionDescriptions {
		if !d.HasAction(action) && !handled[action] {
			t.Errorf("ActionDescriptions names %q, which nothing in this package handles; the row describes an action no key can run", action)
		}
	}
}
