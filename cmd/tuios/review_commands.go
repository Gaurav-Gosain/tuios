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

	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/spf13/cobra"
)

// newReviewCommand builds `tuios review`: read what the agent in a pane
// changed, leave notes on it, and send the notes to the agent.
func newReviewCommand() *cobra.Command {
	var sessionName, window, base, against string
	var paths []string
	var uncommitted, stat, jsonOutput bool
	var context int
	cmd := &cobra.Command{
		Use:   "review [SESSION]",
		Short: "Read what an agent changed, and leave it notes",
		Long: `Show what the agent in a pane changed: the diff of its worktree against the
base it was made from, or for a plain repository against the merge base with
its upstream branch, else only what is not committed. Committed and
uncommitted work show together, untracked files included and ignored files
left out. The diff is read by the daemon, on the machine the pane runs on,
through a temporary git index: the repository, its index and its files are
not changed.

Name the session as an argument or with -s, and the pane with -w (default the
focused pane). --against compares the attempt with another attempt of the same
fan instead of the base.

Notes left with 'tuios review note' are shown under the line they are on, and
follow it when the file changes. 'tuios review send' sends the unsent ones to
the agent as one message, typed when it next comes to rest.

The diff stops at 400 files, 2 MiB of text or 5000 lines in one file: files
past that are listed with their counts only.`,
		Example: `  # What the agent in the focused pane changed
  tuios review

  # A fan attempt, against the base it was made from, or against another attempt
  tuios review api-fan-retry-2
  tuios review api-fan-retry-2 --against api-fan-retry

  # Only what is not committed yet, or only the list of files
  tuios review -w build --uncommitted
  tuios review --stat`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeSessionNames,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				if sessionName != "" && sessionName != args[0] {
					return errors.New("name the session once: as an argument or with -s")
				}
				sessionName = args[0]
			}
			if context < 0 || context > 20 {
				return errors.New("--context is 0 to 20 lines")
			}
			return runReview(os.Stdout, sessionName, window, reviewOptions{
				base: base, against: against, paths: paths, uncommitted: uncommitted, stat: stat, context: context,
			}, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session of the pane (default: this pane's, else the most recently active)")
	cmd.Flags().StringVarP(&window, "window", "w", "", "The pane, by name or id (default: the focused pane)")
	cmd.Flags().StringVar(&base, "base", "", "Diff against this branch, tag or commit instead of the worktree's base")
	cmd.Flags().StringVar(&against, "against", "", "Diff against another attempt of the same fan, by session")
	cmd.Flags().StringArrayVar(&paths, "path", nil, "Only this path, relative to the repository root. Repeatable")
	cmd.Flags().BoolVar(&uncommitted, "uncommitted", false, "Only what is not committed yet")
	cmd.Flags().BoolVar(&stat, "stat", false, "List the changed files with their counts, without the diff")
	cmd.Flags().IntVar(&context, "context", 3, "Lines of context around each change, 0 to 20")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	_ = cmd.RegisterFlagCompletionFunc("against", completeWorktreeSessions)
	cmd.AddCommand(newReviewNoteCommand(), newReviewNotesCommand(), newReviewSendCommand())
	return cmd
}

// reviewOptions are the diff flags of tuios review.
type reviewOptions struct {
	base, against string
	paths         []string
	uncommitted   bool
	stat          bool
	context       int
}

// reviewDiffResult is review-diff's answer, as the CLI reads it.
type reviewDiffResult struct {
	Session     string        `json:"session"`
	Window      string        `json:"window"`
	Worktree    string        `json:"worktree"`
	Base        string        `json:"base"`
	BaseSHA     string        `json:"base_sha"`
	Uncommitted bool          `json:"uncommitted"`
	Against     string        `json:"against"`
	Files       []review.File `json:"files"`
	Totals      review.Totals `json:"totals"`
	Truncated   bool          `json:"truncated"`
	Notes       []review.Note `json:"notes"`
}

func runReview(w io.Writer, sessionName, window string, o reviewOptions, jsonOutput bool) error {
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	params := t.params(map[string]any{"context": o.context})
	if t.window != "" {
		params["window"] = t.window
	}
	if o.base != "" {
		params["base"] = o.base
	}
	if o.against != "" {
		params["against"] = o.against
	}
	if len(o.paths) > 0 {
		params["paths"] = o.paths
	}
	if o.uncommitted {
		params["uncommitted"] = true
	}
	raw, err := t.client.Call("review-diff", params)
	if err != nil {
		return reportVerbError(t.explain("review-diff", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res reviewDiffResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	return printReview(w, res, t.on(), o.stat)
}

// reviewHeading is the first line tuios review prints.
func reviewHeading(res reviewDiffResult, on string) string {
	var against string
	switch {
	case res.Against != "":
		against = "against " + plainLine(res.Against)
	case res.Uncommitted:
		against = "uncommitted changes"
	default:
		against = "against " + plainLine(res.Base)
		if len(res.BaseSHA) >= 7 {
			against += " (" + res.BaseSHA[:7] + ")"
		}
	}
	line := fmt.Sprintf("Review of %s, pane %s%s, %s: %d %s, +%d -%d",
		plainLine(res.Session), shortWindowID(res.Window), on, against,
		res.Totals.Files, pluralWord(res.Totals.Files, "file", "files"), res.Totals.Added, res.Totals.Removed)
	if n := len(res.Notes); n > 0 {
		line += fmt.Sprintf(", %d %s", n, pluralWord(n, "note", "notes"))
	}
	return line
}

// printReview writes a review-diff answer as text: a heading, the list of
// files, then each file's hunks with the notes under the lines they are on.
// Every line of it came from the repository or from whoever wrote a note, so
// each is printed with its control characters left out.
func printReview(w io.Writer, res reviewDiffResult, on string, stat bool) error {
	fmt.Fprintln(w, reviewHeading(res, on))
	if len(res.Files) == 0 {
		_, err := fmt.Fprintln(w, "Nothing changed.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, f := range res.Files {
		name := plainLine(f.Path)
		if f.OldPath != "" {
			name = plainLine(f.OldPath) + " -> " + name
		}
		counts := fmt.Sprintf("+%d -%d", f.Added, f.Removed)
		if f.Binary {
			counts = "binary"
		}
		note := ""
		if f.Truncated {
			note = "counts only: past the diff's size limit"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", plainLine(f.Status), name, counts, note)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if stat {
		return nil
	}
	notesOn := map[string][]review.Note{}
	for _, n := range res.Notes {
		notesOn[n.Path] = append(notesOn[n.Path], n)
	}
	for _, f := range res.Files {
		if len(f.Hunks) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s\n", plainLine(f.Path))
		notes := notesOn[f.Path]
		for _, h := range f.Hunks {
			fmt.Fprintln(w, plainLine(h.Header))
			for _, n := range notes {
				if n.IsHunk() && n.HunkHeader == h.Header {
					fmt.Fprintln(w, noteLine(n))
				}
			}
			for _, l := range h.Lines {
				fmt.Fprintln(w, diffLine(l))
				for _, n := range notes {
					if n.IsHunk() {
						continue
					}
					if (n.Side == review.SideOld && l.Old == n.Line && l.Op != review.OpAdd) ||
						(n.Side != review.SideOld && l.New == n.Line && l.Op != review.OpDelete) {
						fmt.Fprintln(w, noteLine(n))
					}
				}
			}
		}
	}
	return nil
}

// diffLine is one line of a hunk with both line numbers before it.
func diffLine(l review.Line) string {
	num := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	mark := " "
	switch l.Op {
	case review.OpAdd:
		mark = "+"
	case review.OpDelete:
		mark = "-"
	}
	return fmt.Sprintf("%5s %5s %s %s", num(l.Old), num(l.New), mark, strings.ReplaceAll(plainText(strings.ReplaceAll(l.Text, "\n", " ")), "\t", "    "))
}

// noteLine is a note as it is printed under its line.
func noteLine(n review.Note) string {
	state := ""
	switch {
	case n.Outdated:
		state = ", outdated"
	case n.SentAt != 0:
		state = ", sent " + agoOf(n.SentAt)
	}
	return fmt.Sprintf("            > note %s (%s%s): %s", plainLine(n.ID), noteAuthor(n.By), state, plainLine(n.Text))
}

// noteAuthor names who wrote a note.
func noteAuthor(by string) string {
	switch {
	case by == "human", by == "shell", strings.HasPrefix(by, "link:"):
		return plainLine(by)
	case by == "":
		return "unknown"
	}
	return "pane " + shortWindowID(by)
}

// parseNoteTarget reads FILE:LINE, or FILE alone for a note on a hunk.
func parseNoteTarget(spec string, hunk bool) (string, int, error) {
	if hunk {
		if spec == "" {
			return "", 0, errors.New("name the file the hunk is in")
		}
		return spec, 0, nil
	}
	i := strings.LastIndexByte(spec, ':')
	if i <= 0 {
		return "", 0, fmt.Errorf("%q is not FILE:LINE", spec)
	}
	line, err := strconv.Atoi(spec[i+1:])
	if err != nil || line < 1 {
		return "", 0, fmt.Errorf("%q is not FILE:LINE with a line from 1", spec)
	}
	return spec[:i], line, nil
}

// newReviewNoteCommand builds `tuios review note`.
func newReviewNoteCommand() *cobra.Command {
	var sessionName, window, side, hunk, edit, remove string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "note [FILE:LINE] TEXT...",
		Short: "Leave a review note on a line of a pane's changes",
		Long: `Leave a note on a line of what the agent in a pane changed, for 'tuios review
send' to pass to the agent. FILE is relative to the repository root and LINE
is the line on the new side; --side old puts it on a removed line, numbered as
in the base. With --hunk, name the file alone and the hunk by its header
("@@ -88,4 +100,6 @@") for a note on the whole hunk.

The note keeps the line's text, so it follows the line when the file changes;
one whose line is gone is marked outdated. A note is at most 1000 bytes. A
worktree holds at most 200 notes.

--edit ID replaces a note's text and --remove ID drops one. From inside a pane
only the notes that pane wrote can be changed; from a shell, any but the ones
left from the attached client.`,
		Example: `  tuios review note api/retry.go:42 'log the attempt number here too'
  tuios review note -w build --hunk '@@ -88,4 +100,6 @@' api/retry.go 'wrap with context'
  tuios review note --edit n3 'wrap it with the attempt number'
  tuios review note --remove n3`,
		RunE: func(_ *cobra.Command, args []string) error {
			params := map[string]any{}
			switch {
			case edit != "" && remove != "":
				return errors.New("pass --edit or --remove, not both")
			case remove != "":
				if len(args) > 0 {
					return errors.New("--remove takes no text")
				}
				params["action"], params["id"] = "remove", remove
			case edit != "":
				if len(args) == 0 {
					return errors.New("give the note's new text")
				}
				params["action"], params["id"], params["text"] = "edit", edit, strings.Join(args, " ")
			default:
				if len(args) < 2 {
					return errors.New("give FILE:LINE (or FILE with --hunk) and the note's text")
				}
				path, line, err := parseNoteTarget(args[0], hunk != "")
				if err != nil {
					return err
				}
				params["action"], params["path"], params["text"] = "add", path, strings.Join(args[1:], " ")
				if hunk != "" {
					params["hunk"] = hunk
				} else {
					params["line"] = line
				}
				if side != "" {
					params["side"] = side
				}
			}
			return runReviewNote(os.Stdout, sessionName, window, params, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session of the pane (default: this pane's, else the most recently active)")
	cmd.Flags().StringVarP(&window, "window", "w", "", "The pane the note is for, by name or id (default: the focused pane)")
	cmd.Flags().StringVar(&side, "side", "", "new (default) or old: which side of the diff LINE is on")
	cmd.Flags().StringVar(&hunk, "hunk", "", "Put the note on the whole hunk with this header")
	cmd.Flags().StringVar(&edit, "edit", "", "Replace the text of the note with this id")
	cmd.Flags().StringVar(&remove, "remove", "", "Remove the note with this id")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

// newReviewNotesCommand builds `tuios review notes`.
func newReviewNotesCommand() *cobra.Command {
	var sessionName, window string
	var clearAll, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "notes",
		Short: "List the review notes on a pane's changes",
		Long: `List the review notes left on a pane's changes, by file and line: each note's
id, where it is, who wrote it (human, shell, a pane, or link:HOST), whether
it was sent or is outdated, and its text. --clear removes every note you may
remove.`,
		Example: `  tuios review notes
  tuios review notes -w build --clear`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			action := "list"
			if clearAll {
				action = "clear"
			}
			return runReviewNote(os.Stdout, sessionName, window, map[string]any{"action": action}, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session of the pane (default: this pane's, else the most recently active)")
	cmd.Flags().StringVarP(&window, "window", "w", "", "The pane, by name or id (default: the focused pane)")
	cmd.Flags().BoolVar(&clearAll, "clear", false, "Remove every note you may remove")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

func runReviewNote(w io.Writer, sessionName, window string, params map[string]any, jsonOutput bool) error {
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	params = t.params(params)
	if t.window != "" {
		params["window"] = t.window
	}
	raw, err := t.client.Call("review-note", params)
	if err != nil {
		return reportVerbError(t.explain("review-note", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		ID      string        `json:"id"`
		Removed int           `json:"removed"`
		Notes   []review.Note `json:"notes"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	switch params["action"] {
	case "add":
		fmt.Fprintf(w, "Added note %s%s. 'tuios review send' sends it to the agent.\n", plainLine(res.ID), t.on())
		return nil
	case "edit":
		fmt.Fprintf(w, "Edited note %s%s.\n", plainLine(res.ID), t.on())
		return nil
	case "remove":
		fmt.Fprintf(w, "Removed note %v%s.\n", params["id"], t.on())
		return nil
	case "clear":
		fmt.Fprintf(w, "Removed %d %s%s.", res.Removed, pluralWord(res.Removed, "note", "notes"), t.on())
		if len(res.Notes) > 0 {
			fmt.Fprintf(w, " %d written by others %s kept.", len(res.Notes), pluralWord(len(res.Notes), "was", "were"))
		}
		fmt.Fprintln(w)
		return nil
	}
	return printReviewNotes(w, res.Notes)
}

// printReviewNotes writes notes as a table.
func printReviewNotes(w io.Writer, notes []review.Note) error {
	if len(notes) == 0 {
		_, err := fmt.Fprintln(w, "No review notes on this pane. Add one with 'tuios review note FILE:LINE TEXT'.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tWHERE\tBY\tSTATE\tNOTE")
	for _, n := range notes {
		where := fmt.Sprintf("%s:%d", plainLine(n.Path), n.Line)
		if n.IsHunk() {
			where += " (hunk)"
		} else if n.Side == review.SideOld {
			where += " (old)"
		}
		state := "unsent"
		switch {
		case n.Outdated:
			state = "outdated"
		case n.SentAt != 0:
			state = "sent " + agoOf(n.SentAt)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", plainLine(n.ID), where, noteAuthor(n.By), state, plainLine(n.Text))
	}
	return tw.Flush()
}

// newReviewSendCommand builds `tuios review send`.
func newReviewSendCommand() *cobra.Command {
	var sessionName, window string
	var ids []string
	var now, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send the unsent review notes to the pane's agent",
		Long: `Send the pane's unsent review notes to its agent as one message, through the
delivery queue: typed as a prompt when the agent is at rest, else when it next
comes to rest, and never over a prompt it is waiting on. --id sends only the
notes named, sent before or not. --now sends only when the agent is at rest
with nothing queued, and otherwise sends nothing.

The message names who sent it: "a script" from a shell, the pane from inside
one. It says "from the person" only when sent from the attached client.`,
		Example: `  tuios review send
  tuios review send -w build --id n3 --id n4
  tuios review send --now`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runReviewSend(os.Stdout, sessionName, window, ids, now, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session of the pane (default: this pane's, else the most recently active)")
	cmd.Flags().StringVarP(&window, "window", "w", "", "The agent's pane, by name or id (default: the focused pane)")
	cmd.Flags().StringArrayVar(&ids, "id", nil, "Send only this note. Repeatable")
	cmd.Flags().BoolVar(&now, "now", false, "Send only if the agent is at rest now; never queue")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

func runReviewSend(w io.Writer, sessionName, window string, ids []string, now, jsonOutput bool) error {
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	params := t.params(nil)
	if t.window != "" {
		params["window"] = t.window
	}
	if len(ids) > 0 {
		params["ids"] = ids
	}
	if now {
		params["now"] = true
	}
	raw, err := t.client.Call("send-review", params)
	if err != nil {
		return reportVerbError(t.explain("send-review", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		Notes      int    `json:"notes"`
		QueuedID   string `json:"queued_id"`
		Position   int    `json:"position"`
		Delivering bool   `json:"delivering"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Fprintf(w, "%d review %s in one message. %s\n", res.Notes, pluralWord(res.Notes, "note", "notes"),
		describeQueued(res.QueuedID, window, res.Position, res.Delivering)+t.on())
	return nil
}
