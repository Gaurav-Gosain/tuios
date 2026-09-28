package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// openSwitcherOn opens the session switcher, filters it to query and presses
// Enter, once the row want is on screen.
func openSwitcherOn(t *testing.T, term *tuitest.Terminal, want, query string) {
	t.Helper()
	time.Sleep(insertGuard)
	if err := term.SendKeys(tuitest.Ctrl('b'), "S"); err != nil {
		t.Fatalf("open the session switcher: %v", err)
	}
	if err := term.WaitForText(want, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the session switcher never listed %q: %v\n%s", want, err, term.Snapshot())
	}
	if err := term.SendKeys(query); err != nil {
		t.Fatalf("type %q: %v", query, err)
	}
	time.Sleep(300 * time.Millisecond)
	t.Logf("switcher filtered to %q:\n%s", query, term.Snapshot())
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
}

// noSwitchFailure fails the test if the screen shows a refused switch, which
// is what #196 put on screen: the rail identity sent to a daemon as a name.
func noSwitchFailure(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	text := term.Screen().Text()
	if strings.Contains(text, "Switch failed") || strings.Contains(text, "path separator") {
		t.Fatalf("ASSERTION: %s the switch was refused:\n%s", when, term.Snapshot())
	}
}

// TestSwitcherCrossesMachines is #196 on screen: the session switcher takes
// the client from this machine to a session on build and back again. Before
// the fix both Enters sent the row's rail identity to the daemon the client
// was on, so the first opened nothing and the second failed with "session
// name \x00host/local... contains a path separator".
func TestSwitcherCrossesMachines(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	// The rail is where the host listing shows up, and the switcher reads the
	// same listing, so wait for build's session there first.
	toggleSidebarViaPalette(t, term)
	railShows(t, term, "far-shell")
	waitRailCurrent(t, term, "home")

	// Out to build.
	openSwitcherOn(t, term, "far-shell @ build", "far")
	waitRailCurrent(t, term, "far-shell")
	noSwitchFailure(t, term, "going to build,")
	if out, _ := tuiosCLI(t, base, "ls"); strings.Contains(out, "far-shell") {
		t.Fatalf("ASSERTION: this machine's daemon made a session called far-shell, so the switch never left it:\n%s", out)
	}
	t.Logf("on build:\n%s", term.Snapshot())
	saveFrame(t, term, "switcher-to-build")

	// And back to this machine.
	openSwitcherOn(t, term, "home @ local", "home")
	waitRailCurrent(t, term, "home")
	noSwitchFailure(t, term, "coming back from build,")
	t.Logf("back home:\n%s", term.Snapshot())
	saveFrame(t, term, "switcher-back-home")
	alive(t, term, "after crossing machines with the switcher")
}
