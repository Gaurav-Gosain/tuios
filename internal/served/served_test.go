package served

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the test binary from the developer's own XDG directories.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }

// TestNewModelFillsWhatEverySessionLoads checks the parts every served session
// used to load by hand, in four copies.
func TestNewModelFillsWhatEverySessionLoads(t *testing.T) {
	caps := &app.HostCapabilities{TrueColor: true, TerminalName: "test"}
	m := NewModel(app.OSOptions{
		Client: app.ClientBrowser,
		Width:  80,
		Height: 24,
		Caps:   caps,
	}, config.Overrides{SharedBorders: true})
	t.Cleanup(m.Cleanup)

	if m.UserConfig == nil {
		t.Error("the model has no user config")
	}
	if m.KeybindRegistry == nil {
		t.Error("the model has no keybind registry")
	}
	if !m.Settings.SharedBorders {
		t.Error("the server's flags did not reach the session's appearance seed")
	}
	if m.Caps != caps {
		t.Error("the caller's capabilities were replaced")
	}
}

// TestAttachPicksOnlyWhenUnnamed runs the daemon half against a real daemon:
// a named session is attached as named, and pick is asked only for a
// connection that named none.
func TestAttachPicksOnlyWhenUnnamed(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Setenv("SHELL", "/bin/sh")

	d := session.NewDaemon(&session.DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(d.Stop)

	picked := 0
	pick := func([]string) string { picked++; return "picked" }
	opts := app.OSOptions{Client: app.ClientBrowser, Width: 80, Height: 24, Caps: &app.HostCapabilities{TrueColor: true}}

	named := opts
	named.SessionName = "named"
	m, err := Attach(named, config.Overrides{}, "test", nil, pick)
	if err != nil {
		t.Fatalf("attach a named session: %v", err)
	}
	t.Cleanup(m.Cleanup)
	if picked != 0 {
		t.Errorf("pick was asked for a connection that named its session")
	}
	if !m.IsDaemonSession || m.DaemonClient == nil || m.SessionName != "named" {
		t.Errorf("the model is not attached to the named session: daemon=%v client=%v name=%q",
			m.IsDaemonSession, m.DaemonClient != nil, m.SessionName)
	}

	m2, err := Attach(opts, config.Overrides{}, "test", nil, pick)
	if err != nil {
		t.Fatalf("attach an unnamed session: %v", err)
	}
	t.Cleanup(m2.Cleanup)
	if picked != 1 || m2.SessionName != "picked" {
		t.Errorf("an unnamed connection got %q after %d picks, want the picked session after one", m2.SessionName, picked)
	}
}

// TestAttachWithNoDaemonFails is what lets a server fall back to an ephemeral
// session: the error comes back, and nothing is left open.
func TestAttachWithNoDaemonFails(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	pick := func([]string) string { t.Error("pick ran with no daemon to list sessions"); return "" }
	if _, err := Attach(app.OSOptions{Width: 80, Height: 24}, config.Overrides{}, "test", nil, pick); err == nil {
		t.Fatal("Attach succeeded with no daemon running")
	}
}
