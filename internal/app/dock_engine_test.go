package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The dock engine's guards. The first one is the invariant the whole component
// design was shaped around and is the reason the engine parks on a channel
// receive instead of holding a ticker.

// TestDockEngineArmsNoTimerWhenNothingPolls is the idle invariant, stated as a
// test: a dock made only of once, push and event components must not wake at
// all. Not "wake cheaply" - not wake.
//
// It is the unit-test form of the spike's phase 2, and it is what lets
// BenchmarkIdleTick stay where it is with the dock's refresh machinery loaded.
func TestDockEngineArmsNoTimerWhenNothingPolls(t *testing.T) {
	engine := newDockEngine([]*dockComponent{
		{Name: "custom/static", Command: "echo static", Refresh: config.DockRefresh{Kind: config.DockRefreshOnce}},
		{Name: "custom/silent", Command: "sleep 30", Refresh: config.DockRefresh{Kind: config.DockRefreshPush}},
		{
			Name: "custom/onevent", Command: "echo x",
			Refresh: config.DockRefresh{Kind: config.DockRefreshEvent, Events: []string{"agent-state"}},
		},
	})
	t.Cleanup(engine.Stop)
	engine.Start()

	// The once components each run at startup, which is one wake apiece and is
	// the point of "once". Drain them, then watch an idle window.
	deadline := time.After(time.Second)
	initial := 0
drain:
	for initial < 2 {
		select {
		case <-engine.Updates():
			initial++
		case <-deadline:
			break drain
		}
	}

	before := engine.Wakes()
	select {
	case u := <-engine.Updates():
		t.Fatalf("an idle dock woke for %q; no interval component is configured, so there is no timer to fire", u.Name)
	case <-time.After(400 * time.Millisecond):
	}
	if got := engine.Wakes() - before; got != 0 {
		t.Fatalf("an idle dock took %d wakes; want 0", got)
	}
}

// TestDockEngineUnchangedValueDrawsNothing is the render gate: a component that
// polls a value that has not moved costs an execution and no frame. A one
// second cell watching a number that changes every five minutes is meant to be
// twelve execs a minute and about zero renders.
func TestDockEngineUnchangedValueDrawsNothing(t *testing.T) {
	engine := newDockEngine([]*dockComponent{
		{Name: "custom/steady", Command: "echo same", MaxWidth: 24},
	})
	t.Cleanup(engine.Stop)

	first, _ := engine.applyUpdate(dockComponentUpdate{Name: "custom/steady", Text: "same"})
	if !first {
		t.Fatal("the first value did not change the cell")
	}
	again, _ := engine.applyUpdate(dockComponentUpdate{Name: "custom/steady", Text: "same"})
	if again {
		t.Fatal("an unchanged value asked for a frame")
	}
	moved, _ := engine.applyUpdate(dockComponentUpdate{Name: "custom/steady", Text: "different"})
	if !moved {
		t.Fatal("a changed value did not ask for a frame")
	}
}

// TestDockComponentFailureHidesTheCellAndSaysSoOnce pins what a broken
// component looks like: nothing on the bar, and one report to whoever wrote it.
// A cell that kept showing the last value its command produced would be
// confidently wrong, and one that failed in total silence is the failure mode
// the hackability audit kept finding.
func TestDockComponentFailureHidesTheCellAndSaysSoOnce(t *testing.T) {
	engine := newDockEngine([]*dockComponent{
		{Name: "custom/flaky", Command: "false", MaxWidth: 24},
	})
	t.Cleanup(engine.Stop)

	engine.applyUpdate(dockComponentUpdate{Name: "custom/flaky", Text: "ok"})
	if engine.Text("custom/flaky") != "ok" {
		t.Fatal("a working component did not draw")
	}

	_, reported := engine.applyUpdate(dockComponentUpdate{Name: "custom/flaky", Exit: 1, Err: "exit status 1"})
	if !reported {
		t.Fatal("the first failure was not reported")
	}
	if got := engine.Text("custom/flaky"); got != "" {
		t.Fatalf("a failed component still draws %q; a broken cell must be absent, not stale", got)
	}
	_, reportedAgain := engine.applyUpdate(dockComponentUpdate{Name: "custom/flaky", Exit: 1, Err: "exit status 1"})
	if reportedAgain {
		t.Fatal("a failing component reported itself twice; one report per failure streak")
	}

	// Enough consecutive failures and it stops being re-run on its own
	// schedule. A script that cannot run is not made more likely to run by
	// being run more often.
	for range config.DockCustomFailureLimit {
		engine.applyUpdate(dockComponentUpdate{Name: "custom/flaky", Exit: 1, Err: "exit status 1"})
	}
	c, _ := engine.Component("custom/flaky")
	if !c.stopped {
		t.Fatal("a component that failed every time is still being polled")
	}

	// An explicit refresh clears the give-up, so a fixed script recovers
	// without restarting the session.
	if err := engine.Refresh("custom/flaky"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if c, _ := engine.Component("custom/flaky"); c.stopped {
		t.Fatal("refresh-dock did not revive a component that had given up")
	}
}

// TestDockComponentRunsAndLaunders is the contract end to end: a command runs,
// its first line becomes the cell, colour survives, and everything else that
// could reach the screen does not.
func TestDockComponentRunsAndLaunders(t *testing.T) {
	engine := newDockEngine([]*dockComponent{{
		Name:     "custom/hello",
		Command:  "printf 'br\\033[33manch\\033[0m\\033[2Jx\\nsecond line\\n'",
		MaxWidth: 24,
	}})
	t.Cleanup(engine.Stop)
	engine.Start()

	select {
	case u := <-engine.Updates():
		engine.applyUpdate(u)
	case <-time.After(5 * time.Second):
		t.Fatal("the component never reported")
	}

	got := engine.Text("custom/hello")
	if !strings.Contains(got, "\x1b[33m") {
		t.Errorf("cell %q lost its colour; SGR is the one escape a component may emit", got)
	}
	if strings.Contains(got, "\x1b[2J") {
		t.Errorf("cell %q kept an erase sequence; a dock cell may not redraw somebody else's screen", got)
	}
	if strings.Contains(got, "second line") {
		t.Errorf("cell %q took more than the first line", got)
	}
}

// TestDockComponentTimeoutHidesTheCell covers the subprocess that never
// returns. The bar has to stay a bar.
func TestDockComponentTimeoutHidesTheCell(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the component timeout")
	}
	engine := newDockEngine([]*dockComponent{{
		Name: "custom/hang", Command: "sleep 30", MaxWidth: 24,
	}})
	t.Cleanup(engine.Stop)
	engine.Start()

	select {
	case u := <-engine.Updates():
		if u.Err == "" {
			t.Fatalf("a hung component reported success: %+v", u)
		}
		engine.applyUpdate(u)
	case <-time.After(config.DockCustomTimeout + 5*time.Second):
		t.Fatal("a hung component was never killed")
	}
	if got := engine.Text("custom/hang"); got != "" {
		t.Fatalf("a hung component drew %q", got)
	}
}

// TestDockPushComponentReadsLines covers the persistent process: each line it
// writes is one update, and the wakes are driven by the pipe rather than by a
// clock.
func TestDockPushComponentReadsLines(t *testing.T) {
	engine := newDockEngine([]*dockComponent{{
		Name:     "custom/pushed",
		Command:  "printf 'one\\n'; sleep 0.2; printf 'two\\n'; sleep 30",
		Refresh:  config.DockRefresh{Kind: config.DockRefreshPush},
		MaxWidth: 24,
	}})
	t.Cleanup(engine.Stop)
	engine.Start()

	want := []string{"one", "two"}
	for _, w := range want {
		select {
		case u := <-engine.Updates():
			if u.Text != w {
				t.Fatalf("push line = %q, want %q", u.Text, w)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the push component never sent %q", w)
		}
	}
}

// TestDockEventComponentWakesOnItsEventOnly is the cheapest refresh source:
// nothing at all until the daemon says something happened, and then once for a
// burst rather than once per event.
func TestDockEventComponentWakesOnItsEventOnly(t *testing.T) {
	engine := newDockEngine([]*dockComponent{{
		Name:    "custom/agents",
		Command: "echo agents",
		Refresh: config.DockRefresh{Kind: config.DockRefreshEvent, Events: []string{"agent-state"}},
	}})
	t.Cleanup(engine.Stop)

	engine.NotifyEvent("window-focused")
	select {
	case u := <-engine.Updates():
		t.Fatalf("a component watching agent-state woke for an unrelated event: %+v", u)
	case <-time.After(400 * time.Millisecond):
	}

	// A burst is one refresh, not one per event.
	for range 5 {
		engine.NotifyEvent("agent-state")
	}
	select {
	case u := <-engine.Updates():
		if u.Text != "agents" {
			t.Fatalf("event refresh produced %q", u.Text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the event never refreshed the component")
	}
	select {
	case u := <-engine.Updates():
		t.Fatalf("a burst of five events refreshed twice: %+v", u)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestDockSanitizeKeepsColourAndDropsControls pins the launder rules, which are
// half the contract a component author is writing against.
func TestDockSanitizeKeepsColourAndDropsControls(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m"},
		{"\x1b[1;32mbold green\x1b[m", "\x1b[1;32mbold green\x1b[m"},
		{"before\x1b[2Jafter", "beforeafter"},
		{"move\x1b[10;20Hhere", "movehere"},
		{"bell\aand\bback", "bellandback"},
		{"tab\tseparated", "tabseparated"},
		{"\x1b]0;title\x07plain", "plain"},
		{"\x1b]2;a\x1b\\tail", "tail"},
	}
	for _, tc := range cases {
		if got := dockSanitize(tc.in); got != tc.want {
			t.Errorf("dockSanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
