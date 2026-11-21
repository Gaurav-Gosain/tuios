package app

import (
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const (
	startupCols = 120
	startupRows = 40
	startupWait = 10 * time.Second
)

type startupRig struct {
	t    *testing.T
	m    *OS
	mu   sync.Mutex
	seen *session.SessionState
	q    []*session.SessionState
}

func newStartupRig(t *testing.T, cfg *config.UserConfig, arrange bool) *startupRig {
	t.Helper()
	ownSocket(t)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PS1", "$ ")

	prev := config.Global.Motion
	config.Global.Motion = config.MotionNone
	t.Cleanup(func() { config.Global.Motion = prev })

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
	if err := boot.CreateDetachedSession(name, startupCols, startupRows); err != nil {
		t.Fatalf("create detached session: %v", err)
	}
	if _, err := boot.AttachSession(name, false, startupCols, startupRows); err != nil {
		t.Fatalf("bootstrap attach: %v", err)
	}
	boot.StartReadLoop()

	r := &startupRig{t: t}

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

func (r *startupRig) daemonState() *session.SessionState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen
}

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

func startupConfig(tiled bool, layoutMode string) *config.UserConfig {
	cfg := config.DefaultConfig()
	cfg.Startup.Tiled = tiled
	cfg.Startup.Layout = layoutMode
	cfg.Startup.OpenDefaultWindow = true
	return cfg
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
