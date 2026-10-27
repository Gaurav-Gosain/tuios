package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// newTaskCommand builds `tuios task`: the session's task cards, one unit of
// work each, which panes claim, finish and let go.
func newTaskCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Hand out work as task cards that panes claim and finish",
		Long: `Task cards hold the work of a session, one unit each: a title, the prompt for
the pane that takes it, a stage, and the pane that holds it. The daemon keeps
them, so they outlive the pane that added them, and they come back after a
restart with every holder let go.

A pane claims a card with 'tuios task claim --next' and finishes it with
'tuios task done'. Two panes that claim at once take two cards. A card is let
go when its pane closes or its agent leaves the pane, and a conductor hears
it on 'tuios subscribe --types task-changed'.

The stages are todo, plan, doing, verify, review, shipped and done. A claim
moves a card in todo or plan to doing; 'tuios task stage' moves it by hand.

From inside a pane, a card needs the task grant to change, a pane claims only
for itself, and it changes only the card it holds unless it holds admin.`,
		Example: `  # A conductor adds the work
  tuios task add -p 'Read api/retry.go and add a jitter option, with a test.' make the backoff jitter configurable

  # A worker pane takes the oldest open card, and finishes it
  tuios task claim --next
  tuios task done t1

  # What there is, and one card whole
  tuios task ls --open
  tuios task get t1`,
	}
	cmd.AddCommand(
		newTaskAddCommand(), newTaskListCommand(), newTaskGetCommand(), newTaskClaimCommand(),
		newTaskDoneCommand(), newTaskStageCommand(), newTaskBlockCommand(), newTaskReleaseCommand(),
		newTaskRemoveCommand(),
	)
	return cmd
}

// taskFlags are the flags every task subcommand takes.
type taskFlags struct {
	session string
	json    bool
}

func (f *taskFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.session, "session", "s", "", "Session of the cards (default: this pane's, else the most recently active)")
	cmd.Flags().BoolVar(&f.json, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
}

// taskRow is one card as the task verbs return it.
type taskRow struct {
	Session string `json:"session"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Preview string `json:"preview"`
	Stage   string `json:"stage"`
	StageAt int64  `json:"stage_at"`
	Holder  *struct {
		Window string `json:"window"`
		Name   string `json:"name"`
		State  string `json:"state"`
	} `json:"holder"`
	By      string `json:"by"`
	At      int64  `json:"at"`
	Blocked *struct {
		Reason string `json:"reason"`
		By     string `json:"by"`
	} `json:"blocked"`
	Prompt  string `json:"prompt"`
	History []struct {
		Action string `json:"action"`
		By     string `json:"by"`
		At     int64  `json:"at"`
		Stage  string `json:"stage"`
		Holder string `json:"holder"`
		Reason string `json:"reason"`
	} `json:"history"`
}

// holderLabel is the pane that holds the card, as one plain word.
func (r taskRow) holderLabel() string {
	if r.Holder == nil {
		return "-"
	}
	if r.Holder.Name != "" {
		return plainLine(r.Holder.Name)
	}
	return shortWindowID(r.Holder.Window)
}

// byLabel shortens a pane's window id, as queue ls does.
func byLabel(by string) string {
	if len(by) > 12 && !strings.Contains(by, ":") {
		return shortWindowID(by)
	}
	return plainLine(by)
}

// callTask calls verb with params on the session the flags name, and prints
// the JSON result, or hands the card's row to describe. window is the pane a
// claim names, resolved with the session, and empty for the other verbs.
func callTask(f taskFlags, window, verb string, params map[string]any, describe func(taskRow, string) string) error {
	t, err := dialTarget(f.session, window)
	if err != nil {
		return err
	}
	defer t.Close()
	if window != "" {
		// t.params fills a window the caller asks for.
		params["window"] = nil
	}
	raw, err := t.client.Call(verb, t.params(params))
	if err != nil {
		return reportVerbError(t.explain(verb, err), f.json)
	}
	if f.json {
		return printVerbResultOn(t, raw, true)
	}
	var row taskRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Println(describe(row, t.on()))
	return nil
}

// newTaskAddCommand builds `tuios task add`.
func newTaskAddCommand() *cobra.Command {
	var f taskFlags
	var prompt, stage string
	cmd := &cobra.Command{
		Use:   "add [-p PROMPT] [--stage S] TITLE...",
		Short: "Add a task card",
		Long: `Add a task card. The words are joined with spaces into its title. -p gives the
prompt for the pane that takes it, at most 16 KiB; '-p -' reads it from
standard input. The card starts in todo unless --stage says otherwise.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if prompt == "-" {
				b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
				if err != nil {
					return fmt.Errorf("failed to read the prompt: %w", err)
				}
				prompt = string(b)
			}
			params := map[string]any{"title": strings.Join(args, " ")}
			if prompt != "" {
				params["prompt"] = prompt
			}
			if stage != "" {
				params["stage"] = stage
			}
			return callTask(f, "", "add-task", params, func(r taskRow, on string) string {
				return fmt.Sprintf("Added %s%s: %s", plainLine(r.ID), on, plainLine(r.Title))
			})
		},
	}
	f.register(cmd)
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "What the pane that takes the card is to do, or - for standard input")
	cmd.Flags().StringVar(&stage, "stage", "", "The stage it starts in: "+strings.Join(session.TaskStages, ", "))
	_ = cmd.RegisterFlagCompletionFunc("stage", cobra.FixedCompletions(session.TaskStages, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// newTaskListCommand builds `tuios task ls`.
func newTaskListCommand() *cobra.Command {
	var f taskFlags
	var stage string
	var open, allSessions bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the task cards",
		Long: `List the session's task cards, oldest first: each card's id, stage, the pane
that holds it, its age, and its title. A blocked card is marked with !.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if allSessions && f.session != "" {
				return fmt.Errorf("--all-sessions lists every session, so it takes no --session")
			}
			return runTaskList(os.Stdout, f, stage, open, allSessions)
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&stage, "stage", "", "Only the cards in this stage")
	cmd.Flags().BoolVar(&open, "open", false, "Leave out the cards that are done")
	cmd.Flags().BoolVar(&allSessions, "all-sessions", false, "List the cards of every session on this machine")
	_ = cmd.RegisterFlagCompletionFunc("stage", cobra.FixedCompletions(session.TaskStages, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func runTaskList(w io.Writer, f taskFlags, stage string, open, allSessions bool) error {
	t, err := dialSessionTarget(f.session)
	if err != nil {
		return err
	}
	defer t.Close()
	params := t.params(nil)
	if allSessions {
		// Every session, so no session is named, whatever TUIOS_SESSION says.
		params = map[string]any{"all_sessions": true}
	}
	if stage != "" {
		params["stage"] = stage
	}
	if open {
		params["open"] = true
	}
	raw, err := t.client.Call("list-tasks", params)
	if err != nil {
		return reportVerbError(t.explain("list-tasks", err), f.json)
	}
	if f.json {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		Session     string    `json:"session"`
		AllSessions bool      `json:"all_sessions"`
		Tasks       []taskRow `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if res.AllSessions {
		return printTaskRows(w, "any session"+t.on(), res.Tasks, true)
	}
	return printTaskRows(w, "session "+res.Session+t.on(), res.Tasks, false)
}

// printTaskRows writes list-tasks' cards as a table. Every field but the id,
// stage and age came from a caller, so each is kept to one plain line.
// withSession adds a SESSION column first.
func printTaskRows(w io.Writer, where string, rows []taskRow, withSession bool) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintf(w, "No task cards in %s.\n", plainLine(where))
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if withSession {
		fmt.Fprint(tw, "SESSION\t")
	}
	fmt.Fprintln(tw, "ID\tSTAGE\tHOLDER\tAGE\tTITLE")
	for _, r := range rows {
		stage := plainLine(r.Stage)
		if r.Blocked != nil {
			stage += " !"
		}
		if withSession {
			fmt.Fprintf(tw, "%s\t", plainLine(r.Session))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", plainLine(r.ID), stage, r.holderLabel(), agoOf(r.At), plainLine(r.Title))
	}
	return tw.Flush()
}

// newTaskGetCommand builds `tuios task get`.
func newTaskGetCommand() *cobra.Command {
	var f taskFlags
	cmd := &cobra.Command{
		Use:   "get ID",
		Short: "Show one task card whole",
		Long:  `Show one task card: its stage, holder, block, prompt and history.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return callTask(f, "", "get-task", map[string]any{"id": args[0]}, describeTask)
		},
	}
	f.register(cmd)
	return cmd
}

// describeTask is what task get prints.
func describeTask(r taskRow, on string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s: %s\n", plainLine(r.ID), on, plainLine(r.Title))
	fmt.Fprintf(&b, "  stage   %s\n", plainLine(r.Stage))
	fmt.Fprintf(&b, "  holder  %s\n", r.holderLabel())
	fmt.Fprintf(&b, "  added   %s by %s\n", agoOf(r.At), byLabel(r.By))
	if r.Blocked != nil {
		fmt.Fprintf(&b, "  blocked %s (by %s)\n", plainLine(r.Blocked.Reason), byLabel(r.Blocked.By))
	}
	if r.Prompt != "" {
		b.WriteString("\nPrompt:\n")
		for line := range strings.SplitSeq(strings.TrimRight(r.Prompt, "\n"), "\n") {
			fmt.Fprintf(&b, "  %s\n", plainLine(line))
		}
	}
	if len(r.History) > 0 {
		b.WriteString("\nHistory:\n")
		for _, h := range r.History {
			line := fmt.Sprintf("  %s  %-9s by %s", agoOf(h.At), plainLine(h.Action), byLabel(h.By))
			if h.Reason != "" {
				line += ": " + plainLine(h.Reason)
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// newTaskClaimCommand builds `tuios task claim`.
func newTaskClaimCommand() *cobra.Command {
	var f taskFlags
	var next bool
	var window string
	cmd := &cobra.Command{
		Use:   "claim [ID | --next] [-w PANE]",
		Short: "Take a task card for a pane",
		Long: `Take a task card for a pane: the card by its id, or with --next the oldest card
no pane holds that is not blocked or done. From inside a pane the card is taken
for that pane; outside one, name the pane with -w.

The first line printed is the card's id and its title, separated by a space,
so a script can read it: id=$(tuios task claim --next | cut -d' ' -f1). With
nothing to take, it fails with no_task.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			switch {
			case next && len(args) > 0:
				return errors.New("pass an id or --next, not both")
			case !next && len(args) == 0:
				return errors.New("name the card to claim by its id (see tuios task ls), or pass --next")
			}
			params := map[string]any{}
			if next {
				params["next"] = true
			} else {
				params["id"] = args[0]
			}
			return callTask(f, window, "claim-task", params, func(r taskRow, _ string) string {
				out := plainLine(r.ID) + " " + plainLine(r.Title)
				if r.Preview != "" {
					// The whole prompt is tuios task get's.
					out += "\n" + plainLine(r.Preview)
				}
				return out
			})
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVar(&next, "next", false, "Take the oldest open card no pane holds")
	cmd.Flags().StringVarP(&window, "window", "w", "", "The pane that takes the card, by name or id (default: this pane)")
	return cmd
}

// taskIDCommand builds a subcommand that names one card and calls verb.
func taskIDCommand(use, short, long, verb string, describe func(taskRow, string) string) *cobra.Command {
	var f taskFlags
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return callTask(f, "", verb, map[string]any{"id": args[0]}, describe)
		},
	}
	f.register(cmd)
	return cmd
}

// newTaskDoneCommand builds `tuios task done`.
func newTaskDoneCommand() *cobra.Command {
	return taskIDCommand("done ID", "Mark a task card done",
		`Mark a task card done. Its holder lets it go and its block, if any, is lifted.
From inside a pane, only the card the pane holds.`,
		"finish-task", func(r taskRow, on string) string {
			return fmt.Sprintf("Finished %s%s: %s", plainLine(r.ID), on, plainLine(r.Title))
		})
}

// newTaskReleaseCommand builds `tuios task release`.
func newTaskReleaseCommand() *cobra.Command {
	return taskIDCommand("release ID", "Let a task card go without finishing it",
		`Let a task card go without finishing it. It keeps its stage, and another pane
can claim it. From inside a pane, only the card the pane holds.`,
		"release-task", func(r taskRow, on string) string {
			return fmt.Sprintf("Released %s%s. It stays in %s.", plainLine(r.ID), on, plainLine(r.Stage))
		})
}

// newTaskRemoveCommand builds `tuios task rm`.
func newTaskRemoveCommand() *cobra.Command {
	var f taskFlags
	cmd := &cobra.Command{
		Use:   "rm ID",
		Short: "Remove a task card",
		Long:  `Remove a task card and its history. From inside a pane this needs the admin grant.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			t, err := dialSessionTarget(f.session)
			if err != nil {
				return err
			}
			defer t.Close()
			raw, err := t.client.Call("remove-task", t.params(map[string]any{"id": args[0]}))
			if err != nil {
				return reportVerbError(t.explain("remove-task", err), f.json)
			}
			if f.json {
				return printVerbResultOn(t, raw, true)
			}
			fmt.Printf("Removed %s%s.\n", plainLine(args[0]), t.on())
			return nil
		},
	}
	f.register(cmd)
	return cmd
}

// newTaskStageCommand builds `tuios task stage`.
func newTaskStageCommand() *cobra.Command {
	var f taskFlags
	cmd := &cobra.Command{
		Use:   "stage ID STAGE",
		Short: "Move a task card to a stage",
		Long: `Move a task card to a stage by hand: ` + strings.Join(session.TaskStages, ", ") + `. A move to
done lets the holder go. From inside a pane, only the card the pane holds.`,
		Args: cobra.ExactArgs(2),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 1 {
				return session.TaskStages, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(_ *cobra.Command, args []string) error {
			return callTask(f, "", "set-task-stage", map[string]any{"id": args[0], "stage": args[1]}, func(r taskRow, on string) string {
				return fmt.Sprintf("Moved %s%s to %s.", plainLine(r.ID), on, plainLine(r.Stage))
			})
		},
	}
	f.register(cmd)
	return cmd
}

// newTaskBlockCommand builds `tuios task block`.
func newTaskBlockCommand() *cobra.Command {
	var f taskFlags
	var clear bool
	cmd := &cobra.Command{
		Use:   "block ID [REASON...] [--clear]",
		Short: "Mark a task card stuck, or lift the mark",
		Long: `Mark a task card stuck, with what it waits on, or with --clear lift the mark. A
blocked card keeps its holder and its stage, and claim --next skips it. To let
a card go, use 'tuios task release'. From inside a pane, only the card the pane
holds.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			reason := strings.Join(args[1:], " ")
			switch {
			case clear && reason != "":
				return errors.New("pass a reason or --clear, not both")
			case !clear && reason == "":
				return errors.New("say what the card waits on, or pass --clear")
			}
			params := map[string]any{"id": args[0]}
			if clear {
				params["clear"] = true
			} else {
				params["reason"] = reason
			}
			return callTask(f, "", "block-task", params, func(r taskRow, on string) string {
				if r.Blocked == nil {
					return fmt.Sprintf("%s%s is not blocked.", plainLine(r.ID), on)
				}
				return fmt.Sprintf("Blocked %s%s: %s", plainLine(r.ID), on, plainLine(r.Blocked.Reason))
			})
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVar(&clear, "clear", false, "Lift the block")
	return cmd
}
