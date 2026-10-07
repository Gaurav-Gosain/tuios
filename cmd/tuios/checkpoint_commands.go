package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
	"github.com/spf13/cobra"
)

// newCheckpointCommand builds `tuios checkpoint`: list the checkpoints the
// daemon took of a pane's git work tree, read what one turn changed, and put
// the work tree back to one.
func newCheckpointCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "List, read and restore the checkpoints of an agent's turns",
		Long: `Each time the agent in a pane finishes a turn that changed a file, the daemon
saves the pane's git work tree as a checkpoint: tracked and untracked files,
and not ignored ones. The checkpoint is a commit under
refs/tuios/checkpoints/<pane>/<n>. The index, HEAD, the branch and the stash
do not change.

'tuios checkpoint list' lists them. 'tuios checkpoint diff N' shows what turn
N changed. 'tuios checkpoint restore N' puts the work tree back to checkpoint
N, after it saves the work tree as a safety checkpoint, so you can undo the
restore.

Turn checkpoints off, or set how many a pane keeps, under [agents.checkpoints]
in config.toml.`,
		Example: `  tuios checkpoint list -w build
  tuios checkpoint diff -w build 3
  tuios checkpoint restore -w build 2`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newCheckpointListCommand(), newCheckpointDiffCommand(), newCheckpointRestoreCommand())
	return cmd
}

// checkpointFlags are the flags every checkpoint subcommand takes.
type checkpointFlags struct {
	session, window string
	json            bool
}

func (f *checkpointFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.session, "session", "s", "", "Session of the pane (default: this pane's, else the most recently active)")
	cmd.Flags().StringVarP(&f.window, "window", "w", "", "The pane, by name or id (default: the focused pane)")
	cmd.Flags().BoolVar(&f.json, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
}

// callCheckpointVerb calls verb on the pane the flags name, on this machine
// only, as the review commands do. It returns the raw result, or nil after it
// printed the JSON form.
func callCheckpointVerb(f checkpointFlags, verb string, params map[string]any) (json.RawMessage, string, error) {
	host, sess, _, err := resolveTarget(f.session, f.window)
	if err != nil {
		return nil, "", err
	}
	if host != "" {
		return nil, "", &diagnosticError{
			What:  fmt.Sprintf("checkpoints work on this machine's sessions, and %s:%s is on %s.", host, sess, host),
			Cause: "checkpoints of a session on a linked machine are not supported yet.",
			Fix:   fmt.Sprintf("attach to %s and run 'tuios checkpoint' there.", host),
		}
	}
	t, err := dialTarget(f.session, f.window)
	if err != nil {
		return nil, "", err
	}
	defer t.Close()
	if params == nil {
		params = map[string]any{}
	}
	params["window"] = ""
	params = t.params(params)
	if t.window == "" {
		delete(params, "window")
	}
	raw, err := t.client.Call(verb, params)
	if err != nil {
		return nil, "", reportVerbError(t.explain(verb, err), f.json)
	}
	if f.json {
		return nil, "", printVerbResultOn(t, raw, true)
	}
	return raw, t.on(), nil
}

// checkpointN reads a checkpoint number argument.
func checkpointN(arg string) (int, error) {
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%q is not a checkpoint number. Give a number from 1, as tuios checkpoint list shows it", arg)
	}
	return n, nil
}

// checkpointWhat is a checkpoint's label column: its label, or what it is.
func checkpointWhat(cp worktree.Checkpoint) string {
	switch {
	case cp.Kind == worktree.CheckpointSafety && cp.Label != "":
		return "safety: " + plainLine(cp.Label)
	case cp.Kind == worktree.CheckpointSafety:
		return "safety"
	case cp.Label != "":
		return plainLine(cp.Label)
	}
	return "-"
}

// newCheckpointListCommand builds `tuios checkpoint list`.
func newCheckpointListCommand() *cobra.Command {
	var f checkpointFlags
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the checkpoints of a pane, oldest first",
		Long: `List the checkpoints of a pane, oldest first: the number, the turn it ended,
the state the agent was in, when it was taken, and its label (the prompt of
the turn, when the agent reported one). A safety checkpoint is the work tree
as it was before a restore.`,
		Example: `  tuios checkpoint list
  tuios checkpoint list -s work -w build --json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			raw, on, err := callCheckpointVerb(f, "list-checkpoints", nil)
			if err != nil || raw == nil {
				return err
			}
			var res struct {
				Session     string                `json:"session"`
				Window      string                `json:"window"`
				Worktree    string                `json:"worktree"`
				Enabled     bool                  `json:"enabled"`
				Checkpoints []worktree.Checkpoint `json:"checkpoints"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			return printCheckpointList(os.Stdout, res.Session, res.Window, res.Worktree, on, res.Enabled, res.Checkpoints)
		},
	}
	f.add(cmd)
	return cmd
}

func printCheckpointList(w io.Writer, sess, window, root, on string, enabled bool, list []worktree.Checkpoint) error {
	fmt.Fprintf(w, "Checkpoints of %s, pane %s%s, in %s: %d\n", plainLine(sess), shortWindowID(window), on, plainLine(root), len(list))
	if !enabled {
		fmt.Fprintln(w, "Checkpoints are off. Set agents.checkpoints.enabled = true in config.toml to take them.")
	}
	if len(list) == 0 {
		_, err := fmt.Fprintln(w, "No checkpoints yet. One is taken when the agent finishes a turn that changed a file.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "N\tTURN\tSTATE\tTAKEN\tCOMMIT\tLABEL")
	for _, cp := range list {
		taken := "-"
		if cp.At != 0 {
			taken = time.Unix(0, cp.At).Local().Format("15:04:05")
		}
		state := plainLine(cp.State)
		if state == "" {
			state = "-"
		}
		commit := cp.Commit
		if len(commit) > 7 {
			commit = commit[:7]
		}
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%s\t%s\n", cp.N, cp.Turn, state, taken, commit, checkpointWhat(cp))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// The untracked files a checkpoint left out are not undone by a restore
	// of it, so the list says which they are.
	for _, cp := range list {
		if cp.SkippedCount == 0 && len(cp.Skipped) == 0 {
			continue
		}
		n := max(cp.SkippedCount, len(cp.Skipped))
		names := make([]string, len(cp.Skipped))
		for i, p := range cp.Skipped {
			names[i] = plainLine(p)
		}
		more := ""
		if n > len(names) {
			more = fmt.Sprintf(", and %d more", n-len(names))
		}
		fmt.Fprintf(w, "Checkpoint %d left out %d untracked %s over agents.checkpoints.max_untracked_mb: %s%s\n",
			cp.N, n, plural.Word(n, "file", "files"), strings.Join(names, ", "), more)
	}
	return nil
}

// newCheckpointDiffCommand builds `tuios checkpoint diff`.
func newCheckpointDiffCommand() *cobra.Command {
	var f checkpointFlags
	var paths []string
	var stat bool
	var context int
	cmd := &cobra.Command{
		Use:   "diff [N]",
		Short: "Show what one turn changed",
		Long: `Show what turn N changed: checkpoint N against the pane's checkpoint before it.
The first checkpoint is shown against HEAD as it was when it was taken. Leave
N out for the newest checkpoint. Nothing in the repository changes.`,
		Example: `  tuios checkpoint diff
  tuios checkpoint diff -w build 3 --stat`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			params := map[string]any{"context": context}
			if len(args) == 1 {
				n, err := checkpointN(args[0])
				if err != nil {
					return err
				}
				params["n"] = n
			}
			if context < 0 || context > 20 {
				return errors.New("--context is 0 to 20 lines")
			}
			if len(paths) > 0 {
				params["paths"] = paths
			}
			raw, on, err := callCheckpointVerb(f, "checkpoint-diff", params)
			if err != nil || raw == nil {
				return err
			}
			var res struct {
				Session    string              `json:"session"`
				Window     string              `json:"window"`
				Checkpoint worktree.Checkpoint `json:"checkpoint"`
				Base       string              `json:"base"`
				Files      []review.File       `json:"files"`
				Totals     review.Totals       `json:"totals"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			fmt.Printf("Checkpoint %d of %s, pane %s%s, against %s: %d %s, +%d -%d\n",
				res.Checkpoint.N, plainLine(res.Session), shortWindowID(res.Window), on, plainLine(res.Base),
				res.Totals.Files, plural.Word(res.Totals.Files, "file", "files"), res.Totals.Added, res.Totals.Removed)
			if what := checkpointWhat(res.Checkpoint); what != "-" {
				fmt.Printf("Label: %s\n", what)
			}
			return printDiffFiles(os.Stdout, res.Files, nil, stat)
		},
	}
	f.add(cmd)
	cmd.Flags().StringArrayVar(&paths, "path", nil, "Only this path, relative to the repository root. Repeatable")
	cmd.Flags().BoolVar(&stat, "stat", false, "List the changed files with their counts, without the diff")
	cmd.Flags().IntVar(&context, "context", 3, "Lines of context around each change, 0 to 20")
	return cmd
}

// newCheckpointRestoreCommand builds `tuios checkpoint restore`.
func newCheckpointRestoreCommand() *cobra.Command {
	var f checkpointFlags
	var force bool
	cmd := &cobra.Command{
		Use:   "restore N",
		Short: "Put a pane's work tree back to a checkpoint",
		Long: `Put the pane's git work tree back to checkpoint N. First the work tree is saved
as a safety checkpoint. To undo the restore, restore that one. Only the files
that differ are written or removed. The index, HEAD, the branch and ignored
files do not change, so git status shows the restored files as changes.

The restore is refused while the pane's agent is working or waiting on a
prompt. Use --force to restore then.`,
		Example: `  tuios checkpoint restore 2
  tuios checkpoint restore -w build 2 --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			n, err := checkpointN(args[0])
			if err != nil {
				return err
			}
			params := map[string]any{"n": n}
			if force {
				params["force"] = true
			}
			raw, on, err := callCheckpointVerb(f, "restore-checkpoint", params)
			if err != nil || raw == nil {
				return err
			}
			var res struct {
				Session  string              `json:"session"`
				Window   string              `json:"window"`
				Restored worktree.Checkpoint `json:"restored"`
				Safety   worktree.Checkpoint `json:"safety"`
				Written  []string            `json:"written"`
				Removed  []string            `json:"removed"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			fmt.Printf("Restored checkpoint %d in %s, pane %s%s: %d %s written, %d removed.\n",
				res.Restored.N, plainLine(res.Session), shortWindowID(res.Window), on,
				len(res.Written), plural.Word(len(res.Written), "file", "files"), len(res.Removed))
			fmt.Printf("Checkpoint %d holds the work tree as it was before. To undo, run: tuios checkpoint restore -s %s -w %s %d\n",
				res.Safety.N, plainLine(res.Session), shortWindowID(res.Window), res.Safety.N)
			return nil
		},
	}
	f.add(cmd)
	cmd.Flags().BoolVar(&force, "force", false, "Restore while the agent is working or waiting on a prompt")
	return cmd
}
