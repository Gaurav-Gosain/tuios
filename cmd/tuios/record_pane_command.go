package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/spf13/cobra"
)

// recordPrivacyNote is said by every record-pane command's help.
const recordPrivacyNote = `A recording holds everything the pane prints while it runs, and the history
on its screen when it starts: a token a command echoed, a file that was shown.
Nothing is recorded unless you ask. The file is made readable by you only.
The keys typed into the pane are not recorded.`

// newRecordPaneCommand builds `tuios record-pane`: a pane to an asciicast v2
// file, through the record-pane, stop-recording, mark-recording and
// list-recordings verbs. The daemon writes the file, so a recording runs on
// with no client attached.
func newRecordPaneCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "record-pane",
		Short: "Record a pane to an asciicast file (.cast)",
		Long: `Record a pane to an asciicast v2 file (.cast). asciinema play, agg, reel and
the inspector at inspector.getsteep.dev open it.

The recording starts with the screen the pane shows, its history included,
then holds every output and every resize at its real time. It runs in the
daemon until record-pane stop, the --for duration, the --max-size cap, or the
end of the pane.

` + recordPrivacyNote,
		Example: `  # Record the pane named build for a minute
  tuios record-pane start -w build -o build.cast --for 1m

  # Label a moment, then stop
  tuios record-pane mark -w build "tests start"
  tuios record-pane stop -w build

  # Play it
  asciinema play build.cast`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newRecordPaneStartCommand(), newRecordPaneStopCommand(), newRecordPaneMarkCommand(), newRecordPaneListCommand())
	return cmd
}

func newRecordPaneStartCommand() *cobra.Command {
	var sessionName, window, out, title string
	var duration time.Duration
	var maxSize string
	var overwrite, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start recording a pane",
		Long: `Start recording a pane to the file -o names, and return. A relative path is
taken from the folder you run the command in. An existing file is refused
unless you give --overwrite.

Without -w, tuios records the focused pane. One pane has one recording at a
time.

` + recordPrivacyNote,
		Example: `  # Record the focused pane until you stop it
  tuios record-pane start -o session.cast

  # Thirty seconds of the pane named build, at most 8 MiB
  tuios record-pane start -w build -o build.cast --for 30s --max-size 8MiB`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runRecordPaneStart(sessionName, window, out, title, duration, maxSize, overwrite, jsonOutput)
		},
	}
	addSessionFlag(cmd, &sessionName)
	addWindowFlag(cmd, &window, "Target window by name or ID (default: the focused pane)")
	cmd.Flags().StringVarP(&out, "output", "o", "", "The .cast file to write (required)")
	cmd.Flags().DurationVar(&duration, "for", 0, "Stop after this long (default: until stopped)")
	cmd.Flags().StringVar(&maxSize, "max-size", "", "Stop when the file reaches this size, such as 8MiB (default 64MiB, at most 1GiB)")
	cmd.Flags().StringVar(&title, "title", "", "The title players show")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Replace the file when it exists")
	addJSONFlag(cmd, &jsonOutput)
	_ = cmd.MarkFlagRequired("output")
	return cmd
}

// parseRecordSize reads a size such as 8MiB, 512KiB or 1048576.
func parseRecordSize(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	units := []struct {
		suffix string
		mult   int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}, {"B", 1}}
	num, mult := s, int64(1)
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			num, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), u.mult
			break
		}
	}
	var n int64
	if _, err := fmt.Sscan(num, &n); err != nil || n <= 0 {
		return 0, errfmt.Errorf("--max-size %q is not a size. Give one such as 8MiB", s)
	}
	return n * mult, nil
}

func runRecordPaneStart(sessionName, window, out, title string, duration time.Duration, maxSize string, overwrite, jsonOutput bool) error {
	maxBytes, err := parseRecordSize(maxSize)
	if err != nil {
		return err
	}
	if duration < 0 {
		return errfmt.Errorf("--for must not be negative")
	}
	sessionName = paneSessionOr(sessionName)
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	// A path on this machine is made absolute here, where the folder it is
	// relative to is known. A path on another machine must say where it is.
	if !filepath.IsAbs(out) && out != "~" && !strings.HasPrefix(out, "~/") {
		if t.host != "" {
			return errfmt.Errorf("-o %q is on %s, so give an absolute path or one that starts with ~", out, t.host)
		}
		if out, err = filepath.Abs(out); err != nil {
			return err
		}
	}
	params := map[string]any{"window": nil, "out": out}
	if overwrite {
		params["overwrite"] = true
	}
	if duration > 0 {
		params["duration_ms"] = duration.Milliseconds()
	}
	if maxBytes > 0 {
		params["max_bytes"] = maxBytes
	}
	if title != "" {
		params["title"] = title
	}
	raw, err := t.client.Call("record-pane", t.params(params))
	if err != nil {
		return reportVerbError(t.explain("record-pane", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		Path   string `json:"path"`
		Window string `json:"window"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return err
	}
	fmt.Printf("Recording pane %s (%dx%d) to %s.\n", res.Window, res.Width, res.Height, res.Path)
	return nil
}

func newRecordPaneStopCommand() *cobra.Command {
	var sessionName, window string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop recording a pane",
		Long: `Stop a pane's recording. The command returns when the file is complete, and
says how many events it holds and why the recording ended.`,
		Example: `  tuios record-pane stop -w build`,
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runRecordPaneVerb("stop-recording", sessionName, window, nil, jsonOutput)
		},
	}
	addSessionFlag(cmd, &sessionName)
	addWindowFlag(cmd, &window, "Target window by name or ID (default: the focused pane)")
	addJSONFlag(cmd, &jsonOutput)
	return cmd
}

func newRecordPaneMarkCommand() *cobra.Command {
	var sessionName, window string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "mark <label>",
		Short: "Label the current moment of a recording",
		Long: `Write a marker into a pane's recording at the current time. A player lists the
markers and can jump to each one. An agent or a test can label the moment a
step starts.`,
		Example: `  tuios record-pane mark -w build "tests start"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runRecordPaneVerb("mark-recording", sessionName, window, map[string]any{"label": args[0]}, jsonOutput)
		},
	}
	addSessionFlag(cmd, &sessionName)
	addWindowFlag(cmd, &window, "Target window by name or ID (default: the focused pane)")
	addJSONFlag(cmd, &jsonOutput)
	return cmd
}

func runRecordPaneVerb(verb, sessionName, window string, extra map[string]any, jsonOutput bool) error {
	sessionName = paneSessionOr(sessionName)
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	params := map[string]any{"window": nil}
	for k, v := range extra {
		params[k] = v
	}
	raw, err := t.client.Call(verb, t.params(params))
	if err != nil {
		return reportVerbError(t.explain(verb, err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		Path    string  `json:"path"`
		Events  int     `json:"events"`
		Bytes   int64   `json:"bytes"`
		Seconds float64 `json:"seconds"`
		Reason  string  `json:"reason"`
		Label   string  `json:"label"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return err
	}
	if verb == "mark-recording" {
		fmt.Printf("Marked %q in %s.\n", res.Label, res.Path)
		return nil
	}
	fmt.Printf("Wrote %s: %d events, %d bytes, %.1f s (%s).\n", res.Path, res.Events, res.Bytes, res.Seconds, res.Reason)
	return nil
}

func newRecordPaneListCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List the recordings that run",
		Long:    `List the recordings that run now, in every session: the pane and the file.`,
		Example: `  tuios record-pane list`,
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runRecordPaneList(jsonOutput)
		},
	}
	addJSONFlag(cmd, &jsonOutput)
	return cmd
}

func runRecordPaneList(jsonOutput bool) error {
	c, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	raw, err := c.Call("list-recordings", map[string]any{})
	if err != nil {
		return reportVerbError(explainVerbError("list-recordings", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	var res struct {
		Recordings []struct {
			Path      string `json:"path"`
			Window    string `json:"window"`
			StartedAt string `json:"started_at"`
		} `json:"recordings"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return err
	}
	if len(res.Recordings) == 0 {
		fmt.Println("No pane is being recorded.")
		return nil
	}
	for _, r := range res.Recordings {
		fmt.Printf("%s  %s  since %s\n", r.Window, r.Path, r.StartedAt)
	}
	return nil
}
