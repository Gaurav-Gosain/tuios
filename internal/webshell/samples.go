package webshell

import "strings"

// sample is what a tuios subcommand that needs a real machine prints there,
// shown in the demo so a lesson about it has something to look at. The text
// follows the docs page it links to.
type sample struct {
	doc  string // the docs page, under tuios.gaurav.zip/docs/
	text string
}

// samples maps a tuios subcommand to its sample. Aliases share an entry.
var samples = map[string]sample{
	"ls": {"sessions", `╭──────┬─────────┬──────────┬─────────────┬─────────────╮
│ NAME │ WINDOWS │ STATUS   │ CREATED     │ LAST ACTIVE │
├──────┼─────────┼──────────┼─────────────┼─────────────┤
│ work │ 3       │ detached │ 2 hours ago │ 5 mins ago  │
│ dev  │ 2       │ attached │ 1 day ago   │ just now    │
╰──────┴─────────┴──────────┴─────────────┴─────────────╯`},
	"attach": {"sessions", `Attached to work. Your 3 windows are where you left them.`},
	"list-agents": {"agent-messaging", `╭──────────┬────────┬─────────────┬─────────────┬────────┬──────┬───────────────────╮
│ ID       │ NAME   │ STATE       │ HARNESS     │ SOURCE │ MAIL │ NOTE              │
├──────────┼────────┼─────────────┼─────────────┼────────┼──────┼───────────────────┤
│ c7be946f │ review │ needs_input │ claude-code │ report │ 1    │ which retry?      │
│ 29f0307b │ tests  │ working     │ codex       │ report │ 0    │ running the suite │
╰──────────┴────────┴─────────────┴─────────────┴────────┴──────┴───────────────────╯`},
	"send-agent-message": {"agent-messaging", `Sent to review (c7be946f). It is #2 in its inbox.`},
	"fan": {"worktrees", `Started 3 agents on fan/dark-mode. Each prompt is sent when its agent is ready.
  web-fan-dark-mode    fan/dark-mode    ~/.local/share/tuios/worktrees/web/fan-dark-mode
  web-fan-dark-mode-2  fan/dark-mode-2  ~/.local/share/tuios/worktrees/web/fan-dark-mode-2
  web-fan-dark-mode-3  fan/dark-mode-3  ~/.local/share/tuios/worktrees/web/fan-dark-mode-3`},
	"worktree": {"worktrees", `Created branch feat/retry in ~/.local/share/tuios/worktrees/api/feat-retry.
Created session 'api-feat-retry'.`},
	"list-verbs": {"control-protocol", `tuios control protocol, version 1
  list-sessions    the sessions on this daemon
  new-window       open a window in a session
  send-text        type into a pane
  capture-pane     read what a pane shows
  wait-for         block until a pane prints, exits or goes quiet
  set-agent-state  report a pane's agent state
  subscribe        stream events as JSON lines
  ...and more. tuios list-verbs <verb> shows one.`},
	"list-hooks": {"hooks", `╭───────────────────┬────────┬──────┬──────┬───────────┬────────────────────────╮
│ EVENT             │ SIDE   │ RUNS │ EXIT │ LAST      │ COMMAND                │
├───────────────────┼────────┼──────┼──────┼───────────┼────────────────────────┤
│ after-new-window  │ daemon │ 4    │ 0    │ 2 min ago │ notify-send 'new pane' │
│ after-agent-state │ daemon │ 12   │ 0    │ just now  │ ~/bin/ping-phone.sh    │
╰───────────────────┴────────┴──────┴──────┴───────────┴────────────────────────╯`},
}

func init() {
	samples["list-sessions"] = samples["ls"]
	samples["a"] = samples["attach"]
}

// printSample shows a subcommand's sample, framed so it is plain that it
// came from a real machine and not from this tab.
func printSample(t *TTY, sub string) bool {
	s, ok := samples[sub]
	if !ok {
		return false
	}
	t.Print(dim + "On a real machine, this prints:" + reset + "\r\n")
	t.Print(strings.ReplaceAll(s.text, "\n", "\r\n") + "\r\n")
	t.Print(dim + "More: " + reset + cyan + "tuios.gaurav.zip/docs/" + s.doc + reset + "\r\n")
	return true
}
