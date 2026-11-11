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

// paletteCategoryAgents is the palette's section for the agent entries.
const paletteCategoryAgents = "Agents"

// paletteReviewName is the palette row of the review of the focused pane.
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
func (m *OS) ApplyReloadedConfig(cfg *config.UserConfig) tea.Cmd {
	if cfg == nil {
		return nil
	}
	// Runs on the Bubble Tea goroutine, so applying it to this session's
	// settings is single-threaded and reaches nobody else's session.
	if m.ReloadFilter != nil {
		cfg = m.ReloadFilter(cfg)
	}
	// A value set with set-config --no-save stays in force over the file
	// until the client exits or the option is saved. See config/hold.go.
	cfg.KeepHolds(m.UserConfig)
	themeBefore := theme.Generation()
	config.ApplyAppearanceConfig(m.overrides.Config(cfg), &m.Settings)
	m.ListKeys = config.ListKeymap(cfg)
	// ApplyAppearanceConfig switches the theme package, which recolours the
	// chrome, but each pane's emulator holds its own copy of the palette. The
	// theme picker pushes the new one into every pane (applyTheme); this path
	// did not, so a theme saved in any config file recoloured the borders and
	// left every pane, and all output after the save, in the old colours.
	if theme.Generation() != themeBefore {
		m.UpdateAllWindowThemes()
	}
	// A reload is the one time the display's rate is looked for again, after
	// startup: the person may have changed the monitor's mode. Started before
	// applyAppearanceLive below, which would otherwise keep the first answer.
	m.detectDisplayRate(true)
	// Whether the rail is shown is the session's once the session has a
	// value. The file's value is what a new session starts with, so a reload
	// does not show or hide the rail on every client. See keepSessionSidebar.
	m.keepSessionSidebar(false)
	// Retiled, not just repainted. The sidebar's width and side, the dock's
	// position and the pane gap all change how much room the panes have, and
	// this path had only ever repainted them: a gap edited in the file moved
	// the global and left the rectangles where they were.
	m.applyAppearanceLive(true)
	// The file can move the sidebar and the dock, which is the chrome the
	// session's reserve is settled from. settleChrome announces it.
	// The dock section is rebuilt here too rather than only at startup. A
	// feature whose distribution story is "copy a file" that then needed a
	// restart to see the file would be most of the story missing. This is also
	// what puts the new config on the model, which every section outside
	// [appearance] is read from live.
	cmd := m.ReloadDockComponents(cfg)
	// A scratch entry the new file renamed or removed has no key any more.
	m.pruneOrphanScratches()
	// Keybindings reload the same way, minus the write-back: the edit came
	// from the file, so persisting it would be the tail wagging the dog.
	if m.KeybindRegistry != nil {
		m.KeybindRegistry.Reload(cfg)
		m.keybinds.report = m.buildKeybindReport()
		m.keybinds.filtered = nil
	}
	// The beam is client-local, so nothing else carries it: without this the
	// screen and the config disagree about whether it is on, and the next
	// toggle writes the disagreement back to the file.
	m.SetSpotlight(cfg.Spotlight.IsEnabled())
	// The agent switch, which the file can flip as well as the settings page.
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
	Bind string
	// shortcutIsKey marks a Shortcut that defaultShortcuts filled with a key,
	// which liveShortcuts may replace, as opposed to a note it keeps.
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
	AgentState string
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
		// The launcher is its own overlay, and this is the row that opens it.
		// It is the bridge that keeps "one box finds everything" true as an
		// entry point without the two lists having to be ranked against each
		// other (see launcher.go).
		{
			Name:     "Run a program",
			Bind:     "launcher",
			Category: PaletteCategoryRun,
		},

		// Screenshots. Three rows, because the three things a person means by
		// "take a screenshot" want different gestures and only the first needs
		// one.
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

		// Window management
		{
			Name:     "New window",
			Bind:     "prefix_new_window",
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
			Bind:     "prefix_close_window",
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
			Bind:     "minimize_prefix_restore_all",
			Category: "Window",
		},
		{
			Name:     "Focus only: minimize the other windows",
			Bind:     "minimize_prefix_focus_only",
			Category: "Window",
		},

		// Layout
		{
			Name:     "Toggle tiling",
			Bind:     "toggle_tiling",
			Category: "Layout",
		},
		{
			Name:     "Split horizontal",
			Bind:     "prefix_split_horizontal",
			Category: "Layout",
		},
		{
			Name:     "Split horizontal, and run the focused pane's ssh",
			Category: "Layout",
			Bind:     "split_ssh_horizontal",
		},
		{
			Name:     "Split vertical",
			Bind:     "prefix_split_vertical",
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
			Bind:     "prefix_rotate_split",
			Category: "Layout",
		},
		{
			Name:     "Equalize splits",
			Bind:     "prefix_equalize_splits",
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

		// Layout templates
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

		// Navigation
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

		// Layout
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
		// Layout mode: individual commands
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
		// Scrolling-specific actions
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
		// Scrollback
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
		// Floating
		{
			Name:     "Toggle floating",
			Category: "Window",
			Bind:     "window_prefix_float",
		},
		// Navigation
		{
			// No Shortcut: go-to on this session's panes has no binding of
			// its own and is reached from this palette.
			Name:     "All windows",
			Bind:     "all_windows",
			Category: "Navigation",
		},
		// Session & Config
		{
			// The palette's own way in to making a session, which had none.
			// It was a one-cell "+" on a rail heading, and the rail is not
			// always open.
			Name:     "New session",
			Category: "Session",
			Bind:     "new_session",
		},
		{
			Name:     "Settings",
			Bind:     "prefix_settings",
			Category: "Session",
		},
		{
			// The Agents tab, where each harness's integration is shown,
			// installed and updated. Left out while the tab is.
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
		// Keybinds. Three rows onto one overlay, because "change a key" and
		// "stop tuios taking a key" are the two things people come here for and
		// neither of them is spelled "keybind manager". Two of the three spend
		// their meta slot on the "#" token, which is the part nothing else
		// would ever tell the user about.
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
			// The way to move the daemon to the build an update installed.
			// Left out on a client that cannot restart its daemon.
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
			// A task card from anywhere. See task_capture.go.
			Name:     "New task",
			Category: "Session",
			Bind:     "prefix_new_task",
		},
		{
			// The add sheet: test a machine over ssh, install tuios there,
			// and add it. See machine_sheet.go.
			Name:     "Add a machine",
			Bind:     "add_machine",
			Category: "Session",
		},
		{
			// The machines in the rail's order, as next_host and prev_host
			// step through them. Neither has a default key, so the palette
			// is where a person finds them before binding one.
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
			// The agent mailbox: the messages agents leave for each other and
			// for the person, and the place to answer one.
			// It was "Mail: open inbox" on prefix+M; the Inbox is its own thing
			// now, and prefix+M opens it on its mail.
			//
			// The agent rows lead with "Agents:" so typing "agent" finds them.
			// It found "Window management mode", which fuzzy-matches the
			// letters, and nothing to do with agents. Each keeps the word its
			// overlay is called by (Inbox, mailbox), so those still find them.
			Name:     "Agents: open mailbox",
			Bind:     "open_mailbox",
			Category: paletteCategoryAgents,
		},
		{
			// The Inbox: everything waiting for the person, in every session.
			Name:     "Agents: what needs you",
			Bind:     "prefix_inbox",
			Category: paletteCategoryAgents,
		},
		{
			// Task dispatch: stop every card going to agents at once, or
			// start it again (cockpit_dispatch.go).
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
			// The copies between machines: the Copies panel.
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
			// The agent panes in the rail's order, across sessions.
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
			// The review overlay on the focused pane's changes. The word
			// "review" finds it, like "Review changes" in the prefix menu.
			Name:     paletteReviewName,
			Bind:     "prefix_review",
			Category: paletteCategoryAgents,
		},
		{
			// prefix+M had no palette row; "open mailbox" above opens a
			// different view, every thread rather than mail for you.
			Name:     "Agents: mail for you",
			Bind:     "prefix_mail",
			Category: paletteCategoryAgents,
		},
		{
			Name:     "Show help",
			Bind:     "prefix_help",
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
			Bind:     "tape_prefix_manager",
			Category: "Session",
		},
		{
			Name:     "Enter copy mode",
			Bind:     "prefix_selection",
			Category: "Session",
		},
		{
			// No Shortcut: copy_mode_search_forward has no default binding.
			Name:     "Copy mode: search forward",
			Bind:     "copy_mode_search_forward",
			Category: "Session",
		},
		{
			// No Shortcut: copy_mode_search_backward has no default binding.
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

// paletteStateTokens are the states a leading "@" token narrows the palette to.
// Resolved by prefix in this order, so "@a" is attention, "@w" working, "@n"
// needs input, "@d" done, "@i" idle and "@e" errored: one keystroke past the
// "@", which is what makes "who needs me" worth typing at all.
//
// attention is first and is the reason the mechanism exists. It is the rail's
// own definition of a state that wants a human (sidebarAttention), so the
// palette and the rail's gutter mark can never come to mean different things.
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
}

// splitPaletteQuery pulls a leading "@state" token off a query and reports the
// states it admits, whether one was present at all, and the text left to match
// on. A bare "@" names no state and admits every entry that carries one, which
// is the halfway house the user is in while typing the next character.
//
// An "@" naming nothing (a typo, or a state this build does not have) admits
// nothing: a filter the user typed and that quietly did not apply is worse than
// an empty list, which at least says so.
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

// paletteStateMatches reports whether an entry survives a state filter. Only the
// session and window entries carry a state, so filtering by one is also what
// drops every static command: "@attention" is a question about panes.
func paletteStateMatches(item CommandPaletteItem, states []string) bool {
	if item.AgentState == "" {
		return false
	}
	if states == nil {
		return true // a bare "@": anything running an agent
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
	// The keybind token is resolved first and it is exclusive both ways: "#" is
	// a search over actions, so it admits nothing else, and every other query
	// admits no action rows. A few hundred actions leaking into an ordinary
	// search would bury the commands the palette is for.
	keybinds, keybindRest := splitPaletteKeybinds(query)
	kept := make([]CommandPaletteItem, 0, len(items))
	for _, item := range items {
		if item.Keybind == keybinds {
			kept = append(kept, item)
		}
	}
	items = kept
	if keybinds {
		// Straight to the matcher rather than back through this function: the
		// text after the token carries no token of its own, and re-entering
		// would partition the list a second time and throw away every row.
		return matchPaletteItems(items, keybindRest)
	}
	if states, filtered, rest := splitPaletteQuery(query); filtered {
		kept := make([]CommandPaletteItem, 0, len(items))
		for _, item := range items {
			if paletteStateMatches(item, states) {
				kept = append(kept, item)
			}
		}
		// The scored matcher runs over what is left, so "@a server" still ranks a
		// prefix hit above a mid-word one.
		return matchPaletteItems(kept, rest)
	}
	// The setting rows are matched on their own and put after everything
	// else, so the few hundred of them never bury a command, and an empty
	// query does not list them at all.
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
	// A setting row is matched as "Settings: <name>", so typing the word the
	// row is tagged with reaches it, and drawn as its name alone beside the
	// [Settings] tag. The highlight is moved back by the prefix it was matched
	// with.
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

// paletteGrouped reports whether the palette lists its commands under category
// headers: only while nothing is typed. A typed query ranks the rows by how
// well they match, and headers over a ranked list would split it into runs
// that each restart the ranking.
func paletteGrouped(query string) bool { return query == "" }

// groupPaletteItems orders items by category, the categories in the order
// they first appear and the items inside each in the order they were given,
// so each category is one run under one header.
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

// paletteSettingPrefix is what a setting row is matched with ahead of its name.
const paletteSettingPrefix = "Settings: "

// matchPaletteItems is the scored pass, with no token handling: names first,
// then rows admitted only by their category.
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

	// Category hits land after every name hit rather than being scored beside
	// them. That is the old scoring's intent without its magic numbers: typing
	// "layout" should list the Layout section, below anything actually called
	// layout.
	for i, item := range items {
		if named[i] || item.Category == "" || !fuzzy.Match(query, item.Category) {
			continue
		}
		item.Match = nil
		out = append(out, item)
	}
	return out
}

// defaultShortcuts gives each row that names a keybind action and has no
// Shortcut text of its own the key the shipped keymap binds that action to,
// so no row spells a key that can drift from the keymap. A client with no
// keymap of its own, such as the GUI bridge, shows these.
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

// liveShortcuts sets the Shortcut of every row that names a keybind action to
// the key the config binds that action to now. It runs each time the palette
// list is built, so a reloaded config shows on the next open. A row whose
// action has a default key but no live key shows no shortcut. A row with a
// note of its own keeps it, such as "drag to select".
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
		// The first press is the one to show: presses come in scope order,
		// so a global key (launcher's alt+space) wins over its prefix chord,
		// and the chord shows when the global key is unbound.
		items[i].Shortcut, items[i].shortcutIsKey = "", false
		if p := presses[items[i].Bind]; len(p) > 0 {
			items[i].Shortcut, items[i].shortcutIsKey = paletteShortcut(p[0], leader), true
		}
	}
}

// paletteShortcut spells a press the way the palette always has: a chord under
// the leader reads "prefix+" and then the rest, as in "prefix+w 1". The leader
// itself is configurable, and "prefix" names it whatever it is.
func paletteShortcut(press, leader string) string {
	if rest, ok := strings.CutPrefix(press, leader+" "); ok {
		return "prefix+" + rest
	}
	return press
}
