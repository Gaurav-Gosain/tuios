package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Copy pipes: a yank piped through a command, after tmux's copy-pipe.
//
// Two things pipe a yank. appearance.selection.copy_command pipes every yank
// in copy mode (y, c, and y in multi copy mode), and a [[keybindings.copy_pipe]]
// entry pipes the yank of its own key through its own command.
//
// The command runs with sh -c, as a shell command key does: on the machine
// that runs this client (for the SSH and web servers, the server), in the
// focused pane's folder, with the command-key variables. The selection is its
// stdin. The clipboard gets its stdout, or the selection itself when the
// command writes nothing, fails or runs too long. tmux's copy-pipe always puts
// the selection in a buffer and hands the command a copy, so a command that
// only sends the text somewhere (a socket, a file) still leaves the selection
// to paste. Here the stdout wins when there is one, because the point of a
// flatten or a jq filter is to paste its result.
//
// The clipboard is written once, when the command ends, so a clipboard
// manager records one entry and never the raw text then the result.
//
// The command runs in a tea.Cmd, off the update loop, and is stopped after
// CopyPipeTimeout. Its process group goes with it where there is one.

// CopyPipeTimeout is how long a copy command may run.
const CopyPipeTimeout = 10 * time.Second

// copyPipeMaxOutput caps what a copy command may write to stdout. A clipboard
// write this large is already past what most terminals take over OSC 52.
const copyPipeMaxOutput = 16 << 20

// CopyPipeDoneMsg reports how a copy command ended.
type CopyPipeDoneMsg struct {
	Label     string
	Selection string
	Output    string
	Err       error
}

// copyPipeRunner runs a copy command. Tests replace it.
var copyPipeRunner = runCopyPipe

// errCopyPipeTimeout is the error of a command stopped after CopyPipeTimeout.
var errCopyPipeTimeout = errors.New("timeout")

// errCopyPipeTooLarge is the error of a command that wrote more than
// copyPipeMaxOutput.
var errCopyPipeTooLarge = errors.New("output too large")

// Yank puts a copy-mode selection on the clipboard: through
// appearance.selection.copy_command when it is set, else as it is.
func (m *OS) Yank(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	if cmd := m.Settings.CopyCommand; cmd != "" {
		return m.PipeYank(text, cmd, config.CopyCommandLabel(cmd))
	}
	return m.clipboardWriteCmd(text)
}

// PipeYank runs command with text on stdin, off the update loop. The
// CopyPipeDoneMsg it yields puts the result on the clipboard.
func (m *OS) PipeYank(text, command, label string) tea.Cmd {
	if text == "" {
		return nil
	}
	m.CancelPendingCopy()
	dir := m.scratchDir()
	argv := commandArgv(command, m.commandEnv(dir))
	return func() tea.Msg {
		out, err := copyPipeRunner(argv, dir, text, CopyPipeTimeout)
		return CopyPipeDoneMsg{Label: label, Selection: text, Output: copyPipeOutput(out, text), Err: err}
	}
}

// copyPipeOutput is the text the clipboard gets from a command's stdout. A
// filter such as sed or jq ends its output with a new line, and a paste that
// ends in one runs the line in a shell. So when the selection has no new line
// at its end, one is taken off the output.
func copyPipeOutput(out, selection string) string {
	if strings.HasSuffix(selection, "\n") {
		return out
	}
	if s, ok := strings.CutSuffix(out, "\r\n"); ok {
		return s
	}
	return strings.TrimSuffix(out, "\n")
}

// handleCopyPipeDone puts the result of a copy command on the clipboard and
// says what happened.
func (m *OS) handleCopyPipeDone(msg CopyPipeDoneMsg) tea.Cmd {
	d := m.Settings.NotificationDuration
	switch {
	case msg.Err != nil:
		m.ShowNotification(copyPipeFailure(msg.Label, msg.Err)+fmt.Sprintf(" Copied the selection (%d chars).", len(msg.Selection)), "error", 2*d)
		return m.clipboardWriteCmd(msg.Selection)
	case msg.Output == "":
		m.ShowNotification(fmt.Sprintf("%s wrote no output. Copied the selection (%d chars).", msg.Label, len(msg.Selection)), "info", d)
		return m.clipboardWriteCmd(msg.Selection)
	default:
		m.ShowNotification(fmt.Sprintf("Copied %d chars from %s.", len(msg.Output), msg.Label), "success", d)
		return m.clipboardWriteCmd(msg.Output)
	}
}

// copyPipeFailure is the dock message for a command that failed: the exit
// code and the first line of stderr.
func copyPipeFailure(label string, err error) string {
	var exit *copyPipeExitError
	switch {
	case errors.Is(err, errCopyPipeTimeout):
		return fmt.Sprintf("%s did not stop in %d seconds. tuios stopped it.", label, int(CopyPipeTimeout/time.Second))
	case errors.Is(err, errCopyPipeTooLarge):
		return fmt.Sprintf("%s wrote more than %d MB.", label, copyPipeMaxOutput>>20)
	case errors.As(err, &exit):
		if exit.stderr != "" {
			return fmt.Sprintf("%s failed with exit code %d: %s.", label, exit.code, strings.TrimSuffix(exit.stderr, "."))
		}
		return fmt.Sprintf("%s failed with exit code %d.", label, exit.code)
	default:
		return fmt.Sprintf("%s did not start: %v.", label, err)
	}
}

// copyPipeExitError is a command that exited with a code other than 0.
type copyPipeExitError struct {
	code   int
	stderr string // the first line that is not empty
}

func (e *copyPipeExitError) Error() string {
	if e.stderr == "" {
		return fmt.Sprintf("exit code %d", e.code)
	}
	return fmt.Sprintf("exit code %d: %s", e.code, e.stderr)
}

// cappedBuffer keeps the first max bytes written to it and notes the rest.
type cappedBuffer struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room < len(p) {
		b.over = true
		p = p[:max(room, 0)]
	}
	b.buf.Write(p)
	// The whole write is reported, so the command is not killed by a short
	// write while it drains the rest.
	return len(p), nil
}

// runCopyPipe runs argv with input on stdin and returns its stdout.
func runCopyPipe(argv []string, dir, input string, timeout time.Duration) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("the command is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 - the user's own config.toml names the command
	cmd.Dir = dir
	cmd.Env = os.Environ()
	cmd.Stdin = strings.NewReader(input)
	stdout := &cappedBuffer{max: copyPipeMaxOutput}
	stderr := &cappedBuffer{max: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// A child the command put in the background can hold stdout open after
	// the command exits. It is let go after this long.
	cmd.WaitDelay = time.Second
	detachFromTerminal(cmd)
	killGroupOnCancel(cmd)
	err := cmd.Run()
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return "", errCopyPipeTimeout
	case stdout.over:
		return "", errCopyPipeTooLarge
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "", &copyPipeExitError{code: exit.ExitCode(), stderr: firstTextLine(stderr.buf.String())}
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return "", err
	}
	return stdout.buf.String(), nil
}

// firstTextLine is the first line of s that is not blank, trimmed.
func firstTextLine(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
