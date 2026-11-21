package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func holdScan(t *testing.T) (env string, release func()) {
	t.Helper()
	hold := filepath.Join(t.TempDir(), "hold-scan")
	if err := os.WriteFile(hold, nil, 0o644); err != nil {
		t.Fatalf("hold-scan file: %v", err)
	}
	return knob("hold_scan=" + hold), func() { _ = os.Remove(hold) }
}

type launcherFixture struct {
	term   *tuitest.Terminal
	stream *hostStream
	base   string
}

func newLauncherFixture(t *testing.T, n int, extraEnv ...string) *launcherFixture {
	t.Helper()
	stream := &hostStream{}
	base := t.TempDir()
	plantApps(t, base, n)
	term := startIn(t, base, startOpts{
		args: []string{"--standalone"},
		env:  iconEnv(base, extraEnv...),
		out:  stream,
	})
	waitBoot(t, term)
	return &launcherFixture{term: term, stream: stream, base: base}
}

func (f *launcherFixture) live() []string { return liveLauncherPlacements(f.stream.bytes()) }

func (f *launcherFixture) openWithQuery(t *testing.T, query string, wantRows int) {
	t.Helper()
	openLauncher(t, f.term)
	if query == "" {
		return
	}
	if err := f.term.SendKeys(query); err != nil {
		t.Fatalf("type %q: %v", query, err)
	}
	if wantRows > 0 {
		if err := f.term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Count(s.Text(), "zzapp") >= wantRows
		}, uiTimeout); err != nil {
			t.Fatalf("query %q never listed %d rows: %v\n%s", query, wantRows, err, f.term.Snapshot())
		}
	}
}

func (f *launcherFixture) nudgeSelection() {
	before := f.term.Screen().Text()
	_ = f.term.SendKeys(tuitest.Down)
	_ = f.term.WaitFor(func(s tuitest.Screen) bool { return s.Text() != before }, nudgeBudget)
	_ = f.term.SendKeys(tuitest.Up)
}

const nudgeBudget = 3 * time.Second

const placementGuard = 400 * time.Millisecond

func (f *launcherFixture) waitPlacements(want func([]string) bool, timeout time.Duration) []string {
	deadline := time.Now().Add(timeout)
	for {
		got := f.live()
		if want(got) || !time.Now().Before(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *launcherFixture) requirePlaced(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		if got := f.waitPlacements(func(p []string) bool { return len(p) > 0 }, nudgeBudget); len(got) > 0 {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("no launcher icon was ever placed, so the leak check proves nothing\n%s",
				f.term.Snapshot())
		}
		f.nudgeSelection()
	}
}

func (f *launcherFixture) requireClean(t *testing.T, what string) {
	t.Helper()
	f.requireNoPlacements(t, what+" left")
}

func (f *launcherFixture) requireNoPlacements(t *testing.T, what string) {
	t.Helper()
	if got := f.waitPlacements(func(p []string) bool { return len(p) == 0 }, uiTimeout); len(got) != 0 {
		t.Fatalf("%s %d launcher icon placements on the host: %v\n%s",
			what, len(got), got, f.term.Snapshot())
	}
	time.Sleep(placementGuard)
	if got := f.live(); len(got) != 0 {
		t.Fatalf("%s no launcher icon placements, and then %d came back: %v\n%s",
			what, len(got), got, f.term.Snapshot())
	}
}

func (f *launcherFixture) waitClosed(t *testing.T, what string) {
	t.Helper()
	if err := f.term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), launcherTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("the launcher never closed after %s: %v\n%s", what, err, f.term.Snapshot())
	}
}

func TestLauncherIconsCloseByEscape(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "zzapp", 3)
	f.nudgeSelection()
	f.requirePlaced(t)

	if err := f.term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	f.waitClosed(t, "esc")
	f.requireClean(t, "closing with esc")
}

func TestLauncherIconsCloseByEnter(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "zzapp", 3)
	f.nudgeSelection()
	f.requirePlaced(t)

	if err := f.term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	f.waitClosed(t, "enter")
	f.requireClean(t, "launching with enter")
}

func TestLauncherIconsCloseByTab(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "zzapp", 3)
	f.nudgeSelection()
	f.requirePlaced(t)

	if err := f.term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab: %v", err)
	}
	f.waitClosed(t, "tab")
	f.requireClean(t, "typing it out with tab")
}

// TestLauncherIconsCloseByClickAway is the close path that goes through the
// mouse rather than a key, which is a different function and so a different
// chance to skip the cleanup.
func TestLauncherIconsCloseByClickAway(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "zzapp", 3)
	f.nudgeSelection()
	f.requirePlaced(t)

	mouseClick(t, f.term, 2, 1, tuitest.MouseLeft, 0)
	f.waitClosed(t, "a click away")
	f.requireClean(t, "closing by clicking away")
}

// TestLauncherIconsCloseByShortLivedProgram launches something that exits at
// once, so the pane the launch created goes away in the same breath as the
// panel that launched it.
func TestLauncherIconsCloseByShortLivedProgram(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	f := newLauncherFixture(t, 20)
	plantExitingApp(t, f.base)

	f.openWithQuery(t, "zzapp", 3)
	f.nudgeSelection()
	f.requirePlaced(t)

	if err := f.term.SendKeys(tuitest.Ctrl('u')); err != nil {
		t.Fatalf("ctrl+u: %v", err)
	}
	if err := f.term.SendKeys("zzquit"); err != nil {
		t.Fatalf("type query: %v", err)
	}
	if err := f.term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "zzquit") >= 2
	}, uiTimeout); err != nil {
		t.Fatalf("the exiting program never listed: %v\n%s", err, f.term.Snapshot())
	}
	if err := f.term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	f.waitClosed(t, "launching a program that exits at once")
	f.requireClean(t, "launching a program that exits at once")
}

// TestLauncherIconsEmptyQuery is the launcher as it opens, with every program
// on offer and no query at all.
func TestLauncherIconsEmptyQuery(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "", 0)
	if err := f.term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Scanning for programs")
	}, uiTimeout); err != nil {
		t.Fatalf("the scan never filled the unfiltered list: %v\n%s", err, f.term.Snapshot())
	}
	f.nudgeSelection()

	if err := f.term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	f.waitClosed(t, "esc")
	f.requireClean(t, "closing an unfiltered launcher")
}

// TestLauncherIconsNoMatchQuery narrows to nothing, which replaces every row
// with one line of prose. Every icon that was under those rows has to go.
func TestLauncherIconsNoMatchQuery(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "zzapp", 3)
	f.nudgeSelection()
	f.requirePlaced(t)

	if err := f.term.SendKeys("zzznomatch"); err != nil {
		t.Fatalf("type no-match query: %v", err)
	}
	if err := f.term.WaitForText("No program matches", uiTimeout); err != nil {
		t.Fatalf("the no-match line never appeared: %v\n%s", err, f.term.Snapshot())
	}
	f.requireNoPlacements(t, "a query that matches nothing left")

	if err := f.term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	f.waitClosed(t, "esc")
	f.requireClean(t, "closing on a query that matched nothing")
}

// TestLauncherIconsClosedDuringScan closes the launcher in the window where the
// $PATH scan it asked for has not come back yet, so the rows it would have
// rebuilt land on a closed panel.
//
// The scan is held open across every close rather than raced with sleeps, so
// "the scan had not come back" is true on each pass by construction; an
// unheld version only hit that window when the machine happened to be slow
// enough. From the second pass on, the previous pass's landed scan puts rows
// and their icon decodes on screen at open, so the close also catches decodes
// in flight, which the varied sleep is for: it widens the churn and gates no
// assertion.
func TestLauncherIconsClosedDuringScan(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	hold := filepath.Join(t.TempDir(), "hold-scan")
	if err := os.WriteFile(hold, nil, 0o644); err != nil {
		t.Fatalf("hold-scan file: %v", err)
	}
	f := newLauncherFixture(t, 20, knob("hold_scan="+hold))
	for i := range 6 {
		if err := os.WriteFile(hold, nil, 0o644); err != nil {
			t.Fatalf("rearm hold-scan: %v", err)
		}
		openLauncher(t, f.term)
		if err := f.term.SendKeys("zzapp"); err != nil {
			t.Fatalf("type query: %v", err)
		}
		time.Sleep(time.Duration(40*i) * time.Millisecond)
		if err := f.term.SendKeys(tuitest.Esc); err != nil {
			t.Fatalf("esc: %v", err)
		}
		f.waitClosed(t, "esc during scan")
		if err := os.Remove(hold); err != nil {
			t.Fatalf("release hold-scan: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.requireClean(t, "closing while a scan was in flight")
}

// TestLauncherIconsSurviveQueryChanges narrows and widens the query so rows
// scroll in and out repeatedly, then closes. Every row that leaves has to take
// its picture with it.
func TestLauncherIconsSurviveQueryChanges(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "zzapp", 3)
	f.nudgeSelection()
	f.requirePlaced(t)

	for _, q := range []string{"0", "1", "zzz", ""} {
		switch q {
		case "":
			if err := f.term.SendKeys(tuitest.Ctrl('u')); err != nil {
				t.Fatalf("ctrl+u: %v", err)
			}
			if err := f.term.SendKeys("zzapp"); err != nil {
				t.Fatalf("retype: %v", err)
			}
		case "zzz":
			if err := f.term.SendKeys(tuitest.Ctrl('u')); err != nil {
				t.Fatalf("ctrl+u: %v", err)
			}
			if err := f.term.SendKeys("zzznomatch"); err != nil {
				t.Fatalf("type no-match query: %v", err)
			}
			if err := f.term.WaitForText("No program matches", uiTimeout); err != nil {
				t.Fatalf("the no-match line never appeared: %v\n%s", err, f.term.Snapshot())
			}
		default:
			if err := f.term.SendKeys(q); err != nil {
				t.Fatalf("narrow with %q: %v", q, err)
			}
		}
		if q == "zzz" {
			f.requireNoPlacements(t, "narrowing to a query that matches nothing left")
			continue
		}
		f.nudgeSelection()
		f.requirePlaced(t)
	}

	if err := f.term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	f.waitClosed(t, "esc")
	f.requireClean(t, "closing after the query narrowed and widened")
}

// TestLauncherIconsSurviveScrolling walks the selection far enough that the
// scroll window moves repeatedly, placing and unplacing icons as it goes.
func TestLauncherIconsSurviveScrolling(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	f := newLauncherFixture(t, 40)
	f.openWithQuery(t, "zzapp", 3)
	f.nudgeSelection()
	f.requirePlaced(t)

	for range 40 {
		_ = f.term.SendKeys(tuitest.Down)
	}
	time.Sleep(700 * time.Millisecond)
	for range 40 {
		_ = f.term.SendKeys(tuitest.Up)
	}
	f.nudgeSelection()

	if err := f.term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	f.waitClosed(t, "esc")
	f.requireClean(t, "closing after scrolling the list")
}

// TestLauncherIconsDoNotAccumulate opens and closes several times. Nothing may
// be left standing between runs, and the image ids may not climb without bound:
// an upload is meant to be reused, not repeated per open.
func TestLauncherIconsDoNotAccumulate(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	f := newLauncherFixture(t, 20)
	for i := range 5 {
		f.openWithQuery(t, "zzapp", 3)
		f.nudgeSelection()
		f.requirePlaced(t)
		if err := f.term.SendKeys(tuitest.Esc); err != nil {
			t.Fatalf("esc: %v", err)
		}
		f.waitClosed(t, "esc")
		f.requireClean(t, fmt.Sprintf("close number %d", i+1))
	}
	if n := distinctLauncherImages(f.stream.bytes()); n > 24 {
		t.Fatalf("%d distinct launcher image ids were uploaded across five opens; "+
			"an icon is meant to be uploaded once and re-placed", n)
	}
}

// TestLauncherDrawsNoIconsWithoutGraphics is the other half of the capability
// check: with kitty graphics off there is no icon column and no escape at all,
// and every close path still has to leave the screen clean.
func TestLauncherDrawsNoIconsWithoutGraphics(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	f := newLauncherFixture(t, 20, "TUIOS_KITTY_GRAPHICS=0")
	for _, closer := range []struct {
		name string
		key  tuitest.Key
	}{{"esc", tuitest.Esc}, {"enter", tuitest.Enter}, {"tab", tuitest.Tab}} {
		f.openWithQuery(t, "zzapp", 3)
		f.nudgeSelection()
		if err := f.term.SendKeys(closer.key); err != nil {
			t.Fatalf("%s: %v", closer.name, err)
		}
		f.waitClosed(t, closer.name)
		f.requireNoPlacements(t, "with graphics off, closing with "+closer.name+" drew")
	}
	if n := distinctLauncherImages(f.stream.bytes()); n != 0 {
		t.Fatalf("%d launcher images were uploaded with graphics off", n)
	}
}

func requireTypedThenRuns(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if strings.Contains(term.Screen().Text(), runAnythingMarker) {
		t.Fatalf("the program ran, so it was not left for the user to run:\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("run the typed command: %v", err)
	}
	if err := term.WaitForText(runAnythingMarker, shellTimeout); err != nil {
		t.Fatalf("no command was waiting at the prompt to be run: %v\n%s", err, term.Snapshot())
	}
}

const typeOutPrompt = "zzsh$"

func waitForPaneShell(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.WaitForText(typeOutPrompt, shellTimeout); err != nil {
		t.Fatalf("the pane's shell never started: %v\n%s", err, term.Snapshot())
	}
}

func heldShell(t *testing.T, hold string) string {
	t.Helper()
	sh := filepath.Join(t.TempDir(), "heldsh")
	script := "#!/bin/sh\nwhile [ -e \"" + hold + "\" ]; do sleep 0.02; done\n" +
		"exec /usr/bin/env PS1='" + typeOutPrompt + " ' /bin/sh \"$@\"\n"
	if err := os.WriteFile(sh, []byte(script), 0o755); err != nil {
		t.Fatalf("held shell: %v", err)
	}
	return sh
}

// TestLauncherTypesOutIntoASpawningPane is the local half: the pane and its PTY
// exist the moment the launcher asks for them, and the line is written into a
// shell that has not started yet.
//
// The shell is held with heldShell so "has not started yet" is a certainty
// rather than a spawn raced and usually won: the line is provably in the PTY
// before the shell exists, and the shell's line editor still has to pick it
// up when it arrives.
func TestLauncherTypesOutIntoASpawningPane(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	dir := writeProbe(t)
	hold := filepath.Join(t.TempDir(), "hold-shell")
	if err := os.WriteFile(hold, nil, 0o644); err != nil {
		t.Fatalf("hold-shell file: %v", err)
	}
	term, _ := start(t, startOpts{
		args: []string{"--standalone"},
		env: []string{"PATH=" + dir + string(os.PathListSeparator) + agentFreePath("/usr/bin", "/bin"), "PS1=" + typeOutPrompt + " ",
			"SHELL=" + heldShell(t, hold)},
	})
	waitBoot(t, term)
	queryProbe(t, term)

	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), launcherTitle) && countWindows(s) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("tab never opened the pane: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), runAnythingMarker) {
		t.Fatalf("the program ran before its shell had even started:\n%s", term.Snapshot())
	}
	if err := os.Remove(hold); err != nil {
		t.Fatalf("release the shell: %v", err)
	}
	waitForPaneShell(t, term)
	requireTypedThenRuns(t, term)
}

// TestLauncherTypesOutIntoADaemonPane is the other half: the pane is created by
// the daemon and reaches this client in a state push, by which time its shell
// has been running for a while.
//
// It is the half the report was about. The line was parked for a pane matching
// the name the launcher asked for, and the daemon pushed the pane's creation
// and its naming as two separate states, so the client adopted it unnamed and
// nothing ever matched.
func TestLauncherTypesOutIntoADaemonPane(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	dir := writeProbe(t)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("PS1", typeOutPrompt+" ")

	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-type", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{
		args: []string{"attach", "e2e-type"},
		env:  []string{"PATH=" + dir + string(os.PathListSeparator) + agentFreePath("/usr/bin", "/bin")},
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("window mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("never reached window management mode: %v\n%s", err, term.Snapshot())
	}

	queryProbe(t, term)
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 2
	}, shellTimeout); err != nil {
		t.Fatalf("the daemon never created the pane: %v\n%s", err, term.Snapshot())
	}
	waitForPaneShell(t, term)
	requireTypedThenRuns(t, term)
}

// TestLauncherIconsWithABusyNeighbour is the condition that has broken other
// placements: a tiled layout with a pane printing continuously underneath the
// panel. The launcher's own icons are drawn over that, and every open has to
// take its own pictures down again whatever the panes below are doing.
func TestLauncherIconsWithABusyNeighbour(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	stream := &hostStream{}
	base := t.TempDir()
	plantApps(t, base, 20)
	term := startIn(t, base, startOpts{
		cols: 120, rows: 40,
		env: iconEnv(base),
		out: stream,
	})
	waitBoot(t, term)
	f := &launcherFixture{term: term, stream: stream, base: base}

	newWindow(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	waitWindowCount(t, term, 2, "two tiled panes")

	enterTerminalMode(t, term)
	runInShell(t, term, "echo NEIG\"\"HBOUR", "NEIGHBOUR", shellTimeout)
	typeLine(t, term, "while :; do seq 1 40; sleep 0.05; done")
	leaveTerminalMode(t, term)
	time.Sleep(700 * time.Millisecond)

	for i := range 3 {
		f.openWithQuery(t, "zzapp", 3)
		f.nudgeSelection()
		if i == 0 {
			f.requirePlaced(t)
		}
		for range 20 {
			_ = term.SendKeys(tuitest.Down)
		}
		f.nudgeSelection()
		if err := term.SendKeys(tuitest.Esc); err != nil {
			t.Fatalf("esc: %v", err)
		}
		f.waitClosed(t, "esc over a busy neighbour")
		f.requireClean(t, fmt.Sprintf("close number %d over a busy neighbour", i+1))
	}
}

// TestLauncherLeavesTheRightModeBehind pins the half of the two verbs that is
// not about what runs.
//
// Tab hands the pane over ready to be typed into, because the user is about to
// keep typing; Enter does not, because a program was started rather than a
// command line begun. Getting this the wrong way round sends what the user
// types next to the window manager instead of the shell, which is the "typed
// into the wrong place" failure with no error to show for it.
//
// Both are asserted by what the next keystrokes do rather than by the mode
// banner. The banner is a notification raised by the key that switches mode, so
// it says nothing about a switch made on the launcher's behalf, and waiting for
// it fails against a build where the mode is perfectly correct.
func TestLauncherLeavesTheRightModeBehind(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	dir := writeArgProbe(t)
	term, _ := start(t, startOpts{
		args: []string{"--standalone"},
		env:  []string{"PATH=" + dir + string(os.PathListSeparator) + agentFreePath("/usr/bin", "/bin"), "PS1=" + typeOutPrompt + " "},
	})
	waitBoot(t, term)

	queryProbe(t, term)
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab: %v", err)
	}
	waitForPaneShell(t, term)
	if err := term.SendKeys("EXTRA"); err != nil {
		t.Fatalf("type an argument: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("run it: %v", err)
	}
	if err := term.WaitForText("ARG:EXTRA", shellTimeout); err != nil {
		t.Fatalf("the argument typed after tab never reached the command: %v\n%s",
			err, term.Snapshot())
	}

	leaveTerminalMode(t, term)
	before := settledWindowCount(t, term)
	queryProbe(t, term)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == before+1
	}, shellTimeout); err != nil {
		t.Fatalf("enter never opened the program's pane: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("n"); err != nil {
		t.Fatalf("send 'n': %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == before+2
	}, uiTimeout); err != nil {
		t.Fatalf("enter left the client in terminal mode, so a window-manager key "+
			"was typed at a shell instead: %v\n%s", err, term.Snapshot())
	}
}

func openAndRace(t *testing.T, term *tuitest.Terminal, query string, key tuitest.Key) {
	t.Helper()
	if err := term.SendKeys(altSpace); err != nil {
		t.Fatalf("open launcher: %v", err)
	}
	if err := term.WaitForText(launcherTitle, uiTimeout); err != nil {
		t.Fatalf("launcher never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(query); err != nil {
		t.Fatalf("type %q: %v", query, err)
	}
	if err := term.SendKeys(key); err != nil {
		t.Fatalf("press the verb: %v", err)
	}
}

// TestVerbBeforeTheScanLands is the launcher used without waiting to be told
// the row is there.
//
// On the first open of a session the list is filled by a scan running off the
// Update goroutine, so there is nothing selected yet however precisely the
// query was typed. Both verbs used to close the launcher on no selection and
// return, which threw the query away, dismissed the panel and said nothing:
// indistinguishable from the key not being bound. The wait it needs is the
// scan, not the pane's shell.
//
// The scan is held with holdScan rather than raced. An earlier version planted
// four thousand programs to slow the scan and pressed the verb quickly; on CI
// the scan won anyway, the verb landed on a filled list and ran the probe, and
// the "why the key did nothing" wait timed out against a launcher that had
// rightly closed. Held, the ordering is guaranteed on any machine, however
// fast its filesystem or slow its PTY.
func TestVerbBeforeTheScanLands(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	for _, verb := range []struct {
		name string
		key  tuitest.Key
	}{{"tab", tuitest.Tab}, {"enter", tuitest.Enter}} {
		t.Run(verb.name, func(t *testing.T) {
			dir := writeProbe(t)
			holdEnv, release := holdScan(t)
			term, _ := start(t, startOpts{
				cols: 160, rows: 45,
				args: []string{"--standalone"},
				env: []string{"PATH=" + dir + string(os.PathListSeparator) + agentFreePath("/usr/bin", "/bin"),
					"PS1=" + typeOutPrompt + " ", holdEnv},
			})
			waitBoot(t, term)
			openAndRace(t, term, probeName, verb.key)

			if err := term.WaitForText("Still finding the programs", uiTimeout); err != nil {
				t.Fatalf("nothing said why the key did nothing: %v\n%s", err, term.Snapshot())
			}
			if txt := term.Screen().Text(); !strings.Contains(txt, launcherTitle) {
				t.Fatalf("%s before the scan landed dismissed the launcher:\n%s",
					verb.name, term.Snapshot())
			}
			if txt := term.Screen().Text(); !strings.Contains(txt, probeName) {
				t.Fatalf("%s before the scan landed threw the query away:\n%s",
					verb.name, term.Snapshot())
			}

			release()
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Count(s.Text(), probeName) >= 2
			}, uiTimeout); err != nil {
				t.Fatalf("the scan never filled the list: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys(verb.key); err != nil {
				t.Fatalf("%s again: %v", verb.name, err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), launcherTitle)
			}, uiTimeout); err != nil {
				t.Fatalf("the second %s did nothing either: %v\n%s", verb.name, err, term.Snapshot())
			}
			if verb.name == "tab" {
				waitForPaneShell(t, term)
				if err := term.SendKeys(tuitest.Enter); err != nil {
					t.Fatalf("enter: %v", err)
				}
			}
			if err := term.WaitForText(runAnythingMarker, shellTimeout); err != nil {
				t.Fatalf("no command reached the pane: %v\n%s", err, term.Snapshot())
			}
		})
	}
}

// TestVerbOnAQueryThatMatchesNothing is the other empty list: the scan has
// landed and the query matches none of it. Dismissing the panel there throws
// away a query the user is part way through typing, so it stays up and says so.
func TestVerbOnAQueryThatMatchesNothing(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	dir := writeProbe(t)
	term, _ := start(t, startOpts{
		cols: 160, rows: 45,
		args: []string{"--standalone"},
		env:  []string{"PATH=" + dir + string(os.PathListSeparator) + agentFreePath("/usr/bin", "/bin")},
	})
	waitBoot(t, term)
	queryProbe(t, term)
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), launcherTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("launcher never closed: %v\n%s", err, term.Snapshot())
	}

	openLauncher(t, term)
	if err := term.SendKeys("zzznomatch"); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := term.WaitForText("No program matches", uiTimeout); err != nil {
		t.Fatalf("the no-match line never appeared: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab: %v", err)
	}
	if txt := term.Screen().Text(); !strings.Contains(txt, launcherTitle) {
		t.Fatalf("tab on a query matching nothing threw the query away:\n%s", term.Snapshot())
	}
}

// TestLauncherIconsSurviveAResize resizes the terminal under the open launcher.
// A resize must not send an ED 2, which takes every picture off a kitty host, and
// every icon must be placed again, since a host that loses rows scrolls its
// screen and the placements go with it. The real pixels were checked in a kitty
// under Xvfb: see NEGATIVE_CONTROLS.md.
func TestLauncherIconsSurviveAResize(t *testing.T) {
	requireReal(t, "real kitty under Xvfb")
	f := newLauncherFixture(t, 4, "TUIOS_CELL_SIZE=10x20")
	f.openWithQuery(t, "zzapp", 4)
	f.nudgeSelection()
	f.requirePlaced(t)
	for _, size := range [][2]int{{140, 40}, {120, 40}, {120, 41}, {120, 40}, {121, 40}, {120, 40}, {120, 50}, {120, 40}} {
		mark := len(f.stream.bytes())
		if err := f.term.Resize(size[0], size[1]); err != nil {
			t.Fatal(err)
		}
		if err := f.term.WaitFor(func(tuitest.Screen) bool {
			s := string(f.stream.bytes()[mark:])
			return strings.Contains(s, hostClear) && strings.Count(s, "_Ga=p,") >= 4
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: a resize to %v did not place the icons again: %v", size, err)
		}
		if s := string(f.stream.bytes()[mark:]); strings.Contains(s, "\x1b[2J") {
			t.Errorf("ASSERTION: a resize to %v sent an ED 2 to a host with kitty graphics", size)
		}
	}
}
