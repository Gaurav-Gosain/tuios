package config

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// FileKey is a config.toml key that the option registry (optionSpecs) does
// not carry: a list, a table under a name such as [hosts.NAME], or a key that
// must not change while tuios runs.
//
// fileKeys is kept apart from optionSpecs on purpose. Every caller that
// changes a setting at run time reads optionSpecs: set-option and set-config,
// the settings page, look apply, the browser tour and the CLI flags. A key
// that is not there cannot be set by any of them, so a pane cannot widen its
// own grants with set-option agents.permissions.mode. fileKeys exists so that
// docs/config-options.md describes these keys too, from the code, and the
// hand-written tables in docs/CONFIGURATION.md can go.
// TestFileKeysCannotBeSetAtRunTime fails if a later change lets one of them
// through.
//
// This is a test file on purpose: nothing in tuios reads fileKeys at run
// time, so the shipped binary does not carry the table. The tests and the
// generator of docs/config-options.md read it. internal/app's settings test
// reads the same paths from that generated page.
type FileKey struct {
	// Path is the dotted path. NAME stands for a name the person chooses,
	// such as the name of a host.
	Path string
	// Type is bool, int, string, list (of values), map (of a name to a
	// value), table (a table whose keys Description names) or tables (a list
	// of such tables).
	Type    string
	Section string
	// Description is one or two sentences of plain English.
	Description string
	// Default is what the key is when the file does not set it. It is
	// derived from DefaultConfig when DefaultConfig sets the field. Write
	// it only for a field DefaultConfig leaves unset, in words when the
	// default is not a value ("the receive folder").
	Default  string
	Accepted []string
}

// The types a FileKey can have, besides OptionBool, OptionInt and
// OptionString.
const (
	FileKeyList   = "list"
	FileKeyMap    = "map"
	FileKeyTable  = "table"
	FileKeyTables = "tables"
)

// tomlList writes a list of text as TOML writes it.
func tomlList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// goDuration writes a duration as a person writes it in the file: 10m, 2h.
func goDuration(d time.Duration) string {
	s := d.String()
	s = strings.TrimSuffix(s, "0s")
	return strings.TrimSuffix(s, "0m")
}

// dockLookKey is a [dock] key that a look may set too (see lookFileKeys).
// Its description and default come from that table.
func dockLookKey(path string) FileKey {
	k := lookFileKeys[path]
	desc := strings.TrimSuffix(k.Description, ".") + ". A look can set it."
	typ := k.Type
	if typ == LookList {
		typ = FileKeyList
	}
	return FileKey{Path: path, Type: typ, Section: "dock", Description: desc, Default: k.Default, Accepted: slices.DeleteFunc(slices.Clone(k.Accepted), func(s string) bool { return s == "" })}
}

// fileKeys is every config.toml key outside optionSpecs, but for the two
// kinds of table that have a reference of their own (see fileKeysSkipped).
var fileKeys = []FileKey{
	{Path: "workspaces.base_index", Type: OptionInt, Section: "workspaces", Default: "1", Accepted: []string{"0", "1"},
		Description: "Number workspaces and their default digit keys from 0 or 1. The daemon's workspace IDs stay one-based."},

	// [keybindings]: the keys that are not a section of bindings.
	{Path: "keybindings.preset", Type: OptionString, Section: "keybindings", Accepted: []string{"tuios", "tmux", "screen", "zellij"},
		Description: "The keys of another multiplexer, put over the tuios defaults. Empty is tuios. See KEYBINDINGS.md."},
	{Path: "keybindings.leader_key", Type: OptionString, Section: "keybindings",
		Description: "The leader key, which starts the prefix commands. A preset can change it."},
	{Path: "keybindings.leader_twice", Type: OptionString, Section: "keybindings", Default: "send", Accepted: []string{"send", "last_workspace"},
		Description: "What the leader does when you press it two times. send sends the leader to the pane in terminal mode. last_workspace goes back to the last workspace, as in screen."},
	{Path: "keybindings.keyboard_layout", Type: OptionString, Section: "keybindings", Default: "us", Accepted: []string{"us", "other"},
		Description: "The keyboard layout for a key that the terminal does not describe. With us, a binding on opt+shift+7 also matches opt+&. Set other for AZERTY, QWERTZ and other layouts."},
	{Path: "keybindings.option_glyphs", Type: OptionString, Section: "keybindings", Default: "auto", Accepted: []string{"auto", "alt", "bind", "type"},
		Description: "What a character that the macOS Option key typed does. bind runs the Option binding, type sends the character to the pane, and alt runs the binding only when the terminal also reports Alt. auto reads the layout of the Mac."},
	{Path: "keybindings.lock", Type: OptionBool, Section: "keybindings", Default: "false",
		Description: "Give every key to the pane in terminal mode, except the leader. The leader unlocks for one action. The zellij preset sets it."},
	{Path: "keybindings.nest_exit_key", Type: OptionString, Section: "keybindings", Default: DefaultNestExitKey,
		Description: "The key that ends nest mode. In nest mode every key goes to the pane, the leader too."},
	{Path: "keybindings.repeat", Type: FileKeyList, Section: "keybindings", Default: "built in",
		Description: "The actions that stay armed after the leader, as tmux bind -r does. The same key again runs the action again. An empty list turns repeat off."},

	// [daemon]
	{Path: "daemon.agent_binaries", Type: FileKeyList, Section: "daemon", Default: "[]",
		Description: "More program names to treat as agents, added to the built-in list."},
	{Path: "daemon.single_client", Type: OptionBool, Section: "daemon", Default: "false",
		Description: "Keep one client for each session. Each attach detaches the other clients of the session, as tuios attach -d does."},
	{Path: "daemon.ssh_agent", Type: OptionString, Section: "daemon", Default: "off", Accepted: []string{"off", "follow"},
		Description: "follow gives each session a link to the ssh agent of the client used last. New panes get SSH_AUTH_SOCK set to the link."},
	{Path: "daemon.display", Type: OptionString, Section: "daemon", Default: "follow", Accepted: []string{"follow", "off"},
		Description: "follow gives the new panes of a daemon with no display the display of the local client. off gives them none."},
	{Path: "daemon.respond_from_shell", Type: OptionBool, Section: "daemon", Default: "false",
		Description: "Let tuios respond, run from a shell outside every pane, answer the prompt of an agent. tuios refuses a caller inside a pane either way."},

	// [hosts.NAME]
	{Path: "hosts.NAME.addr", Type: OptionString, Section: "hosts", Default: "required",
		Description: "The address of the machine: anything ssh accepts, an ssh_config alias too. tuios ignores and logs a host with no addr, or with one that starts with -."},
	{Path: "hosts.NAME.connect_timeout", Type: OptionInt, Section: "hosts", Default: "10",
		Description: "The seconds one dial may take before the host counts as unreachable. 0 uses the default."},
	{Path: "hosts.NAME.command", Type: OptionString, Section: "hosts",
		Description: "The tuios binary on the host. Empty finds it on the PATH, at the usual install paths, or through a login shell."},
	{Path: "hosts.NAME.ssh_options", Type: FileKeyList, Section: "hosts", Default: "[]",
		Description: "More ssh arguments, put before the address. tuios accepts only safe options. -A forwards your ssh agent to the host."},
	{Path: "hosts.NAME.repos_root", Type: OptionString, Section: "hosts",
		Description: "The folder on the host that holds its checkouts, such as ~/src. fan --host, worktree new --host and start-agent look there for a repository by its origin URL."},
	{Path: "hosts.NAME.tailscale_login", Type: OptionString, Section: "hosts",
		Description: "The origin of a Headscale server that sends the Tailscale SSH check of this host. tuios shows a sign-in link only for the Tailscale origins and this one."},
	{Path: "hosts.NAME.allow", Type: FileKeyList, Section: "hosts", Default: tomlList(DefaultLinkAllow), Accepted: LinkCapabilities,
		Description: "What the machine may do here over the link. An empty list allows nothing. Unset takes the value of [hosts.\"*\"], then the default."},
	{Path: "hosts.NAME.hold_mail", Type: OptionBool, Section: "hosts", Default: "false",
		Description: "Hold mail from the machine in the Inbox until you pass it on. Unset takes the value of [hosts.\"*\"]."},
	{Path: "hosts.NAME.hosted_grace", Type: OptionString, Section: "hosts", Default: goDuration(DefaultHostedGrace),
		Description: "How long a pane that this machine runs for the other machine waits after the link drops, as a Go duration. 0 ends the pane with the link."},
	{Path: "hosts.NAME.files_roots", Type: FileKeyList, Section: "hosts", Default: "the receive folder",
		Description: "The folders the machine may write in, with the files capability. The receive folder is ~/Downloads/tuios, or $XDG_DOWNLOAD_DIR/tuios on Linux. An empty list allows only the drop folder."},
	{Path: "hosts.NAME.files_read_roots", Type: FileKeyList, Section: "hosts", Default: "not set",
		Description: "The folders the machine may read. Not set allows every file you can read, apart from the read deny list. A list also allows the folders of files_roots."},
	{Path: "hosts.NAME.files_min_free", Type: OptionString, Section: "hosts", Default: "1GiB",
		Description: "The free space a write must leave on its disk: a whole number with B, KB, MB, GB, TB, KiB, MiB, GiB or TiB. 0 turns the check off."},
	{Path: "hosts.NAME.files_max_entries", Type: OptionInt, Section: "hosts", Default: strconv.Itoa(DefaultFilesMaxEntries),
		Description: "The most new files and folders the machine may make here in ten minutes. Past it, a copy waits. 0 turns the bound off."},

	// [tailscale]
	{Path: "tailscale.enabled", Type: OptionBool, Section: "tailscale", Default: "true",
		Description: "Offer the machines of your tailnet as addresses. The list is empty on a machine that is not on a tailnet."},
	{Path: "tailscale.addr", Type: OptionString, Section: "tailscale", Default: "dns", Accepted: []string{"dns", "name", "ip"},
		Description: "The form of the address: dns for the MagicDNS name, name for the short name, ip for the 100.x address."},
	{Path: "tailscale.user", Type: OptionString, Section: "tailscale",
		Description: "The ssh login to put before every address."},
	{Path: "tailscale.users", Type: FileKeyMap, Section: "tailscale", Default: "none",
		Description: "A login for each machine, by its short name. It wins over user."},
	{Path: "tailscale.os", Type: FileKeyList, Section: "tailscale", Default: "the systems that run tuios",
		Description: "The operating systems to offer. An empty list offers every machine."},
	{Path: "tailscale.offline", Type: OptionBool, Section: "tailscale", Default: "false",
		Description: "Also offer machines that are offline."},
	{Path: "tailscale.self", Type: OptionBool, Section: "tailscale", Default: "false",
		Description: "Also offer this machine."},
	{Path: "tailscale.shared", Type: OptionBool, Section: "tailscale", Default: "false",
		Description: "Also offer machines that other people shared into your tailnet."},
	{Path: "tailscale.include", Type: FileKeyList, Section: "tailscale", Default: "[]",
		Description: "Glob patterns on the short name and the MagicDNS name. Empty offers every machine."},
	{Path: "tailscale.exclude", Type: FileKeyList, Section: "tailscale", Default: "[]",
		Description: "Glob patterns of machines not to offer. exclude wins over include."},
	{Path: "tailscale.max", Type: OptionInt, Section: "tailscale", Default: "built in",
		Description: "The most machines to offer. 0 uses the default."},
	{Path: "tailscale.socket", Type: OptionString, Section: "tailscale",
		Description: "The path to the tailscaled socket, when it is not in the usual place."},

	// [agents]: the tables under it, and its keys that hold a word with a
	// meaning of its own.
	{Path: "agents.herdr_protocol", Type: OptionString, Section: "agents", Default: "always", Accepted: []string{"always", "agents", "off"},
		Description: "Which panes learn about the socket for the herdr pane state protocol: always for every pane, agents for a pane that starts such a harness, off for none."},
	{Path: "agents.host_program_status", Type: OptionString, Section: "agents", Default: "session", Accepted: []string{"session", "panes", "off"},
		Description: "How tuios reports the agent states of its panes to your terminal, with OSC 7501. session sends one record for the session, panes one for each pane."},
	{Path: "agents.silent_after", Type: OptionString, Section: "agents", Default: goDuration(DefaultSilentAfter),
		Description: "How long a pane whose hooks reported working may stay silent before it shows as unknown, as a Go duration. 0 or off turns it off."},
	{Path: "agents.approvals.enabled", Type: FileKeyList, Section: "agents", Default: "[]",
		Description: "The harnesses whose permission prompts wait for an answer from the Inbox: claude-code (or claude), opencode, kilo and qwen. Empty turns it off."},
	{Path: "agents.approvals.hold_seconds", Type: OptionInt, Section: "agents", Default: "120",
		Description: "How long a prompt waits for your answer before the harness asks in its own pane, from 10 to 300 seconds. 0 uses the default."},
	{Path: "agents.approvals.hold_plans", Type: OptionBool, Section: "agents", Default: "true",
		Description: "Also hold a plan that an agent asks you to approve when it leaves plan mode."},
	{Path: "agents.approvals.risk.builtin", Type: OptionBool, Section: "agents", Default: "true",
		Description: "Keep the risk rules that tuios ships."},
	{Path: "agents.approvals.risk.panes_may_allow", Type: OptionBool, Section: "agents", Default: "false",
		Description: "Let a pane that holds the respond grant allow a call that matched a risk rule. Without it, such a pane may only deny the call."},
	{Path: "agents.approvals.risk.rule", Type: FileKeyTables, Section: "agents", Default: "none",
		Description: "Your own risk rules, added to the shipped ones. Each has a name, the tools it applies to (empty for every tool) and an RE2 pattern."},
	{Path: "agents.permissions.mode", Type: OptionString, Section: "agents", Default: "open", Accepted: []string{"open", "strict"},
		Description: "open gives a pane started with no grants of its own the admin grant. strict gives it grants. Any other value reads as strict."},
	{Path: "agents.permissions.grants", Type: FileKeyList, Section: "agents", Default: tomlList(DefaultStrictGrants),
		Description: "What such a pane holds under strict. An empty list gives nothing. Add files to let a pane copy files near its work."},
	{Path: "agents.recap.mode", Type: OptionString, Section: "agents", Default: RecapToast, Accepted: RecapModes,
		Description: "Where the away recap shows: toast in the dock and the Inbox, inbox only in the Inbox, off only in tuios agent-log --recap."},
	{Path: "agents.recap.away", Type: OptionString, Section: "agents", Default: goDuration(DefaultRecapAway),
		Description: "How long you must be away from a pane before the dock shows its recap, as a Go duration."},
	{Path: "agents.recap.test_patterns", Type: FileKeyList, Section: "agents", Default: "built in",
		Description: "The commands that count as a test run in the recap, such as go test and npm test. A list replaces the built-in one."},
	{Path: "agents.queue.max", Type: OptionInt, Section: "agents", Default: strconv.Itoa(DefaultQueueMax),
		Description: "The messages that the queue of one pane holds, at most 64. 0 uses the default."},
	{Path: "agents.mail.wake", Type: OptionBool, Section: "agents", Default: "true",
		Description: "Type one line into an agent at rest that has unread mail, so that it reads the mail."},
	{Path: "agents.mail.questions_to_parent", Type: OptionBool, Section: "agents", Default: "true",
		Description: "Send a question from a delegated child agent to its parent by mail first. The Inbox holds it for 10 minutes before it alerts you."},
	{Path: "agents.checkpoints.enabled", Type: OptionBool, Section: "agents", Default: "true",
		Description: "Save the git work tree of the pane of an agent each time the agent ends a turn that changed a file."},
	{Path: "agents.checkpoints.keep", Type: OptionInt, Section: "agents", Default: strconv.Itoa(DefaultCheckpointKeep),
		Description: "How many checkpoints one pane keeps, at most 1000. 0 uses the default."},
	{Path: "agents.checkpoints.max_untracked_mb", Type: OptionInt, Section: "agents", Default: strconv.Itoa(DefaultCheckpointMaxUntrackedMB),
		Description: "Leave an untracked file larger than this many megabytes out of a checkpoint. A negative value sets no limit."},
	{Path: "agents.presets.NAME", Type: FileKeyTable, Section: "agents", Default: "none",
		Description: "A way to start one kind of agent: tuios start-agent --preset NAME. It names the agent and any of args, add_dirs, grants, model, effort, permission_mode, env, after_ready and ready_timeout."},

	// [notify]
	{Path: "notify.enabled", Type: OptionBool, Section: "notify", Default: "true",
		Description: "Send notifications to your phone. false turns every notification off. tuios sends nothing without a provider."},
	{Path: "notify.web_url", Type: OptionString, Section: "notify",
		Description: "The address of tuios-web. Each notification links to web_url/inbox?item=ID."},
	{Path: "notify.content", Type: OptionString, Section: "notify", Default: "summary", Accepted: []string{"summary", "title"},
		Description: "summary sends the kind, the pane name and the line that the Inbox shows. title sends only the kind and the session."},
	{Path: "notify.quiet_active_seconds", Type: OptionInt, Section: "notify", Default: "120",
		Description: "Hold a notification while you typed at an attached client in this many seconds. 0 sends at once."},
	{Path: "notify.cooldown_seconds", Type: OptionInt, Section: "notify", Default: "60",
		Description: "The shortest time between two notifications for the same pane and kind."},
	{Path: "notify.max_per_hour", Type: OptionInt, Section: "notify", Default: "30",
		Description: "The most notifications in one hour. 0 sets no limit."},
	{Path: "notify.allow_http_redirects", Type: OptionBool, Section: "notify", Default: "false",
		Description: "Let a provider redirect to a plain http address."},
	{Path: "notify.triggers.approval", Type: OptionBool, Section: "notify", Default: "true", Description: "Notify for a permission prompt that waits for you."},
	{Path: "notify.triggers.plan", Type: OptionBool, Section: "notify", Default: "true", Description: "Notify for a plan to approve."},
	{Path: "notify.triggers.question", Type: OptionBool, Section: "notify", Default: "true", Description: "Notify for an agent that asks a question in its pane."},
	{Path: "notify.triggers.ask", Type: OptionBool, Section: "notify", Default: "true", Description: "Notify for tuios ask-human."},
	{Path: "notify.triggers.mail", Type: OptionBool, Section: "notify", Default: "false", Description: "Notify for agent mail to you."},
	{Path: "notify.triggers.errored", Type: OptionBool, Section: "notify", Default: "false", Description: "Notify for an agent that stopped on an error."},
	{Path: "notify.triggers.finished", Type: OptionBool, Section: "notify", Default: "false", Description: "Notify for an agent that finished a turn."},
	{Path: "notify.ntfy", Type: FileKeyTable, Section: "notify", Default: "none",
		Description: "Send with ntfy: url (the topic), priority (1 to 5), and token, token_env or token_file."},
	{Path: "notify.pushover", Type: FileKeyTable, Section: "notify", Default: "none",
		Description: "Send with Pushover: user and token, each also as _env or _file, and url for a relay."},
	{Path: "notify.webhook", Type: FileKeyTable, Section: "notify", Default: "none",
		Description: "Send a JSON POST to url. token, token_env or token_file adds an Authorization: Bearer header."},
	{Path: "notify.webpush.subject", Type: OptionString, Section: "notify", Default: DefaultWebPushSubject,
		Description: "The VAPID subject: an https URL or a mailto: address that a push service can use to contact you."},
	{Path: "notify.webpush.allow_insecure", Type: OptionBool, Section: "notify", Default: "false",
		Description: "Let tuios send to a push service on a loopback or private address, by https or http."},

	// [dock]
	dockLookKey("dock.left"),
	dockLookKey("dock.center"),
	dockLookKey("dock.right"),
	{Path: "dock.custom.NAME.command", Type: OptionString, Section: "dock", Default: "required",
		Description: "The command of your own dock cell, run with sh -c. The first line of its output is the text of the cell."},
	{Path: "dock.custom.NAME.refresh", Type: OptionString, Section: "dock", Default: "once",
		Description: "When the command runs: once, a duration such as 30s, push (it stays running and each line is an update), or event:TYPE[,TYPE]."},
	{Path: "dock.custom.NAME.on-click", Type: OptionString, Section: "dock",
		Description: "A command to run with sh -c when you click the cell."},
	{Path: "dock.custom.NAME.max-width", Type: OptionInt, Section: "dock", Default: strconv.Itoa(DockCustomDefaultMaxWidth),
		Description: "The widest the cell may be, in cells. 0 uses the default."},
	{Path: "dock.custom.NAME.fg", Type: OptionString, Section: "dock", Description: "The text colour of the cell, as #rrggbb."},
	{Path: "dock.custom.NAME.bg", Type: OptionString, Section: "dock", Description: "The ground colour of the cell, as #rrggbb."},
	{Path: "dock.custom.NAME.bold", Type: OptionBool, Section: "dock", Default: "false", Description: "Draw the text of the cell in bold."},
	{Path: "dock.custom.NAME.cap_left", Type: OptionString, Section: "dock", Description: "Text drawn before the cell, in the colour of its ground."},
	{Path: "dock.custom.NAME.cap_right", Type: OptionString, Section: "dock", Description: "Text drawn after the cell, in the colour of its ground."},
	{Path: "dock.session", Type: FileKeyTable, Section: "dock", Default: "none",
		Description: "The session cell: format, with {session}, {workspace} and {pane} (default " + DefaultDockSessionFormat + "), and fg, bg, bold, cap_left and cap_right as a custom cell has them."},
	dockLookKey("dock.tabs.style"),
	dockLookKey("dock.tabs.separator"),
	dockLookKey("dock.tabs.separator_fg"),
	dockLookKey("dock.tabs.active_fg"),
	dockLookKey("dock.tabs.active_bg"),
	dockLookKey("dock.tabs.active_bold"),
	dockLookKey("dock.tabs.active_underline"),
	dockLookKey("dock.tabs.inactive_fg"),
	dockLookKey("dock.tabs.inactive_bg"),
	dockLookKey("dock.tabs.inactive_bold"),
	dockLookKey("dock.tabs.caps"),
	dockLookKey("dock.tabs.number_glyphs"),
	dockLookKey("dock.tabs.zoom_marker"),
	dockLookKey("dock.tabs.show_add"),
	dockLookKey("dock.tabs.attention_fg"),
	dockLookKey("dock.tabs.bell_marker"),
	dockLookKey("dock.tabs.activity_marker"),

	// The session rail. Its commands are file-only so that a pane cannot
	// replace a command that runs in the client.
	{Path: "appearance.sidebar.custom.title", Type: OptionString, Section: "sidebar", Default: SidebarCustomDefaultTitle,
		Description: "The heading of the custom section of the rail."},
	{Path: "appearance.sidebar.custom.command", Type: OptionString, Section: "sidebar",
		Description: "The command of the custom section, run with sh -c. Each line of its output is a row."},
	{Path: "appearance.sidebar.custom.refresh", Type: OptionString, Section: "sidebar", Default: "once",
		Description: "When the command runs: once, a duration such as 30s (one second at least), or event:TYPE[,TYPE]. tuios refuses push."},
	{Path: "appearance.sidebar.pane.title", Type: OptionString, Section: "sidebar", Default: SidebarPaneDefaultTitle,
		Description: "The heading of the pane section of the rail."},
	{Path: "appearance.sidebar.pane.command", Type: OptionString, Section: "sidebar",
		Description: "The program that the pane section runs, with sh -c (cmd /C on Windows)."},
	{Path: "appearance.sidebar.pane.height", Type: OptionInt, Section: "sidebar", Default: "0",
		Description: "The most rows the pane takes. 0 takes the share of the section in appearance.sidebar.sections, such as pane:30."},
	{Path: "appearance.sidebar.left", Type: FileKeyTable, Section: "sidebar", Default: "none",
		Description: "A rail on the left edge, apart from the main rail. Its keys: enabled, width, sections, session, session_workspace (1 to 9, or 0 for every workspace), custom, and the subagent keys of [appearance.sidebar]."},
	{Path: "appearance.sidebar.right", Type: FileKeyTable, Section: "sidebar", Default: "none",
		Description: "A rail on the right edge, with the same keys as appearance.sidebar.left."},
	{Path: "appearance.sidebar.agent_row", Type: FileKeyTable, Section: "sidebar", Default: "built in",
		Description: "What an agent row of the rail shows, and the rules that colour its parts. See CONFIGURATION.md."},
	{Path: "appearance.scroll_column_presets", Type: FileKeyList, Section: "appearance", Default: "[33, 50, 55, 67, 90, 100]",
		Description: "The widths that scroll_cycle_width steps through in the scrolling layout, in percent of the screen."},

	// Lists and tables of other sections.
	{Path: "notifications.agent.sounds.done", Type: OptionString, Section: "notifications", Default: "built in",
		Description: "A sound file to play when an agent stops."},
	{Path: "notifications.agent.sounds.needs_input", Type: OptionString, Section: "notifications", Default: "built in",
		Description: "A sound file to play when an agent waits for you or fails."},
	{Path: "hints.patterns", Type: FileKeyList, Section: "hints", Default: "[]",
		Description: "Your own hint patterns, as Go regular expressions. A group named match sets the part that is copied."},
	{Path: "links.rules", Type: FileKeyTables, Section: "links", Default: "none",
		Description: "Your own links: a regex in Go syntax, and the url a match opens. $0 is the whole match, and $1 or ${name} is a group."},
	{Path: "files.open", Type: FileKeyTables, Section: "files", Default: "none",
		Description: "The program that opens a file from the rail: a match glob and a command (a list). The first rule that matches wins. With no match, the system opener runs."},
	{Path: "tasks.stages", Type: FileKeyList, Section: "tasks", Default: "every stage",
		Description: "The stages that the task board shows. The order stays " + strings.Join(TaskStages, ", ") + "."},
	{Path: "ssh.handshake_timeout", Type: OptionString, Section: "ssh",
		Description: "Close a connection that does not log in within this time, as a Go duration. 0 turns it off."},
	{Path: "ssh.idle_timeout", Type: OptionString, Section: "ssh",
		Description: "Close a connection after this time with no traffic in either direction, as a Go duration. 0 turns it off."},
	{Path: "ssh.max_handshakes", Type: OptionInt, Section: "ssh",
		Description: "The most connections that may log in at the same time. tuios closes a new connection past it. 0 sets no limit."},
	{Path: "ssh.max_sessions", Type: OptionInt, Section: "ssh",
		Description: "The most sessions open at the same time, over all connections. tuios refuses a new session past it. 0 sets no limit."},
	{Path: "actions", Type: FileKeyTables, Section: "actions and menus", Default: "none",
		Description: "Actions of your own: a name, and a label, icon, key, targets, when and trusted. See CONFIGURATION.md."},
	{Path: "menus.NAME", Type: FileKeyTable, Section: "actions and menus", Default: "none",
		Description: "A menu of your own, or a change to a shipped menu: title, sticky, items, add, after and remove."},
	{Path: "scratch.session", Type: OptionString, Section: "scratch",
		Description: "No longer used. The scratch popup runs a plain shell. tuios names the key when it loads a config that has it."},
	{Path: "plugins.enabled", Type: FileKeyList, Section: "plugins", Default: "[]",
		Description: "The ids of the plugins that tuios runs. tuios plugins enable and disable change it."},
	{Path: "plugins.dirs", Type: FileKeyList, Section: "plugins", Default: "[]",
		Description: "More plugin folders, or manifests, to list. tuios plugins link adds to it."},
}

// fileKeysSkipped are the parts of UserConfig that neither optionSpecs nor
// fileKeys describes, each with the reason.
var fileKeysSkipped = map[string]string{
	// Each section of [keybindings] maps an action to keys. KEYBINDINGS.md
	// and docs/keybindings-defaults.md (written from the keymaps) are their
	// reference. The keys of [keybindings] that are not bindings are in
	// fileKeys.
	"keybindings": "a binding section; see KEYBINDINGS.md",
	// [hooks] maps an event to commands. HOOKS.md lists the events.
	"hooks": "a map of event to commands; see HOOKS.md",
}

// FileKeys returns fileKeys with the defaults that DefaultConfig gives
// filled in.
func FileKeys() []FileKey {
	def := DefaultConfig()
	out := slices.Clone(fileKeys)
	for i, k := range out {
		if v, ok := fileKeyDefault(def, k.Path); ok {
			out[i].Default = v
		}
	}
	return out
}

// IsFileKey reports whether path is a file-only key, or a key inside a
// file-only table. NAME matches any one part of the path.
func IsFileKey(path string) bool {
	parts := strings.Split(path, ".")
	for _, k := range fileKeys {
		kp := strings.Split(k.Path, ".")
		table := k.Type == FileKeyTable || k.Type == FileKeyTables || k.Type == FileKeyMap
		if len(parts) < len(kp) || (!table && len(parts) != len(kp)) {
			continue
		}
		if slices.EqualFunc(kp, parts[:len(kp)], func(a, b string) bool { return a == "NAME" || a == b }) {
			return true
		}
	}
	return false
}

// fileKeyDefault is the value that DefaultConfig sets at path, as the file
// writes it. ok is false for a path under a NAME, and for a field that
// DefaultConfig leaves unset or empty.
func fileKeyDefault(cfg *UserConfig, path string) (string, bool) {
	if strings.Contains(path, "NAME") {
		return "", false
	}
	v := reflect.ValueOf(cfg).Elem()
	for seg := range strings.SplitSeq(path, ".") {
		for v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return "", false
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return "", false
		}
		f, ok := fieldByTOMLNameDeep(v, seg)
		if !ok {
			return "", false
		}
		v = f
	}
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Bool:
		if !v.Bool() {
			return "", false
		}
		return "true", true
	case reflect.Int, reflect.Int64:
		if v.Int() == 0 {
			return "", false
		}
		return strconv.FormatInt(v.Int(), 10), true
	case reflect.String:
		if v.String() == "" {
			return "", false
		}
		return v.String(), true
	case reflect.Slice:
		if v.Len() == 0 {
			return "", false
		}
		items := make([]string, v.Len())
		for i := range v.Len() {
			items[i] = fmt.Sprint(v.Index(i).Interface())
		}
		return tomlList(items), true
	}
	return "", false
}

// fieldByTOMLNameDeep is fieldByTOMLName, inside embedded structs too.
func fieldByTOMLNameDeep(v reflect.Value, name string) (reflect.Value, bool) {
	if f, ok := fieldByTOMLName(v, name); ok {
		return f, true
	}
	for i := range v.NumField() {
		sf := v.Type().Field(i)
		if !sf.Anonymous || sf.Type.Kind() != reflect.Struct {
			continue
		}
		if f, ok := fieldByTOMLNameDeep(v.Field(i), name); ok {
			return f, true
		}
	}
	return reflect.Value{}, false
}
