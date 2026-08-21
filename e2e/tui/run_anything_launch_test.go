package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// runAnythingMarker is what the probe program prints, so seeing it on screen
// proves the program itself ran in the new pane.
const runAnythingMarker = "RUN_ANYTHING_OK"

// probeName is distinctive enough that the palette query matches nothing else,
// so Enter can only launch the probe.
const probeName = "zzprobe"

// writeProbe puts an executable in a directory whose path contains a space and
// returns the directory. The space is the point: the launcher execs the listed
// path as the pane's own process, so no shell ever re-parses it, and a path
// that would have needed quoting must launch exactly like any other.
func writeProbe(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "odd dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("probe dir: %v", err)
	}
	script := "#!/bin/sh\necho " + runAnythingMarker + "\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, probeName), []byte(script), 0o755); err != nil {
		t.Fatalf("probe script: %v", err)
	}
	return dir
}

// launchProbe drives the palette end to end: open, query, wait for the scanned
// row, run it, and wait for the program's output in the new pane.
func launchProbe(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("send ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "for the launcher")
	if err := term.SendKeys(probeName); err != nil {
		t.Fatalf("type the probe's name: %v", err)
	}
	// The $PATH scan lands asynchronously, so the row may trail the query. The
	// row is recognised by its category tag alone: the palette elides a long
	// name+path pair to fit, so neither the name nor the full path reliably
	// survives, and with this query the probe is the only program that matches.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "[Run]")
	}, uiTimeout); err != nil {
		t.Fatalf("the scanned program never reached the palette: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("run the probe: %v", err)
	}
	if err := term.WaitForText(runAnythingMarker, shellTimeout); err != nil {
		t.Fatalf("the launched program never printed: %v\n%s", err, term.Snapshot())
	}
}

// TestRunAnythingExecsLocalPane pins the local half of the launch semantics: a
// program picked in the palette becomes a pane whose process is that program,
// exec'd from a path containing a space, with no shell in between to re-parse
// or race it.
func TestRunAnythingExecsLocalPane(t *testing.T) {
	dir := writeProbe(t)
	term, _ := start(t, startOpts{
		args: []string{"--standalone"},
		env:  []string{"PATH=" + dir + ":/usr/bin:/bin"},
	})
	waitBoot(t, term)
	launchProbe(t, term)
}

// TestRunAnythingExecsDaemonPane is the daemon half: the argv rides the
// NewWindow intent to the daemon, the daemon execs it as the pane's process,
// and the pane comes back in a state push. This is the path that used to type
// the command into a freshly spawned shell and hope it was ready.
func TestRunAnythingExecsDaemonPane(t *testing.T) {
	dir := writeProbe(t)
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-run", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{
		args: []string{"attach", "e2e-run"},
		env:  []string{"PATH=" + dir + ":/usr/bin:/bin"},
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	// An attached client boots into terminal mode; the palette works there too,
	// but window-management mode is the launcher's home ground and keeps this
	// test out of the insert-guard's way.
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("switch to window mode: %v", err)
	}
	if err := term.WaitForText("Window Management Mode", uiTimeout); err != nil {
		t.Fatalf("client never reached window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	launchProbe(t, term)
}
