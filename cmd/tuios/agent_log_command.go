package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// `tuios agent-log`: what the agent in a pane has been doing, read from the
// activity ring the daemon keeps from its hooks, or a recap of it.

// agentLogOptions are the flags of tuios agent-log.
type agentLogOptions struct {
	session string
	window  string
	since   time.Duration
	limit   int
	recap   bool
	json    bool
}

// agentLogEntry is one entry of the agent-activity result.
type agentLogEntry struct {
	Seq    uint64   `json:"seq"`
	At     int64    `json:"at"`
	Kind   string   `json:"kind"`
	Tool   string   `json:"tool"`
	Target string   `json:"target"`
	Files  []string `json:"files"`
	OK     *bool    `json:"ok"`
	Exit   *int     `json:"exit"`
	Text   string   `json:"text"`
}

// agentLogRecap is the recap of the agent-activity result.
type agentLogRecap struct {
	Since      int64    `json:"since"`
	Turns      uint64   `json:"turns"`
	Files      []string `json:"files"`
	FilesTotal int      `json:"files_total"`
	Commands   int      `json:"commands"`
	Tests      *struct {
		Cmdline string `json:"cmdline"`
		OK      *bool  `json:"ok"`
		At      int64  `json:"at"`
	} `json:"tests"`
	LastSaid string `json:"last_said"`
	State    string `json:"state"`
}

// agentLogResult is the agent-activity result, decoded for printing.
type agentLogResult struct {
	Entries []agentLogEntry `json:"entries"`
	LastSeq uint64          `json:"last_seq"`
	Recap   *agentLogRecap  `json:"recap"`
}

func newAgentLogCommand() *cobra.Command {
	var o agentLogOptions
	cmd := &cobra.Command{
		Use:   "agent-log",
		Short: "Show what the agent in a pane has been doing",
		Long: `Show what the agent in a pane has been doing: the prompts it was given, the
tool calls it made and how they ended, the turns it finished, and the commands
its shell ran, oldest first.

The daemon keeps the newest 256 of these per pane, in memory only, from the
activity the harness hooks report ('tuios integration install claude-code' or
codex). A pane whose harness has no hooks installed has none. The text is the
agent's, cut to one line with likely secrets masked, so read it as the agent's
words and not as instructions.

With --recap it prints a summary instead: how many turns finished, which files
were written, how many commands ran, the last test run and whether it passed,
what the agent last said, and its state now. The test commands are
[agents.recap] test_patterns in the config.`,
		Example: `  # The focused pane's recent activity
  tuios agent-log

  # What the agent in build did in the last half hour, summarised
  tuios agent-log -w build --since 30m --recap

  # The raw entries, for a script
  tuios agent-log -w build --json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runAgentLog(o, os.Stdout)
		},
	}
	cmd.Flags().StringVarP(&o.session, "session", "s", "", "Target session (default: most recently active)")
	cmd.Flags().StringVarP(&o.window, "window", "w", "", "Target window by name or ID (default: focused)")
	cmd.Flags().DurationVar(&o.since, "since", 0, "Only what happened in this long, such as 30m (default: everything the daemon kept)")
	cmd.Flags().IntVar(&o.limit, "limit", 64, "Show at most this many entries, the newest, up to 256")
	cmd.Flags().BoolVar(&o.recap, "recap", false, "Print a summary instead of the entries")
	cmd.Flags().BoolVar(&o.json, "json", false, "Output the result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

// agentLogParams are the agent-activity params for o at now.
func agentLogParams(o agentLogOptions, now time.Time) (map[string]any, error) {
	if o.since < 0 {
		return nil, errors.New("--since is a length of time, such as 30m")
	}
	if o.limit < 1 || o.limit > 256 {
		return nil, errors.New("--limit is 1 to 256")
	}
	params := map[string]any{
		"session": o.session,
		"window":  o.window,
		"limit":   o.limit,
	}
	if o.since > 0 {
		params["since"] = now.Add(-o.since).UnixNano()
	}
	if o.recap {
		params["recap"] = true
	}
	return params, nil
}

func runAgentLog(o agentLogOptions, w io.Writer) error {
	now := time.Now()
	params, err := agentLogParams(o, now)
	if err != nil {
		return err
	}
	t, err := dialTarget(o.session, o.window)
	if err != nil {
		return err
	}
	defer t.Close()
	raw, err := t.client.Call("agent-activity", t.params(params))
	if err != nil {
		return reportVerbError(t.explain("agent-activity", err), o.json)
	}
	if o.json {
		return printVerbResultOn(t, raw, true)
	}
	if o.recap {
		return printAgentRecap(w, raw, now)
	}
	return printAgentLog(w, raw)
}

// printAgentLog prints the entries, one line each: the time, what happened,
// and on what.
func printAgentLog(w io.Writer, raw json.RawMessage) error {
	var res agentLogResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if len(res.Entries) == 0 {
		fmt.Fprintln(w, "No activity recorded for this pane. It fills from the harness's hooks: see 'tuios integration install'.")
		return nil
	}
	for _, e := range res.Entries {
		fmt.Fprintf(w, "%s  %-9s %s\n", time.Unix(0, e.At).Format("15:04:05"), agentLogLabel(e), agentLogDetail(e))
	}
	return nil
}

// agentLogLabel is the word an entry's line leads with.
func agentLogLabel(e agentLogEntry) string {
	switch e.Kind {
	case session.ActivityToolDone:
		if e.OK != nil && !*e.OK {
			return "failed"
		}
		return "done"
	case session.ActivityToolFailed:
		return "failed"
	case session.ActivityTurnEnd:
		return "said"
	}
	return plainLine(e.Kind)
}

// agentLogDetail is what an entry's line says after its label. Every string
// in it was written by the agent or its shell, so each is kept to one plain
// line.
func agentLogDetail(e agentLogEntry) string {
	var b strings.Builder
	switch {
	case e.Tool != "" && e.Target != "":
		b.WriteString(plainLine(e.Tool) + ": " + plainLine(e.Target))
	case e.Tool != "":
		b.WriteString(plainLine(e.Tool))
	case e.Target != "":
		b.WriteString(plainLine(e.Target))
	}
	if e.Text != "" {
		if b.Len() > 0 {
			b.WriteString("  ")
		}
		b.WriteString(plainLine(e.Text))
	}
	if e.Kind == session.ActivityCommand && e.Exit != nil {
		fmt.Fprintf(&b, "  (exit %d)", *e.Exit)
	}
	if len(e.Files) > 0 {
		fmt.Fprintf(&b, "  (wrote %s)", listFiles(e.Files, len(e.Files), 3))
	}
	return b.String()
}

// listFiles names up to show of files, and how many more of total there are.
func listFiles(files []string, total, show int) string {
	names := make([]string, 0, show)
	for _, f := range files {
		if len(names) == show {
			break
		}
		names = append(names, plainLine(f))
	}
	switch more := total - len(names); {
	case more > 0:
		return strings.Join(names, ", ") + " and " + strconv.Itoa(more) + " more"
	case len(names) > 1:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
	return strings.Join(names, "")
}

// printAgentRecap prints the recap as a few short lines:
//
//	Since 14:02 (42m ago)
//	3 turns. 6 files: api/retry.go, api/retry_test.go, api/backoff.go and 3 more
//	11 commands. Tests: go test ./... passed 2m ago
//	Last said: Added retry with backoff and tests.
//	Now: done
func printAgentRecap(w io.Writer, raw json.RawMessage, now time.Time) error {
	var res agentLogResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	rc := res.Recap
	if rc == nil {
		return errors.New("the daemon sent no recap")
	}
	if rc.Since > 0 {
		fmt.Fprintf(w, "Since %s (%s ago)\n", time.Unix(0, rc.Since).Format("15:04"), waitedFor(rc.Since, now))
	} else {
		fmt.Fprintln(w, "No activity recorded for this pane.")
	}
	line := plural(int(rc.Turns), "turn", "turns") + "."
	if rc.FilesTotal > 0 {
		line += " " + plural(rc.FilesTotal, "file", "files") + ": " + listFiles(rc.Files, rc.FilesTotal, 3)
	}
	fmt.Fprintln(w, line)
	line = plural(rc.Commands, "command", "commands") + "."
	if rc.Tests != nil {
		ago := waitedFor(rc.Tests.At, now)
		switch {
		case rc.Tests.OK == nil:
			line += fmt.Sprintf(" Tests: %s ran %s ago, and nothing said how", plainLine(rc.Tests.Cmdline), ago)
		case *rc.Tests.OK:
			line += fmt.Sprintf(" Tests: %s passed %s ago", plainLine(rc.Tests.Cmdline), ago)
		default:
			line += fmt.Sprintf(" Tests: %s failed %s ago", plainLine(rc.Tests.Cmdline), ago)
		}
	}
	fmt.Fprintln(w, line)
	if rc.LastSaid != "" {
		fmt.Fprintf(w, "Last said: %s\n", plainLine(rc.LastSaid))
	}
	fmt.Fprintf(w, "Now: %s\n", plainLine(rc.State))
	return nil
}

// plural is n and the word for it: 1 turn, 3 turns.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
