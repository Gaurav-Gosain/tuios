package tuie2e

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// These tests hold the scripting surface to what a script reads from it: a
// subscribe filter naming a type no event has is refused instead of
// streaming nothing, subscribe --format text --state --once prints one
// readable line for the first matching state and exits, and close-window
// closes one pane by name with no client attached.
//
// The output of each command is saved under artifactDir.
//
// Negative controls (NEGATIVE_CONTROLS.md):
//   - TestSubscribeRefusesUnknownTypes: the checkEventTypes call cut from
//     verbSubscribe: subscribe --types window-creted runs and prints nothing.
//   - TestSubscribeRefusesUnknownTypes, the text half: the --state check in
//     runSubscribe always true: the idle report prints a line and --once
//     exits on it, so the line is "working -> idle".
//   - TestCloseWindowClosesOnePane: the window passed to dialSessionTarget's
//     params instead of dialTarget, which sends an empty window: the focused
//     pane, keep, is closed instead of build.

// startDetached makes a detached session named name under base.
func startDetached(t *testing.T, base, name string) {
	t.Helper()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("new %s --detach: %v\n%s", name, err, out)
	}
}

// subscribeCmd is a tuios command against the daemon under base, for a
// test that reads its output while it runs.
func subscribeCmd(ctx context.Context, t *testing.T, base string, args ...string) *exec.Cmd {
	t.Helper()
	pinPreV080Looks(t, base)
	cmd := exec.CommandContext(ctx, tuiosBin, args...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	return cmd
}

// subscribeWithin is splitCLI with a deadline, for a command that may not exit.
func subscribeWithin(t *testing.T, base string, d time.Duration, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	pinPreV080Looks(t, base)
	cmd := exec.CommandContext(ctx, tuiosBin, args...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func TestSubscribeRefusesUnknownTypes(t *testing.T) {
	base := t.TempDir()
	startDetached(t, base, "events")
	var transcript strings.Builder

	for _, tc := range []struct{ types, want string }{
		{"window-creted", "unknown event type window-creted"},
		{"after-new-window", "after-new-window is a hook name. The event it fires on is window-created"},
	} {
		// A filter that is not refused streams nothing and never exits, so
		// the call has a deadline: it fails the check below instead of the
		// whole run timing out.
		out, errOut, err := subscribeWithin(t, base, uiTimeout, "subscribe", "-s", "events", "--types", tc.types, "--count", "1")
		transcript.WriteString("$ tuios subscribe --types ")
		transcript.WriteString(tc.types)
		transcript.WriteString("\n")
		transcript.WriteString(out)
		transcript.WriteString(errOut)
		transcript.WriteString("\n")
		if err == nil || strings.Contains(err.Error(), "killed") {
			t.Errorf("subscribe --types %s was not refused (%v)\n%s", tc.types, err, out)
			continue
		}
		if !strings.Contains(out+errOut, tc.want) {
			t.Errorf("subscribe --types %s said %q, want it to contain %q", tc.types, out+errOut, tc.want)
		}
	}

	// The positive half: a real type still streams its event.
	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	defer cancel()
	sub := subscribeCmd(ctx, t, base, "subscribe", "-s", "events", "--types", "window-created", "--count", "1")
	pipe, err := sub.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(pipe)
	// subscribe prints its ack line first. The window is made after that,
	// so the event cannot come before the subscription.
	if !lines.Scan() || !strings.Contains(lines.Text(), "subscribed") {
		t.Fatalf("subscribe --types window-created did not acknowledge: %q", lines.Text())
	}
	ack := lines.Text()
	if out, err := tuiosCLI(t, base, "new-window", "-s", "events", "probe"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	var event string
	if lines.Scan() {
		event = lines.Text()
	}
	_ = sub.Wait()
	transcript.WriteString("$ tuios subscribe --types window-created --count 1\n")
	transcript.WriteString(ack)
	transcript.WriteString("\n")
	transcript.WriteString(event)
	transcript.WriteString("\n")
	if !strings.Contains(event, `"type":"window-created"`) {
		t.Errorf("subscribe --types window-created printed %q, want a window-created event", event)
	}

	// The text half. The pane reports working before the subscription, then
	// idle, which --state drops, then needs_input. The one line printed
	// must name the state the agent came from and the message it gave, and
	// --once must end the run with status 0.
	report := func(args ...string) {
		t.Helper()
		args = append([]string{"set-agent-state"}, args...)
		if out, err := tuiosCLI(t, base, append(args, "-s", "events", "-w", "probe")...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	report("working", "-m", "Reading files")
	textArgs := []string{"subscribe", "-s", "events", "--format", "text", "--state", "needs_input", "--once"}
	text := subscribeCmd(ctx, t, base, textArgs...)
	var textOut strings.Builder
	text.Stdout = &textOut
	ackPipe, err := text.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := text.Start(); err != nil {
		t.Fatal(err)
	}
	// In text mode the ack goes to standard error. The reports come after it.
	ackLines := bufio.NewScanner(ackPipe)
	if !ackLines.Scan() || !strings.HasPrefix(ackLines.Text(), "Subscribed at seq ") {
		t.Fatalf("subscribe --format text did not acknowledge on standard error: %q", ackLines.Text())
	}
	textAck := ackLines.Text()
	report("idle")
	report("needs_input", "-m", "Allow Bash?")
	for ackLines.Scan() {
		textAck += "\n" + ackLines.Text()
	}
	if err := ackLines.Err(); err != nil {
		t.Errorf("read the standard error of subscribe: %v", err)
	}
	textErr := text.Wait()
	for _, part := range []string{"$ tuios ", strings.Join(textArgs, " "), "\n", textOut.String(), "[stderr] ", textAck, "\n"} {
		transcript.WriteString(part)
	}
	if textErr != nil {
		t.Errorf("subscribe --once did not exit 0 after its event: %v", textErr)
	}
	want := regexp.MustCompile(`^\d\d:\d\d:\d\d  events probe  idle -> needs_input  Allow Bash\?\n$`)
	if !want.MatchString(textOut.String()) {
		t.Errorf("subscribe --format text printed %q, want one line matching %s", textOut.String(), want)
	}

	// A --timeout that ends before --once is met fails the run.
	timeoutArgs := []string{"subscribe", "-s", "events", "--format", "text", "--state", "errored", "--once", "--timeout", "500"}
	out, errOut, err := subscribeWithin(t, base, uiTimeout, timeoutArgs...)
	for _, part := range []string{"$ tuios ", strings.Join(timeoutArgs, " "), "\n", out, errOut} {
		transcript.WriteString(part)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(errOut, "no event matched in 500 ms") {
		t.Errorf("subscribe --timeout 500 --once: err %v, stderr %q, want exit 1 and the timeout named", err, errOut)
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "transcript.txt"), []byte(transcript.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCloseWindowClosesOnePane(t *testing.T) {
	base := t.TempDir()
	startDetached(t, base, "closing")
	// build first, so keep has focus: closing the focused pane instead of
	// the named one closes keep.
	for _, name := range []string{"build", "keep"} {
		if out, err := tuiosCLI(t, base, "new-window", "-s", "closing", name); err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
	}
	out, err := tuiosCLI(t, base, "close-window", "-s", "closing", "build")
	if err != nil {
		t.Fatalf("close-window build: %v\n%s", err, out)
	}
	transcript := "$ tuios close-window -s closing build\n" + out
	list, err := tuiosCLI(t, base, "list-windows", "-s", "closing")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, list)
	}
	transcript += "$ tuios list-windows -s closing\n" + list
	if strings.Contains(list, "build") {
		t.Errorf("build is still listed after close-window:\n%s", list)
	}
	if !strings.Contains(list, "keep") {
		t.Errorf("close-window build closed keep too:\n%s", list)
	}
	// A name no pane has is refused.
	if out, err := tuiosCLI(t, base, "close-window", "-s", "closing", "no-such-pane"); err == nil {
		t.Errorf("close-window no-such-pane succeeded:\n%s", out)
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "transcript.txt"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
}
