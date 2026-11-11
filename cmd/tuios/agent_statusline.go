package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/agenthook"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// agentStatusLineDeadline bounds the tuios half of a status line run: reading
// the payload, finding the pane and reporting. Claude Code runs the command
// on every change to the conversation, so a slow daemon must not hold it.
const agentStatusLineDeadline = 300 * time.Millisecond

// agentStatusLineOptions are the flags of tuios agent-statusline.
type agentStatusLineOptions struct {
	session string
	window  string
	then    string
	explain bool
	// turnEnd says the harness's turn just ended, so values that changed
	// go now, whatever the interval.
	turnEnd bool
	timeout time.Duration
	// integration is the version marker of a managed entry, ignored.
	integration int
}

// agentStatusLineIO is everything a status line run touches outside itself.
type agentStatusLineIO struct {
	stdin    io.Reader
	stdinTTY bool
	stdout   io.Writer
	stderr   io.Writer
	getenv   func(string) string
	dial     func() (verbCaller, error)
	self     func() (sid int, ancestors []int)
	// stampDir is where the last values sent for each pane are kept.
	stampDir func() (string, error)
	now      func() time.Time
	// runThen runs the person's own status line command.
	runThen func(ctx context.Context, command string, stdin []byte, stdout, stderr io.Writer) int
}

func newAgentStatusLineCommand() *cobra.Command {
	var o agentStatusLineOptions
	cmd := &cobra.Command{
		Use:   "agent-statusline <harness>",
		Short: "Feed a harness's status line to the pane's agent metadata",
		Long: `Read a coding-agent harness's status line payload on stdin and write the
model, context use and cost it names to the tuios pane it runs in, as agent
metadata (model, context, cost) for the rail, the Inbox and the peek.

This is what the status line tuios integration install claude-code --statusline
writes runs. Claude Code runs it with a JSON object on stdin each time the
conversation changes. The opencode plugin runs it for opencode and Kilo with
the assistant message's model and cost.

It prints nothing of its own, so a Claude Code status line of only this is
empty. --then runs your own status line command with the same stdin and
prints its output unchanged, exiting with its status. That is how a status
line of your own is kept.

Every field is optional: a field the payload does not have is not written.
The pane is found from --window, then TUIOS_PANE_ID, then the process's
terminal and parent processes, as for agent-hook. It calls only
set-agent-meta, for that pane. It reports at most once every 15 seconds per
pane while the values change (a model change, context use crossing 80%, or
anything with --turn-end goes at once), and not at all while they stay the same, except once every 10
minutes so a restarted daemon gets them back. What the interval holds back is
kept and sent by the next run that is due, or at the end of the turn: the
Claude Code Stop hook (tuios agent-hook) sends it, and a run after the Stop
goes at once. The last values sent and the held ones are kept beside the
daemon's socket, in statusline-<session>-<pane>.json. It reads at
most 1 MiB of stdin, gives up on the daemon after 300ms, and exits 0 whatever
goes wrong on the tuios side.`,
		Example: `  # What the Claude Code status line runs
  tuios agent-statusline claude-code

  # Keep your own status line and feed tuios too
  tuios agent-statusline claude-code --then '~/.claude/statusline.sh'

  # Try a payload by hand
  echo '{"model":{"display_name":"Opus"},"context_window":{"used_percentage":42}}' | tuios agent-statusline claude-code --explain`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			code := runAgentStatusLine(o, args[0], agentStatusLineIO{
				stdin:    os.Stdin,
				stdinTTY: term.IsTerminal(int(os.Stdin.Fd())),
				stdout:   os.Stdout,
				stderr:   os.Stderr,
				getenv:   os.Getenv,
				dial: func() (verbCaller, error) {
					// A failed dial returns a nil *VerbClient. Returning it as
					// it is would put a typed nil in the interface, which a
					// later io.Closer check takes for a live client (#374).
					c, err := session.DialVerbClientAs(version)
					if err != nil {
						return nil, err
					}
					return c, nil
				},
				self:     integration.SelfProcess,
				stampDir: statusLineStampDir,
				now:      time.Now,
				runThen:  runStatusLineThen,
			})
			if code != 0 {
				return &statusError{code: code}
			}
			return nil
		},
	}
	addSessionFlag(cmd, &o.session, "Session of the pane to report for (default: TUIOS_SESSION)")
	addWindowFlag(cmd, &o.window, "Pane to report for (default: TUIOS_PANE_ID, then the controlling terminal, then the parent processes)")
	cmd.Flags().StringVar(&o.then, "then", "", "Your own status line command: run with the same stdin, its output printed unchanged")
	cmd.Flags().BoolVar(&o.explain, "explain", false, "Print what was decided and why to stderr")
	cmd.Flags().BoolVar(&o.turnEnd, "turn-end", false, "The turn just ended: send changed values now, whatever the interval")
	cmd.Flags().DurationVar(&o.timeout, "timeout", agentStatusLineDeadline, "Give up on the daemon after this long")
	cmd.Flags().IntVar(&o.integration, "integration", 0, "Version marker of a managed entry. tuios ignores it")
	_ = cmd.Flags().MarkHidden("integration")
	return cmd
}

// runAgentStatusLine runs one status line and returns the exit code: the
// chained command's, or 0.
func runAgentStatusLine(o agentStatusLineOptions, harness string, sio agentStatusLineIO) int {
	var payload []byte
	if sio.stdin != nil && !sio.stdinTTY {
		payload, _ = io.ReadAll(io.LimitReader(sio.stdin, integration.StatusLineMaxPayload))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	thenDone := make(chan int, 1)
	if o.then != "" && sio.runThen != nil {
		go func() { thenDone <- sio.runThen(ctx, o.then, payload, sio.stdout, sio.stderr) }()
	} else {
		thenDone <- 0
	}

	timeout := o.timeout
	if timeout <= 0 {
		timeout = agentStatusLineDeadline
	}
	done := make(chan agenthook.StatusLineOutcome, 1)
	go func() {
		done <- agenthook.ReportStatusLine(agenthook.StatusLineOptions{Session: o.session, Window: o.window, TurnEnd: o.turnEnd}, harness, payload,
			agenthook.IO{Getenv: sio.getenv, Dial: sio.dial, Self: sio.self, StampDir: sio.stampDir, Now: sio.now})
	}()
	var out agenthook.StatusLineOutcome
	select {
	case out = <-done:
	case <-time.After(timeout):
		out = agenthook.StatusLineOutcome{Harness: harness, Error: "gave up after " + timeout.String()}
	}
	if o.explain && sio.stderr != nil {
		line, _ := jsonx.Marshal(out)
		fmt.Fprintln(sio.stderr, string(line))
	}
	return <-thenDone
}

// statusLineStampDir is the directory of the daemon's socket, which is per
// user, 0700, and cleared with the boot on the systems that have
// XDG_RUNTIME_DIR.
func statusLineStampDir() (string, error) {
	sock, err := session.GetSocketPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(sock), nil
}

// runStatusLineThen runs the person's status line command through the shell,
// with the same stdin, its output copied through unchanged. It returns the
// command's exit status.
func runStatusLineThen(ctx context.Context, command string, stdin []byte, stdout, stderr io.Writer) int {
	cmd := statusLineShell(ctx, command)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		if code := exit.ExitCode(); code > 0 {
			return code
		}
		return 1
	default:
		fmt.Fprintf(stderr, "tuios agent-statusline: %v\n", err)
		return 127
	}
}

// statusLineShell is the command that runs a status line command line: sh -c,
// the shell Claude Code runs its status line through. On Windows it is the sh
// on PATH (Git Bash, which Claude Code needs there), else cmd /C.
func statusLineShell(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		if sh, err := exec.LookPath("sh"); err == nil {
			return exec.CommandContext(ctx, sh, "-c", command)
		}
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command)
}
