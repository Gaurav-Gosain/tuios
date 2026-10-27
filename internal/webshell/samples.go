package webshell

import "strings"

// sample is what a tuios subcommand that needs a real machine prints there,
// shown in the demo so a lesson about it has something to look at. The text
// follows the docs page it links to.
type sample struct {
	doc  string // the docs page, under tuios.dev/docs/
	text string
}

// samples maps a tuios subcommand to its sample. Aliases share an entry. A
// key of two words is a subcommand of a group, such as "task add", and a
// group with any such key has no sample of its own. See sampleFor.
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
	"new":         {"sessions", `Created detached session 'work'. Attach with 'tuios attach work'.`},
	"move-window": {"sessions#move-a-window-to-another-session", `moved 77072830 from session work to session review, workspace 1`},
	"note set":    {"sessions", `Set the note of session work: next: run the migration`},
	"note get":    {"sessions", `next: run the migration`},
	"restart-server --dry-run": {"daemon-restart", `Daemon now      0.11.1
After restart   0.11.1   (from /usr/local/bin/tuios)
This restart starts the same version.

These stop:
  claude in api

These come back:
  Each session, with its layout, its pane history and new shells.`},
	"hosts": {"remote-hosts", `╭───────┬───────────────┬────────┬─────────┬───────┬──────────┬─────────────────────────┬──────────╮
│ HOST  │ ADDRESS       │ STATUS │ VERSION │ PROTO │ SESSIONS │ LOAD                    │ LAST OK  │
├───────┼───────────────┼────────┼─────────┼───────┼──────────┼─────────────────────────┼──────────┤
│ build │ you@buildbox  │ up     │ 0.11.1  │ 3     │ 3        │ 0.42 on 16 cores, 12 ms │ just now │
│ pi    │ you@raspberry │ up     │ 0.11.1  │ 3     │ 1        │ 0.08 on 4 cores, 31 ms  │ just now │
╰───────┴───────────────┴────────┴─────────┴───────┴──────────┴─────────────────────────┴──────────╯

2 hosts. Attach a session on a host with 'tuios attach --host NAME SESSION'.`},
	"task add": {"agent-messaging", `Added t1: make the backoff jitter configurable`},
	"task ls": {"agent-messaging", `ID  STAGE  HOLDER  AGE       TITLE
t1  doing  api     just now  make the backoff jitter configurable
t2  todo   -       4m ago    split the retry tests`},
	"task claim": {"agent-messaging", `t1 make the backoff jitter configurable
Read api/retry.go and add a jitter option, with a test.`},
	"look list": {"themes", `ID        NAME           AUTHOR
19000270  Neon 84        Gaurav-Gosain
19000271  Ink            Gaurav-Gosain
19000272  Gruvbox Max    Gaurav-Gosain
19000274  Tokyo Omarchy  Gaurav-Gosain
19000276  Mocha Cozy     Gaurav-Gosain
19000278  Paper          Gaurav-Gosain
19000280  Amber CRT      Gaurav-Gosain`},
}

func init() {
	samples["list-sessions"] = samples["ls"]
	samples["a"] = samples["attach"]
	samples["new-session"] = samples["new"]
	samples["task list"] = samples["task ls"]
	samples["hosts ls"] = samples["hosts"]
	samples["hosts list"] = samples["hosts"]
	for key := range samples {
		if group, _, ok := strings.Cut(key, " "); ok {
			groups[group] = true
		}
	}
}

// groups are the subcommands whose samples are per subcommand of their own.
var groups = map[string]bool{}

// sampleFor finds the sample for tuios args: the subcommand and the word
// after it first, so tuios task add has its own, then the subcommand alone.
// A group's other subcommands have none, so tuios task rm does not print
// what tuios task add prints, and tuios hosts add does not print the table.
func sampleFor(args []string) (sample, bool) {
	sub, next := argOr(args, 1, ""), argOr(args, 2, "")
	if next != "" {
		if s, ok := samples[sub+" "+next]; ok {
			return s, true
		}
		if groups[sub] {
			return sample{}, false
		}
	}
	s, ok := samples[sub]
	return s, ok
}

// printSample shows the sample for tuios args, framed so it is plain that
// it came from a real machine and not from this tab.
func printSample(t *TTY, args []string) bool {
	s, ok := sampleFor(args)
	if !ok {
		return false
	}
	// A table cut at the right edge still reads as a table. A wrapped one
	// does not, so a line too wide for the pane is cut.
	lines := strings.Split(s.text, "\n")
	t.Lay(func(cols int) []string {
		var out []string
		for _, l := range wrapWords("On a real machine, this prints:", cols) {
			out = append(out, dim+l+reset)
		}
		out = append(out, fitLines(lines, cols)...)
		return append(out, wrapTokens([]string{dim + "More:" + reset, cyan + "tuios.dev/docs/" + s.doc + reset}, cols)...)
	})
	return true
}
