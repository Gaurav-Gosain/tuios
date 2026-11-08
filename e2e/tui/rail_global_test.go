package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// railShownNow reports whether the rail's sessions header is on screen.
func railShownNow(s tuitest.Screen) bool {
	return strings.Contains(s.Text(), sidebarHeader)
}

// waitRailShown waits until the rail is shown or hidden.
func waitRailShown(t *testing.T, term *tuitest.Terminal, shown bool, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railShownNow(s) == shown }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s: the rail is not %s\n%s", what, map[bool]string{true: "shown", false: "hidden"}[shown], term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
}

// railGlobalClient starts two sessions, attaches to the first, and visits
// the second and back, so both have a rail state of their own.
func railGlobalClient(t *testing.T, config string) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\nwidth = 24\n"+config)
	for _, name := range []string{"alpha", "beta"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	term := attachIn(t, base, "alpha", startOpts{cols: 120, rows: 36})
	waitRailShown(t, term, true, "alpha at attach")
	switchClient(t, base, term, "beta")
	waitRailShown(t, term, true, "beta on the first visit")
	switchClient(t, base, term, "alpha")
	waitRailShown(t, term, true, "back on alpha")
	return term, base
}

// TestRailToggleIsGlobal is issue 589. The rail's open or closed state was
// kept per session: hidden in one session, it came back in the next one
// visited. The toggle now shows or hides it in every session.
func TestRailToggleIsGlobal(t *testing.T) {
	term, base := railGlobalClient(t, "")

	sendKeys(t, term, tuitest.Ctrl('b'), "b")
	waitRailShown(t, term, false, "the toggle on alpha")
	switchClient(t, base, term, "beta")
	time.Sleep(time.Second)
	waitRailShown(t, term, false, "beta after the rail was hidden on alpha")

	sendKeys(t, term, tuitest.Ctrl('b'), "b")
	waitRailShown(t, term, true, "the toggle on beta")
	switchClient(t, base, term, "alpha")
	time.Sleep(time.Second)
	waitRailShown(t, term, true, "alpha after the rail was shown on beta")
}

// TestRailTogglePerSession: appearance.sidebar.per_session = true keeps the
// old behaviour, where each session keeps its own rail state.
func TestRailTogglePerSession(t *testing.T) {
	term, base := railGlobalClient(t, "per_session = true\n")

	sendKeys(t, term, tuitest.Ctrl('b'), "b")
	waitRailShown(t, term, false, "the toggle on alpha")
	switchClient(t, base, term, "beta")
	time.Sleep(time.Second)
	waitRailShown(t, term, true, "beta keeps its own rail with per_session")
}
