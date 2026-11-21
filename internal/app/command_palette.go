package app

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

const paletteCategoryAgents = "Agents"

const paletteReviewName = "Agents: Review changes of the focused pane"

// ConfigReloadedMsg carries a config parsed by the file watcher goroutine so it
// can be applied on the Bubble Tea goroutine. The watcher must not touch the
// appearance globals directly (the render loop reads them concurrently); it
// delivers this message via the program's Send instead, and Update applies it
// with ApplyReloadedConfig.
type ConfigReloadedMsg struct {
	Config *config.UserConfig
}

// ConfigReloadFailedMsg says the config file on disk cannot be used. The
// running config stands, and the client says so on screen.
//
// The watcher used to write this to the log and stop there, so a typo in the
// file meant every later save was ignored for a reason nobody could see.
type ConfigReloadFailedMsg struct {
	Err error
}

// ApplyReloadedConfig puts a config read from disk into force. It is the one
// path for both ways in: the file watcher and the palette's "Reload config".
//
// The file wins. Everything the file names replaces what is running, including
// a beam somebody switched on with a key, because that is what the sidebar and
// the pane gap on this same path already do and two rules would be worse than
// one. What the file does not name is untouched: the focused pane, the mode,
// where the beam is pointing, and whether this session may write the file at
// all.
//
// A config that arrives this way is not the person's word: a process in a
// pane can write the file. So a new or changed hook in it waits (see
// hook_reload.go). ReloadConfigAsked is the way in that can turn one on.
func (m *OS) ApplyReloadedConfig(cfg *config.UserConfig) tea.Cmd {
	return m.applyReloadedConfig(cfg, false)
}

func (m *OS) applyReloadedConfig(cfg *config.UserConfig, byPerson bool) tea.Cmd {
	if cfg == nil {
		return nil
	}
	if m.ReloadFilter != nil {
		cfg = m.ReloadFilter(cfg)
	}
	cfg.KeepHolds(m.UserConfig)
	m.reloadHooks(cfg, byPerson)
	themeBefore := theme.Generation()
	config.ApplyAppearanceConfig(m.overrides.Config(cfg), &m.Settings)
	m.ListKeys = config.ListKeymap(cfg)
	if theme.Generation() != themeBefore {
		m.UpdateAllWindowThemes()
	}
	m.detectDisplayRate(true)
	m.keepSessionSidebar(false)
	m.applyAppearanceLive(true)
	cmd := m.ReloadDockComponents(cfg)
	m.pruneOrphanScratches()
	if m.KeybindRegistry != nil {
		m.KeybindRegistry.Reload(cfg)
		m.keybinds.report = m.buildKeybindReport()
		m.keybinds.filtered = nil
	}
	m.PruneKeyModes()
	m.SetSpotlight(cfg.Spotlight.IsEnabled())
	cmd = tea.Batch(cmd, m.applyAgentsSwitch())
	m.MarkAllDirty()
	return cmd
}

// CommandPaletteItem represents a single command in the command palette.
type CommandPaletteItem struct {
	Name string // Display name: "Split horizontal"
	// Shortcut is the row's right-hand meta slot, in muted ink. For a command it
	// is the key that runs it ("prefix+v"); for a row whose consequence is
	// heavier than the palette's usual jump it is that consequence in words
	// ("switches session"), since a row with no key still owes the user a warning.
	Shortcut string
	// Bind names the keybind action the row runs, when it has one. The palette
	// then shows that action's live key in Shortcut (see liveShortcuts), so a
	// key moved or unbound in config.toml does not go on showing its default.
	// A Bind row spells no key in Shortcut: defaultShortcuts fills it from the
	// shipped keymap. Its Shortcut holds only a note, for an action with no
	// default key ("drag to select").
	Bind          string
	shortcutIsKey bool
	Category      string // "Window", "Layout", "Session", "Navigation", "Clipboard"
	// Match holds the byte offsets in Name that the live query matched, filled
	// in by FilterCommandPalette so the renderer can underline them without
	// running the matcher a second time. Nil when nothing was typed, and for a
	// row admitted on its Category alone.
	Match []int
	// AgentState marks a session/window entry whose Name carries an agent-state
	// glyph (sessionPaletteLabel), so the renderer can color the glyph without
	// putting ANSI into Name, which the fuzzy filter matches raw.
	AgentState  string
	promptID    string
	promptOther bool
	promptMark  int8
	// AgentSeen is the unread bit behind a done AgentState: true once the
	// person has looked at the pane, which draws idle's mark (agentMark).
	AgentSeen bool
	// AgentSubagents is how many subagents the pane's agent is running, or
	// for a session the pane its state was rolled up from, which draws a
	// pane at rest as waiting.
	AgentSubagents int
	// Keybind marks a row that names one action, which enter runs and
	// PaletteRebindKey rebinds. Those rows are
	// reached only behind the "#" token and are hidden from every other query,
	// which is what keeps a few hundred of them out of a list of twenty
	// commands. See splitPaletteKeybinds.
	Keybind bool
	// Setting marks a row that opens the settings page on one row. There is
	// one per settings row, so they are left out of the empty palette and
	// ranked after every command, and a query that names a setting still
	// reaches it: "pane background" or "settings: pane background".
	Setting bool
	Action  func(m *OS) (*OS, tea.Cmd)
}

// GetCommandPaletteItems returns all available commands for the command palette.
func GetCommandPaletteItems() []CommandPaletteItem {
	items := []CommandPaletteItem{
		{
			Name:     "Run a program",
			Bind:     "launcher",
			Category: PaletteCategoryRun,
		},

		{
			Name:     "Start the screen saver",
			Bind:     "start_screensaver",
			Category: "View",
		},
		{
			Name:     "Screenshot this window",
			Bind:     "screenshot_window",
			Category: "Window",
		},
		{
			Name:     "Screenshot a region",
			Shortcut: "drag to select",
			Category: "Window",
			Bind:     "screenshot",
		},
		{
			Name:     "Screenshot the screen",
			Category: "Window",
			Bind:     "screenshot_screen",
		},
		{
			Name:     "Paste the clipboard image as a file path",
			Bind:     "paste_image",
			Category: "Clipboard",
		},

		{
			Name:     "New window",
			Bind:     "new_window",
			Category: "Window",
		},
		{
			Name:     "New window that runs the focused pane's ssh",
			Category: "Window",
			Bind:     "new_window_ssh",
		},
		{
			Name:     "New window on another machine",
			Shortcut: "",
			Bind:     "new_window_on_host",
			Category: "Window",
		},
		{
			Name:     "Close window",
			Bind:     "close_window",
			Category: "Window",
		},
		{
			Name:     "Rename window",
			Bind:     "prefix_rename_window",
			Category: "Window",
		},
		{
			Name:     "Toggle zoom",
			Bind:     "prefix_fullscreen",
			Category: "Window",
		},
		{
			Name:     "Minimize window",
			Bind:     "minimize_prefix_focused",
			Category: "Window",
		},
		{
			Name:     "Restore all minimized",
			Bind:     "restore_all",
			Category: "Window",
		},
		{
			Name:     "Focus only: minimize the other windows",
			Bind:     "minimize_prefix_focus_only",
			Category: "Window",
		},

		{
			Name:     "Toggle tiling",
			Bind:     "toggle_tiling",
			Category: "Layout",
		},
		{
			Name:     "Split horizontal",
			Bind:     "split_horizontal",
			Category: "Layout",
		},
		{
			Name:     "Split horizontal, and run the focused pane's ssh",
			Category: "Layout",
			Bind:     "split_ssh_horizontal",
		},
		{
			Name:     "Split vertical",
			Bind:     "split_vertical",
			Category: "Layout",
		},
		{
			Name:     "Split vertical, and run the focused pane's ssh",
			Category: "Layout",
			Bind:     "split_ssh_vertical",
		},
		{
			Name:     "Smart split",
			Category: "Layout",
			Bind:     "smart_split",
		},
		{
			Name:     "Toggle shared borders",
			Bind:     "toggle_shared_borders",
			Category: "Layout",
		},
		{
			Name:     "Rotate split",
			Bind:     "rotate_split",
			Category: "Layout",
		},
		{
			Name:     "Equalize splits",
			Bind:     "equalize_splits",
			Category: "Layout",
		},
		{
			Name:     "Cycle tiling scheme",
			Category: "Layout",
			Bind:     "cycle_tiling_scheme",
		},
		{
			Name:     "Set tiling scheme: spiral",
			Category: "Layout",
			Bind:     "set_tiling_scheme_spiral",
		},
		{
			Name:     "Set tiling scheme: longest side",
			Category: "Layout",
			Bind:     "set_tiling_scheme_longest_side",
		},
		{
			Name:     "Set tiling scheme: alternate",
			Category: "Layout",
			Bind:     "set_tiling_scheme_alternate",
		},
		{
			Name:     "Set tiling scheme: smart split",
			Category: "Layout",
			Bind:     "set_tiling_scheme_smart_split",
		},
		{
			Name:     "Cycle master position",
			Category: "Layout",
			Bind:     "cycle_master_position",
		},
	}
	for _, pos := range config.MasterPositions {
		items = append(items, CommandPaletteItem{
			Name:     "Set master position: " + pos,
			Bind:     "set_master_position_" + pos,
			Category: "Layout",
		})
	}
	items = append(items, []CommandPaletteItem{
		{
			Name:     "Add a master pane",
			Category: "Layout",
			Bind:     "add_master",
		},
		{
			Name:     "Remove a master pane",
			Category: "Layout",
			Bind:     "remove_master",
		},
		{
			Name:     "Swap with master pane",
			Category: "Layout",
			Bind:     "swap_with_master",
		},
		{
			Name:     "Focus master pane",
			Category: "Layout",
			Bind:     "focus_master",
		},
		{
			Name:     "Snap fullscreen",
			Bind:     "snap_fullscreen",
			Category: "Layout",
		},

		{
			Name:     "Save layout",
			Category: "Layout",
			Bind:     "layout_prefix_save",
		},
		{
			Name:     "Load layout",
			Bind:     "layout_prefix_load",
			Category: "Layout",
		},

		{
			Name:     "Next window",
			Bind:     "prefix_next_window",
			Category: "Navigation",
		},
		{
			Name:     "Previous window",
			Bind:     "prefix_prev_window",
			Category: "Navigation",
		},
		{
			Name:     "Workspace 1",
			Bind:     "workspace_prefix_switch_1",
			Category: "Navigation",
		},
		{
			Name:     "Workspace 2",
			Bind:     "workspace_prefix_switch_2",
			Category: "Navigation",
		},
		{
			Name:     "Workspace 3",
			Bind:     "workspace_prefix_switch_3",
			Category: "Navigation",
		},
		{
			Name:     "Workspace 4",
			Bind:     "workspace_prefix_switch_4",
			Category: "Navigation",
		},
		{
			Name:     "Workspace 5",
			Bind:     "workspace_prefix_switch_5",
			Category: "Navigation",
		},
		{
			Name:     "Workspace 6",
			Bind:     "workspace_prefix_switch_6",
			Category: "Navigation",
		},
		{
			Name:     "Workspace 7",
			Bind:     "workspace_prefix_switch_7",
			Category: "Navigation",
		},
		{
			Name:     "Workspace 8",
			Bind:     "workspace_prefix_switch_8",
			Category: "Navigation",
		},
		{
			Name:     "Workspace 9",
			Bind:     "workspace_prefix_switch_9",
			Category: "Navigation",
		},
		{
			Name:     "Next workspace",
			Category: "Navigation",
			Bind:     "next_workspace",
		},
		{
			Name:     "Previous workspace",
			Category: "Navigation",
			Bind:     "prev_workspace",
		},

		{
			Name:     "Next layout",
			Bind:     "next_saved_layout",
			Category: "Layout",
		},
		{
			Name:     "Previous layout",
			Bind:     "prev_saved_layout",
			Category: "Layout",
		},
		{
			Name:     "Toggle multifocus",
			Category: "Window",
			Bind:     "toggle_multifocus_active",
		},
		{
			Name:     "Toggle multifocus on all panes",
			Category: "Window",
			Bind:     "toggle_multifocus_all",
		},
		{
			Name:     "Close workspace",
			Category: "Window",
			Bind:     "close_workspace",
		},
		{
			Name:     "Clear multifocus",
			Bind:     "clear_multifocus",
			Category: "Window",
		},
		{
			Name:     "Layout: BSP tiling",
			Bind:     "layout_bsp",
			Category: "Layout",
		},
		{
			Name:     "Layout: master-stack",
			Bind:     "layout_master_stack",
			Category: "Layout",
		},
		{
			Name:     "Layout: scrolling (niri-style)",
			Bind:     "layout_scrolling",
			Category: "Layout",
		},
		{
			Name:     "Layout: disable tiling",
			Bind:     "layout_off",
			Category: "Layout",
		},
		{
			Name:     "Scroll: cycle column width",
			Category: "Layout",
			Bind:     "scroll_cycle_width",
		},
		{
			Name:     "Scroll: maximize column width",
			Category: "Layout",
			Bind:     "scroll_maximize",
		},
		{
			Name:     "Scroll: move window into the column below",
			Category: "Layout",
			Bind:     "scroll_consume",
		},
		{
			Name:     "Scroll: move window out to its own column",
			Category: "Layout",
			Bind:     "scroll_expel",
		},
		{
			Name:     "Edit the scrollback in the editor",
			Bind:     config.ActionEditScrollback,
			Category: "Window",
		},
		{
			Name:     "Clear the scrollback",
			Bind:     config.ActionClearScrollback,
			Category: "Window",
		},
		{
			Name:     "Toggle floating",
			Category: "Window",
			Bind:     "window_prefix_float",
		},
		{
			Name:     "All windows",
			Bind:     "all_windows",
			Category: "Navigation",
		},
		{
			Name:     "New session",
			Category: "Session",
			Bind:     "new_session",
		},
		{
			Name:     "Settings",
			Bind:     "open_settings",
			Category: "Session",
		},
		{
			Name:     paletteAgentsSettingsName,
			Bind:     "prefix_agents_settings",
			Category: "Session",
		},
		{
			Name:     "Keybind preset",
			Bind:     "open_keybind_presets",
			Category: "Session",
		},
		{
			Name:     "Use simple controls",
			Bind:     "use_simple_controls",
			Category: "Session",
		},
		{
			Name:     "Use keyboard controls",
			Bind:     "use_keyboard_controls",
			Category: "Session",
		},
		{
			Name:     "Theme picker",
			Bind:     "theme_picker",
			Category: "Session",
		},
		{
			Name:     "Choose a look",
			Bind:     "look_picker",
			Category: "Session",
		},
		{
			Name:     "Share my look",
			Bind:     "share_look",
			Category: "Session",
		},
		{
			Name:     "Edit theme",
			Bind:     "edit_theme",
			Category: "Session",
		},
		{
			Name:     "Keybind manager",
			Bind:     "prefix_keybinds",
			Category: PaletteCategoryKeybind,
		},
		{
			Name:     "Rebind a key",
			Shortcut: "or type #close",
			Category: PaletteCategoryKeybind,
			Bind:     "prefix_keybinds",
		},
		{
			Name:     "Unbind a key",
			Shortcut: "or type #close",
			Bind:     "prefix_keybinds",
			Category: PaletteCategoryKeybind,
		},
		{
			Name:     paletteRestartDaemonName,
			Shortcut: "stops programs",
			Bind:     "restart_daemon",
			Category: "Session",
		},
		{
			Name:     "Reload config",
			Category: "Session",
			Bind:     "reload_config",
		},
		{
			Name:     "Toggle sidebar",
			Bind:     "prefix_toggle_sidebar",
			Category: "Session",
		},
		{
			Name:     "Toggle focus follows mouse",
			Bind:     "toggle_focus_follows_mouse",
			Category: "Session",
		},
		{
			Name:     "Switch session",
			Bind:     "prefix_session_switcher",
			Category: "Session",
		},
		{
			Name:     "New task",
			Category: "Session",
			Bind:     "prefix_new_task",
		},
		{
			Name:     "Add a machine",
			Bind:     "add_machine",
			Category: "Session",
		},
		{
			Name:     "Next machine",
			Bind:     "next_host",
			Category: "Session",
		},
		{
			Name:     "Previous machine",
			Bind:     "prev_host",
			Category: "Session",
		},
		{
			Name:     "Agents: open mailbox",
			Bind:     "open_mailbox",
			Category: paletteCategoryAgents,
		},
		{
			Name:     "Agents: what needs you",
			Bind:     "prefix_inbox",
			Category: paletteCategoryAgents,
		},
		{
			Name:     "Agents: start an agent",
			Bind:     "new_agent",
			Category: paletteCategoryAgents,
		},
		{
			Name:     "Agents: pause all task dispatch",
			Bind:     "dispatch_pause",
			Category: paletteCategoryAgents,
		},
		{
			Name:     "Agents: start task dispatch again",
			Bind:     "dispatch_resume",
			Category: paletteCategoryAgents,
		},
		{
			Name:     "Copies between machines",
			Bind:     "prefix_transfers",
			Category: "Files",
		},
		{
			Name:     "Agents: go to the oldest waiting",
			Bind:     "prefix_next_attention",
			Category: paletteCategoryAgents,
		},
		{
			Name:     "Agents: next agent",
			Bind:     "next_agent",
			Category: paletteCategoryAgents,
		},
		{
			Name:     "Agents: previous agent",
			Bind:     "prev_agent",
			Category: paletteCategoryAgents,
		},
		{
			Name:     paletteReviewName,
			Bind:     "prefix_review",
			Category: paletteCategoryAgents,
		},
		{
			Name:     "Agents: mail for you",
			Bind:     "prefix_mail",
			Category: paletteCategoryAgents,
		},
		{
			Name:     "Show help",
			Bind:     "toggle_help",
			Category: "Session",
		},
		{
			Name:     "Show logs",
			Bind:     "debug_prefix_logs",
			Category: "Session",
		},
		{
			Name:     "Toggle scrollback browser",
			Bind:     "prefix_scrollback",
			Category: "Session",
		},
		{
			Name:     "Panes: find a pane in every session",
			Bind:     "choose_tree",
			Category: "Session",
		},
		{
			Name:     "Go to: an agent, session, workspace or pane",
			Bind:     "goto_picker",
			Category: "Session",
		},
		{
			Name:     "Session note: what this session is for or what comes next",
			Bind:     "set_note",
			Category: "Session",
		},
		{
			Name:     "New worktree: a branch and a session in this pane's repository",
			Bind:     "new_worktree",
			Category: "Session",
		},
		{
			Name:     "Hints: label text on the pane to copy it",
			Bind:     "hints",
			Category: "Session",
		},
		{
			Name:     "Calm mode: stop motion and quiet messages",
			Bind:     "toggle_calm",
			Category: "View",
		},
		{
			Name:     "Scratch: show or hide the scratch terminal",
			Bind:     "toggle_scratch",
			Category: "Session",
		},
		{
			Name:     "Path: pick a file or folder and type its path",
			Bind:     "pick_path",
			Category: "Session",
		},
		{
			Name:     "Panes: label the panes and focus one",
			Bind:     "display_panes",
			Category: "Session",
		},
		{
			Name:     "Hints: label text on all panes to copy it",
			Category: "Session",
			Bind:     "hints_all_panes",
		},
		{
			Name:     "Toggle show keys",
			Bind:     "debug_prefix_showkeys",
			Category: "Session",
		},
		{
			Name:     "Picture in picture: pin or unpin the focused pane",
			Bind:     "toggle_pip",
			Category: "Session",
		},
		{
			Name:     "Toggle spotlight",
			Bind:     "toggle_spotlight",
			Category: "Session",
		},
		{
			Name:     "Toggle animations",
			Bind:     "debug_prefix_animations",
			Category: "Session",
		},
		{
			Name:     "Window management mode",
			Bind:     "prefix_exit_mode",
			Category: "Session",
		},
		{
			Name:     "Tape: review the project tape",
			Bind:     "tape_prefix_review",
			Category: "Session",
		},
		{
			Name:     "Open the tape manager",
			Bind:     "toggle_tape_manager",
			Category: "Session",
		},
		{
			Name:     "Enter copy mode",
			Bind:     "prefix_selection",
			Category: "Session",
		},
		{
			Name:     "Copy mode: search forward",
			Bind:     "copy_mode_search_forward",
			Category: "Session",
		},
		{
			Name:     "Copy mode: search backward",
			Bind:     "copy_mode_search_backward",
			Category: "Session",
		},
		{
			Name:     paletteMultiCopyName,
			Bind:     "multi_copy_mode",
			Category: "Session",
		},
	}...)
	defaultShortcuts(items)
	return items
}

var paletteStateTokens = []struct {
	name   string
	states []string
}{
	{"attention", []string{"needs_input", "errored"}},
	{"working", []string{"working"}},
	{"needs_input", []string{"needs_input"}},
	{"done", []string{"done"}},
	{"idle", []string{"idle"}},
	{"errored", []string{"errored"}},
	{"limit_reached", []string{"limit_reached"}},
}

func splitPaletteQuery(query string) (states []string, filtered bool, rest string) {
	tok, after, _ := strings.Cut(strings.TrimSpace(query), " ")
	if !strings.HasPrefix(tok, "@") {
		return nil, false, query
	}
	rest = strings.TrimSpace(after)
	name := strings.ToLower(tok[1:])
	if name == "" {
		return nil, true, rest
	}
	for _, t := range paletteStateTokens {
		if strings.HasPrefix(t.name, name) {
			return t.states, true, rest
		}
	}
	return []string{}, true, rest
}

func paletteStateMatches(item CommandPaletteItem, states []string) bool {
	if item.AgentState == "" {
		return false
	}
	if states == nil {
		return true
	}
	return slices.Contains(states, item.AgentState)
}

// FilterCommandPalette filters command palette items by a query string, best
// match first, after an optional leading "@state" token narrows the list to the
// panes in that state (see splitPaletteQuery).
//
// Matching is the shared scored matcher, so one box ranks static commands and
// panes against each other. Name is matched on its own and Category is a weaker
// fallback, so a row admitted only by its section name can never outrank one
// that matched what it actually says.
func FilterCommandPalette(items []CommandPaletteItem, query string) []CommandPaletteItem {
	keybinds, keybindRest := splitPaletteKeybinds(query)
	kept := make([]CommandPaletteItem, 0, len(items))
	for _, item := range items {
		if item.Keybind == keybinds {
			kept = append(kept, item)
		}
	}
	items = kept
	if keybinds {
		return matchPaletteItems(items, keybindRest)
	}
	if states, filtered, rest := splitPaletteQuery(query); filtered {
		kept := make([]CommandPaletteItem, 0, len(items))
		for _, item := range items {
			if paletteStateMatches(item, states) {
				kept = append(kept, item)
			}
		}
		return matchPaletteItems(kept, rest)
	}
	commands := make([]CommandPaletteItem, 0, len(items))
	var settings []CommandPaletteItem
	for _, item := range items {
		if item.Setting {
			settings = append(settings, item)
		} else {
			commands = append(commands, item)
		}
	}
	if paletteGrouped(query) {
		return groupPaletteItems(commands)
	}
	out := matchPaletteItems(commands, query)
	if strings.TrimSpace(query) == "" || len(settings) == 0 {
		return out
	}
	var m fuzzy.Matcher
	for _, h := range m.FilterIndex(query, len(settings), func(i int) string { return paletteSettingPrefix + settings[i].Name }) {
		item := settings[h.Index]
		item.Match = nil
		for _, p := range h.Positions {
			if p >= len(paletteSettingPrefix) {
				item.Match = append(item.Match, p-len(paletteSettingPrefix))
			}
		}
		out = append(out, item)
	}
	return out
}

func paletteGrouped(query string) bool { return query == "" }

func groupPaletteItems(items []CommandPaletteItem) []CommandPaletteItem {
	rank := map[string]int{}
	for _, it := range items {
		if _, ok := rank[it.Category]; !ok {
			rank[it.Category] = len(rank)
		}
	}
	out := slices.Clone(items)
	sortx.SortStableFunc(out, func(a, b CommandPaletteItem) int { return rank[a.Category] - rank[b.Category] })
	return out
}

const paletteSettingPrefix = "Settings: "

func matchPaletteItems(items []CommandPaletteItem, query string) []CommandPaletteItem {
	if query == "" {
		return items
	}
	var m fuzzy.Matcher
	hits := m.FilterIndex(query, len(items), func(i int) string {
		return printableTitle(items[i].Name)
	})

	named := make([]bool, len(items))
	out := make([]CommandPaletteItem, 0, len(items))
	for _, h := range hits {
		named[h.Index] = true
		item := items[h.Index]
		item.Match = h.Positions
		out = append(out, item)
	}

	for i, item := range items {
		if named[i] || item.Category == "" || !fuzzy.Match(query, item.Category) {
			continue
		}
		item.Match = nil
		out = append(out, item)
	}
	return out
}

func defaultShortcuts(items []CommandPaletteItem) {
	presses := config.PressesByAction(config.DefaultKeybindRegistry())
	for i := range items {
		if items[i].Bind == "" || items[i].Shortcut != "" {
			continue
		}
		if p := presses[items[i].Bind]; len(p) > 0 {
			items[i].Shortcut = paletteShortcut(p[0], config.DefaultLeaderKey)
			items[i].shortcutIsKey = true
		}
	}
}

func (m *OS) liveShortcuts(items []CommandPaletteItem) {
	reg := m.KeybindRegistry
	if reg == nil {
		return
	}
	presses := config.PressesByAction(reg)
	leader := reg.GetConfig().Keybindings.LeaderKey
	if leader == "" {
		leader = config.DefaultLeaderKey
	}
	for i := range items {
		if items[i].Bind == "" || (items[i].Shortcut != "" && !items[i].shortcutIsKey) {
			continue
		}
		items[i].Shortcut, items[i].shortcutIsKey = "", false
		if p := presses[items[i].Bind]; len(p) > 0 {
			items[i].Shortcut, items[i].shortcutIsKey = paletteShortcut(p[0], leader), true
		}
	}
}

func paletteShortcut(press, leader string) string {
	if rest, ok := strings.CutPrefix(press, leader+" "); ok {
		return "prefix+" + rest
	}
	return press
}
