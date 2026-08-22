package tuie2e

import (
	"fmt"
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
