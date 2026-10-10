package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
)

// tuios transfers: the copies the daemon runs, and control of them.

func newTransfersCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "transfers",
		Aliases: []string{"copies"},
		Short:   "List the copies the daemon runs, and pause, resume, cancel or wait for them",
		Long: `List the copies the daemon runs: the ones tuios cp started, and the ones the
other clients started. A finished copy stays in the list for 30 minutes, or
until 'tuios transfers clear'.

In a pane that holds the files grant and not admin, the list has only the
copies that pane started, and the other commands act only on those.`,
		Example: `  tuios transfers
  tuios transfers --json
  tuios transfers pause 3f9a1c2b7d00
  tuios transfers resume 3f9a1c2b7d00
  tuios transfers cancel 3f9a1c2b7d00
  tuios transfers wait
  tuios transfers clear`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runTransfersList(jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print the rows as JSON")
	control := func(verb, use, short, done string) *cobra.Command {
		var j bool
		c := &cobra.Command{
			Use:   use + " ID...",
			Short: short,
			Args:  cobra.MinimumNArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				return runTransfersControl(verb, done, args, j)
			},
		}
		c.Flags().BoolVar(&j, "json", false, "Print the rows as JSON")
		return c
	}
	var waitOpts struct {
		json, quiet bool
	}
	wait := &cobra.Command{
		Use:   "wait [ID...]",
		Short: "Wait for copies to end, and exit as tuios cp does",
		Long: `Wait for the copies named, or for every copy that has not ended, and show
their progress as tuios cp does. The exit code is the one tuios cp gives for
the worst of them: 0, 1, 3, 4, 5, 6, or 130 for a cancelled copy. A copy
whose files were skipped because they differ exits 3.`,
		Example: `  tuios transfers wait
  tuios transfers wait 3f9a1c2b7d00 --json`,
		RunE: func(_ *cobra.Command, args []string) error {
			return runTransfersWait(args, waitOpts.json, waitOpts.quiet)
		},
	}
	wait.Flags().BoolVar(&waitOpts.json, "json", false, "Print the copies' events as JSON lines")
	wait.Flags().BoolVarP(&waitOpts.quiet, "quiet", "q", false, "Print only errors")
	var clearJSON bool
	clear := &cobra.Command{
		Use:   "clear [ID...]",
		Short: "Remove the copies that ended from the list",
		RunE: func(_ *cobra.Command, args []string) error {
			return runTransfersClear(args, clearJSON)
		},
	}
	clear.Flags().BoolVar(&clearJSON, "json", false, "Print the result as JSON")
	cmd.AddCommand(
		control("transfer-pause", "pause", "Pause copies. What they copied stays", "is paused"),
		control("transfer-resume", "resume", "Go on with paused copies, try waiting ones now, or try failed ones again", "goes on"),
		control("transfer-cancel", "cancel", "Cancel copies and remove the part of the file in flight", "is cancelled"),
		wait, clear,
	)
	return cmd
}

func listCopies(ctl *session.VerbClient) ([]copyRow, json.RawMessage, error) {
	raw, err := ctl.Call("transfer-list", nil)
	if err != nil {
		return nil, nil, explainVerbError("transfer-list", err)
	}
	var out struct {
		Transfers []copyRow `json:"transfers"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return out.Transfers, raw, nil
}

func runTransfersList(jsonOut bool) error {
	ctl, err := dialVerb()
	if err != nil {
		return reportVerbError(err, jsonOut)
	}
	defer func() { _ = ctl.Close() }()
	rows, raw, err := listCopies(ctl)
	if err != nil {
		return reportVerbError(err, jsonOut)
	}
	if jsonOut {
		var v any
		_ = json.Unmarshal(raw, &v)
		outputJSON(v)
		return nil
	}
	if len(rows) == 0 {
		fmt.Println("No copies. Start one with: tuios cp SRC HOST:DST")
		return nil
	}
	p := colorprofile.Detect(os.Stdout, os.Environ())
	theme.SetColorProfile(p)
	pal := theme.UI()
	data := make([][]string, 0, len(rows))
	for _, r := range rows {
		from := r.Src.String()
		name := r.Name
		if r.Kind == "dir" {
			name += "/"
		}
		done := fmt.Sprintf("%s / %s", humanBytes(r.Done), humanBytes(r.Size))
		if r.Kind == "dir" {
			done += fmt.Sprintf(", %s of %s files", commas(r.FilesDone), commas(r.Files))
		}
		rate, left := "", ""
		if r.Rate > 0 {
			rate = humanBytes(int64(r.Rate)) + "/s"
		}
		if r.ETAms > 0 {
			left = humanDuration(time.Duration(r.ETAms) * time.Millisecond)
		}
		if r.Error != "" && (r.State == "failed" || r.State == "waiting") {
			left = r.Error
		}
		data = append(data, []string{r.ID, r.State, name, from, r.where(), done, rate, left})
	}
	t := table.New().
		Headers("ID", "STATE", "NAME", "FROM", "TO", "DONE", "RATE", "LEFT").
		Rows(data...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(pal.FgMute)).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return base.Bold(true).Foreground(pal.Accent)
			}
			if col == 1 && row >= 0 && row < len(rows) {
				switch rows[row].State {
				case "done":
					return base.Foreground(pal.Success)
				case "failed":
					return base.Foreground(pal.Warn)
				case "waiting", "conflict", "paused":
					return base.Foreground(pal.Warning)
				}
			}
			return base
		})
	_, _ = fmt.Fprintln(&colorprofile.Writer{Forward: os.Stdout, Profile: p}, t.Render())
	return nil
}

func runTransfersControl(verb, done string, ids []string, jsonOut bool) error {
	ctl, err := dialVerb()
	if err != nil {
		return reportVerbError(err, jsonOut)
	}
	defer func() { _ = ctl.Close() }()
	var out []json.RawMessage
	var firstErr error
	for _, id := range ids {
		raw, err := ctl.Call(verb, map[string]any{"id": id})
		if err != nil {
			e := explainVerbError(verb, err)
			if firstErr == nil {
				firstErr = e
			}
			if !jsonOut {
				reportCommandError(e)
			}
			continue
		}
		out = append(out, raw)
		if !jsonOut {
			var r copyRow
			_ = json.Unmarshal(raw, &r)
			fmt.Printf("The copy %s of %s %s.\n", r.ID, r.Name, doneWord(done, r))
		}
	}
	if jsonOut {
		outputJSON(map[string]any{"transfers": out})
	}
	if firstErr != nil {
		return &statusError{code: 1}
	}
	return nil
}

// doneWord is what a control did, with what the row says when the copy had
// ended before it.
func doneWord(done string, r copyRow) string {
	if r.ended() && !strings.Contains(done, "cancel") {
		return "ended already (" + r.State + ")"
	}
	return done
}

func runTransfersWait(ids []string, jsonOut, quiet bool) error {
	ctl, err := dialVerb()
	if err != nil {
		return reportVerbError(err, jsonOut)
	}
	defer func() { _ = ctl.Close() }()
	out := outTerminal
	switch {
	case jsonOut:
		out = outJSON
	case quiet:
		out = outQuiet
	case !isTerminal(os.Stderr):
		out = outPlain
	}
	fl := newFollower(ctl, out, false)
	fl.subscribe()
	rows, _, err := listCopies(ctl)
	if err != nil {
		return reportVerbError(err, jsonOut)
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, r := range rows {
		if len(ids) == 0 && r.ended() {
			continue
		}
		if len(ids) > 0 && !want[r.ID] {
			continue
		}
		delete(want, r.ID)
		fl.add(r)
	}
	for id := range want {
		return &diagnosticError{What: "No copy has the id " + id + ".", Fix: "run 'tuios transfers' to see the ids. A finished copy leaves the list after 30 minutes."}
	}
	if len(fl.ids) == 0 {
		if !jsonOut && !quiet {
			fmt.Println("No copy runs now.")
		}
		return nil
	}
	fl.run(nil)
	return exitStatusError(fl.summary())
}

func runTransfersClear(ids []string, jsonOut bool) error {
	ctl, err := dialVerb()
	if err != nil {
		return reportVerbError(err, jsonOut)
	}
	defer func() { _ = ctl.Close() }()
	params := map[string]any{}
	if len(ids) > 0 {
		params["ids"] = ids
	}
	raw, err := ctl.Call("transfer-clear", params)
	if err != nil {
		return reportVerbError(explainVerbError("transfer-clear", err), jsonOut)
	}
	var r struct {
		Cleared int `json:"cleared"`
	}
	_ = json.Unmarshal(raw, &r)
	if jsonOut {
		outputJSON(r)
		return nil
	}
	fmt.Printf("%s %s that ended %s removed from the list.\n", commas(r.Cleared), pluralWord(r.Cleared, "copy", "copies"), pluralWord(r.Cleared, "was", "were"))
	return nil
}
