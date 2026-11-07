package main

import (
	"fmt"
	"io"
	"os"
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
	cmd := &cobra.Command{
		Use:   "dispatch [status | pause | resume | mode manual|pull | quiet SPAN|off]",
		Short: "Show or change how task cards go to agents",
		Long: `A board is a project's cards (--project) or a session's own cards. Its dispatch
mode is manual, where a person starts each card, or pull, where an agent that
finishes its card is given the board's next open card: the oldest in todo,
plan or doing that no pane holds, not blocked, not marked --ask-first, and for that
agent's harness or any. The agent hears of it as mail from tuios. Dispatch
types nothing into a pane, and gives no card to a pane that waits for you.

The safety switches:
  pause            stop all dispatch at once (any pane may pause)
  resume           start it again, and lift every stop below
  quiet 22:00-07:00  give no card in these hours, local time (off to clear)
A board stops by itself after 3 dispatched cards in a row end without being
finished, and all dispatch stops when an agent reports a usage limit
(tuios set-agent-state errored --kind usage_limit). Only resume starts it.

From inside a pane, every action but pause needs the admin grant.`,
		Example: `  # Pull mode for this repository's board
  tuios task dispatch mode pull --project .

  # Stop everything now, and start it again later
  tuios task dispatch pause
  tuios task dispatch resume

  # No new work at night
  tuios task dispatch quiet 22:00-07:00`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			verb, params := "get-task-dispatch", map[string]any{}
			action := "status"
			if len(args) > 0 {
				action = args[0]
			}
			switch action {
			case "status":
			case "pause", "resume":
				verb, params["action"] = "set-task-dispatch", action
			case "mode", "quiet":
				if len(args) != 2 {
					return errfmt.Errorf("tuios task dispatch %s takes one value", action)
				}
				verb, params["action"] = "set-task-dispatch", action
				params[action] = args[1]
			default:
				return errfmt.Errorf("unknown dispatch action %q: status, pause, resume, mode or quiet", action)
			}
			if len(args) == 2 && action != "mode" && action != "quiet" {
				return errfmt.Errorf("tuios task dispatch %s takes no value", action)
			}
			if project != "" {
				url, err := projectFlag(project)
				if err != nil {
					return err
				}
				params["project"] = url
			}
			return runTaskDispatch(os.Stdout, f, verb, params)
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&project, "project", "", "The project board, by origin URL, or . for this folder's (default: the session's own cards)")
	cmd.ValidArgs = []string{"status", "pause", "resume", "mode", "quiet"}
	return cmd
}

// runTaskDispatch calls verb and prints the status it returns.
func runTaskDispatch(w io.Writer, f taskFlags, verb string, params map[string]any) error {
	t, err := dialSessionTarget(f.session)
	if err != nil {
		return err
	}
	defer t.Close()
	if f.session != "" || params["action"] == "mode" {
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
