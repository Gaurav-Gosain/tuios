//go:build unix

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestDockEngineCoalescesAnEventMidRun: an event that lands while the command
// is running is kept as one pending re-run rather than dropped, for a rail
// section and a dock cell alike. Their value is about the state now, and an
// event that lands mid-run would otherwise leave the old value on screen until
// the next event (issue 1416).
//
// Deterministic: the command sleeps past the debounce, so the second event
// is guaranteed to land in flight.
//
// Negative control, confirmed red: drop the Coalesce branch from fire. The
// third update never arrives.
func TestDockEngineCoalescesAnEventMidRun(t *testing.T) {
	t.Run("rail section", func(t *testing.T) {
		testDockEngineCoalesces(t, &dockComponent{Name: "rail/custom", MultiLine: true, Coalesce: true})
	})
	t.Run("dock cell", func(t *testing.T) {
		testDockEngineCoalesces(t, &dockComponent{Name: "custom/slow"})
	})
}

func testDockEngineCoalesces(t *testing.T, comp *dockComponent) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "mark")
	if err := os.WriteFile(mark, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	comp.Command = "sleep 1; cat " + mark
	comp.Refresh = config.DockRefresh{Kind: config.DockRefreshEvent, Events: []string{"window-focused"}}
	engine := newDockEngine([]*dockComponent{comp})
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

// TestDockEngineDropsAnIntervalMidRun is the other half of the rule. A cell
// slower than its interval polls at the rate it can manage, so the interval
// that lands mid-run is dropped and queues nothing, and the process count stays
// at one. The deadline is an hour off, so the only trigger here is the fire this
// test makes, and the only way to a second update is a queued re-run.
//
// Negative control: make fire keep a re-run for every kind of refresh. The
// second update arrives.
func TestDockEngineDropsAnIntervalMidRun(t *testing.T) {
	comp := &dockComponent{
		Name:    "custom/poll",
		Command: "sleep 1; echo polled",
		Refresh: config.DockRefresh{Kind: config.DockRefreshInterval, Interval: time.Hour},
	}
	engine := newDockEngine([]*dockComponent{comp})
	t.Cleanup(engine.Stop)
	engine.Start()

	// The run at start holds the flag from before the command starts.
	deadline := time.Now().Add(5 * time.Second)
	for {
		engine.mu.Lock()
		running := comp.running
		engine.mu.Unlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run at start never began")
		}
		time.Sleep(5 * time.Millisecond)
	}
	engine.fire(comp)

	awaitDockUpdate(t, engine, 5*time.Second, "the run at start")
	select {
	case extra := <-engine.Updates():
		t.Fatalf("an interval mid-run queued a re-run, which read %q", extra.Text)
	case <-time.After(2 * time.Second):
	}
}
