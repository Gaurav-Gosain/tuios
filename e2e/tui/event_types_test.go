package tuie2e

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests hold the scripting surface to what a script reads from it: a
// subscribe filter naming a type no event has is refused instead of
// streaming nothing, and close-window closes one pane by name with no client
// attached.
//
// The output of each command is saved under artifactDir.
//
// Negative controls (NEGATIVE_CONTROLS.md):
//   - TestSubscribeRefusesUnknownTypes: the checkEventTypes call cut from
//     verbSubscribe: subscribe --types window-creted runs and prints nothing.
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

func TestSubscribeRefusesUnknownTypes(t *testing.T) {
	base := t.TempDir()
	startDetached(t, base, "events")
	var transcript strings.Builder

	for _, tc := range []struct{ types, want string }{
		{"window-creted", "unknown event type window-creted"},
		{"after-new-window", "after-new-window is a hook name. The event it fires on is window-created"},
	} {
		out, errOut, err := splitCLI(t, base, "subscribe", "-s", "events", "--types", tc.types, "--count", "1")
		transcript.WriteString("$ tuios subscribe --types " + tc.types + "\n" + out + errOut + "\n")
		if err == nil {
			t.Errorf("subscribe --types %s succeeded, want a refusal\n%s", tc.types, out)
			continue
		}
		if !strings.Contains(out+errOut, tc.want) {
			t.Errorf("subscribe --types %s said %q, want it to contain %q", tc.types, out+errOut, tc.want)
		}
	}

	// The positive half: a real type still streams its event.
	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	defer cancel()
	pinPreV080Looks(t, base)
	sub := exec.CommandContext(ctx, tuiosBin, "subscribe", "-s", "events", "--types", "window-created", "--count", "1")
	sub.Dir = workDirIn(t, base)
	sub.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		sub.Env = append(sub.Env, key+"="+xdgDir(base, key))
	}
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
	transcript.WriteString("$ tuios subscribe --types window-created --count 1\n" + ack + "\n" + event + "\n")
	if !strings.Contains(event, `"type":"window-created"`) {
		t.Errorf("subscribe --types window-created printed %q, want a window-created event", event)
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
