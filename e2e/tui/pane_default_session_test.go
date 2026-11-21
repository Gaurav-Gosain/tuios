package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const paneSessionProbe = `#!/usr/bin/env bash
out=$1 bin=$2
unset TUIOS_SESSION
until [ -e "$out/go" ]; do sleep 0.05; done
"$bin" capture-pane -w x > "$out/capture" 2>&1
"$bin" list-windows --json > "$out/list" 2>&1
"$bin" send-text -w x 'echo TYPED''_BY_PROBE
' > "$out/send" 2>&1
: > "$out/done"
exec sleep 600
`

// TestAPaneCallWithoutSessionUsesThePaneSession: session home holds a window
// x and the probe pane. Session other is made later, so it is the most recent
// one, and it holds a window x too. The probe in home runs capture-pane,
// list-windows and send-text with no -s.
//
// Negative control: on origin/main the probe's capture reads OTHER_MARK, its
// listing names other's windows, and its text lands in other's x.
func TestAPaneCallWithoutSessionUsesThePaneSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("new home: %v\n%s", err, out)
	}
	homeX := newPaneRunning(t, base, "home", "x", "sh", "-c", "echo HOME''_MARK; exec sh")
	out := t.TempDir()
	probe := writeAgentScript(t, base, "probe.sh", paneSessionProbe)
	probeID := newPaneRunning(t, base, "home", "probe", "bash", probe, out, tuiosBin)

	if o, err := tuiosCLI(t, base, "new", "other", "--detach"); err != nil {
		t.Fatalf("new other: %v\n%s", err, o)
	}
	otherX := newPaneRunning(t, base, "other", "x", "sh", "-c", "echo OTHER''_MARK; exec sh")

	capture := func(sess, win string) string {
		o, _ := tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", win)
		return o
	}
	waitText := func(sess, win, marker string) {
		t.Helper()
		deadline := time.Now().Add(shellTimeout)
		for !strings.Contains(capture(sess, win), marker) {
			if time.Now().After(deadline) {
				t.Fatalf("%s:%s never showed %s:\n%s", sess, win, marker, capture(sess, win))
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitText("home", homeX, "HOME_MARK")
	waitText("other", otherX, "OTHER_MARK")

	person, err := tuiosCLI(t, base, "capture-pane", "-w", "x")
	if err != nil || !strings.Contains(person, "OTHER_MARK") {
		t.Fatalf("ASSERTION: the person's capture with no -s did not read the most recent session (other): %v\n%s", err, person)
	}

	if err := os.WriteFile(filepath.Join(out, "go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		if _, err := os.Stat(filepath.Join(out, "done")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the probe pane never finished:\n%s", capture("home", probeID))
		}
		time.Sleep(50 * time.Millisecond)
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(out, name))
		return string(b)
	}

	if got := read("capture"); !strings.Contains(got, "HOME_MARK") || strings.Contains(got, "OTHER_MARK") {
		t.Errorf("ASSERTION: capture-pane -w x from a pane in home did not read home's x:\n%s", got)
	}
	if got := read("list"); !strings.Contains(got, probeID) || strings.Contains(got, otherX) {
		t.Errorf("ASSERTION: list-windows from a pane in home did not list home (probe %s, other x %s):\n%s", probeID, otherX, got)
	}
	if got := read("send"); strings.TrimSpace(got) != "" {
		t.Errorf("send-text -w x from the probe printed something: %s", got)
	}
	waitText("home", homeX, "TYPED_BY_PROBE")
	time.Sleep(500 * time.Millisecond)
	if got := capture("other", otherX); strings.Contains(got, "TYPED_BY_PROBE") {
		t.Errorf("ASSERTION: send-text -w x from a pane in home typed into other's x:\n%s", got)
	}
}
