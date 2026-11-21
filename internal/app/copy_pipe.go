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
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

// CopyPipeTimeout is how long a copy command may run.
const CopyPipeTimeout = 10 * time.Second

const copyPipeMaxOutput = 16 << 20

// CopyPipeDoneMsg reports how a copy command ended.
type CopyPipeDoneMsg struct {
	Label     string
	Selection string
	Output    string
	Err       error
}

var copyPipeRunner = runCopyPipe

var errCopyPipeTimeout = errors.New("timeout")

var errCopyPipeTooLarge = errors.New("output too large")

// Yank puts a copy-mode selection on the clipboard: through
// appearance.selection.copy_command when it is set, else as it is. It also
// keeps the selection as a paste buffer (paste_buffers.go), unless a
// copy_command exits 0 with no output (handleCopyPipeDone).
func (m *OS) Yank(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	if cmd := m.Settings.CopyCommand; cmd != "" {
		return m.PipeYank(text, cmd, config.CopyCommandLabel(cmd))
	}
	return tea.Batch(m.clipboardWriteCmd(text), m.SaveToPasteBuffers(text))
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

func copyPipeOutput(out, selection string) string {
	if strings.HasSuffix(selection, "\n") {
		return out
	}
	if s, ok := strings.CutSuffix(out, "\r\n"); ok {
		return s
	}
	return strings.TrimSuffix(out, "\n")
}

func (m *OS) handleCopyPipeDone(msg CopyPipeDoneMsg) tea.Cmd {
	d := m.Settings.NotificationDuration
	switch {
	case msg.Err != nil:
		m.ShowNotification(copyPipeFailure(msg.Label, msg.Err)+fmt.Sprintf(" Copied the selection (%d chars).", len(msg.Selection)), "error", 2*d)
		return tea.Batch(m.clipboardWriteCmd(msg.Selection), m.SaveToPasteBuffers(msg.Selection))
	case msg.Output == "":
		m.ShowNotification(msg.Label+" wrote no output. The clipboard is unchanged.", "info", d)
		return nil
	default:
		m.ShowNotification(fmt.Sprintf("Copied %d chars from %s.", len(msg.Output), msg.Label), "success", d)
		return tea.Batch(m.clipboardWriteCmd(msg.Output), m.SaveToPasteBuffers(msg.Selection))
	}
}

func copyPipeFailure(label string, err error) string {
	var exit *copyPipeExitError
	switch {
	case errors.Is(err, errCopyPipeTimeout):
		return fmt.Sprintf("%s did not stop in %d seconds. tuios stopped it.", label, int(CopyPipeTimeout/time.Second))
	case errors.Is(err, errCopyPipeTooLarge):
		return label + " wrote more than " + units.Bytes(int64(copyPipeMaxOutput)) + "."
	case errors.As(err, &exit):
		if exit.stderr != "" {
			return fmt.Sprintf("%s failed with exit code %d: %s.", label, exit.code, strings.TrimSuffix(exit.stderr, "."))
		}
		return fmt.Sprintf("%s failed with exit code %d.", label, exit.code)
	default:
		return fmt.Sprintf("%s did not start: %v.", label, err)
	}
}

type copyPipeExitError struct {
	code   int
	stderr string
}

func (e *copyPipeExitError) Error() string {
	if e.stderr == "" {
		return fmt.Sprintf("exit code %d", e.code)
	}
	return fmt.Sprintf("exit code %d: %s", e.code, e.stderr)
}

type cappedBuffer struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := b.max - b.buf.Len(); room < n {
		b.over = true
		p = p[:max(room, 0)]
	}
	b.buf.Write(p)
	return n, nil
}

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
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return "", &copyPipeExitError{code: exit.ExitCode(), stderr: firstTextLine(stderr.buf.String())}
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return "", err
	}
	return stdout.buf.String(), nil
}

func firstTextLine(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
