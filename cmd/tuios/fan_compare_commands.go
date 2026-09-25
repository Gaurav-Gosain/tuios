package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
	"github.com/spf13/cobra"
)

// tuios fan compare, diff and verify: the attempts of a fan side by side, what
// two of them did differently, and one check run in all of them. compare and
// verify are the daemon's compare-fan and verify-fan, so they work the same
// on another machine. diff runs git here, like worktree diff, because it
// reads the worktrees' files.

// fanCompareResult is compare-fan as the CLI reads it.
type fanCompareResult struct {
	Group string          `json:"group"`
	Repo  string          `json:"repo"`
	Base  string          `json:"base"`
	Rows  []fanCompareRow `json:"rows"`
}

// fanCompareRow is one attempt of a compare-fan result.
type fanCompareRow struct {
	Session      string             `json:"session"`
	Branch       string             `json:"branch"`
	Agent        string             `json:"agent"`
	Harness      string             `json:"harness"`
	State        string             `json:"state"`
	Files        *int               `json:"files"`
	Added        *int               `json:"added"`
	Removed      *int               `json:"removed"`
	Gone         bool               `json:"gone"`
	Note         string             `json:"note"`
	Verify       *session.FanVerify `json:"verify"`
	PromptStatus string             `json:"prompt_status"`
	LastCommand  *struct {
		Cmdline string `json:"cmdline"`
		Exit    *int   `json:"exit"`
		At      int64  `json:"at"`
	} `json:"last_command"`
}

func newFanCompareCommand() *cobra.Command {
	var jsonOut, noChanges bool
	cmd := &cobra.Command{
		Use:   "compare [[<host>:]<session>]",
		Short: "Compare the attempts of a fan-out side by side",
		Long: `Show every attempt of a fan-out on one line each: its agent and state, the
files and lines it changed against the fan's base (committed or not,
untracked files included), and the last check 'tuios fan verify' ran in it,
or else the last command a shell in it finished.

Name any session of the fan. HOST:SESSION compares a fan on another machine.`,
		Example: `  tuios fan compare api-fan-retry
  tuios fan compare api-fan-retry --json
  tuios fan compare build:api-fan-retry`,
		Args:              cobra.RangeArgs(0, 1),
		ValidArgsFunction: completeWorktreeSessions,
		RunE: func(_ *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			return runFanCompare(target, !noChanges, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output result as JSON")
	cmd.Flags().BoolVar(&noChanges, "no-changes", false, "Skip counting each attempt's changes, which runs git in every worktree")
	return cmd
}

func runFanCompare(target string, changes, jsonOutput bool) error {
	host, name := splitHostSession(target)
	t, err := dialHost(host)
	if err != nil {
		return err
	}
	defer t.Close()
	params := map[string]any{"changes": changes}
	if name != "" {
		params["session"] = name
	}
	raw, err := t.client.CallWithTimeout("compare-fan", params, 2*time.Minute)
	if err != nil {
		return reportVerbError(t.explain("compare-fan", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res fanCompareResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Print(renderFanCompare(res, t.on(), time.Now()))
	return nil
}

// renderFanCompare draws compare-fan as aligned lines, the way a person reads
// a short list, and the two commands that come next.
func renderFanCompare(res fanCompareResult, on string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s in %s%s, %d %s against %s\n", res.Group, res.Repo, on, len(res.Rows), pluralWord(len(res.Rows), "attempt", "attempts"), res.Base)
	cells := make([][]string, 0, len(res.Rows))
	for _, r := range res.Rows {
		agent := r.Agent
		if agent == "" {
			agent = orNone(r.Harness)
		}
		files, lines := "-", "-"
		if r.Files != nil {
			files = fmt.Sprintf("%d %s", *r.Files, pluralWord(*r.Files, "file", "files"))
			lines = fmt.Sprintf("+%d -%d", derefInt(r.Added), derefInt(r.Removed))
		}
		if r.Gone {
			files, lines = "gone", "-"
		}
		cells = append(cells, []string{r.Session, agent, r.State, files, lines, fanCheckText(r, now)})
	}
	widths := make([]int, 5)
	for _, row := range cells {
		for i := range widths {
			widths[i] = max(widths[i], len(row[i]))
		}
	}
	for _, row := range cells {
		for i, cell := range row {
			b.WriteString("  ")
			switch {
			case i == len(row)-1:
				b.WriteString(cell)
			case i == 3 || i == 4:
				// Counts line up on the right.
				b.WriteString(strings.Repeat(" ", widths[i]-len(cell)) + cell)
			default:
				b.WriteString(cell + strings.Repeat(" ", widths[i]-len(cell)))
			}
		}
		b.WriteString("\n")
	}
	if len(res.Rows) > 0 {
		b.WriteString("Keep one with 'tuios fan keep <session>'. Compare two with 'tuios fan diff A B'.\n")
	}
	return b.String()
}

// fanCheckText says what the last check found: the verify-fan check when one
// ran, else the last command a shell in the attempt finished.
func fanCheckText(r fanCompareRow, now time.Time) string {
	if v := r.Verify; v != nil {
		switch v.State {
		case session.VerifyRunning:
			return v.Command + " running"
		case session.VerifyPassed:
			return v.Command + " passed " + agoFrom(v.FinishedAt, now)
		default:
			why := "failed"
			switch {
			case v.Note != "":
				why = "failed: " + v.Note
			case v.Exit != nil:
				why = fmt.Sprintf("failed, exit %d", *v.Exit)
			}
			return v.Command + " " + why + " " + agoFrom(v.FinishedAt, now)
		}
	}
	if c := r.LastCommand; c != nil {
		switch {
		case c.Exit == nil:
			return fmt.Sprintf("last command ended (%s)", c.Cmdline)
		case *c.Exit == 0:
			return fmt.Sprintf("last command passed (%s)", c.Cmdline)
		default:
			return fmt.Sprintf("last command exited %d (%s)", *c.Exit, c.Cmdline)
		}
	}
	if r.Note != "" {
		return "not counted: " + r.Note
	}
	return "no check yet"
}

// agoFrom is a unix-nano time as "14m ago".
func agoFrom(ns int64, now time.Time) string {
	if ns == 0 {
		return ""
	}
	d := max(now.Sub(time.Unix(0, ns)), 0)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func newFanDiffCommand() *cobra.Command {
	var stat bool
	cmd := &cobra.Command{
		Use:   "diff <session-a> <session-b>",
		Short: "Show what one attempt of a fan-out did differently from another",
		Long: `Diff two attempts of a fan-out against each other: what the second one's
files hold that the first one's do not, committed or not, untracked files
included. Neither worktree is touched.

The diff is made by git on this machine, so both sessions must be here. For a
fan on another machine, bring the work here with 'tuios worktree pull'.`,
		Example: `  tuios fan diff api-fan-retry api-fan-retry-2
  tuios fan diff api-fan-retry api-fan-retry-2 --stat`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeWorktreeSessions,
		RunE: func(_ *cobra.Command, args []string) error {
			return runFanDiff(args[0], args[1], stat)
		},
	}
	cmd.Flags().BoolVar(&stat, "stat", false, "Show a summary of the changed files instead of the diff")
	return cmd
}

func runFanDiff(a, b string, stat bool) error {
	for _, name := range []string{a, b} {
		if host, s := splitHostSession(name); host != "" {
			return &diagnosticError{
				What:  fmt.Sprintf("fan diff reads the worktrees' files, and %s is on %s.", s, host),
				Cause: "the diff is made by git on this machine.",
				Fix:   fmt.Sprintf("run 'tuios worktree pull %s' to bring its work here, or 'tuios fan diff' on %s.", name, host),
			}
		}
	}
	rows, err := listWorktrees("", "", false)
	if err != nil {
		return err
	}
	find := func(name string) (*worktreeRow, error) {
		for i := range rows {
			if rows[i].Session == name {
				if rows[i].Gone {
					return nil, &diagnosticError{What: fmt.Sprintf("the worktree of %q is gone.", name), Cause: rows[i].Path + " no longer exists."}
				}
				return &rows[i], nil
			}
		}
		return nil, &diagnosticError{
			What:  fmt.Sprintf("session %q is not a worktree session.", name),
			Cause: "it is not in the worktree listing.",
			Fix:   "run 'tuios worktree ls'.",
		}
	}
	ra, err := find(a)
	if err != nil {
		return err
	}
	rb, err := find(b)
	if err != nil {
		return err
	}
	if ra.RepoRoot != rb.RepoRoot {
		return &diagnosticError{
			What:  fmt.Sprintf("%s and %s are worktrees of different repositories.", a, b),
			Cause: fmt.Sprintf("%s is of %s and %s of %s.", a, ra.RepoRoot, b, rb.RepoRoot),
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	diff, err := worktree.DiffWorking(ctx, ra.Path, rb.Path, stat)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Printf("%s and %s hold the same files.\n", a, b)
		return nil
	}
	fmt.Printf("diff from %s (%s) to %s (%s)\n", a, ra.Branch, b, rb.Branch)
	fmt.Print(diff)
	if !strings.HasSuffix(diff, "\n") {
		fmt.Println()
	}
	return nil
}

func newFanVerifyCommand() *cobra.Command {
	var jsonOut, noWait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "verify [<host>:]<session> -- <command>...",
		Short: "Run one check in every attempt of a fan-out",
		Long: `Run a command in every attempt of a fan-out, in a window named verify in
each session, and say which passed.

The command runs with sh -c in each worktree, with your PATH. One argument
after -- is a shell line, so it can hold && and pipes. Several are one
command and its arguments, each quoted for the shell as you gave it. It is always
the command you give: tuios never reads one from the repository. The window
holds no grants, so the check cannot drive tuios. A window whose check passed
closes; one whose check failed stays open so you can read the output, until
you press enter in it or the next check starts. A check still running in an attempt is stopped first.

The command waits for every check and exits 1 when any failed. --no-wait
returns once they are started; 'tuios fan compare' shows how they end.`,
		Example: `  tuios fan verify api-fan-retry -- go test ./...
  tuios fan verify api-fan-retry --timeout 10m -- 'make lint && make test'
  tuios fan verify api-fan-retry --no-wait -- go vet ./...`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			dash := c.ArgsLenAtDash()
			if dash != 1 {
				return errors.New("fan verify takes the session, then -- and the command: tuios fan verify <session> -- go test ./...") //nolint:staticcheck // ST1005: the string ends in the ./... package pattern, not a full stop
			}
			return fanVerifyRun(args[0], fanVerifyCommandLine(args[1:]), timeout, !noWait, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output result as JSON")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Return once the checks are started")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Count a check that runs longer than this as failed, and close its window (default: no limit)")
	return cmd
}

// fanVerifyRun is runFanVerify, replaced in tests.
var fanVerifyRun = runFanVerify

// fanVerifyCommandLine is the shell line for the words after --. One word is
// a shell line as it stands, so 'make lint && make test' keeps its &&. Several
// are argv: each is quoted for the shell where it needs it, so a pattern such
// as 'TestA|TestB' reaches the command as one argument instead of becoming a
// pipe. A word with nothing the shell reads specially is left bare, so a plain
// command reads the same, and still runs under cmd.exe on a Windows host.
func fanVerifyCommandLine(words []string) string {
	if len(words) == 1 {
		return words[0]
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = shellWord(w)
	}
	return strings.Join(quoted, " ")
}

// shellWord is w as one POSIX shell word: bare when every byte is one the
// shell passes through, single-quoted otherwise.
func shellWord(w string) string {
	if w == "" {
		return "''"
	}
	for _, r := range w {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r)) {
			return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
		}
	}
	return w
}

func runFanVerify(target, command string, timeout time.Duration, wait, jsonOutput bool) error {
	host, name := splitHostSession(target)
	t, err := dialHost(host)
	if err != nil {
		return err
	}
	defer t.Close()
	params := map[string]any{"session": name, "command": command}
	if timeout > 0 {
		params["timeout_ms"] = timeout.Milliseconds()
	}
	// The check finds its tools where this shell does. A host looks them up
	// on its own, and refuses variables from here.
	if path := os.Getenv("PATH"); path != "" && t.host == "" {
		params["env"] = map[string]string{"PATH": path}
	}
	raw, err := t.client.CallWithTimeout("verify-fan", params, time.Minute)
	if err != nil {
		return reportVerbError(t.explain("verify-fan", err), jsonOutput)
	}
	var started struct {
		Group    string   `json:"group"`
		Sessions []string `json:"sessions"`
		Skipped  []struct {
			Session string `json:"session"`
			Reason  string `json:"reason"`
		} `json:"skipped"`
	}
	if err := json.Unmarshal(raw, &started); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if !wait {
		if jsonOutput {
			return printVerbResultOn(t, raw, true)
		}
		fmt.Printf("Started %q in %d %s of %s%s. See how they end with 'tuios fan compare %s'.\n",
			command, len(started.Sessions), pluralWord(len(started.Sessions), "attempt", "attempts"), started.Group, t.on(), target)
		for _, s := range started.Skipped {
			fmt.Printf("  %s: not started, %s\n", s.Session, s.Reason)
		}
		return nil
	}
	if len(started.Sessions) == 0 {
		return reportVerbError(errors.New("no check was started"), jsonOutput)
	}
	if !jsonOutput {
		fmt.Printf("Running %q in %d %s of %s%s.\n", command, len(started.Sessions), pluralWord(len(started.Sessions), "attempt", "attempts"), started.Group, t.on())
	}
	rows, err := waitFanVerify(t, started.Sessions[0], started.Sessions)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	failed := 0
	for _, r := range rows {
		if r.Verify == nil || r.Verify.State != session.VerifyPassed {
			failed++
		}
	}
	if jsonOutput {
		out := map[string]any{"group": started.Group, "command": command, "rows": rows, "failed": failed}
		if len(started.Skipped) > 0 {
			out["skipped"] = started.Skipped
		}
		if t.host != "" {
			out["host"] = t.host
		}
		if err := printJSON(out); err != nil {
			return err
		}
	} else {
		for _, r := range rows {
			fmt.Printf("  %s: %s\n", r.Session, fanVerifyOutcome(r.Verify))
		}
		for _, s := range started.Skipped {
			fmt.Printf("  %s: not started, %s\n", s.Session, s.Reason)
		}
		fmt.Printf("%d of %d passed.\n", len(rows)-failed, len(rows))
	}
	if failed > 0 {
		return &statusError{code: 1}
	}
	return nil
}

// fanVerifyOutcome is one attempt's check as a clause.
func fanVerifyOutcome(v *session.FanVerify) string {
	if v == nil {
		return "no check recorded"
	}
	took := ""
	if v.FinishedAt > v.StartedAt && v.StartedAt > 0 {
		took = " in " + time.Duration(v.FinishedAt-v.StartedAt).Round(100*time.Millisecond).String()
	}
	switch {
	case v.State == session.VerifyPassed:
		return "passed" + took
	case v.Note != "":
		return "failed: " + v.Note
	case v.Exit != nil:
		return fmt.Sprintf("failed, exit %d%s. Its verify window is open with the output", *v.Exit, took)
	default:
		return "failed" + took
	}
}

// waitFanVerify polls compare-fan until the check in every session it
// started has ended. The daemon ends each one: it passes, fails, or times out.
func waitFanVerify(t *verbTarget, anySession string, sessions []string) ([]fanCompareRow, error) {
	want := map[string]bool{}
	for _, s := range sessions {
		want[s] = true
	}
	for {
		raw, err := t.client.CallWithTimeout("compare-fan", map[string]any{"session": anySession, "changes": false}, time.Minute)
		if err != nil {
			return nil, t.explain("compare-fan", err)
		}
		var res fanCompareResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
		var rows []fanCompareRow
		running := false
		for _, r := range res.Rows {
			if !want[r.Session] {
				continue
			}
			rows = append(rows, r)
			if r.Verify != nil && r.Verify.State == session.VerifyRunning {
				running = true
			}
		}
		if !running {
			return rows, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}
