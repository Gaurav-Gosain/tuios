package herdrcli

// The help text of each command group: herdr's own usage lines at v0.9.3
// (print_*_help in src/cli), so a person who reads them sees the grammar
// herdr documents.

const topHelp = `herdr ` + Version + `+tuios: herdr's command line, answered by tuios

This herdr runs in a tuios pane through HERDR_BIN_PATH. It sends herdr's
socket commands to tuios, which answers them as herdr does. It does not start
herdr's app.

Usage:
  herdr pane <subcommand> ...
  herdr tab <subcommand> ...
  herdr workspace <subcommand> ...
  herdr agent <subcommand> ...
  herdr worktree <subcommand> ...
  herdr notification show <title> ...
  herdr api snapshot
  herdr server reload-config
  herdr terminal title set <title>
  herdr status [server|client] [--json]
  herdr --version

Run herdr <command> help for the subcommands of one command.
Commands that act on herdr's own machine answer error unsupported.`

var groupHelp = map[string]string{
	"status": statusUsage,
	"pane": `herdr pane commands:
  herdr pane list [--workspace <workspace_id>]
  herdr pane current [--pane ID|--current]
  herdr pane get <pane_id>
  herdr pane layout [--pane ID|--current]
  herdr pane process-info [--pane ID|--current]
  herdr pane neighbor --direction left|right|up|down [--pane ID|--current]
  herdr pane edges [--pane ID|--current]
  herdr pane focus --direction left|right|up|down [--pane ID|--current]
  herdr pane resize --direction left|right|up|down [--amount FLOAT] [--pane ID|--current]
  herdr pane zoom [<pane_id>|--pane ID|--current] [--toggle|--on|--off]
  herdr pane rename <pane_id> <label>|--clear
  herdr pane read <pane_id> [--source visible|recent|recent-unwrapped] [--lines N] [--format text|ansi] [--ansi]
  herdr pane input [<pane_id>|--pane ID|--current] --right-click herdr|pane
  herdr pane split [<pane_id>|--pane ID|--current] --direction right|down [--ratio FLOAT] [--cwd PATH] [--env KEY=VALUE] [--right-click herdr|pane] [--focus] [--no-focus]
  herdr pane swap --direction left|right|up|down [--pane ID|--current]
  herdr pane swap --source-pane ID --target-pane ID
  herdr pane move <pane_id> --tab <tab_id> --split right|down [--target-pane ID] [--ratio FLOAT] [--focus|--no-focus]
  herdr pane move <pane_id> --new-tab [--workspace ID] [--label TEXT] [--focus|--no-focus]
  herdr pane move <pane_id> --new-workspace [--label TEXT] [--tab-label TEXT] [--focus|--no-focus]
  herdr pane close <pane_id>
  herdr pane send-text <pane_id> <text>
  herdr pane send-keys <pane_id> <key> [key ...]
  herdr pane wait-output <pane_id> (--match TEXT | --regex PATTERN) [--source visible|recent|recent-unwrapped] [--lines N] [--timeout MS] [--raw]
  herdr pane report-agent <pane_id> --source ID --agent LABEL --state idle|working|blocked|unknown [--message TEXT] [--seq N] [--agent-session-id ID] [--agent-session-path PATH]
  herdr pane report-agent-session <pane_id> --source ID --agent LABEL [--seq N] [--agent-session-id ID] [--agent-session-path PATH]
  herdr pane release-agent <pane_id> --source ID --agent LABEL [--seq N]
  herdr pane report-metadata <pane_id> --source ID [--agent LABEL] [--applies-to-source ID] [--title TEXT|--clear-title] [--display-agent TEXT|--clear-display-agent] [--state-label STATUS=TEXT] [--clear-state-labels] [--token NAME=VALUE] [--clear-token NAME] [--seq N] [--ttl-ms N]
  herdr pane run <pane_id> <command>`,
	"tab": `herdr tab commands:
  herdr tab list [--workspace <workspace_id>]
  herdr tab create [--workspace <workspace_id>] [--cwd PATH] [--label TEXT] [--env KEY=VALUE] [--focus] [--no-focus]
  herdr tab get <tab_id>
  herdr tab focus <tab_id>
  herdr tab rename <tab_id> <label>
  herdr tab close <tab_id>`,
	"workspace": `herdr workspace commands:
  herdr workspace list
  herdr workspace create [--cwd PATH] [--label TEXT] [--env KEY=VALUE] [--focus] [--no-focus]
  herdr workspace get <workspace_id>
  herdr workspace focus <workspace_id>
  herdr workspace rename <workspace_id> <label>
  herdr workspace report-metadata <workspace_id> --source ID [--token NAME=VALUE] [--clear-token NAME] [--seq N] [--ttl-ms N]
  herdr workspace close <workspace_id> [--group]`,
	"agent": `herdr agent commands:
  herdr agent list
  herdr agent get <target>
  herdr agent read <target> [--source visible|recent|recent-unwrapped|detection] [--lines N] [--format text|ansi] [--ansi]
  herdr agent send-keys <target> <key> [key ...]
  herdr agent prompt <target> <text> [--wait] [--until STATUS]... [--timeout MS]
  herdr agent rename <target> <name>|--clear
  herdr agent focus <target>
  herdr agent wait <target> [--until STATUS]... [--timeout MS]
  herdr agent attach <target> [--takeover]
  herdr agent start <name> --kind KIND --pane ID [--timeout MS] [-- <agent-args...>]
  herdr agent explain <target> [--json|--format text|json] [--verbose]
  targets accept unique agent names and pane ids that currently host agents`,
	"worktree": `herdr worktree commands:
  herdr worktree list [--workspace ID | --cwd PATH] [--trust-repository]
  herdr worktree create [--workspace ID | --cwd PATH] [--branch NAME] [--base REF] [--path PATH] [--label TEXT] [--focus] [--no-focus] [--trust-repository]
  herdr worktree open [--workspace ID | --cwd PATH] (--path PATH | --branch NAME) [--label TEXT] [--focus] [--no-focus] [--trust-repository]
  herdr worktree remove --workspace ID [--force] [--trust-repository]`,
	"notification": `herdr notification commands:
  herdr notification show <title> [--body TEXT] [--position top-left|top-right|bottom-left|bottom-right] [--sound none|done|request]`,
	"api": `herdr api commands:
  herdr api snapshot
  herdr api schema [--json | --output PATH]`,
	"terminal": `herdr terminal commands:
  herdr terminal title set <title>
  herdr terminal title clear
  herdr terminal attach and herdr terminal session use herdr's client protocol, which tuios does not serve`,
	"server": `herdr server commands:
  herdr server reload-config
  herdr server agent-manifests [--json]
  herdr server reload-agent-manifests`,
}
