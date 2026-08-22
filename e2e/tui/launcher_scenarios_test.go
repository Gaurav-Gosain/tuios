package tuie2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The launcher's icons are kitty placements, and a placement outlives the
// panel that drew it, so every way the panel can go away is its own way to
// leave a picture on screen. These walk all of them and assert on the host
// stream rather than the grid: an image is not in the grid, and a test that
// checks a delete was written proves only that bytes were composed.

// launcherFixture is one launcher run: a tuios in a hermetic root with planted
// desktop entries, and the host stream it wrote.
type launcherFixture struct {
	term   *tuitest.Terminal
	stream *hostStream
	base   string
}

// newLauncherFixture boots tuios with n planted apps.
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

// live is the launcher placements the host still holds.
func (f *launcherFixture) live() []string { return liveLauncherPlacements(f.stream.bytes()) }

// openWithQuery opens the launcher and filters it to the planted apps.
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

// settle gives tuios time to compose and write the frames an action caused.
//
// It nudges the selection first. Icon placements are queued to go out behind
// the next frame bubbletea actually writes, and a frame whose text is byte for
// byte the frame before it is not written at all: the icon cells are blanks, so
// the frame that first carries a decoded icon is exactly such a frame. Moving
// the selection changes a row's colours, which is a text change, so it is what
// gets the queued graphics onto the wire. See the launcher-icon findings.
func (f *launcherFixture) settle(t *testing.T, nudge bool) {
	t.Helper()
	if nudge {
		_ = f.term.SendKeys(tuitest.Down)
		time.Sleep(250 * time.Millisecond)
		_ = f.term.SendKeys(tuitest.Up)
	}
	time.Sleep(900 * time.Millisecond)
}

// requirePlaced fails when the run never drew an icon, which would make every
// leak assertion after it vacuous.
func (f *launcherFixture) requirePlaced(t *testing.T) {
	t.Helper()
	if got := f.live(); len(got) == 0 {
		t.Fatalf("no launcher icon was ever placed, so the leak check proves nothing\n%s",
			f.term.Snapshot())
	}
}

// requireClean fails when any launcher placement outlived the panel.
func (f *launcherFixture) requireClean(t *testing.T, what string) {
	t.Helper()
	if got := f.live(); len(got) != 0 {
		t.Fatalf("%s left %d launcher icon placements on the host: %v\n%s",
			what, len(got), got, f.term.Snapshot())
	}
}

// waitClosed waits for the launcher panel to leave the screen.
func (f *launcherFixture) waitClosed(t *testing.T, what string) {
	t.Helper()
	if err := f.term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), launcherTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("the launcher never closed after %s: %v\n%s", what, err, f.term.Snapshot())
	}
}

// --- close paths -----------------------------------------------------------

func TestLauncherIconsCloseByEscape(t *testing.T) {
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "zzapp", 3)
	f.settle(t, true)
	f.requirePlaced(t)

	if err := f.term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	f.waitClosed(t, "esc")
	f.settle(t, false)
	f.requireClean(t, "closing with esc")
}

func TestLauncherIconsCloseByEnter(t *testing.T) {
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "zzapp", 3)
	f.settle(t, true)
	f.requirePlaced(t)

	if err := f.term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	f.waitClosed(t, "enter")
	f.settle(t, false)
	f.requireClean(t, "launching with enter")
}

func TestLauncherIconsCloseByTab(t *testing.T) {
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "zzapp", 3)
	f.settle(t, true)
	f.requirePlaced(t)

	if err := f.term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab: %v", err)
	}
	f.waitClosed(t, "tab")
	f.settle(t, false)
	f.requireClean(t, "typing it out with tab")
}

// TestLauncherIconsCloseByClickAway is the close path that goes through the
// mouse rather than a key, which is a different function and so a different
// chance to skip the cleanup.
func TestLauncherIconsCloseByClickAway(t *testing.T) {
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "zzapp", 3)
	f.settle(t, true)
	f.requirePlaced(t)

	// Top left, far outside the centred panel.
	mouseClick(t, f.term, 2, 1, tuitest.MouseLeft, 0)
	f.waitClosed(t, "a click away")
	f.settle(t, false)
	f.requireClean(t, "closing by clicking away")
}

// TestLauncherIconsCloseByShortLivedProgram launches something that exits at
// once, so the pane the launch created goes away in the same breath as the
// panel that launched it.
func TestLauncherIconsCloseByShortLivedProgram(t *testing.T) {
	f := newLauncherFixture(t, 20)
	plantExitingApp(t, f.base)

	f.openWithQuery(t, "zzapp", 3)
	f.settle(t, true)
	f.requirePlaced(t)

	// Narrow onto the program that exits the moment it starts, and run it.
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
	f.settle(t, false)
	f.requireClean(t, "launching a program that exits at once")
}

// TestLauncherIconsEmptyQuery is the launcher as it opens, with every program
// on offer and no query at all.
func TestLauncherIconsEmptyQuery(t *testing.T) {
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "", 0)
	f.settle(t, true)

	if err := f.term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	f.waitClosed(t, "esc")
	f.settle(t, false)
	f.requireClean(t, "closing an unfiltered launcher")
}

// TestLauncherIconsNoMatchQuery narrows to nothing, which replaces every row
// with one line of prose. Every icon that was under those rows has to go.
func TestLauncherIconsNoMatchQuery(t *testing.T) {
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "zzapp", 3)
	f.settle(t, true)
	f.requirePlaced(t)

	if err := f.term.SendKeys("zzznomatch"); err != nil {
		t.Fatalf("type no-match query: %v", err)
	}
	if err := f.term.WaitForText("No program matches", uiTimeout); err != nil {
		t.Fatalf("the no-match line never appeared: %v\n%s", err, f.term.Snapshot())
	}
	f.settle(t, true)
	// The rows are gone while the panel is still up, so nothing may still be
	// placed: this is the leak that shows as icons floating over prose.
	if got := f.live(); len(got) != 0 {
		t.Fatalf("a query that matches nothing left %d icons placed: %v\n%s",
			len(got), got, f.term.Snapshot())
	}

	if err := f.term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	f.waitClosed(t, "esc")
	f.settle(t, false)
	f.requireClean(t, "closing on a query that matched nothing")
}

// --- scan in flight --------------------------------------------------------

// TestLauncherIconsClosedDuringScan closes the launcher in the window where the
// $PATH scan it asked for has not come back yet, so the rows it would have
// rebuilt land on a closed panel.
func TestLauncherIconsClosedDuringScan(t *testing.T) {
	f := newLauncherFixture(t, 20)
	for i := range 6 {
		openLauncher(t, f.term)
		if err := f.term.SendKeys("zzapp"); err != nil {
			t.Fatalf("type query: %v", err)
		}
		// Deliberately no wait: close while the scan and the decodes are still
		// in flight.
		time.Sleep(time.Duration(40*i) * time.Millisecond)
		if err := f.term.SendKeys(tuitest.Esc); err != nil {
			t.Fatalf("esc: %v", err)
		}
		f.waitClosed(t, "esc during scan")
	}
	f.settle(t, false)
	f.requireClean(t, "closing while a scan was in flight")
}

// --- query changes and scrolling -------------------------------------------

// TestLauncherIconsSurviveQueryChanges narrows and widens the query so rows
// scroll in and out repeatedly, then closes. Every row that leaves has to take
// its picture with it.
func TestLauncherIconsSurviveQueryChanges(t *testing.T) {
	f := newLauncherFixture(t, 20)
	f.openWithQuery(t, "zzapp", 3)
	f.settle(t, true)
	f.requirePlaced(t)

	// Narrow to a few rows, then to none, then back to many.
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
		f.settle(t, true)
	}

	if err := f.term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	f.waitClosed(t, "esc")
	f.settle(t, false)
	f.requireClean(t, "closing after the query narrowed and widened")
}

// TestLauncherIconsSurviveScrolling walks the selection far enough that the
// scroll window moves repeatedly, placing and unplacing icons as it goes.
func TestLauncherIconsSurviveScrolling(t *testing.T) {
	f := newLauncherFixture(t, 40)
	f.openWithQuery(t, "zzapp", 3)
	f.settle(t, true)
	f.requirePlaced(t)

	for range 40 {
		_ = f.term.SendKeys(tuitest.Down)
	}
	time.Sleep(700 * time.Millisecond)
	for range 40 {
		_ = f.term.SendKeys(tuitest.Up)
	}
	f.settle(t, true)

	if err := f.term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	f.waitClosed(t, "esc")
	f.settle(t, false)
	f.requireClean(t, "closing after scrolling the list")
}

// --- repeated opens --------------------------------------------------------

// TestLauncherIconsDoNotAccumulate opens and closes several times. Nothing may
// be left standing between runs, and the image ids may not climb without bound:
// an upload is meant to be reused, not repeated per open.
func TestLauncherIconsDoNotAccumulate(t *testing.T) {
	f := newLauncherFixture(t, 20)
	for i := range 5 {
		f.openWithQuery(t, "zzapp", 3)
		f.settle(t, true)
		if i == 0 {
			f.requirePlaced(t)
		}
		if err := f.term.SendKeys(tuitest.Esc); err != nil {
			t.Fatalf("esc: %v", err)
		}
		f.waitClosed(t, "esc")
		f.settle(t, false)
		f.requireClean(t, fmt.Sprintf("close number %d", i+1))
	}
	if n := distinctLauncherImages(f.stream.bytes()); n > 24 {
		t.Fatalf("%d distinct launcher image ids were uploaded across five opens; "+
			"an icon is meant to be uploaded once and re-placed", n)
	}
}

// --- graphics off ----------------------------------------------------------

// TestLauncherDrawsNoIconsWithoutGraphics is the other half of the capability
// check: with kitty graphics off there is no icon column and no escape at all,
// and every close path still has to leave the screen clean.
func TestLauncherDrawsNoIconsWithoutGraphics(t *testing.T) {
	f := newLauncherFixture(t, 20, "TUIOS_KITTY_GRAPHICS=0")
	for _, closer := range []struct {
		name string
		key  tuitest.Key
	}{{"esc", tuitest.Esc}, {"enter", tuitest.Enter}, {"tab", tuitest.Tab}} {
		f.openWithQuery(t, "zzapp", 3)
		f.settle(t, true)
		if err := f.term.SendKeys(closer.key); err != nil {
			t.Fatalf("%s: %v", closer.name, err)
		}
		f.waitClosed(t, closer.name)
		f.settle(t, false)
		if got := f.live(); len(got) != 0 {
			t.Fatalf("closing with %s drew launcher placements with graphics off: %v",
				closer.name, got)
		}
	}
	if n := distinctLauncherImages(f.stream.bytes()); n != 0 {
		t.Fatalf("%d launcher images were uploaded with graphics off", n)
	}
}

// --- type it out -----------------------------------------------------------

// requireTypedThenRuns is the only honest assertion for the type-it-out path.
//
// Seeing the command's name on screen proves nothing: the pane's border carries
// it as a title, and the tty's own line discipline echoes bytes written before
// the shell has turned echo off, so text can be on screen that no line editor
// ever received. Pressing Enter and requiring the program to run is what tells
// a command line waiting to be edited from a picture of one.
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

// typeOutPrompt is the prompt the pane's shell prints under these tests. It
// replaces the harness default because it is waited on, and a bare "$" is
// something other parts of the screen can carry.
const typeOutPrompt = "zzsh$"

// waitForPaneShell waits until the pane's shell has printed its prompt.
//
// The prompt is what tells the shell being up from the tty having echoed the
// launcher's bytes before it started: the line discipline puts the typed line
// on screen either way, but only a shell that is running and reading prints a
// prompt. It has to be a prompt from /bin/sh rather than a greeting from a
// shell that has to be installed; this once waited on fish, which left the
// pane unopenable wherever fish is absent.
func waitForPaneShell(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.WaitForText(typeOutPrompt, shellTimeout); err != nil {
		t.Fatalf("the pane's shell never started: %v\n%s", err, term.Snapshot())
	}
}

// TestLauncherTypesOutIntoASpawningPane is the local half: the pane and its PTY
// exist the moment the launcher asks for them, and the line is written into a
// shell that has not started yet.
func TestLauncherTypesOutIntoASpawningPane(t *testing.T) {
	dir := writeProbe(t)
	term, _ := start(t, startOpts{
		args: []string{"--standalone"},
		// The harness's own /bin/sh, which every machine this runs on has.
		env: []string{"PATH=" + dir + ":/usr/bin:/bin", "PS1=" + typeOutPrompt + " "},
	})
	waitBoot(t, term)
	queryProbe(t, term)

	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab: %v", err)
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
	dir := writeProbe(t)
	// The daemon spawns the pane's shell from its own environment, and this
	// path types a bare name for that shell to resolve, so the probe has to be
	// on the daemon's $PATH and not only on the client's. The daemon inherits
	// this process's, which is what makes setting it here enough.
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-type", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{
		args: []string{"attach", "e2e-type"},
		env:  []string{"PATH=" + dir + ":/usr/bin:/bin"},
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("window mode: %v", err)
	}
	if err := term.WaitForText("Window Management Mode", uiTimeout); err != nil {
		t.Fatalf("never reached window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	queryProbe(t, term)
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 2
	}, shellTimeout); err != nil {
		t.Fatalf("the daemon never created the pane: %v\n%s", err, term.Snapshot())
	}
	requireTypedThenRuns(t, term)
}

// TestLauncherIconsWithABusyNeighbour is the condition that has broken other
// placements: a tiled layout with a pane printing continuously underneath the
// panel. The launcher's own icons are drawn over that, and every open has to
// take its own pictures down again whatever the panes below are doing.
func TestLauncherIconsWithABusyNeighbour(t *testing.T) {
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

	// One pane prints without stopping for the rest of the test.
	enterTerminalMode(t, term)
	runInShell(t, term, "echo NEIGHBOUR", "NEIGHBOUR", shellTimeout)
	typeLine(t, term, "while :; do seq 1 40; sleep 0.05; done")
	leaveTerminalMode(t, term)
	time.Sleep(700 * time.Millisecond)

	for i := range 3 {
		f.openWithQuery(t, "zzapp", 3)
		f.settle(t, true)
		if i == 0 {
			f.requirePlaced(t)
		}
		// Scroll the window so rows change picture, which is what makes the
		// placements churn rather than merely exist.
		for range 20 {
			_ = term.SendKeys(tuitest.Down)
		}
		f.settle(t, true)
		if err := term.SendKeys(tuitest.Esc); err != nil {
			t.Fatalf("esc: %v", err)
		}
		f.waitClosed(t, "esc over a busy neighbour")
		f.settle(t, false)
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
	dir := writeArgProbe(t)
	term, _ := start(t, startOpts{
		args: []string{"--standalone"},
		env:  []string{"PATH=" + dir + ":/usr/bin:/bin", "PS1=" + typeOutPrompt + " "},
	})
	waitBoot(t, term)

	// Tab, then keep typing: the argument has to reach the shell's line editor
	// and end up on the command that runs. This is the reason the key exists.
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

	// Enter is the other verb. It starts a program rather than a command line,
	// so the client stays where the window manager can hear it: 'n' has to open
	// a window rather than be typed at a shell.
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
