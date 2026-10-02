//go:build unix

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestDockEngineCoalescesAnEventMidRun is the rail section's one scheduling
// difference from a dock cell: an event that lands while the command is
// running is kept as one pending re-run rather than dropped. A dock cell
// drops it (fire's drop-if-in-flight), and for a cell that is right: a value
// is a value. The rail's rows are about the focused pane, and a focus change
// that lands mid-run would otherwise leave the old pane's text on screen
// until the next event.
//
// Deterministic: the command sleeps past the debounce, so the second event
// is guaranteed to land in flight.
//
// Negative control, confirmed red: drop the Coalesce branch from fire. The
// third update never arrives.
func TestDockEngineCoalescesAnEventMidRun(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "mark")
	if err := os.WriteFile(mark, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := newDockEngine([]*dockComponent{{
		Name:      "rail/custom",
		Command:   "sleep 1; cat " + mark,
		MultiLine: true,
		Coalesce:  true,
		Refresh:   config.DockRefresh{Kind: config.DockRefreshEvent, Events: []string{"window-focused"}},
	}})
	t.Cleanup(engine.Stop)
	// An open rail, as InitDockComponents tells it before Start: the engine
	// runs nothing for the rail while the width is zero.
	engine.SetRailContext(railContext{Width: 26, Height: 10})
	engine.Start()

	first := awaitDockUpdate(t, engine, 5*time.Second, "the run at start")
	if first.Text != "first" {
		t.Fatalf("the first run read %q, want first", first.Text)
	}
	engine.NotifyEvent("window-focused")
	time.Sleep(500 * time.Millisecond) // past the debounce, inside the sleep
	if err := os.WriteFile(mark, []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine.NotifyEvent("window-focused")
	engine.NotifyEvent("window-focused")

	second := awaitDockUpdate(t, engine, 5*time.Second, "the run the first event started")
	third := awaitDockUpdate(t, engine, 5*time.Second, "the one pending re-run")
	if third.Text != "second" {
		t.Fatalf("the pending re-run read %q after %q, want second", third.Text, second.Text)
	}
	select {
	case extra := <-engine.Updates():
		t.Fatalf("a burst of two events mid-run cost two re-runs, the second read %q", extra.Text)
	case <-time.After(2 * time.Second):
	}
}
