package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// newTaskDispatchCommand builds `tuios task dispatch`: each board's dispatch
// mode and the switches that stop dispatch.
func newTaskDispatchCommand() *cobra.Command {
	var f taskFlags
	var project string
	var preset session.DispatchPreset
	var capMachine, verifyTimeout int
	cmd := &cobra.Command{
		Use:   "dispatch [status | pause | resume | mode manual|pull|auto | preset | cap N | quiet SPAN|off | verify COMMAND|off]",
		Short: "Show or change how task cards go to agents",
		Long: `A board is a project's cards (--project) or a session's own cards. Its dispatch
mode is manual, where a person starts each card, or pull, where an agent that
finishes its card is given the board's next open card: the oldest in todo,
plan or doing that no pane holds, not blocked, not marked --ask-first, and for that
agent's harness or any. The agent hears of it as mail from tuios. Dispatch
types nothing into a pane, and gives no card to a pane that waits for you.

In auto, tuios starts the preset agent (--agent, --model, --effort,
--permission-mode, --machine) for each open card, in a new worktree. cap
bounds the agents at once (default 3, and --machine-cap 2 on one machine).

The safety switches:
  pause            stop all dispatch at once (any pane may pause)
  resume           start it again, and lift every stop below
  quiet 22:00-07:00  give no card in these hours, local time (off to clear)
A board stops by itself after 3 dispatched cards in a row end without being
finished, and all dispatch stops when an agent reports a usage limit
(tuios set-agent-state errored --kind usage_limit). Only resume starts it.

An agent that is done with its card does not close it. The card goes to
verify when the board has a verify command (verify "go test ./..."), and
tuios runs that command in the card's worktree: a pass moves the card to
review, a fail sends it back to todo with the last line of the output and
one attempt more. With no verify command the card goes to review. You close
it with tuios task done, or it moves to shipped when its pull request merges.

From inside a pane, every action but pause needs the admin grant, and
verify is refused from every pane.`,
		Example: `  # Pull mode for this repository's board
  tuios task dispatch mode pull --project .

  # Stop everything now, and start it again later
  tuios task dispatch pause
  tuios task dispatch resume

  # No new work at night
  tuios task dispatch quiet 22:00-07:00

  # Start an agent for each open card of this repository's board
  tuios task dispatch mode auto --agent claude --effort high --project .
  tuios task dispatch cap 4 --machine-cap 2

  # Run the tests on each card its agent is done with, at most 15 minutes
  tuios task dispatch verify "go test ./..." --verify-timeout 900 --project .`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			verb, params := "get-task-dispatch", map[string]any{}
			action := "status"
			if len(args) > 0 {
				action = args[0]
			}
			switch action {
			case "status":
			case "pause", "resume", "preset":
				verb, params["action"] = "set-task-dispatch", action
			case "cap":
				n, err := strconv.Atoi(args[len(args)-1])
				if len(args) != 2 || err != nil {
					return errfmt.Errorf("tuios task dispatch cap takes a number, such as 3")
				}
				verb, params["action"], params["cap"], params["machine_cap"] = "set-task-dispatch", action, n, capMachine
			case "verify":
				if len(args) != 2 {
					return errfmt.Errorf("tuios task dispatch verify takes one shell line in quotes, or off")
				}
				verb, params["action"], params["verify"], params["verify_timeout"] = "set-task-dispatch", action, args[1], verifyTimeout
			case "mode", "quiet":
				if len(args) != 2 {
					return errfmt.Errorf("tuios task dispatch %s takes one value", action)
				}
				verb, params["action"] = "set-task-dispatch", action
				params[action] = args[1]
			default:
				return errfmt.Errorf("unknown dispatch action %q: status, pause, resume, mode, preset, cap, quiet or verify", action)
			}
			if len(args) == 2 && action != "mode" && action != "quiet" && action != "cap" && action != "verify" {
				return errfmt.Errorf("tuios task dispatch %s takes no value", action)
			}
			if project != "" {
				url, err := projectFlag(project)
				if err != nil {
					return err
				}
				params["project"] = url
			}
			if preset.Agent != "" {
				params["agent"], params["model"], params["effort"], params["permission_mode"], params["machine"] = preset.Agent, preset.Model, preset.Effort, preset.PermissionMode, preset.Machine
			}
			return runTaskDispatch(os.Stdout, f, verb, params)
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&project, "project", "", "The project board, by origin URL, or . for this folder's (default: the session's own cards)")
	cmd.Flags().StringVar(&preset.Agent, "agent", "", "The agent auto starts, such as claude")
	cmd.Flags().StringVar(&preset.Model, "model", "", "Its model")
	cmd.Flags().StringVar(&preset.Effort, "effort", "", "Its effort")
	cmd.Flags().StringVar(&preset.PermissionMode, "permission-mode", "", "Its permission mode (default: its own)")
	cmd.Flags().StringVar(&preset.Machine, "machine", "", "here (default), any, or a linked machine")
	cmd.Flags().IntVar(&capMachine, "machine-cap", 0, "With cap: agents on one machine")
	cmd.Flags().IntVar(&verifyTimeout, "verify-timeout", 0, "With verify: its limit in seconds (default 600)")
	cmd.ValidArgs = []string{"status", "pause", "resume", "mode", "preset", "cap", "quiet", "verify"}
	return cmd
}

// runTaskDispatch calls verb and prints the status it returns.
func runTaskDispatch(w io.Writer, f taskFlags, verb string, params map[string]any) error {
	t, err := dialSessionTarget(f.session)
	if err != nil {
		return err
	}
	defer t.Close()
	if f.session != "" || params["action"] == "mode" || params["action"] == "preset" || params["action"] == "verify" {
		params = t.params(params)
	}
	raw, err := t.client.Call(verb, params)
	if err != nil {
		return reportVerbError(t.explain(verb, err), f.json)
	}
	if f.json {
		return printVerbResultOn(t, raw, true)
	}
	var st session.DispatchStatus
	if err := jsonx.Unmarshal(raw, &st); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	return printDispatch(w, st)
}

// printDispatch writes the status as a person reads it.
func printDispatch(w io.Writer, st session.DispatchStatus) error {
	var b strings.Builder
	switch {
	case st.Paused:
		fmt.Fprintf(&b, "Dispatch: paused by %s\n", byLabel(st.PausedBy))
	case st.Stopped != "":
		fmt.Fprintf(&b, "Dispatch: stopped, %s. Run tuios task dispatch resume to start it again.\n", plainLine(st.Stopped))
	default:
		b.WriteString("Dispatch: on\n")
	}
	if st.Quiet != "" {
		now := ""
		if st.QuietNow {
			now = " (now)"
		}
		fmt.Fprintf(&b, "Quiet hours: %s%s\n", plainLine(st.Quiet), now)
	}
	rows := st.Boards
	if st.Board != nil && !hasBoard(rows, st.Board.Board) {
		rows = append(rows, *st.Board)
	}
	for _, r := range rows {
		line := fmt.Sprintf("  %-6s %s", r.Mode, plainLine(r.Board))
		if r.Stopped != "" {
			line += ", stopped: " + plainLine(r.Stopped)
		} else if r.Failures > 0 {
			line += fmt.Sprintf(", %d failed in a row", r.Failures)
		}
		if pr := r.Preset; pr != nil && r.Mode == session.DispatchAuto {
			where := pr.Machine
			if where == "" || where == "here" {
				where = "this machine"
			}
			line += plainLine(fmt.Sprintf(", starts %s on %s, %d running (cap %d, %d a machine), %d waiting",
				strings.Join(strings.Fields(pr.Agent+" "+pr.Model+" "+pr.Effort+" "+pr.PermissionMode), " "), where, r.Running, st.Cap, st.MachineCap, r.Waiting))
			if r.Note != "" {
				line += " (" + plainLine(r.Note) + ")"
			}
		}
		if r.Verify != "" {
			line += plainLine(fmt.Sprintf(", verify: %s (%ds)", r.Verify, r.VerifyTimeout))
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if len(rows) == 0 {
		b.WriteString("Every board is manual.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// hasBoard reports whether rows has board.
func hasBoard(rows []session.DispatchBoardRow, board string) bool {
	for _, r := range rows {
		if r.Board == board {
			return true
		}
	}
	return false
}
