package app

import (
	"runtime"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// dockReloadRuntime stands in for the Bubble Tea runtime, as far as the dock
// is concerned: every command runs on its own goroutine, and the dock messages
// it produces are fed back through Update on the test goroutine. A listener the
// model has abandoned stays parked exactly as it would in the real program.
type dockReloadRuntime struct {
	msgs chan tea.Msg
}

func (r *dockReloadRuntime) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				r.run(c)
			}
			return
		}
		if _, ok := msg.(dockComponentMsg); ok {
			r.msgs <- msg
		}
	}()
}

// drain delivers dock messages to the model until none arrives for a while.
func (r *dockReloadRuntime) drain(m *OS) {
	for {
		select {
		case msg := <-r.msgs:
			_, cmd := m.Update(msg)
			r.run(cmd)
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

// settledGoroutines waits for the goroutine count to reach want or below, and
// returns the count it saw last. A stopped engine's goroutines leave on their
// own schedule, so a single read would race them.
func settledGoroutines(want int) int {
	deadline := time.Now().Add(5 * time.Second)
	n := runtime.NumGoroutine()
	for n > want && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		n = runtime.NumGoroutine()
	}
	return n
}

// stableGoroutines returns the goroutine count once it has stopped moving.
func stableGoroutines() int {
	prev := runtime.NumGoroutine()
	for range 50 {
		time.Sleep(50 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == prev {
			return n
		}
		prev = n
	}
	return prev
}

// TestDockReloadDoesNotLeakListeners is the regression test for a config
// reload leaving the previous dock engine's listener behind. Each reload built
// a new engine and armed a listener on its channel, while the listener on the
// old channel stayed parked on a receive nothing would ever answer. A client
// that reloaded its config forty times carried forty dead listeners.
//
// The count after many reloads has to stay within a small bound of the count
// after one.
func TestDockReloadDoesNotLeakListeners(t *testing.T) {
	cfg := config.DefaultConfig()
	center := []string{"custom/hello", "clock"}
	cfg.Dock.Center = &center
	cfg.Dock.Custom = map[string]config.DockCustomConfig{
		"hello": {Command: "echo hello"},
	}

	m := &OS{
		Settings:       config.Global,
		Width:          120,
		Height:         44,
		WorkspaceFocus: map[int]int{},
		NumWorkspaces:  9,
		ConfigReadOnly: true,
	}
	m.UserConfig = cfg
	t.Cleanup(m.StopDockComponents)

	rt := &dockReloadRuntime{msgs: make(chan tea.Msg, 256)}
	rt.run(m.InitDockComponents())
	rt.drain(m)

	rt.run(m.ApplyReloadedConfig(cfg))
	rt.drain(m)
	if m.dockEngine.Text("custom/hello") != "hello" {
		t.Fatal("the custom component did not draw after a reload")
	}
	base := stableGoroutines()

	const reloads = 40
	for range reloads {
		rt.run(m.ApplyReloadedConfig(cfg))
		rt.drain(m)
	}
	if m.dockEngine.Text("custom/hello") != "hello" {
		t.Fatal("the custom component did not draw after many reloads")
	}

	const slack = 5
	after := settledGoroutines(base + slack)
	t.Logf("goroutines: %d after one reload, %d after %d more", base, after, reloads)
	if after > base+slack {
		t.Fatalf("goroutines grew from %d to %d over %d reloads; a reload leaves the old dock listener behind", base, after, reloads)
	}
}
