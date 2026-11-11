// Package envvars is the one list of the TUIOS_ environment variables tuios
// reads or sets: what each means, who reads it, and whether a person is meant to
// use it. docs/ENVIRONMENT.md is generated from it (go generate ./internal/envvars),
// and the tests in this package fail when a name in the code is not in the list,
// when a name in the list is in no code, or when the page is out of date.
package envvars

import (
	"slices"
	"strings"
)

// Kind says what a variable is for. The first six are for people and appear in
// the generated page; Test and Internal do not.
type Kind string

const (
	// Pane variables are set by tuios in every pane, for the program there to read.
	Pane Kind = "pane"
	// Setting variables are set by a person to change how tuios runs.
	Setting Kind = "setting"
	// Timing variables set a time or a limit the daemon or the client uses.
	Timing Kind = "timing"
	// Switch variables turn one behaviour back to an older one, or turn a fast
	// path off. A legacy switch is removed in a later release.
	Switch Kind = "switch"
	// Debug variables write a log or a trace for a person looking into a problem.
	Debug Kind = "debug"
	// Hook variables are set by tuios for a hook command, a dock or rail
	// command, or the command of an xpanes pane.
	Hook Kind = "hook"
	// Plugin variables are set by tuios for a plugin process.
	Plugin Kind = "plugin"
	// Test variables are hooks for the test suites. They change nothing in a
	// normal run.
	Test Kind = "test"
	// Internal variables carry a value from one tuios process to the next one.
	Internal Kind = "internal"
)

// Var is one environment variable.
type Var struct {
	Name    string
	Kind    Kind
	Meaning string
	// Readers names the places that read the variable, or for a pane variable
	// the program that sets it.
	Readers string
}

// UserVisible reports whether the generated page lists the variable.
func (v Var) UserVisible() bool { return v.Kind != Test && v.Kind != Internal }

// The names of the variables a pane gets, which many packages read. Use these
// and not the literal: internal/lint rejects the literal outside this package.
const (
	PaneID        = "TUIOS_PANE_ID"
	WindowID      = "TUIOS_WINDOW_ID"
	WindowName    = "TUIOS_WINDOW_NAME"
	Session       = "TUIOS_SESSION"
	SessionRemote = "TUIOS_SESSION_REMOTE"
	Socket        = "TUIOS_SOCKET"
	PaneToken     = "TUIOS_PANE_TOKEN"
	PaneGrants    = "TUIOS_PANE_GRANTS"
	PaneHosted    = "TUIOS_PANE_HOSTED"
	PaneTTY       = "TUIOS_PANE_TTY"
	Restored      = "TUIOS_RESTORED"
	Host          = "TUIOS_HOST"
)

// All lists every variable, sorted by name.
var All = []Var{
	{"TUIOS_ACTIVE_PANE_CWD", Internal, "The folder of the focused pane, given to a command key's program.", "internal/app/command_keys.go sets it"},
	{"TUIOS_ACTIVE_PANE_ID", Internal, "The id of the focused pane, given to a command key's program.", "internal/app/command_keys.go sets it"},
	{"TUIOS_AGENT", Setting, "Names the harness that owns the pane, such as claude-code. A sandbox wrapper sets it, so a hook from another harness in the pane is ignored.", "internal/integration, internal/session detector"},
	{"TUIOS_AGENT_AUTODETECT", Setting, "Turns agent detection by process name on or off for the daemon. It follows [daemon] agent_autodetect.", "internal/session/daemon.go"},
	{"TUIOS_AGENT_BINARIES", Setting, "A comma list of program names the daemon treats as agents, added to the built-in list. It follows [daemon] agent_binaries.", "internal/session/daemon.go"},
	{"TUIOS_AGENT_DETECT_SECONDS", Timing, "Seconds between the daemon's looks for an agent in a pane. It follows [daemon] agent_detect_seconds.", "internal/session/daemon.go"},
	{"TUIOS_AGENT_STALL_SECONDS", Timing, "Seconds a working agent may show no output before the pane is marked silent.", "internal/session/daemon.go"},
	{"TUIOS_ALLOW_NESTED", Setting, "Set to 1 to attach to a session from a pane of the same session. tuios attach --force does the same.", "internal/session/nested_attach.go"},
	{"TUIOS_BASH_LOGIN", Internal, "Tells the bash rc file tuios writes to read the files a login bash reads. tuios unsets it afterwards.", "internal/shellinteg"},
	{"TUIOS_BIN", Plugin, "The path of the tuios binary that started a plugin.", "internal/session/herdr_plugins.go sets it"},
	{"TUIOS_CELL_SIZE", Setting, "The size of one terminal cell in pixels, as WIDTHxHEIGHT, for a terminal that does not report it. Used to place pictures.", "internal/app/capabilities.go, internal/server"},
	{"TUIOS_CLEANUP_SECONDS", Timing, "Seconds between the daemon's cleanup passes. The default is 30.", "internal/session/daemon.go"},
	{"TUIOS_DEBUG_CAPS", Debug, "Set to 1 to write what tuios learned about the host terminal to a file.", "internal/app/capabilities.go"},
	{"TUIOS_DEBUG_INTERNAL", Internal, "Set to 1 by --debug so the processes tuios starts, the daemon included, write their debug logs.", "internal/debuglog"},
	{"TUIOS_DISPATCH_READY_SECONDS", Timing, "Seconds the task dispatcher waits for a new agent to be ready for its prompt.", "internal/session/task_dispatch_auto.go"},
	{"TUIOS_E2E", Test, "Set to 1 by the end-to-end suite. Several test hooks only act with it.", "internal/ptyspawn, internal/session"},
	{"TUIOS_E2E_CHILD_HOLD_MS", Test, "Holds a child pane's attention item for this many milliseconds.", "internal/session/attention_children.go"},
	{"TUIOS_E2E_DRAFT_SURFACE_MS", Test, "Sets how long a queued draft shows on the surface.", "internal/session/agent_queue_draft.go"},
	{"TUIOS_E2E_HOLD_NAV", Test, "A file path the navigator waits on before it answers.", "internal/app/navigator.go"},
	{"TUIOS_E2E_HOLD_NEW_WINDOW", Test, "A file path the daemon waits on before it makes a window.", "internal/session/daemon_command.go"},
	{"TUIOS_E2E_HOLD_PANE", Test, "A file path the daemon waits on before it starts a pane's process.", "internal/session/daemon_command.go"},
	{"TUIOS_E2E_HOLD_SCAN", Test, "A file path the run-anything scan waits on.", "internal/app/run_anything.go"},
	{"TUIOS_E2E_KITTY_LINK_STALL", Test, "A file path the kitty link check waits on.", "internal/session/kitty_link_near.go"},
	{"TUIOS_E2E_MAC_INPUT_SOURCE", Test, "Stands in for the macOS input source name.", "internal/config/mac_input_source.go"},
	{"TUIOS_E2E_NAV_CAPTURES", Test, "How many pane captures the navigator takes.", "internal/app/navigator.go"},
	{"TUIOS_E2E_NO_BUFFER_VERBS", Test, "Turns the paste buffer verbs off, with TUIOS_E2E.", "internal/session/verb_buffers.go"},
	{"TUIOS_E2E_NO_PROCESS_CWD", Test, "Makes a pane's folder come from OSC 7 only, as on Windows, with TUIOS_E2E.", "internal/ptyspawn/cwd.go"},
	{"TUIOS_E2E_PLATFORM", Test, "Set to darwin to get the macOS defaults and key handling on another platform.", "internal/config/platform.go"},
	{"TUIOS_E2E_QUEUE_DEDUP_SKEW", Test, "A file path that skews a queue client's id.", "internal/session/queue_client_id.go"},
	{"TUIOS_E2E_RUN_USER", Test, "Names the user whose runtime folder the socket path uses.", "internal/sockpath/sockpath_unix.go"},
	{"TUIOS_E2E_SYMBOL_DRAW_COST", Test, "A duration added to each picture-as-symbols draw.", "internal/app/sixel_symbols.go"},
	{"TUIOS_E2E_TML_PUSH_DELAY", Test, "A duration the daemon waits before it pushes a TML surface.", "internal/session/tml_surface.go"},
	{"TUIOS_E2E_TMP", Test, "The temporary folder the socket path uses.", "internal/sockpath/sockpath_unix.go"},
	{"TUIOS_E2E_WATCH_START_DELAY_MS", Test, "Delays the start of a folder watch by this many milliseconds.", "internal/session/daemon_dirwatch.go"},
	{"TUIOS_ENV", Test, "A marker the test environment filters keep, so a test can tell its variables from the host's.", "internal/testutil"},
	{"TUIOS_EVENTS_LOG", Debug, "A file that records every key and mouse event the client reads. It records passwords too: use it for a test or a bug report only.", "internal/debuglog"},
	{"TUIOS_GUI_BRIDGE_PPROF", Debug, "An address on which tuios gui-bridge serves pprof.", "cmd/tuios/gui_bridge_command.go"},
	{"TUIOS_GUI_BRIDGE_QUEUE_MAX", Setting, "The most messages the gui-bridge queues for a slow client.", "internal/guibridge/bridge.go"},
	{"TUIOS_HANDOFF_FALLBACK", Internal, "Marks a daemon image started as the fallback of a live upgrade.", "internal/session/handoff_fallback.go"},
	{"TUIOS_HANDOFF_KEPT", Internal, "Maps the descriptors a keeper holds for a live upgrade.", "internal/session/handoff_keeper.go"},
	{"TUIOS_HANDOFF_OLD_COPY", Internal, "The folders of the binary copies earlier daemon images ran from, for the next image to remove.", "internal/session/handoff_copy.go"},
	{"TUIOS_HARNESS_DIR", Setting, "A folder of harness manifests to read, in place of the user folder under the XDG config home.", "internal/harness/registry.go"},
	{"TUIOS_HOST", Pane, "The name of the machine a pane runs on, as the daemon knows it. The CLI signs a request with it.", "internal/session sets it; cmd/tuios/target.go reads it"},
	{"TUIOS_HOST_RECONNECT_BUDGET", Timing, "How long the client keeps trying to reach a lost host before it gives up, as a duration. The default is three minutes.", "internal/app/host_reconnect.go"},
	{"TUIOS_KITTY_ANIMATION", Setting, "1 or 0 forces the host's kitty animation support on or off, in place of what tuios detects.", "internal/app/capabilities.go"},
	{"TUIOS_KITTY_GRAPHICS", Setting, "1 or 0 forces the host's kitty graphics support on or off, in place of what tuios detects.", "internal/app/capabilities.go, internal/server"},
	{"TUIOS_KITTY_PLACEHOLDERS", Setting, "1 or 0 forces the host's unicode placeholder support on or off, in place of what tuios detects.", "internal/app/capabilities.go"},
	{"TUIOS_LOAD1", Test, "Stands in for the machine's one-minute load.", "internal/session/machine_load.go"},
	{"TUIOS_LOG_LEVEL", Debug, "The daemon's starting log level, until a flag or the config sets it.", "internal/session/debug.go"},
	{"TUIOS_LOOK_GALLERY", Test, "Moves the look gallery to another site. A plain http site is taken on a loopback address only.", "cmd/tuios/look_pick.go"},
	{"TUIOS_NO_DAEMON", Setting, "Set to 1 to run in one process with no daemon, as --standalone does, for a shell that has to keep working.", "cmd/tuios/run.go"},
	{"TUIOS_NO_DAEMON_APP", Setting, "Set to 1 to start the daemon directly even when the desktop app is installed.", "cmd/tuios/daemon_app.go"},
	{"TUIOS_NO_FASTBOX", Switch, "Set to 1 to send every pane box through lipgloss, to compare the two paths.", "internal/app/border_box_fast.go"},
	{"TUIOS_NO_FASTPATH", Switch, "Set to 1 to turn the fullscreen fast path off, to compare it with the compositor.", "internal/app/render.go"},
	{"TUIOS_NO_PRESHAPED", Switch, "Set to 1 to send every pane body through the wrap, to compare the two border-box paths.", "internal/app/render.go"},
	{"TUIOS_NO_SERVICE", Setting, "Set to 1 to start the daemon directly even when a service is installed.", "cmd/tuios/daemon_service.go"},
	{"TUIOS_NO_SOUND", Setting, "Set to 1 to silence every sound, whatever the config says. For a test, a CI job or a recording.", "internal/sound"},
	{"TUIOS_PANE_GRANTS", Pane, "What the pane may do through tuios, as a list of grants.", "internal/guestenv sets it"},
	{"TUIOS_PANE_HOSTED", Pane, "Set when the pane's process runs on another machine.", "internal/guestenv sets it"},
	{"TUIOS_PANE_ID", Pane, "The id of the pane. tuios commands run in the pane use it as their target.", "internal/guestenv sets it; cmd/tuios and internal/session read it"},
	{"TUIOS_PANE_TOKEN", Pane, "The pane's token, which proves to the daemon which pane a request comes from.", "internal/guestenv sets it; cmd/tuios/mcp_command.go and internal/session read it"},
	{"TUIOS_PANE_TTY", Pane, "The pane's terminal device, for a program that has lost its own.", "internal/guestenv sets it; internal/session/nested_attach.go reads it"},
	{"TUIOS_PLUGIN_CONFIG_DIR", Plugin, "The folder of a plugin's configuration.", "internal/session/herdr_plugins.go sets it"},
	{"TUIOS_PLUGIN_CONTEXT_JSON", Plugin, "The context of a plugin call, as JSON.", "internal/session/herdr_plugins.go sets it"},
	{"TUIOS_PLUGIN_CWD", Plugin, "The folder of the pane a plugin was called for.", "internal/session/plugin_host.go sets it"},
	{"TUIOS_PLUGIN_ENTRY", Plugin, "The entry of the plugin that runs.", "internal/session/plugin_host.go sets it"},
	{"TUIOS_PLUGIN_GODEBUG", Internal, "GODEBUG for a plugin reaper. Set to always, the Go heap takes 2 MB pages, which more than doubled a reaper's memory.", "internal/herdrplugin/reaper.go"},
	{"TUIOS_PLUGIN_ID", Plugin, "The id of the plugin.", "internal/session sets it"},
	{"TUIOS_PLUGIN_PANE", Plugin, "The pane a plugin was called for.", "internal/session/plugin_host.go sets it"},
	{"TUIOS_PLUGIN_ROOT", Plugin, "The folder the plugin is installed in.", "internal/session/herdr_plugins.go sets it"},
	{"TUIOS_PLUGIN_SESSION", Plugin, "The session a plugin was called for.", "internal/session/plugin_host.go sets it"},
	{"TUIOS_PLUGIN_STATE_DIR", Plugin, "The folder of a plugin's state.", "internal/session/herdr_plugins.go sets it"},
	{"TUIOS_PLUGIN_TOKEN", Plugin, "The token that proves to the daemon which plugin a request comes from.", "internal/session/plugin_token.go sets it"},
	{"TUIOS_PR_POLL_SECONDS", Timing, "Seconds between the daemon's polls of GitHub for pull request state. A lower number uses more of the API's rate limit.", "internal/session/pr_poll.go"},
	{"TUIOS_PTY_LOG", Debug, "A folder to write a raw log of every pane's output to, for a replay into an emulator.", "internal/session/pty_log.go"},
	{"TUIOS_RELEASE_API", Setting, "The base URL of the release API that the plugin installer asks, in place of GitHub's.", "cmd/tuios/plugins_release.go"},
	{"TUIOS_RENDER_TRACE", Debug, "Writes a trace of each frame's passes. The value names the file or the mode.", "internal/app/render_trace.go"},
	{"TUIOS_RESTORED", Pane, "Set when the pane's process was started again after a restart of the daemon.", "internal/guestenv sets it"},
	{"TUIOS_SERVER", Setting, "The name of the daemon to use when --server does not name one. Each name has its own socket and saved sessions.", "internal/serverscope, internal/service"},
	{"TUIOS_SESSION", Pane, "The name of the session the pane belongs to. A command run in the pane uses it as the default session.", "internal/guestenv sets it; cmd/tuios reads it"},
	{"TUIOS_SESSION_ORDER_LEGACY", Switch, "Set to 1 for the older order of sessions in the rail.", "internal/app/session_order.go"},
	{"TUIOS_SESSION_REMOTE", Pane, "Set when the session is on another machine.", "internal/guestenv sets it"},
	{"TUIOS_SIDEBAR_LEGACY", Switch, "Set to 1 for the older rail visibility rules.", "internal/session/sidebar_visibility.go"},
	{"TUIOS_SIXEL_GRAPHICS", Setting, "1 or 0 forces the host's sixel support on or off, in place of what tuios detects.", "internal/app/capabilities.go, internal/server"},
	{"TUIOS_SOCKET", Pane, "The path of the daemon's socket. A tuios command in the pane connects there.", "internal/guestenv sets it; cmd/tuios, internal/herdrplugin read it"},
	{"TUIOS_SSH", Setting, "The ssh program tuios runs for another machine, in place of ssh.", "cmd/tuios/host_sync.go, internal/federation"},
	{"TUIOS_STATS_FILE", Debug, "A file the client writes its tick and render counts to, when it gets SIGUSR1 and at exit.", "internal/app/tick_stats.go"},
	{"TUIOS_SUBAGENT_FADE_SECONDS", Timing, "Seconds a finished subagent stays on its agent's row.", "internal/session/agent_subagents.go"},
	{"TUIOS_TAPE_AUTORUN", Setting, "off, ask or auto: what to do with a project tape. It wins over [tape] autorun. For CI and demos.", "internal/app/tape_detect.go"},
	{"TUIOS_TEST_CARD_CHECK", Test, "Turns on the extra checks of a task card's moves.", "internal/session/task_move.go"},
	{"TUIOS_TEST_HANDOFF", Test, "Turns on the handoff test verbs.", "internal/session/handoff_testhooks.go"},
	{"TUIOS_TEST_HANDOFF_CRASH", Test, "Makes a live upgrade crash at the named step.", "internal/session/handoff_fallback.go"},
	{"TUIOS_TEST_HANDOFF_CRASH_MODE", Test, "How the crash happens: panic or exit.", "internal/session/handoff_fallback.go"},
	{"TUIOS_TEST_HANDOFF_FAIL", Test, "Makes a live upgrade fail at the named step.", "internal/session/handoff_fallback.go"},
	{"TUIOS_TEST_HANDOFF_WATCHDOG_MS", Test, "The watchdog time of a live upgrade, in milliseconds.", "internal/session/handoff_fallback.go"},
	{"TUIOS_TEST_ISOLATED", Test, "Marks a process that a test run started.", "internal/testutil"},
	{"TUIOS_TEST_STATE_PUSH_HOLD_MS", Test, "Holds a state push for this many milliseconds.", "internal/session/daemon_stream.go"},
	{"TUIOS_TEST_STATE_SYNC_HOLD_MS", Test, "Holds a state sync for this many milliseconds.", "internal/app/remote_after_sync.go"},
	{"TUIOS_TEST_TRANSFER_HOLD_MS", Test, "Holds a file transfer for this many milliseconds.", "internal/session/transfer.go"},
	{"TUIOS_TEST_UPGRADE_CONFIRM_MS", Test, "How long the upgrade confirmation waits, in milliseconds.", "internal/session/upgrade_confirm.go"},
	{"TUIOS_TML_LINK_LEGACY", Switch, "Set to 1 for the older way a TML surface is linked.", "internal/session/tml_surface.go"},
	{"TUIOS_TMUX_SHIM_LOG", Debug, "The file the tmux shim logs to. Empty means the default path.", "internal/tmuxcompat/log.go"},
	{"TUIOS_TMUX_SHIM_LOG_ALL", Debug, "Set to 1 to log every call of the tmux shim, not only the ones it could not fully answer.", "internal/tmuxcompat/log.go"},
	{"TUIOS_TRANSFER_KEEP_SECONDS", Timing, "Seconds a finished copy stays in the list of transfers.", "internal/session/transfer_expiry.go"},
	{"TUIOS_VIEW_CHROME_LEGACY", Switch, "Set to 1 for the older rule for the chrome a view of a session shows.", "internal/session/tuiclient_window_size.go"},
	{"TUIOS_VIEW_ONLY", Setting, "Set to 1 to attach as a viewer that sends no input.", "internal/session/tuiclient_window_size.go"},
	{"TUIOS_WEB_PASSWORD", Setting, "The password for tuios-web, in place of a flag, so it does not show in the process list.", "cmd/tuios-web/access.go"},
	{"TUIOS_WINDOW_ID", Pane, "The id of the pane. The same value as TUIOS_PANE_ID, under the older name.", "internal/guestenv sets it; cmd/tuios and internal/session read it"},
	{"TUIOS_WINDOW_NAME", Pane, "The name of the pane.", "internal/guestenv sets it"},
	{"TUIOS_WINDOW_SIZE_LEGACY", Switch, "Set to 1 for the older rule that sizes the window of a client.", "internal/session/tuiclient_window_size.go"},
	{"TUIOS_WORKTREE_DIR", Setting, "The folder new worktrees go in, in place of the default.", "internal/worktree/path.go"},
	{"TUIOS_ZSH_ZDOTDIR", Internal, "Carries the user's ZDOTDIR past tuios's zsh shim, which sets it back. If it is absent the user had none.", "internal/shellinteg"},
	{"TUIOS_AGENT_HARNESS", Hook, "The harness of the agent in the pane, for an agent hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_AGENT_MESSAGE", Hook, "The message of the agent state, for an agent hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_AGENT_PREV_STATE", Hook, "The agent state before the change, for an agent hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_AGENT_STATE", Hook, "The agent state after the change, for an agent hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_CLICK_BUTTON", Hook, "The mouse button that clicked a dock component, as a number, for its command.", "internal/app/dock_runtime.go sets it"},
	{"TUIOS_COMMAND", Hook, "The command that ended, for a command hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_DOCK_COMPONENT", Hook, "The name of the custom dock component the command is for.", "internal/dockengine/engine.go sets it"},
	{"TUIOS_DURATION_MS", Hook, "How long the command ran, in milliseconds, for a command hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_EVENT", Hook, "The name of the event a hook runs for.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_EXIT_CODE", Hook, "The exit code of the command that ended, for a command hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_HEIGHT", Hook, "The height of the terminal in rows, for a hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_INTEGRATION_ID", Internal, "A marker in the files tuios writes for a harness integration, so tuios can find them again.", "internal/integration/formats.go"},
	{"TUIOS_INTEGRATION_VERSION", Internal, "The version of an integration file that tuios wrote.", "internal/integration"},
	{"TUIOS_LAYOUT", Hook, "The layout mode, for a hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_PLUGIN_EVENT", Plugin, "The name of the event a plugin was called for.", "internal/session/plugin_host.go sets it"},
	{"TUIOS_PLUGIN_EVENT_JSON", Plugin, "The event a plugin was called for, as JSON.", "internal/session/plugin_host.go sets it"},
	{"TUIOS_PREV_WORKSPACE", Hook, "The workspace before a switch, for a hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_RAIL_HEIGHT", Hook, "The height in rows of the rail section a custom command draws.", "internal/dockengine/engine.go sets it"},
	{"TUIOS_RAIL_SECTION", Hook, "The name of the rail section a custom command draws.", "internal/dockengine/engine.go sets it"},
	{"TUIOS_RAIL_WIDTH", Hook, "The width in columns of the rail section a custom command draws.", "internal/dockengine/engine.go sets it"},
	{"TUIOS_SESSION_ID", Hook, "The id of the session, for a hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_SETTLE_REASON", Hook, "Why an agent settled, for an agent hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_SOUND_PATH", Internal, "The sound file the Windows player plays.", "internal/sound/players_windows.go sets it"},
	{"TUIOS_THEME", Hook, "The name of the theme, for a hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_THEME_BG", Hook, "The background colour of the theme, for a hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_THEME_LIGHT", Hook, "Whether the theme is light, for a hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_WIDTH", Hook, "The width of the terminal in columns, for a hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_WORKSPACE", Hook, "The number of the workspace, for a hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_WORKTREE_BRANCH", Hook, "The branch of the worktree, for a worktree hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_WORKTREE_CREATED_BRANCH", Hook, "Whether the worktree command made the branch, for a worktree hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_WORKTREE_PATH", Hook, "The folder of the worktree, for a worktree hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_WORKTREE_REPO_ROOT", Hook, "The root of the repository, for a worktree hook.", "internal/hooks/hooks.go sets it"},
	{"TUIOS_XPANES_INDEX", Hook, "The number of the pane an xpanes command runs in, from 1.", "cmd/tuios/xpanes_command.go sets it"},
	{"TUIOS_XPANES_ITEM", Hook, "The item an xpanes command runs for.", "cmd/tuios/xpanes_command.go sets it"},
}

func init() { slices.SortFunc(All, func(a, b Var) int { return strings.Compare(a.Name, b.Name) }) }

// ByName returns the variable called name.
func ByName(name string) (Var, bool) {
	i, ok := slices.BinarySearchFunc(All, name, func(v Var, n string) int { return strings.Compare(v.Name, n) })
	if !ok {
		return Var{}, false
	}
	return All[i], true
}
