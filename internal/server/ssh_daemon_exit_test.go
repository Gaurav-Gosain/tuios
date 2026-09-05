package server

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// daemonExitFixture is a running daemon, an SSH-style client attached to a
// session on it, and that client's model with the SSH host's handlers wired in.
type daemonExitFixture struct {
	daemon *session.Daemon
	client *session.TUIClient
	model  *app.OS
	name   string
}

func newDaemonExitFixture(t *testing.T, name string) *daemonExitFixture {
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
		IsSSHMode:       true,
		RemoteClient:    true,
		Width:           80,
		Height:          24,
	})
	m.WireDaemonClient(client)
	client.StartReadLoop()

	return &daemonExitFixture{daemon: d, client: client, model: m, name: name}
}

// awaitExit runs the command Init arms for the daemon's terminal events and
// returns the message it delivers. It fails rather than blocking for ever, which
// is the symptom this whole test exists for: with no handler registered nothing
// is ever queued and the client renders its last frame until someone closes the
// window.
func (f *daemonExitFixture) awaitExit(t *testing.T) tea.Msg {
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
		t.Fatalf("the client was never told the session or the daemon went away: it would render its last frame for ever")
		return nil
	}
}

// quitsWith feeds msg to the model and checks that it asks to stop, records why,
// and has a message for the user. Anything less is the frozen client.
func quitsWith(t *testing.T, m *app.OS, msg tea.Msg, want app.ExitReason) string {
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

// TestSSHClientStopsWhenItsSessionIsKilled kills the attached session from
// another client and checks that the SSH client hears about it and stops.
func TestSSHClientStopsWhenItsSessionIsKilled(t *testing.T) {
	f := newDaemonExitFixture(t, "sshkilled")

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
	if ended.SessionName != f.name {
		t.Fatalf("session named %q, want %q", ended.SessionName, f.name)
	}
	notice := quitsWith(t, f.model, ended, app.ExitSessionKilled)
	if !containsAll(notice, f.name, "Connect again") {
		t.Fatalf("the exit message does not name the session and what to do next: %q", notice)
	}
}

// TestSSHClientStopsWhenTheDaemonGoesAway stops the daemon under an attached SSH
// client and checks that the client hears about it and stops.
func TestSSHClientStopsWhenTheDaemonGoesAway(t *testing.T) {
	f := newDaemonExitFixture(t, "sshlost")

	f.daemon.Stop()

	msg := f.awaitExit(t)
	if _, ok := msg.(app.DaemonDisconnectedMsg); !ok {
		t.Fatalf("got %T, want app.DaemonDisconnectedMsg", msg)
	}
	notice := quitsWith(t, f.model, msg, app.ExitDaemonLost)
	if !containsAll(notice, "daemon", "Connect again") {
		t.Fatalf("the exit message does not say what happened and what to do next: %q", notice)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
