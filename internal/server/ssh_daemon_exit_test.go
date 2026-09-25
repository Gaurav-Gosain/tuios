package server

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
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
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
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
