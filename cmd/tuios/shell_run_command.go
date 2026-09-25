package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// runResult is the run verb's result as the CLI reads it.
type runResult struct {
	Window     string `json:"window"`
	Cmdline    string `json:"cmdline"`
	ExitCode   *int   `json:"exit_code"`
	Output     string `json:"output"`
	Truncated  bool   `json:"truncated"`
	DurationMS int64  `json:"duration_ms"`
	CommandSeq uint64 `json:"command_seq"`
}

// runInPane is `tuios run`: it types one command line at a pane's prompt,
// waits for it, prints what it printed and exits with its status.
func runInPane(sessionName, windowTarget string, args []string, timeout, lines int, jsonOutput bool) error {
	command := strings.Join(args, " ")
	if strings.TrimSpace(command) == "" {
		return errors.New("run needs a command line, e.g. tuios run -w build -- go test ./...") //nolint:staticcheck // ST1005: the string ends in the ./... package pattern, not a full stop
	}
	t, err := dialTarget(sessionName, windowTarget)
	if err != nil {
		return err
	}
	defer t.Close()

	params := map[string]any{
		"session": sessionName,
		"window":  windowTarget,
		"command": command,
	}
	if timeout > 0 {
		params["timeout"] = timeout
	}
	if lines > 0 {
		params["lines"] = lines
	}
	wait := time.Duration(timeout) * time.Millisecond
	if timeout <= 0 {
		wait = 30 * time.Second
	}
	raw, err := t.client.CallWithTimeout("run", t.params(params), wait+15*time.Second)
	if err != nil {
		return reportVerbError(t.explain("run", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}
	return printRunResult(os.Stdout, os.Stderr, raw)
}

// printRunResult writes the command's output to out and returns its status as
// the process's, so `tuios run` composes in a script the way the command
// itself would. A shell that sent no status is said on errs, and the run
// exits 0.
func printRunResult(out, errs io.Writer, raw json.RawMessage) error {
	var res runResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if res.Output != "" {
		fmt.Fprintln(out, res.Output)
	}
	if res.Truncated {
		fmt.Fprintln(errs, "tuios: the output was cut; the lines above are its end")
	}
	if res.ExitCode == nil {
		fmt.Fprintln(errs, "tuios: the shell reported no exit status for this command")
		return nil
	}
	if *res.ExitCode != 0 {
		return &statusError{code: *res.ExitCode}
	}
	return nil
}

// newRunCommand is `tuios run`.
func newRunCommand() *cobra.Command {
	var sessionName, windowTarget string
	var timeout, lines int
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "run -- <command line>",
		Short: "Run a command at a pane's shell prompt and exit with its status",
		Long: `Type one command line at a pane's shell prompt, wait for it to finish, print
what it printed, and exit with its exit status.

It reads where the command starts and ends from the shell's own OSC 133 marks,
so it needs a shell with prompt integration: fish and zsh with it turned on,
bash with a setup, or any shell a terminal injects its integration script into.
A pane whose shell sends no marks is refused with no_shell_integration, and
nothing is typed.

It types only at a prompt. A pane already running a command is refused with
not_at_prompt, and nothing is typed. The words after -- are joined with spaces
into one line that the shell parses, so quote for the shell as you would when
typing it.

A timeout does not stop the command. The error names the wait-for command that
picks it up where run left off.`,
		Example: `  # Run the tests in the build pane and use the status
  tuios run -w build --timeout 600000 -- go test ./... && echo passed

  # Only the last 40 lines of a long build
  tuios run -w build --lines 40 -- make

  # The whole result, with the exit code and how long it took
  tuios run -w build --json -- make lint`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			return runInPane(sessionName, windowTarget, args, timeout, lines, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Target session (default: most recently active)")
	cmd.Flags().StringVarP(&windowTarget, "window", "w", "", "Target window by name or ID (default: focused)")
	cmd.Flags().IntVar(&timeout, "timeout", 0, "Milliseconds to wait for the command (default: 30000)")
	cmd.Flags().IntVar(&lines, "lines", 0, "Keep only the last N lines of the output (0 keeps all)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}
