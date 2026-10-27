package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newNoteCommand builds `tuios note`: the return note of a session or a pane.
func newNoteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "note",
		Short: "Set, read or clear the return note of a session or a pane",
		Long: `A note is one short line on a session or a pane. It says what the session
is for or what comes next. tuios shows it in the dock when you come back to
the session, in the sidebar, in the session switcher and in the Inbox.

The note is kept with the session. It survives a detach and a daemon restart.
A note is at most 200 bytes on one line.

From inside a pane, the note goes on this pane's session. Add -w to put it on
one pane.`,
		Example: `  # Say what comes next before you stop
  tuios note set next: run the migration

  # Read it back
  tuios note get

  # A note on one pane
  tuios note set -w build waiting for CI

  # Remove the note
  tuios note clear`,
	}
	cmd.AddCommand(newNoteSetCommand(), newNoteGetCommand(), newNoteClearCommand())
	return cmd
}

// noteFlags adds the -s and -w flags every note command takes.
func noteFlags(cmd *cobra.Command, sessionName, window *string) {
	cmd.Flags().StringVarP(sessionName, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
	cmd.Flags().StringVarP(window, "window", "w", "", "A pane by name or id, for the pane's note (default: the session's note)")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
}

func newNoteSetCommand() *cobra.Command {
	var sessionName, window string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "set TEXT...",
		Short: "Set the note",
		Long: `Set the note of a session, or of a pane with -w. The words are joined with
spaces. Newlines become spaces, and the note is cut to 200 bytes.`,
		Example: `  tuios note set next: run the migration
  tuios note set -s work "next: review the payments PR"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSetNote(sessionName, window, strings.Join(args, " "), jsonOutput)
		},
	}
	noteFlags(cmd, &sessionName, &window)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func newNoteGetCommand() *cobra.Command {
	var sessionName, window string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Print the note",
		Long:  `Print the note of a session, or of a pane with -w. Prints nothing when there is no note.`,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runGetNote(sessionName, window, jsonOutput)
		},
	}
	noteFlags(cmd, &sessionName, &window)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func newNoteClearCommand() *cobra.Command {
	var sessionName, window string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Remove the note",
		Long:  `Remove the note of a session, or of a pane with -w.`,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSetNote(sessionName, window, "", jsonOutput)
		},
	}
	noteFlags(cmd, &sessionName, &window)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

// noteParams is the verb's params: the window key only for a pane's note.
func noteParams(sessionName, window string) map[string]any {
	p := map[string]any{"session": sessionName}
	if window != "" {
		p["window"] = window
	}
	return p
}

// runSetNote sets or, with an empty note, clears a note.
func runSetNote(sessionName, window, note string, jsonOutput bool) error {
	p := noteParams(sessionName, window)
	p["note"] = note
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	raw, err := t.client.Call("set-note", t.params(p))
	if err != nil {
		return reportVerbError(t.explain("set-note", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Note    string `json:"note"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	what := "session " + res.Session
	if res.Window != "" {
		what = "pane " + window + " in session " + res.Session
	}
	if res.Note == "" {
		fmt.Printf("Removed the note of %s.\n", what)
		return nil
	}
	fmt.Printf("Set the note of %s: %s\n", what, res.Note)
	return nil
}

// runGetNote prints a note, and nothing when there is none.
func runGetNote(sessionName, window string, jsonOutput bool) error {
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	raw, err := t.client.Call("get-note", t.params(noteParams(sessionName, window)))
	if err != nil {
		return reportVerbError(t.explain("get-note", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		Note string `json:"note"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if res.Note != "" {
		fmt.Println(res.Note)
	}
	return nil
}
