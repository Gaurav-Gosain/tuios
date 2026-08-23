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

// launcherTitle is the header the launcher overlay renders. It is a different
// overlay from the command palette, which is the point: a program is a thing
// you start, not a verb tuios performs.
const launcherTitle = "Run a program"

// altSpace is the launcher's direct binding.
var altSpace = tuitest.Alt(" ")

// openLauncher opens the launcher and waits for it to be on screen.
func openLauncher(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(altSpace); err != nil {
		t.Fatalf("send alt+space: %v", err)
	}
	if err := term.WaitForText(launcherTitle, uiTimeout); err != nil {
		t.Fatalf("the launcher never opened: %v\n%s", err, term.Snapshot())
	}
}

// queryProbe opens the launcher and types the probe's name, waiting for its row.
func queryProbe(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	openLauncher(t, term)
	if err := term.SendKeys(probeName); err != nil {
		t.Fatalf("type the probe's name: %v", err)
	}
	// The scan lands asynchronously, so the row trails the query. Waiting for
	// the name is not enough on its own: the search box echoes it the moment it
	// is typed, and that alone once satisfied this wait and let the test press
	// Enter on an empty list. Two copies means the search box AND a row.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), probeName) >= 2
	}, uiTimeout); err != nil {
		t.Fatalf("the scanned program never reached the launcher: %v\n%s", err, term.Snapshot())
	}
}

// launchProbe drives the launcher end to end: open, query, run it with Enter,
// and wait for the program's output in the new pane.
func launchProbe(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	queryProbe(t, term)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("run the probe: %v", err)
	}
	if err := term.WaitForText(runAnythingMarker, shellTimeout); err != nil {
		t.Fatalf("the launched program never printed: %v\n%s", err, term.Snapshot())
	}
}

// TestLauncherIsNotTheCommandPalette is the separation, checked where it is
// visible: ctrl+p lists commands and does not list programs, and alt+space
// lists programs.
func TestLauncherIsNotTheCommandPalette(t *testing.T) {
	dir := writeProbe(t)
	term, _ := start(t, startOpts{
		args: []string{"--standalone"},
		env:  []string{"PATH=" + dir + ":/usr/bin:/bin"},
	})
	waitBoot(t, term)

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("send ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "for the command palette")
	if err := term.SendKeys(probeName); err != nil {
		t.Fatalf("type the probe's name: %v", err)
	}
	// Long enough for a scan to have landed, had one been able to put the
	// program in this box.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "No matching commands")
	}, uiTimeout); err != nil {
		t.Fatalf("a program on $PATH is still ranked among the commands: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the palette: %v", err)
	}
	// The launcher's key is only routed once the palette has let go of it.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), paletteTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("the palette never closed: %v\n%s", err, term.Snapshot())
	}

	queryProbe(t, term)
}

// TestLauncherTypesTheCommandOut is the second verb on a row. Tab opens a pane
// running a shell with the command line waiting at its prompt, so the marker
// the program prints must NOT appear: nothing has been run yet. The program's
// name on the prompt is what proves the line arrived.
func TestLauncherTypesTheCommandOut(t *testing.T) {
	dir := writeProbe(t)
	term, _ := start(t, startOpts{
		args: []string{"--standalone"},
		env:  []string{"PATH=" + dir + ":/usr/bin:/bin"},
	})
	waitBoot(t, term)
	queryProbe(t, term)

	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("type the probe out: %v", err)
	}
	// The pane's shell echoes what it was sent once its line editor is up.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), probeName) && !strings.Contains(s.Text(), launcherTitle)
	}, shellTimeout); err != nil {
		t.Fatalf("the command never reached the new pane's prompt: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), runAnythingMarker) {
		t.Fatalf("the program ran, so it was not left for the user to run:\n%s", term.Snapshot())
	}

	// And pressing Enter now runs the very command that was waiting, which is
	// the whole point of putting it there.
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("run the typed command: %v", err)
	}
	if err := term.WaitForText(runAnythingMarker, shellTimeout); err != nil {
		t.Fatalf("the waiting command did not run when entered: %v\n%s", err, term.Snapshot())
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
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never reached window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	launchProbe(t, term)
}
