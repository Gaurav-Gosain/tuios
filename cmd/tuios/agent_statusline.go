package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// agentStatusLineDeadline bounds the tuios half of a status line run: reading
// the payload, finding the pane and reporting. Claude Code runs the command
// on every change to the conversation, so a slow daemon must not hold it.
const agentStatusLineDeadline = 300 * time.Millisecond

// statusLineInterval is the shortest gap between two reports for one pane
// while the values keep changing. A status line runs up to three times a
// second during a turn, and the rail has no use for every one.
const statusLineInterval = 15 * time.Second

// statusLineRefresh is how long unchanged values go before they are sent
// again, so a daemon that restarted gets them back.
const statusLineRefresh = 10 * time.Minute

// statusLineNoPaneWait is how long a process no pane was found for waits
// before it asks the daemon again.
const statusLineNoPaneWait = time.Minute

// contextWarnAt is the context use the rail warns at. Crossing it is reported
// at once, whatever the interval.
const contextWarnAt = 80

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
prints its output unchanged, exiting with its status; that is how a status
line of your own is kept.

Every field is optional: a field the payload does not have is not written.
The pane is found from --window, then TUIOS_PANE_ID, then the process's
terminal and parent processes, as for agent-hook. It calls only
set-agent-meta, for that pane. It reports at most once every 15 seconds per
pane while the values change (a model change, context use crossing 80%, or
anything with --turn-end goes at once), and not at all while they stay the same, except once every 10
minutes so a restarted daemon gets them back. The last values sent are kept
beside the daemon's socket, in statusline-<session>-<pane>.json. It reads at
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
					return session.DialVerbClientAs(version)
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
	cmd.Flags().StringVarP(&o.session, "session", "s", "", "Session of the pane to report for (default: TUIOS_SESSION)")
	cmd.Flags().StringVarP(&o.window, "window", "w", "", "Pane to report for (default: TUIOS_PANE_ID, then the controlling terminal, then the parent processes)")
	cmd.Flags().StringVar(&o.then, "then", "", "Your own status line command: run with the same stdin, its output printed unchanged")
	cmd.Flags().BoolVar(&o.explain, "explain", false, "Print what was decided and why to stderr")
	cmd.Flags().BoolVar(&o.turnEnd, "turn-end", false, "The turn just ended: send changed values now, whatever the interval")
	cmd.Flags().DurationVar(&o.timeout, "timeout", agentStatusLineDeadline, "Give up on the daemon after this long")
	cmd.Flags().IntVar(&o.integration, "integration", 0, "Version marker of a managed entry; ignored")
	_ = cmd.Flags().MarkHidden("integration")
	return cmd
}

// statusLineOutcome is what --explain prints.
type statusLineOutcome struct {
	Harness string                        `json:"harness"`
	Values  *integration.StatusLineValues `json:"values,omitempty"`
	Session string                        `json:"session,omitempty"`
	Window  string                        `json:"window,omitempty"`
	PaneBy  string                        `json:"pane_by,omitempty"`
	// Sent says set-agent-meta was called and answered.
	Sent  bool   `json:"sent"`
	Skip  string `json:"skip,omitempty"`
	Error string `json:"error,omitempty"`
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
	done := make(chan statusLineOutcome, 1)
	go func() { done <- reportStatusLine(o, harness, payload, sio) }()
	var out statusLineOutcome
	select {
	case out = <-done:
	case <-time.After(timeout):
		out = statusLineOutcome{Harness: harness, Error: "gave up after " + timeout.String()}
	}
	if o.explain && sio.stderr != nil {
		line, _ := json.Marshal(out)
		fmt.Fprintln(sio.stderr, string(line))
	}
	return <-thenDone
}

// reportStatusLine reads the payload and, when the values are due, writes
// them to the pane.
func reportStatusLine(o agentStatusLineOptions, harness string, payload []byte, sio agentStatusLineIO) statusLineOutcome {
	out := statusLineOutcome{Harness: harness}
	if len(bytes.TrimSpace(payload)) == 0 {
		out.Skip = "empty payload"
		return out
	}
	if owner, foreign := integration.StatusLineForeign(harness, sio.getenv); foreign {
		out.Skip = "foreign harness: " + integration.AgentHintEnv + " names " + owner
		return out
	}
	values, err := integration.ParseStatusLine(harness, payload)
	if err != nil {
		out.Skip = err.Error()
		return out
	}
	out.Values = &values
	if values.Empty() {
		out.Skip = "the payload names no model, context or cost"
		return out
	}

	var client verbCaller
	closeClient := func() {
		if c, ok := client.(io.Closer); ok {
			_ = c.Close()
		}
	}
	defer closeClient()
	getenv := sio.getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	now := time.Now
	if sio.now != nil {
		now = sio.now
	}
	stampDir := ""
	if sio.stampDir != nil {
		if dir, err := sio.stampDir(); err == nil {
			stampDir = dir
		}
	}
	out.Session = firstNonEmptyString(o.session, getenv("TUIOS_SESSION"))
	switch {
	case o.window != "":
		out.Window, out.PaneBy = o.window, "flag"
	case getenv("TUIOS_PANE_ID") != "":
		out.Window, out.PaneBy = getenv("TUIOS_PANE_ID"), "env"
	default:
		// No pane named: ask the daemon which pane this process runs in,
		// the way agent-hook does. A status line runs several times a
		// second, so a process in no pane (a harness outside tuios) asks
		// at most once a minute.
		var sid int
		var ancestors []int
		if sio.self != nil {
			sid, ancestors = sio.self()
		}
		noPane := ""
		if stampDir != "" {
			noPane = filepath.Join(stampDir, statusLineStampName("nopane", strconv.Itoa(sid)))
			if fi, err := os.Stat(noPane); err == nil && now().Sub(fi.ModTime()) < statusLineNoPaneWait {
				out.Skip = "no pane found for this process in the last minute"
				return out
			}
		}
		if client, err = sio.dial(); err != nil {
			out.Error = err.Error()
			return out
		}
		out.Session, out.Window, out.PaneBy, err = resolveHookPane(agentHookOptions{}, agentHookIO{getenv: getenv}, client, sid, ancestors)
		if err != nil {
			if noPane != "" {
				_ = os.WriteFile(noPane, nil, 0o600)
				_ = os.Chtimes(noPane, now(), now())
			}
			out.Error = err.Error()
			return out
		}
	}

	stampPath := ""
	var prev *statusLineStamp
	if stampDir != "" {
		stampPath = filepath.Join(stampDir, statusLineStampName(out.Session, out.Window))
		prev = readStatusLineStamp(stampPath)
	}
	if due, why := statusLineDue(prev, values, now(), o.turnEnd); !due {
		out.Skip = why
		return out
	}

	if client == nil {
		if client, err = sio.dial(); err != nil {
			out.Error = err.Error()
			return out
		}
	}
	params := map[string]any{
		"window": out.Window,
		"tokens": values.Tokens(),
		"source": integration.StatusLineSource,
	}
	if out.Session != "" {
		params["session"] = out.Session
	}
	if _, err := client.Call("set-agent-meta", params); err != nil {
		out.Error = err.Error()
		return out
	}
	out.Sent = true
	if stampPath != "" {
		writeStatusLineStamp(stampPath, prev, values, now())
	}
	return out
}

// statusLineStamp is the last values sent for one pane, and when.
type statusLineStamp struct {
	Session string            `json:"session,omitempty"`
	Values  map[string]string `json:"values"`
	// At is when they were sent, in Unix milliseconds.
	At int64 `json:"at"`
}

// statusLineDue says whether values should be sent now, given what was sent
// last, and why not when they should not. turnEnd sends changed values at
// once, so a turn's last cost is not held until the next turn.
func statusLineDue(prev *statusLineStamp, values integration.StatusLineValues, now time.Time, turnEnd bool) (bool, string) {
	if prev == nil {
		return true, ""
	}
	if values.Session != prev.Session {
		return true, ""
	}
	since := now.Sub(time.UnixMilli(prev.At))
	if since < 0 {
		// The clock went back: treat the stamp as old.
		return true, ""
	}
	next := values.Tokens()
	changed := false
	for k, v := range next {
		if prev.Values[k] != v {
			changed = true
			break
		}
	}
	if !changed {
		if since >= statusLineRefresh {
			return true, ""
		}
		return false, "unchanged since the last report"
	}
	if next[integration.MetaModel] != "" && next[integration.MetaModel] != prev.Values[integration.MetaModel] {
		return true, ""
	}
	if warned(prev.Values[integration.MetaContext]) != warned(next[integration.MetaContext]) && next[integration.MetaContext] != "" {
		return true, ""
	}
	if since >= statusLineInterval || turnEnd {
		return true, ""
	}
	return false, "changed, but reported " + since.Round(time.Millisecond).String() + " ago"
}

// warned reports whether a context value is at or over the rail's warning.
func warned(context string) bool {
	n, err := strconv.Atoi(strings.TrimSuffix(context, "%"))
	return err == nil && n >= contextWarnAt
}

// statusLineStampName is the stamp file for one pane. Anything but letters,
// digits, '-' and '_' becomes '_', so a name cannot leave the directory.
func statusLineStampName(sessionName, window string) string {
	safe := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
				b.WriteRune(r)
			default:
				b.WriteByte('_')
			}
		}
		return b.String()
	}
	return "statusline-" + safe(sessionName) + "-" + safe(window) + ".json"
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

func readStatusLineStamp(path string) *statusLineStamp {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 64<<10 {
		return nil
	}
	var st statusLineStamp
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	return &st
}

// writeStatusLineStamp records what was sent, merged over what was sent
// before, since a payload that leaves a field out does not clear it.
func writeStatusLineStamp(path string, prev *statusLineStamp, values integration.StatusLineValues, now time.Time) {
	st := statusLineStamp{Session: values.Session, Values: map[string]string{}, At: now.UnixMilli()}
	if prev != nil && prev.Session == values.Session {
		maps.Copy(st.Values, prev.Values)
	}
	maps.Copy(st.Values, values.Tokens())
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".statusline-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(name, 0o600) != nil {
		return
	}
	_ = os.Rename(name, path)
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
