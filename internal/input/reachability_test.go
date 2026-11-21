package input

import (
	"encoding/json"
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
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

type bindingSection struct {
	name   string
	binds  map[string][]string
	gate   string
	leader bool
	modes  []app.Mode
	newOS  func(*testing.T) *app.OS
}

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

func reachScriptOS(t *testing.T) *app.OS {
	t.Helper()
	o := reachOS(t)
	o.ScriptMode = true
	return o
}

func reachFilesOS(t *testing.T) *app.OS {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	return filesRailOS(t, dir, "report.txt")
}

func reachSections(t *testing.T) []bindingSection {
	t.Helper()
	k := config.DefaultConfig().Keybindings
	bothModes := []app.Mode{app.WindowManagementMode, app.TerminalMode}
	windowMode := []app.Mode{app.WindowManagementMode}

	main := func(name string, binds map[string][]string) bindingSection {
		return bindingSection{name: name, binds: binds, modes: windowMode, newOS: reachOS}
	}
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
		sub("tuios_prefix", "prefix_tuios", k.TuiosPrefix),

		{name: "terminal_mode", binds: k.TerminalMode, modes: []app.Mode{app.TerminalMode}, newOS: reachOS},
		{name: "global", binds: k.Global, modes: bothModes, newOS: reachOS},
		{name: "script", binds: k.Script, modes: bothModes, newOS: reachScriptOS},
		{name: "sidebar", binds: k.Sidebar, modes: bothModes, newOS: func(t *testing.T) *app.OS {
			t.Helper()
			return railOS(t)
		}},
		{name: "sidebar_files", binds: k.SidebarFiles, modes: windowMode, newOS: reachFilesOS},
		{name: "sidebar_agents", binds: k.SidebarAgents, modes: windowMode, newOS: reachAgentsOS},
		{name: "quick_look", binds: k.QuickLook, modes: windowMode, newOS: reachQuickLookOS},
		{name: "quick_look_inside", binds: k.QuickLookInside, modes: windowMode, newOS: reachQuickLookInsideOS},
		{name: "inbox", binds: k.Inbox, modes: bothModes, newOS: reachInboxOS},
		{name: "inbox_peek", binds: k.InboxPeek, modes: bothModes, newOS: reachInboxPeekOS},
		{name: "mail", binds: k.Mail, modes: bothModes, newOS: reachMailOS},
		{name: "transfers", binds: k.Transfers, modes: bothModes, newOS: reachTransfersOS},
		{name: "copy_mode", binds: k.CopyMode, modes: bothModes, newOS: reachCopyModeOS},
		{name: "review", binds: k.Review, modes: bothModes, newOS: reachReviewOS},
	}
}

func reachQuickLookOS(t *testing.T) *app.OS {
	t.Helper()
	o := reachFilesOS(t)
	if !o.QuickLookOpenTextForTest("one\ntwo\n") {
		t.Fatal("the fixture's cursor is not on a file row")
	}
	return o
}

func reachQuickLookInsideOS(t *testing.T) *app.OS {
	t.Helper()
	o := reachQuickLookOS(t)
	if !o.QuickLookGoInside() {
		t.Fatal("the fixture's text cannot be moved through")
	}
	return o
}

func reachReviewOS(t *testing.T) *app.OS {
	t.Helper()
	o, _, _ := reviewInputOS(t)
	k := config.DefaultConfig().Keybindings
	o = pressRun(t, o, k.LeaderKey)
	o = pressRun(t, o, "v")
	if !o.ReviewOpen() {
		t.Fatal("ctrl+b v did not open the review on the test pane")
	}
	return o
}

func reachCopyModeOS(t *testing.T) *app.OS {
	t.Helper()
	o, _ := osWithFocusedPane(t, config.DefaultConfig(), app.WindowManagementMode)
	w := o.GetFocusedWindow()
	w.EnterCopyMode()
	if !w.InCopyMode() {
		t.Fatal("copy mode did not start on the test pane")
	}
	return o
}

func reachInboxOS(t *testing.T) *app.OS {
	t.Helper()
	o := inboxInputOS(t)
	o.OpenInbox("")
	return o
}

func reachInboxPeekOS(t *testing.T) *app.OS {
	t.Helper()
	o := inboxInputOS(t)
	o.SetInboxVerbCaller(func(verb string, _ map[string]any, _ time.Duration) (json.RawMessage, error) {
		return json.Marshal(session.PromptPeek{
			Session: "local", Window: "b", State: "needs_input", Blocked: true, Found: true, Answerable: true,
			PromptID: "p1", Actions: []string{"approve", "approve_always", "deny", "text"},
		})
	}, func() string { return "n" })
	o.OpenInbox("")
	cmd := o.InboxPeek()
	if !o.InboxPeeking() || cmd == nil {
		t.Fatal("the fixture's approval did not open a peek")
	}
	o.Update(cmd())
	return o
}

func reachTransfersOS(t *testing.T) *app.OS {
	t.Helper()
	o := reachOS(t)
	o.OpenTransfers()
	return o
}

func reachMailOS(t *testing.T) *app.OS {
	t.Helper()
	o := mailThreadOS(t)
	o.OpenAgentMail()
	o.AgentMail.Loading = false
	return o
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
						if unit, pending := pendingActions[action]; pending {
							t.Skipf("%s is bound ahead of its work (%s)", action, unit)
						}
						o := sec.newOS(t)
						o.Mode = mode
						if sec.leader {
							o = pressKey(t, o, k.LeaderKey)
						}
						if sec.gate != "" {
							o = pressKey(t, o, gateKey(t, k, sec.gate))
						}
						o = pressBinding(t, o, key)
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

var pendingActions = map[string]string{
	config.ActionInboxDenyReason:    "safer approvals",
	config.ActionInboxDetailDown:    "safer approvals",
	config.ActionInboxDetailUp:      "safer approvals",
	config.ActionPrefixNextFinished: "Inbox lifecycle, before any agent",
}

// TestPendingActionsStillDoNothing presses every key of a pending action the
// way the table above would, and asserts that the action is not recorded and
// that the key did what it did before it was bound: nothing in the Inbox, the
// rail's own binding on an agent row, and after the leader key in terminal
// mode, the key typed into the focused pane with no repeat window left open.
func TestPendingActionsStillDoNothing(t *testing.T) {
	k := config.DefaultConfig().Keybindings
	for _, sec := range reachSections(t) {
		for _, action := range sortedActions(sec.binds) {
			if _, pending := pendingActions[action]; !pending {
				continue
			}
			for _, key := range sec.binds[action] {
				t.Run(sec.name+"/"+action+"/"+key, func(t *testing.T) {
					o := sec.newOS(t)
					o.Mode = app.WindowManagementMode
					if sec.leader {
						o.Mode = app.TerminalMode
					}
					var typed []byte
					if w := o.GetFocusedWindow(); w != nil {
						w.DaemonMode = true
						w.DaemonWriteFunc = func(b []byte) error { typed = append(typed, b...); return nil }
					}
					before := len(o.RecentActions())
					if sec.leader {
						o = pressKey(t, o, k.LeaderKey)
					}
					if sec.gate != "" {
						o = pressKey(t, o, gateKey(t, k, sec.gate))
					}
					o = pressKey(t, o, key)
					if sec.leader && sec.gate == "" {
						if string(typed) != key {
							t.Errorf("%s in terminal mode typed %q into the pane, want %q as before it was bound", describeChord(k, sec, key), typed, key)
						}
						if o.PrefixRepeatLive() {
							t.Errorf("%s left the repeat window open although it did nothing", describeChord(k, sec, key))
						}
						o = pressKey(t, o, key)
						if string(typed) != key+key {
							t.Errorf("a second %s after %s typed %q into the pane, want %q", key, describeChord(k, sec, key), typed, key+key)
						}
					}
					got := o.RecentActions()
					for _, a := range got[before:] {
						if a == action {
							t.Fatalf("%s ran on %s: it is built now, so take it out of pendingActions", action, key)
						}
					}
					if sec.name == "sidebar_agents" {
						if rail := o.KeybindRegistry.Action(config.TableSidebar, key); rail != "" && (len(got) == before || got[len(got)-1] != rail) {
							t.Errorf("%s on an agent row did not fall through to the rail's %s (actions run: %v)", key, rail, got[before:])
						}
					}
				})
			}
		}
	}
	bound := map[string]bool{}
	for _, sec := range reachSections(t) {
		for action := range sec.binds {
			bound[action] = true
		}
	}
	for action := range pendingActions {
		if !bound[action] {
			t.Errorf("pendingActions names %q, which no section binds", action)
		}
	}
}

func reachAgentsOS(t *testing.T) *app.OS {
	t.Helper()
	o := railOS(t)
	o.Windows[0].AgentState = "done"
	o.Windows[0].AgentHarness = "claude-code"
	o.Windows[0].AgentQueued = 1
	o.Windows[0].AgentSubagents = 1
	o.Windows[0].AgentSubagentList = []sessiontree.Subagent{{
		ID: "sa-1", Type: "Explore", Description: "Look around", State: "running",
		StartedAt: time.Now().UnixNano(),
	}}
	o.Settings.SidebarSubagents.Collapse = config.SidebarSubagentCollapseExpanded
	o.ExitSidebarFocus()
	o.EnterSidebarFocus()
	o.View()
	for range 64 {
		if o.SidebarCursorOnAgent() {
			return o
		}
		o.SidebarCursorMove(1)
		o.View()
	}
	t.Fatal("the rail's cursor never reached the agent row")
	return nil
}

var notTerminalSections = map[string]string{
	"gui":   "the native GUI, through the GUI bridge's keybinds event",
	"lists": "every list overlay, through listKey",
	"menus": "user-declared prefix menus, none ship",
	"modes": "user-declared key modes, none ship",
}

// TestReachabilityTableCoversEveryBindingSection stops the table from going
// stale in the one way it can: a new section added to the config that nobody
// adds a row for here. The config struct is the list; this compares against it.
func TestReachabilityTableCoversEveryBindingSection(t *testing.T) {
	covered := map[string]bool{}
	for _, sec := range reachSections(t) {
		covered[sec.name] = true
	}
	typ := reflect.TypeFor[config.KeybindingsConfig]()
	for f := range typ.Fields() {
		if f.Type.Kind() != reflect.Map {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		if name == "" {
			t.Errorf("field %s has no toml name, so no section can be matched to it", f.Name)
			continue
		}
		if _, gui := notTerminalSections[name]; gui {
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

var actionsWithNoDefaultBinding = map[string]string{
	"clear_selection":         "context menu row",
	"leave_mode":              "esc in a key mode, or a key the user binds",
	"kill_session":            "context menu row",
	"kill_session_next":       "context menu row",
	"kill_session_quit":       "context menu row",
	"restart_daemon":          "command palette row, rail machine menu row, settings row, version notice",
	"use_simple_controls":     "command palette row",
	"use_keyboard_controls":   "command palette row",
	"paste_clipboard":         "context menu row",
	"selection_copy":          "agents view selection menu row",
	"selection_copy_markdown": "agents view selection menu row",
	"selection_search":        "agents view selection menu row",
	"repo_new_worktree":       "rail repo header menu row",
	"repo_toggle_fold":        "rail repo header menu row",
	"session_new_worktree":    "rail session row menu row",
	"rename_session":          "context menu row",
	"screenshot_window":       "context menu row",
	"set_accent":              "context menu row",
	"set_session_accent":      "context menu row",
	"workspace_pill_switch":   "context menu row",
	"settings_sidebar":        "rail mouse row",
	"rename_workspace":        "user binding",

	"split_ssh_horizontal": "palette row and user binding",
	"split_ssh_vertical":   "palette row and user binding",
	"new_window_ssh":       "palette row and user binding",

	"toggle_logs":        "shared body, reached as debug_prefix_logs",
	"toggle_cache_stats": "shared body, reached as debug_prefix_cache",
	"stop_recording":     "shared body, reached as tape_prefix_stop",

	"resize_width_10":  "user binding",
	"resize_width_20":  "user binding",
	"resize_width_30":  "user binding",
	"resize_width_40":  "user binding",
	"resize_height_10": "user binding",
	"resize_height_20": "user binding",
	"resize_height_30": "user binding",
	"resize_height_40": "user binding",

	"mark_pane":            "user binding",
	"join_marked_left":     "user binding",
	"join_marked_right":    "user binding",
	"join_marked_up":       "user binding",
	"join_marked_down":     "user binding",
	"resize_pane_left_5":   "user binding",
	"resize_pane_right_5":  "user binding",
	"resize_pane_up_5":     "user binding",
	"resize_pane_down_5":   "user binding",
	"resize_pane_left_20":  "user binding",
	"resize_pane_right_20": "user binding",
	"resize_pane_up_20":    "user binding",
	"resize_pane_down_20":  "user binding",
	"new_workspace":        "user binding",
	"last_workspace":       "user binding",
	"prefix_tuios":         "keybind preset",
	"break_pane":           "user binding",
	"last_session":         "user binding",
	"send_prefix":          "user binding",
	"move_workspace_left":  "user binding",
	"move_workspace_right": "user binding",
	"reload_config":        "user binding",
	"toggle_mouse":         "user binding",
	"cycle_layout":         "user binding",

	"toggle_multifocus_active":  "palette row, user binding",
	"toggle_multifocus_all":     "palette row, user binding",
	"close_workspace":           "palette row, workspace tab menu, user binding",
	"copy_mode_search_forward":  "user binding and palette row",
	"copy_mode_search_backward": "user binding and palette row",
	"clear_scrollback":          "user binding and palette row",
	"hints_all_panes":           "user binding and palette row",
	"resize_master_shrink_left": "user binding",

	"scroll_focus_left":  "user binding",
	"scroll_focus_right": "user binding",
	"scroll_move_left":   "user binding",
	"scroll_move_right":  "user binding",
	"next_workspace":     "palette row, user binding",
	"prev_workspace":     "palette row, user binding",
	"next_host":          "palette row, user binding",
	"prev_host":          "palette row, user binding",
	"switch_session_1":   "user binding",
	"switch_session_2":   "user binding",
	"switch_session_3":   "user binding",
	"switch_session_4":   "user binding",
	"switch_session_5":   "user binding",
	"switch_session_6":   "user binding",
	"switch_session_7":   "user binding",
	"switch_session_8":   "user binding",
	"switch_session_9":   "user binding",
	"scroll_cycle_width": "user binding",
	"scroll_consume":     "user binding",
	"scroll_expel":       "user binding",
	"scroll_maximize":    "user binding",

	"smart_split": "user binding",

	"cycle_tiling_scheme":            "palette row, user binding",
	"set_tiling_scheme_spiral":       "palette row, user binding",
	"set_tiling_scheme_longest_side": "palette row, user binding",
	"set_tiling_scheme_alternate":    "palette row, user binding",
	"set_tiling_scheme_smart_split":  "palette row, user binding",

	"set_master_position_left":   "palette row, user binding",
	"set_master_position_right":  "palette row, user binding",
	"set_master_position_top":    "palette row, user binding",
	"set_master_position_bottom": "palette row, user binding",
	"set_master_position_center": "palette row, user binding",

	"new_window_on_host":         "palette row, user binding",
	"toggle_shared_borders":      "palette row, user binding",
	"next_saved_layout":          "palette row, user binding",
	"prev_saved_layout":          "palette row, user binding",
	"clear_multifocus":           "palette row, user binding",
	"layout_bsp":                 "palette row, user binding",
	"layout_master_stack":        "palette row, user binding",
	"layout_scrolling":           "palette row, user binding",
	"layout_off":                 "palette row, user binding",
	"all_windows":                "palette row, user binding",
	"theme_picker":               "palette row, user binding",
	"look_picker":                "palette row, user binding",
	"share_look":                 "palette row, user binding",
	"edit_theme":                 "palette row, user binding",
	"toggle_focus_follows_mouse": "palette row, user binding",
	"add_machine":                "palette row, user binding",
	"open_mailbox":               "palette row, user binding",
	"new_agent":                  "palette row, Rex's Start an Agent, user binding",
	"dispatch_pause":             "palette row, user binding",
	"dispatch_resume":            "palette row, user binding",
	"multi_copy_mode":            "palette row, user binding",

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
	for _, action := range dispatchActions() {
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

func pressKey(t *testing.T, o *app.OS, spec string) *app.OS {
	t.Helper()
	next, _ := HandleKeyPress(keyEventFor(t, spec), o)
	return next
}

func pressBinding(t *testing.T, o *app.OS, spec string) *app.OS {
	t.Helper()
	base := spec
	var mod tea.KeyMod
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
	if base != "middle" {
		return pressKey(t, o, spec)
	}
	next, _ := HandleInput(tea.MouseClickMsg{
		X: 10, Y: 10, Button: tea.MouseMiddle, Mod: mod,
	}, o)
	got, ok := next.(*app.OS)
	if !ok {
		t.Fatalf("middle binding returned %T, want *app.OS", next)
	}
	return got
}

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
			msg.Text = base
		}
	}
	canon := spec
	for _, name := range []string{"opt+", "option+"} {
		canon = strings.ReplaceAll(canon, name, "alt+")
	}
	if msg.String() != canon && msg.Keystroke() != canon {
		t.Fatalf("no key event spells %q (the nearest event spells itself %q): "+
			"either the binding is dead, or namedBindingKeys is missing a key",
			canon, msg.Keystroke())
	}
	return msg
}

var bindingMods = map[string]tea.KeyMod{
	"ctrl":   tea.ModCtrl,
	"alt":    tea.ModAlt,
	"opt":    tea.ModAlt,
	"option": tea.ModAlt,
	"shift":  tea.ModShift,
	"meta":   tea.ModMeta,
	"hyper":  tea.ModHyper,
	"super":  tea.ModSuper,
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

// TestTheDispatcherHandlesEveryAction holds the dispatcher to the action
// table: every entry the dispatcher runs (config.RunDispatch) has a handler.
// The other way round, a handler bound under a name the table does not hold,
// panics when the dispatcher is built.
func TestTheDispatcherHandlesEveryAction(t *testing.T) {
	if missing := GetDispatcher().handlers.Missing(); len(missing) > 0 {
		t.Errorf("the action table says the dispatcher runs these, and it has no handler for them: %v", missing)
	}
}

// TestEveryDescribedActionIsHandled holds the actions a view runs to the code
// that runs them. A description for an action nothing handles is a row in
// help, which-key and the keybind manager that names something no key can
// run: toggle_showkeys and prefix_logs sat there while the live actions were
// debug_prefix_showkeys and debug_prefix_logs. The dispatcher's own actions
// are held by TestTheDispatcherHandlesEveryAction.
//
// Both sides come from the code. The catalogue is config.ActionDescriptions.
// The handled set is every action this package
// names by string in a handler that takes it by section instead: the terminal
// mode's own keys, the script keys, the global binds. The hold layer's held
// key is read through its constant. The Inbox, its peek and the mailbox name
// their actions through config constants rather than strings, so their
// sections count as handled through the reachability table, whose test
// presses every key of them and sees its action run.
func TestEveryDescribedActionIsHandled(t *testing.T) {
	handled := map[string]bool{app.HoldModeAction: true, config.ActionCopyModeFirstNonBlank: true}
	k := config.DefaultConfig().Keybindings
	for _, section := range []map[string][]string{k.Inbox, k.InboxPeek, k.Mail, k.SidebarAgents, k.CopyMode, k.Transfers, k.Lists, k.Review} {
		for action := range section {
			handled[action] = true
		}
	}
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
		if spec, _ := config.LookupAction(action); spec.Run == config.RunDispatch {
			continue
		}
		if !handled[action] {
			t.Errorf("ActionDescriptions names %q, which nothing in this package handles; the row describes an action no key can run", action)
		}
	}
}

func dispatchActions() []string {
	var names []string
	for _, spec := range config.Actions() {
		if spec.Run != config.RunDispatch {
			continue
		}
		if spec.Count == 0 {
			names = append(names, spec.Name)
			continue
		}
		for n := 1; n <= int(spec.Count); n++ {
			names = append(names, spec.Name+strconv.Itoa(n))
		}
	}
	return names
}
