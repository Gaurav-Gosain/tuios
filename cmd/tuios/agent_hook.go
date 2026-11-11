package main

import (
	"os"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/agenthook"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newAgentHookCommand() *cobra.Command {
	var o agenthook.Options
	cmd := &cobra.Command{
		Use:   "agent-hook <harness> [event] [payload]",
		Short: "Report a harness hook event as the pane's agent state",
		Long: `Report a coding-agent harness's hook event to the tuios pane it runs in.

This is what the hooks tuios integration install writes run. The harness
hands its hook payload on stdin (or, for the Codex notify command, as the last
argument). The event comes from the payload, or from the event argument when
the payload does not name it. Supported harnesses: claude-code, codex,
copilot, cursor-agent, gemini-cli, opencode, amp, kilo, kimi, omp, pi and qwen,
which report the pane's state. antigravity, crush, devin, droid, grok,
hermes and qoder, which report only the conversation id (set-agent-session)
and leave the state to the pane's screen rules.

The pane is found from --window, then TUIOS_PANE_ID, then the process's
controlling terminal, then its parent processes, so a harness or sandbox
that scrubs the environment is still reported for. A payload that does not
parse, an event with no mapping, and an event from a harness other than the
one TUIOS_AGENT names are all reported as nothing, never as done. A
subagent's own tool calls never move the pane's state.

It asks the daemon which set-agent-state fields it supports and sends only
those. A conditional report (if_state) is not sent to a daemon older than the
condition, since that daemon would apply it unconditionally.

For claude-code and codex, a prompt, a tool call, its result and the end of a
turn also carry the event itself as activity (and gemini-cli's BeforeTool its
tool name), which the daemon keeps in the pane's activity ring for
'tuios agent-log' and the rail's "now" line. Only the first line of any text is
sent, with likely secrets masked. A Stop's done report says the first line of
what the agent said last.

Claude Code's SubagentStart and SubagentStop report no state. Each sends the
subagent with report-agent-activity, and the daemon keeps the pane's
subagents: the count of those started and not yet stopped in the subagents
metadata, and a list of each one's description, tool and end, which the rail
shows on a pane at rest. A subagent's own tool calls, the Agent tool's launch
and result, a TaskStop, and the background_tasks list of a Stop each send a
subagent_update for it. A SessionStart sends its idle report and then a
session_start, which clears them. A daemon without report-agent-activity
gets none of these, and one before subagent_update gets no updates.

A report that ends a turn (done or errored) also sends what the pane's status
line feed (tuios agent-statusline) held back, with set-agent-meta for the same
pane, so the turn's last context and cost reach the rail.

For claude-code and codex, a SessionStart prints four lines that these
harnesses add to the agent's context: the pane and the session the agent runs
in, the commands that reach the other agents and the person, the safety rule,
and "Run tuios --skill for everything else". Claude Code's SessionStart after a
compaction prints them too, and reports no state.

For the same two harnesses, SessionStart, UserPromptSubmit and PostToolUse
also add one line about the pane's unread mail, when there is mail the agent
was not told about: the count, the senders and the command that reads it. It
never holds a subject or a body. Each message is announced once. A
SessionStart announces all unread mail again, since the agent no longer holds
the earlier line. A hook that cannot write its output does not use up the
notice. PostToolUse prints the line as hookSpecificOutput JSON,
because these harnesses ignore its plain output. Outside tuios, and with
--window, the hook prints none of these lines.

It always exits 0, prints nothing a harness would read as an answer (Gemini
CLI gets an empty JSON object), and gives up after 500ms when the daemon is
slow or gone. Use --explain to see on stderr what it decided and why.

The one exception is a permission prompt the Inbox may answer. When
[agents.approvals] in the config names the harness (claude-code, opencode,
kilo or qwen), the hook for Claude Code's or Qwen Code's PermissionRequest, or
for opencode's permission.asked, waits after its report for the person to
answer the Inbox item, for up to hold_seconds (120 by default). A Claude Code plan
(ExitPlanMode) is held the same way unless hold_plans is false. It then prints
the harness's own decision. It prints nothing, and the harness asks in its pane as before,
when the wait ends without an answer, when approvals are off, when the daemon
is gone or restarts, and on any error. It never prints an approval it was not
given.`,
		Example: `  # What a Claude Code hook runs
  tuios agent-hook claude-code --integration 1

  # Try a payload by hand
  echo '{"hook_event_name":"Stop","session_id":"s1"}' | tuios agent-hook claude --explain`,
		Args: cobra.RangeArgs(1, 3),
		RunE: func(_ *cobra.Command, args []string) error {
			agenthook.Run(o, args, agenthook.IO{
				Stdin:    os.Stdin,
				StdinTTY: term.IsTerminal(int(os.Stdin.Fd())),
				Stdout:   os.Stdout,
				Stderr:   os.Stderr,
				Getenv:   os.Getenv,
				Dial: func() (verbCaller, error) {
					// A failed dial returns a nil *VerbClient. Returning it as
					// it is would put a typed nil in the interface, which a
					// later io.Closer check takes for a live client.
					c, err := session.DialVerbClientAs(version)
					if err != nil {
						return nil, err
					}
					return c, nil
				},
				Self:       integration.SelfProcess,
				HarnessPID: integration.HarnessPID,
				StampDir:   statusLineStampDir,
				Now:        time.Now,
			})
			return nil
		},
	}
	addSessionFlag(cmd, &o.Session, "Session of the pane to report for (default: TUIOS_SESSION)")
	addWindowFlag(cmd, &o.Window, "Pane to report for, by window id or name (default: TUIOS_PANE_ID, then the controlling terminal, then the parent processes)")
	cmd.Flags().BoolVar(&o.Explain, "explain", false, "Print what was decided and why to stderr")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", agenthook.Deadline, "Give up after this long")
	cmd.Flags().IntVar(&o.Integration, "integration", 0, "Version marker of a managed hook entry. tuios ignores it")
	_ = cmd.Flags().MarkHidden("integration")
	return cmd
}

// verbCaller is the one method a command needs from a verb client, so a test
// can stand in for the daemon.
type verbCaller = agenthook.Caller
