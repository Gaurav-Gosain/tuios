<!-- This file is generated from internal/envvars. Run `go generate ./internal/envvars` after a change to the table. -->

# Environment variables

tuios reads and sets these variables. The list is generated from the one table in the code, `internal/envvars`, and a test fails when a variable in the code is not in it.

## Variables every pane gets

tuios sets these in the environment of the program in each pane. A `tuios` command run there reads them, so it needs no `--session` or `--window`. They are removed from the environment of a program that tuios starts on another machine by ssh, unless the pane is hosted there.

| Variable | What it does |
|----------|--------------|
| `TUIOS_HOST` | The name of the machine a pane runs on, as the daemon knows it. The CLI signs a request with it. |
| `TUIOS_PANE_GRANTS` | What the pane may do through tuios, as a list of grants. |
| `TUIOS_PANE_HOSTED` | Set when the pane's process runs on another machine. |
| `TUIOS_PANE_ID` | The id of the pane. tuios commands run in the pane use it as their target. |
| `TUIOS_PANE_TOKEN` | The pane's token, which proves to the daemon which pane a request comes from. |
| `TUIOS_PANE_TTY` | The pane's terminal device, for a program that has lost its own. |
| `TUIOS_RESTORED` | Set when the pane's process was started again after a restart of the daemon. |
| `TUIOS_SESSION` | The name of the session the pane belongs to. A command run in the pane uses it as the default session. |
| `TUIOS_SESSION_REMOTE` | Set when the session is on another machine. |
| `TUIOS_SOCKET` | The path of the daemon's socket. A tuios command in the pane connects there. |
| `TUIOS_WINDOW_ID` | The id of the pane. The same value as TUIOS_PANE_ID, under the older name. |
| `TUIOS_WINDOW_NAME` | The name of the pane. |

## Variables you can set

Set these in the shell that starts tuios, or in the environment of a service.

| Variable | What it does |
|----------|--------------|
| `TUIOS_AGENT` | Names the harness that owns the pane, such as claude-code. A sandbox wrapper sets it, so a hook from another harness in the pane is ignored. |
| `TUIOS_AGENT_AUTODETECT` | Turns agent detection by process name on or off for the daemon. It follows [daemon] agent_autodetect. |
| `TUIOS_AGENT_BINARIES` | A comma list of program names the daemon treats as agents, added to the built-in list. It follows [daemon] agent_binaries. |
| `TUIOS_ALLOW_NESTED` | Set to 1 to attach to a session from a pane of the same session. tuios attach --force does the same. |
| `TUIOS_CELL_SIZE` | The size of one terminal cell in pixels, as WIDTHxHEIGHT, for a terminal that does not report it. Used to place pictures. |
| `TUIOS_GUI_BRIDGE_QUEUE_MAX` | The most messages the gui-bridge queues for a slow client. |
| `TUIOS_HARNESS_DIR` | A folder of harness manifests to read, in place of the user folder under the XDG config home. |
| `TUIOS_KITTY_ANIMATION` | 1 or 0 forces the host's kitty animation support on or off, in place of what tuios detects. |
| `TUIOS_KITTY_GRAPHICS` | 1 or 0 forces the host's kitty graphics support on or off, in place of what tuios detects. |
| `TUIOS_KITTY_PLACEHOLDERS` | 1 or 0 forces the host's unicode placeholder support on or off, in place of what tuios detects. |
| `TUIOS_NO_DAEMON` | Set to 1 to run in one process with no daemon, as --standalone does, for a shell that has to keep working. |
| `TUIOS_NO_DAEMON_APP` | Set to 1 to start the daemon directly even when the desktop app is installed. |
| `TUIOS_NO_SERVICE` | Set to 1 to start the daemon directly even when a service is installed. |
| `TUIOS_NO_SOUND` | Set to 1 to silence every sound, whatever the config says. For a test, a CI job or a recording. |
| `TUIOS_RELEASE_API` | The base URL of the release API that the plugin installer asks, in place of GitHub's. |
| `TUIOS_SERVER` | The name of the daemon to use when --server does not name one. Each name has its own socket and saved sessions. |
| `TUIOS_SIXEL_GRAPHICS` | 1 or 0 forces the host's sixel support on or off, in place of what tuios detects. |
| `TUIOS_SSH` | The ssh program tuios runs for another machine, in place of ssh. |
| `TUIOS_TAPE_AUTORUN` | off, ask or auto: what to do with a project tape. It wins over [tape] autorun. For CI and demos. |
| `TUIOS_VIEW_ONLY` | Set to 1 to attach as a viewer that sends no input. |
| `TUIOS_WEB_PASSWORD` | The password for tuios-web, in place of a flag, so it does not show in the process list. |
| `TUIOS_WORKTREE_DIR` | The folder new worktrees go in, in place of the default. |

## Timings and limits

Most of these also have a key in `[daemon]`. The variable wins when both are set.

| Variable | What it does |
|----------|--------------|
| `TUIOS_AGENT_DETECT_SECONDS` | Seconds between the daemon's looks for an agent in a pane. It follows [daemon] agent_detect_seconds. |
| `TUIOS_AGENT_STALL_SECONDS` | Seconds a working agent may show no output before the pane is marked silent. |
| `TUIOS_CLEANUP_SECONDS` | Seconds between the daemon's cleanup passes. The default is 30. |
| `TUIOS_DISPATCH_READY_SECONDS` | Seconds the task dispatcher waits for a new agent to be ready for its prompt. |
| `TUIOS_HOST_RECONNECT_BUDGET` | How long the client keeps trying to reach a lost host before it gives up, as a duration. The default is three minutes. |
| `TUIOS_PR_POLL_SECONDS` | Seconds between the daemon's polls of GitHub for pull request state. A lower number uses more of the API's rate limit. |
| `TUIOS_SUBAGENT_FADE_SECONDS` | Seconds a finished subagent stays on its agent's row. |
| `TUIOS_TRANSFER_KEEP_SECONDS` | Seconds a finished copy stays in the list of transfers. |

## Switches

A `LEGACY` switch turns one behaviour back to the older one and is removed in a later release. The `NO_` switches turn a fast path off so it can be compared with the slow one.

| Variable | What it does |
|----------|--------------|
| `TUIOS_NO_FASTBOX` | Set to 1 to send every pane box through lipgloss, to compare the two paths. |
| `TUIOS_NO_FASTPATH` | Set to 1 to turn the fullscreen fast path off, to compare it with the compositor. |
| `TUIOS_NO_PRESHAPED` | Set to 1 to send every pane body through the wrap, to compare the two border-box paths. |
| `TUIOS_SESSION_ORDER_LEGACY` | Set to 1 for the older order of sessions in the rail. |
| `TUIOS_SIDEBAR_LEGACY` | Set to 1 for the older rail visibility rules. |
| `TUIOS_TML_LINK_LEGACY` | Set to 1 for the older way a TML surface is linked. |
| `TUIOS_VIEW_CHROME_LEGACY` | Set to 1 for the older rule for the chrome a view of a session shows. |
| `TUIOS_WINDOW_SIZE_LEGACY` | Set to 1 for the older rule that sizes the window of a client. |

## Logs and traces

These write files. Some record what you type, so use them for a bug report and delete the files after.

| Variable | What it does |
|----------|--------------|
| `TUIOS_DEBUG_CAPS` | Set to 1 to write what tuios learned about the host terminal to a file. |
| `TUIOS_EVENTS_LOG` | A file that records every key and mouse event the client reads. It records passwords too: use it for a test or a bug report only. |
| `TUIOS_GUI_BRIDGE_PPROF` | An address on which tuios gui-bridge serves pprof. |
| `TUIOS_LOG_LEVEL` | The daemon's starting log level, until a flag or the config sets it. |
| `TUIOS_PTY_LOG` | A folder to write a raw log of every pane's output to, for a replay into an emulator. |
| `TUIOS_RENDER_TRACE` | Writes a trace of each frame's passes. The value names the file or the mode. |
| `TUIOS_STATS_FILE` | A file the client writes its tick and render counts to, when it gets SIGUSR1 and at exit. |
| `TUIOS_TMUX_SHIM_LOG` | The file the tmux shim logs to. Empty means the default path. |
| `TUIOS_TMUX_SHIM_LOG_ALL` | Set to 1 to log every call of the tmux shim, not only the ones it could not fully answer. |

## Variables a command gets

tuios sets these for a hook command, for the command of a custom dock component or rail section, and for each pane of `tuios xpanes`.

| Variable | What it does |
|----------|--------------|
| `TUIOS_AGENT_HARNESS` | The harness of the agent in the pane, for an agent hook. |
| `TUIOS_AGENT_MESSAGE` | The message of the agent state, for an agent hook. |
| `TUIOS_AGENT_PREV_STATE` | The agent state before the change, for an agent hook. |
| `TUIOS_AGENT_STATE` | The agent state after the change, for an agent hook. |
| `TUIOS_CLICK_BUTTON` | The mouse button that clicked a dock component, as a number, for its command. |
| `TUIOS_COMMAND` | The command that ended, for a command hook. |
| `TUIOS_DOCK_COMPONENT` | The name of the custom dock component the command is for. |
| `TUIOS_DURATION_MS` | How long the command ran, in milliseconds, for a command hook. |
| `TUIOS_EVENT` | The name of the event a hook runs for. |
| `TUIOS_EXIT_CODE` | The exit code of the command that ended, for a command hook. |
| `TUIOS_HEIGHT` | The height of the terminal in rows, for a hook. |
| `TUIOS_LAYOUT` | The layout mode, for a hook. |
| `TUIOS_PREV_WORKSPACE` | The workspace before a switch, for a hook. |
| `TUIOS_RAIL_HEIGHT` | The height in rows of the rail section a custom command draws. |
| `TUIOS_RAIL_SECTION` | The name of the rail section a custom command draws. |
| `TUIOS_RAIL_WIDTH` | The width in columns of the rail section a custom command draws. |
| `TUIOS_SESSION_ID` | The id of the session, for a hook. |
| `TUIOS_SETTLE_REASON` | Why an agent settled, for an agent hook. |
| `TUIOS_THEME` | The name of the theme, for a hook. |
| `TUIOS_THEME_BG` | The background colour of the theme, for a hook. |
| `TUIOS_THEME_LIGHT` | Whether the theme is light, for a hook. |
| `TUIOS_WIDTH` | The width of the terminal in columns, for a hook. |
| `TUIOS_WORKSPACE` | The number of the workspace, for a hook. |
| `TUIOS_WORKTREE_BRANCH` | The branch of the worktree, for a worktree hook. |
| `TUIOS_WORKTREE_CREATED_BRANCH` | Whether the worktree command made the branch, for a worktree hook. |
| `TUIOS_WORKTREE_PATH` | The folder of the worktree, for a worktree hook. |
| `TUIOS_WORKTREE_REPO_ROOT` | The root of the repository, for a worktree hook. |
| `TUIOS_XPANES_INDEX` | The number of the pane an xpanes command runs in, from 1. |
| `TUIOS_XPANES_ITEM` | The item an xpanes command runs for. |

## Variables a plugin gets

tuios sets these for the process of a plugin.

| Variable | What it does |
|----------|--------------|
| `TUIOS_BIN` | The path of the tuios binary that started a plugin. |
| `TUIOS_PLUGIN_CONFIG_DIR` | The folder of a plugin's configuration. |
| `TUIOS_PLUGIN_CONTEXT_JSON` | The context of a plugin call, as JSON. |
| `TUIOS_PLUGIN_CWD` | The folder of the pane a plugin was called for. |
| `TUIOS_PLUGIN_ENTRY` | The entry of the plugin that runs. |
| `TUIOS_PLUGIN_EVENT` | The name of the event a plugin was called for. |
| `TUIOS_PLUGIN_EVENT_JSON` | The event a plugin was called for, as JSON. |
| `TUIOS_PLUGIN_ID` | The id of the plugin. |
| `TUIOS_PLUGIN_PANE` | The pane a plugin was called for. |
| `TUIOS_PLUGIN_ROOT` | The folder the plugin is installed in. |
| `TUIOS_PLUGIN_SESSION` | The session a plugin was called for. |
| `TUIOS_PLUGIN_STATE_DIR` | The folder of a plugin's state. |
| `TUIOS_PLUGIN_TOKEN` | The token that proves to the daemon which plugin a request comes from. |

tuios also reads 33 variables that only its test suites set, and 12 that one tuios process passes to the next. They are in the table and not on this page.
