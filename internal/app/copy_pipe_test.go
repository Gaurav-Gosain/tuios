package app

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func skipWithoutSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the copy command runs with sh -c")
	}
}

// pipeOS is a client whose clipboard writes are OSC 52 only, so a test never
// touches the clipboard of the machine that runs it.
func pipeOS(t *testing.T) *OS {
	t.Helper()
	m := scratchOS(t, false)
	m.RemoteClient = true
	return m
}

// The selection goes to stdin, and stdout comes back.
func TestRunCopyPipeFeedsStdinAndReadsStdout(t *testing.T) {
	skipWithoutSh(t)
	out, err := runCopyPipe([]string{"sh", "-c", "tr '\\n' ' '"}, t.TempDir(), "a\nb\nc", time.Second)
	if err != nil || out != "a b c" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

// A non-zero exit carries its code and the first line of stderr.
func TestRunCopyPipeReportsTheExitCode(t *testing.T) {
	skipWithoutSh(t)
	_, err := runCopyPipe([]string{"sh", "-c", "cat >/dev/null; printf '\\nno socket here\\nmore\\n' >&2; exit 7"}, "", "x", time.Second)
	var exit *copyPipeExitError
	if !errors.As(err, &exit) || exit.code != 7 || exit.stderr != "no socket here" {
		t.Fatalf("err = %#v", err)
	}
	if got := copyPipeFailure("flatten", err); got != "flatten failed with exit code 7: no socket here." {
		t.Fatalf("message = %q", got)
	}
}

// A command that runs too long is stopped, with its children, in time.
func TestRunCopyPipeTimesOut(t *testing.T) {
	skipWithoutSh(t)
	start := time.Now()
	_, err := runCopyPipe([]string{"sh", "-c", "sleep 30; echo late"}, "", "x", 200*time.Millisecond)
	if !errors.Is(err, errCopyPipeTimeout) {
		t.Fatalf("err = %v, want the timeout", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the stop took %v", took)
	}
	if got := copyPipeFailure("slow", err); got != "slow did not stop in 10 seconds. tuios stopped it." {
		t.Fatalf("message = %q", got)
	}
}

// One new line comes off the end of the output unless the selection ends in
// one.
func TestCopyPipeOutputTrimsOneNewLine(t *testing.T) {
	cases := []struct{ out, sel, want string }{
		{"a b\n", "a\nb", "a b"},
		{"a b\r\n", "a\nb", "a b"},
		{"a b\n\n", "a\nb", "a b\n"},
		{"a\nb\n", "a\nb\n", "a\nb\n"},
		{"", "x", ""},
	}
	for _, c := range cases {
		if got := copyPipeOutput(c.out, c.sel); got != c.want {
			t.Errorf("copyPipeOutput(%q, %q) = %q, want %q", c.out, c.sel, got, c.want)
		}
	}
}

// PipeYank runs the command with the command-key variables, and its output
// goes to the clipboard.
func TestPipeYankPutsTheOutputOnTheClipboard(t *testing.T) {
	skipWithoutSh(t)
	m := pipeOS(t)
	cmd := m.PipeYank("one\ntwo", `printf '%s|%s|' "$TUIOS_SESSION" "$TUIOS_ACTIVE_PANE_ID"; tr '\n' ' '`, "flatten")
	msg, ok := cmd().(CopyPipeDoneMsg)
	if !ok {
		t.Fatalf("the command yielded %T", cmd())
	}
	id := ""
	if w := m.GetFocusedWindow(); w != nil {
		id = w.ID
	}
	want := "work|" + id + "|one two"
	if msg.Err != nil || msg.Output != want {
		t.Fatalf("msg = %+v, want output %q", msg, want)
	}
	if got := fmt.Sprint(m.handleCopyPipeDone(msg)()); got != want {
		t.Fatalf("clipboard = %q, want %q", got, want)
	}
	if got := lastNotificationText(t, m); got != fmt.Sprintf("Copied %d chars from flatten.", len(want)) {
		t.Fatalf("message = %q", got)
	}
}

// A command with no output, a failure and a timeout leave the selection on
// the clipboard.
func TestCopyPipeFallsBackToTheSelection(t *testing.T) {
	m := pipeOS(t)
	cases := []struct {
		msg  CopyPipeDoneMsg
		note string
	}{
		{CopyPipeDoneMsg{Label: "send", Selection: "raw"}, "send wrote no output. Copied the selection (3 chars)."},
		{CopyPipeDoneMsg{Label: "send", Selection: "raw", Err: &copyPipeExitError{code: 2, stderr: "refused"}},
			"send failed with exit code 2: refused. Copied the selection (3 chars)."},
		{CopyPipeDoneMsg{Label: "send", Selection: "raw", Err: errCopyPipeTimeout},
			"send did not stop in 10 seconds. tuios stopped it. Copied the selection (3 chars)."},
	}
	for _, c := range cases {
		if got := fmt.Sprint(m.handleCopyPipeDone(c.msg)()); got != "raw" {
			t.Errorf("%q: clipboard = %q, want the selection", c.note, got)
		}
		if got := lastNotificationText(t, m); got != c.note {
			t.Errorf("message = %q, want %q", got, c.note)
		}
	}
}

// Yank pipes through copy_command only when it is set.
func TestYankUsesTheCopyCommand(t *testing.T) {
	skipWithoutSh(t)
	m := pipeOS(t)
	if got := fmt.Sprint(m.Yank("plain")()); got != "plain" {
		t.Fatalf("without a copy command the yank wrote %q", got)
	}
	m.Settings.CopyCommand = "tr a-z A-Z"
	msg, ok := m.Yank("plain")().(CopyPipeDoneMsg)
	if !ok || msg.Output != "PLAIN" || msg.Label != "tr a-z A-Z" {
		t.Fatalf("msg = %+v", msg)
	}
}

// A long command line is cut for the dock.
func TestCopyCommandLabelIsShort(t *testing.T) {
	long := strings.Repeat("x", 40)
	if got := config.CopyCommandLabel(long); len([]rune(got)) != 32 || !strings.HasSuffix(got, "…") {
		t.Fatalf("label = %q", got)
	}
}

// A write past the cap is reported whole, so the writer is not told of a
// short write, and the rest is dropped.
func TestCappedBufferReportsTheWholeWrite(t *testing.T) {
	b := &cappedBuffer{max: 4}
	if n, err := b.Write([]byte("abcdef")); n != 6 || err != nil {
		t.Fatalf("Write = %d, %v; want 6, nil", n, err)
	}
	if n, err := b.Write([]byte("gh")); n != 2 || err != nil {
		t.Fatalf("second Write = %d, %v; want 2, nil", n, err)
	}
	if got := b.buf.String(); got != "abcd" || !b.over {
		t.Fatalf("buffer = %q, over = %v", got, b.over)
	}
}
