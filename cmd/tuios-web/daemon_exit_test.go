package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// webExitFixture is a running daemon, a browser-style client attached to a
// session on it, and that client's model with the web host's handlers wired in.
type webExitFixture struct {
	daemon *session.Daemon
	model  *app.OS
	name   string
}

func newWebExitFixture(t *testing.T, name string) *webExitFixture {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")

	d := session.NewDaemon(&session.DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(d.Stop)

	client := session.NewTUIClient()
	if err := client.Connect("test", 80, 24); err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.AttachSession(name, true, 80, 24); err != nil {
		t.Fatalf("client attach: %v", err)
	}

	m := app.NewOS(app.OSOptions{
		UserConfig:      config.DefaultConfig(),
		IsDaemonSession: true,
		DaemonClient:    client,
		SessionName:     client.SessionName(),
		BrowserClient:   true,
		RemoteClient:    true,
		Width:           80,
		Height:          24,
	})
	registerMultiClientHandlers(m, client)
	client.StartReadLoop()

	return &webExitFixture{daemon: d, model: m, name: name}
}

// awaitExit runs the command Init arms for the daemon's terminal events. It
// fails rather than blocking for ever, which is the symptom: with no handler
// registered nothing is queued and the browser tab holds its last frame.
func (f *webExitFixture) awaitExit(t *testing.T) tea.Msg {
	t.Helper()
	cmd := app.ListenForDaemonExit(f.model.DaemonExitChan)
	if cmd == nil {
		t.Fatalf("no listener for the daemon's terminal events")
	}
	got := make(chan tea.Msg, 1)
	go func() { got <- cmd() }()
	select {
	case msg := <-got:
		return msg
	case <-time.After(10 * time.Second):
		t.Fatalf("the browser client was never told the session or the daemon went away: it would hold its last frame for ever")
		return nil
	}
}

func webQuitsWith(t *testing.T, m *app.OS, msg tea.Msg, want app.ExitReason) string {
	t.Helper()
	_, cmd := m.Update(msg)
	if cmd == nil {
		t.Fatalf("%T did not stop the client", msg)
	}
	if out := cmd(); out != (tea.QuitMsg{}) {
		t.Fatalf("%T returned %T, want tea.QuitMsg", msg, out)
	}
	if m.ExitReason != want {
		t.Fatalf("exit reason %v, want %v", m.ExitReason, want)
	}
	notice := m.ExitNotice()
	if notice == "" {
		t.Fatalf("the client stops with nothing on screen to say why")
	}
	return notice
}

// TestWebClientStopsWhenItsSessionIsKilled kills the attached session from
// another client and checks that the browser client hears about it and stops.
func TestWebClientStopsWhenItsSessionIsKilled(t *testing.T) {
	f := newWebExitFixture(t, "webkilled")

	killer := session.NewTUIClient()
	if err := killer.Connect("test", 80, 24); err != nil {
		t.Fatalf("killer connect: %v", err)
	}
	defer func() { _ = killer.Close() }()
	if err := killer.KillSessionByName(f.name); err != nil {
		t.Fatalf("kill session: %v", err)
	}

	msg := f.awaitExit(t)
	ended, ok := msg.(app.SessionEndedMsg)
	if !ok {
		t.Fatalf("got %T, want app.SessionEndedMsg", msg)
	}
	notice := webQuitsWith(t, f.model, ended, app.ExitSessionKilled)
	if !strings.Contains(notice, f.name) || !strings.Contains(notice, "Connect again") {
		t.Fatalf("the exit message does not name the session and what to do next: %q", notice)
	}
}

// TestWebClientStopsWhenTheDaemonGoesAway stops the daemon under an attached
// browser client and checks that the client hears about it and stops.
func TestWebClientStopsWhenTheDaemonGoesAway(t *testing.T) {
	f := newWebExitFixture(t, "weblost")

	f.daemon.Stop()

	msg := f.awaitExit(t)
	if _, ok := msg.(app.DaemonDisconnectedMsg); !ok {
		t.Fatalf("got %T, want app.DaemonDisconnectedMsg", msg)
	}
	notice := webQuitsWith(t, f.model, msg, app.ExitDaemonLost)
	if !strings.Contains(notice, "daemon") || !strings.Contains(notice, "Connect again") {
		t.Fatalf("the exit message does not say what happened and what to do next: %q", notice)
	}
}
