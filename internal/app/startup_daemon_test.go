package app

import (
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// These tests cover the path the [startup] settings are actually used on: a
// client attaching to a daemon session that already holds the window the daemon
// created for it. Two things went wrong there and neither is visible from an
// OS built by hand.
//
// The session is not empty, because the daemon gave it a window, so the guard
// on applyStartupPreferences skipped every [startup] setting the user asked
// for. And the daemon, not the client, holds AutoTiling: a client that turns
// tiling on without pushing that to the daemon has it turned straight back off
// by the next state the daemon broadcasts, which is what made scrolling mode in
// particular refuse to start tiled.

const (
	startupCols = 120
	startupRows = 40
	startupWait = 10 * time.Second
)

// startupRig is a real daemon, a session the daemon built with its own initial
// window, a client OS attached to it the way cmd/tuios attaches, and a second
// connection that watches what the daemon ends up holding.
type startupRig struct {
	t    *testing.T
	m    *OS
	mu   sync.Mutex
	seen *session.SessionState
	q    []*session.SessionState
}

// newStartupRig brings all of that up with the given [startup] config.
//
// It does not reuse the rehydration rig's attachClientOS: that one hardcodes a
// default config, and the whole question here is what a non-default [startup]
// section does on the attach path.
func newStartupRig(t *testing.T, cfg *config.UserConfig, arrange bool) *startupRig {
	t.Helper()
	ownSocket(t)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PS1", "$ ")

	prev := config.AnimationsEnabled
	config.AnimationsEnabled = false
	t.Cleanup(func() { config.AnimationsEnabled = prev })

	d := session.NewDaemon(&session.DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(d.Stop)

	const name = "startup"
	boot := session.NewTUIClient()
	if err := boot.Connect("test", startupCols, startupRows); err != nil {
		t.Fatalf("bootstrap connect: %v", err)
	}
	// A detached create is how the daemon builds a session on its own: it comes
	// back holding one real window with one real PTY, and that window is marked
	// Unplaced because the daemon has no viewport to place it in.
	if err := boot.CreateDetachedSession(name, startupCols, startupRows); err != nil {
		t.Fatalf("create detached session: %v", err)
	}
	if _, err := boot.AttachSession(name, false, startupCols, startupRows); err != nil {
		t.Fatalf("bootstrap attach: %v", err)
	}
	boot.StartReadLoop()

	r := &startupRig{t: t}

	// The watcher stays attached for the whole test and reads every state the
	// daemon broadcasts, so what the daemon holds can be asserted on without
	// asking the client under test to vouch for it.
	watch := session.NewTUIClient()
	if err := watch.Connect("test", startupCols, startupRows); err != nil {
		t.Fatalf("watcher connect: %v", err)
	}
	watchState, err := watch.AttachSession(name, false, startupCols, startupRows)
	if err != nil {
		t.Fatalf("watcher attach: %v", err)
	}
	r.record(watchState)
	watch.OnStateSync(func(state *session.SessionState, _, _ string) { r.record(state) })
	watch.StartReadLoop()
	t.Cleanup(func() { _ = watch.Close() })

	if err := boot.Detach(); err != nil {
		t.Fatalf("bootstrap detach: %v", err)
	}
	_ = boot.Close()

	if arrange {
		arrangeSession(t, r, name)
	}

	c := session.NewTUIClient()
	if err := c.Connect("test", startupCols, startupRows); err != nil {
		t.Fatalf("connect: %v", err)
	}
	state, err := c.AttachSession(name, false, startupCols, startupRows)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	c.StartReadLoop()
	t.Cleanup(func() { _ = c.Close() })
	if state == nil || len(state.Windows) == 0 {
		t.Fatalf("the daemon-built session came back with no windows")
	}

	m := NewOS(OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
		IsDaemonSession: true,
		DaemonClient:    c,
		SessionName:     c.SessionName(),
	})
	if err := m.RestoreFromState(state); err != nil {
		t.Fatalf("restore state: %v", err)
	}
	if err := m.SetupPTYOutputHandlers(); err != nil {
		t.Fatalf("setup pty handlers: %v", err)
	}
	// What the program loop does with a broadcast, queued so it is applied on
	// the test's own goroutine rather than the read loop's.
	c.OnStateSync(func(state *session.SessionState, _, _ string) {
		r.mu.Lock()
		r.q = append(r.q, state)
		r.mu.Unlock()
	})
	drainExits(t, m)
	t.Cleanup(func() {
		for _, w := range m.Windows {
			w.Close()
		}
	})

	r.m = m
	return r
}

// arrangeSession makes the session one a user arranged: a client attaches,
// places the panes against a real viewport and pushes that, which is what clears
// Unplaced daemon-side. It leaves tiling off, so a later attach that stamped
// [startup] tiling over the arrangement would be visible.
//
// The push is retried because the daemon rejects one built from a version older
// than its own: it reconciles the push away and sends the merged state back for
// the client to build its next one from. A real client does that through its
// program loop; this does it by hand, which is what makes the arrangement
// something the test can rely on having happened.
func arrangeSession(t *testing.T, r *startupRig, name string) {
	t.Helper()
	m, c := attachClientOS(t, name, startupCols, startupRows, false)
	var (
		mu       sync.Mutex
		incoming []*session.SessionState
	)
	c.OnStateSync(func(state *session.SessionState, _, _ string) {
		mu.Lock()
		incoming = append(incoming, state)
		mu.Unlock()
	})
	rigWaitUntil(t, "the daemon to accept the arrangement", func() bool {
		mu.Lock()
		queued := incoming
		incoming = nil
		mu.Unlock()
		for _, state := range queued {
			if err := m.ApplyStateSync(state); err != nil {
				t.Fatalf("arranging client: apply state sync: %v", err)
			}
		}
		if st := r.daemonState(); st != nil && !stateUnarranged(st) {
			return true
		}
		m.forgetSyncedState()
		m.SyncStateToDaemon()
		return false
	})
	for _, w := range m.Windows {
		w.Close()
	}
	if err := c.Detach(); err != nil {
		t.Fatalf("arranging client detach: %v", err)
	}
	_ = c.Close()
}

func (r *startupRig) record(state *session.SessionState) {
	if state == nil {
		return
	}
	r.mu.Lock()
	r.seen = state
	r.mu.Unlock()
}

// daemonState is the last state the daemon broadcast.
func (r *startupRig) daemonState() *session.SessionState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen
}

// boot delivers the first WindowSizeMsg, which is what applies the [startup]
// settings, and then keeps applying whatever the daemon broadcasts until cond
// holds or the wait runs out. Applying the broadcasts is the point: the daemon's
// echo of AutoTiling is what used to turn the tiling back off.
func (r *startupRig) boot(cond func() bool) {
	r.t.Helper()
	r.bootFor(cond, startupWait)
}

// bootFor is boot with the wait chosen, for the cases whose condition is that
// nothing happens: those cannot wait for a signal, so they wait out the window
// in which the settings would have been applied. Applying them is synchronous
// with the first WindowSizeMsg, so the window only has to cover the sync that
// follows it.
func (r *startupRig) bootFor(cond func() bool, wait time.Duration) {
	r.t.Helper()
	r.m.Update(tea.WindowSizeMsg{Width: startupCols, Height: startupRows})
	deadline := time.Now().Add(wait)
	for {
		r.mu.Lock()
		var next *session.SessionState
		if len(r.q) > 0 {
			next, r.q = r.q[0], r.q[1:]
		}
		r.mu.Unlock()
		if next != nil {
			if err := r.m.ApplyStateSync(next); err != nil {
				r.t.Fatalf("apply state sync: %v", err)
			}
			continue
		}
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// startupConfig is a config with the [startup] section under test.
func startupConfig(tiled bool, layoutMode string) *config.UserConfig {
	cfg := config.DefaultConfig()
	cfg.Startup.Tiled = tiled
	cfg.Startup.Layout = layoutMode
	cfg.Startup.OpenDefaultWindow = true
	return cfg
}

// paneTarget is where the layout has put a pane.
//
// Scrolling mode slides a pane to its column even with animations off, because
// a viewport that jumps is disorienting, so the pane's own X and Y are still the
// old ones until the animation has ticked. Its endpoint is what the layout
// decided, and that is the thing under test here rather than the easing.
func paneTarget(m *OS, w *terminal.Window) (x, y, width, height int) {
	for _, a := range m.Animations {
		if a.Window == w && !a.Complete {
			return a.EndX, a.EndY, a.EndWidth, a.EndHeight
		}
	}
	return w.X, w.Y, w.Width, w.Height
}

// TestStartupTilingOnDaemonBuiltSession is the reported bug: with startup.tiled
// on, a client attaching to a session the daemon built starts untiled. Every
// layout mode is covered, because the guard that skipped it is mode-blind and
// the daemon sync that lost it was not.
func TestStartupTilingOnDaemonBuiltSession(t *testing.T) {
	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack, LayoutModeScrolling} {
		t.Run(mode, func(t *testing.T) {
			r := newStartupRig(t, startupConfig(true, mode), false)
			r.boot(func() bool {
				st := r.daemonState()
				return r.m.AutoTiling && st != nil && st.AutoTiling
			})

			if !r.m.AutoTiling {
				t.Fatalf("the client did not start tiled in %s mode", mode)
			}
			if got := r.m.LayoutName(); got != mode {
				t.Fatalf("layout mode is %q, want %q", got, mode)
			}
			// The daemon holds AutoTiling for the session and echoes it to every
			// client. If it never heard about this one, the next echo turns the
			// tiling off again and the pane drifts back to a floating box.
			if st := r.daemonState(); st == nil || !st.AutoTiling {
				t.Fatalf("the daemon was never told the session is tiled, so its next push undoes it")
			}
			// The flag on its own proves nothing: the pane has to have been laid
			// out. A tiled pane fills the layout box top to bottom and starts at
			// its origin; the floating box the daemon's window would otherwise
			// keep is half the screen, inset from both.
			bounds := r.m.GetBSPBounds()
			w := r.m.Windows[0]
			x, y, _, height := paneTarget(r.m, w)
			if x != bounds.X || y != bounds.Y || height != bounds.H {
				t.Fatalf("the pane was never tiled: window=(%d,%d %dx%d) box=(%d,%d %dx%d)",
					x, y, w.Width, height, bounds.X, bounds.Y, bounds.W, bounds.H)
			}
			// One window, not two: the daemon already opened the session's default
			// window, so open_default_window has nothing left to do.
			if len(r.m.Windows) != 1 {
				t.Fatalf("expected the daemon's one window, got %d", len(r.m.Windows))
			}
		})
	}
}

// TestStartupLeavesAnArrangedSessionAlone is the other half, and the reason the
// guard exists at all: a session whose panes a client has already placed is the
// user's own arrangement, and attaching to it must not stamp [startup] over it.
func TestStartupLeavesAnArrangedSessionAlone(t *testing.T) {
	r := newStartupRig(t, startupConfig(true, LayoutModeScrolling), true)
	r.bootFor(func() bool { return false }, 2*time.Second)

	if r.m.AutoTiling {
		t.Error("tiling was forced onto a session the user had arranged")
	}
	if len(r.m.Windows) != 1 {
		t.Errorf("the arranged session gained a window: %d windows", len(r.m.Windows))
	}
	if st := r.daemonState(); st != nil && st.AutoTiling {
		t.Error("the daemon was told to tile a session the user had arranged")
	}
}

// TestStartupTilingSurvivesADaemonPush pins the sync on its own, without the
// attach path around it: turning tiling on has to reach the daemon, or the next
// state the daemon sends takes it away again.
func TestStartupTilingSurvivesADaemonPush(t *testing.T) {
	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack, LayoutModeScrolling} {
		t.Run(mode, func(t *testing.T) {
			r := newStartupRig(t, startupConfig(true, mode), false)
			r.boot(func() bool {
				st := r.daemonState()
				return r.m.AutoTiling && st != nil && st.AutoTiling
			})
			// A second pane, asked for through the daemon, is the ordinary event
			// that makes the daemon broadcast the session again.
			if err := r.m.DaemonClient.SendIntent("NewWindow"); err != nil {
				t.Fatalf("ask the daemon for a window: %v", err)
			}
			r.boot(func() bool { return len(r.m.Windows) == 2 })

			if len(r.m.Windows) != 2 {
				t.Fatalf("the second window never arrived: %d windows", len(r.m.Windows))
			}
			if !r.m.AutoTiling {
				t.Fatalf("a daemon push turned the startup tiling off in %s mode", mode)
			}
			for _, w := range r.m.Windows {
				if w.Width >= startupCols {
					t.Errorf("pane %s is the full width (%d): the panes are not tiled",
						shortID(w.ID), w.Width)
				}
			}
		})
	}
}
