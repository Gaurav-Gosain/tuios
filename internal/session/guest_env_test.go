package session

import (
	"slices"
	"testing"
)

// Guest processes pick their image protocol from the environment, so a shell
// spawned by the daemon has to be told what the attached client's terminal can
// actually display. The daemon's own environment says nothing useful: it is
// detached from any terminal, so every window used to advertise TERM_PROGRAM=
// TUIOS and tools like chafa fell back to block art even under kitty.

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range slices.Backward(env) {
		if len(kv) > len(prefix) && kv[:len(prefix)] == prefix {
			return kv[len(prefix):]
		}
	}
	return ""
}

func TestBuildEnvAdvertisesGraphicsCapabilities(t *testing.T) {
	tests := []struct {
		name  string
		kitty bool
		sixel bool
		want  string
	}{
		{name: "no client graphics", want: "TUIOS"},
		{name: "kitty client", kitty: true, want: "ghostty"},
		{name: "sixel client", sixel: true, want: "WezTerm"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sess, err := NewSession("env", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			sess.SetGraphicsCapabilities(tc.kitty, tc.sixel)

			env := sess.buildEnv("win-1", false)
			if got := envValue(env, "TERM_PROGRAM"); got != tc.want {
				t.Errorf("TERM_PROGRAM = %q, want %q", got, tc.want)
			}
			// TERM must stay untouched: claiming xterm-kitty would need a
			// terminfo entry the host may not have installed.
			if got := envValue(env, "TERM"); got != "xterm-256color" {
				t.Errorf("TERM = %q, want xterm-256color", got)
			}
			if got := envValue(env, "TUIOS_WINDOW_ID"); got != "win-1" {
				t.Errorf("TUIOS_WINDOW_ID = %q, want win-1", got)
			}
		})
	}
}

// TestAttachRecordsClientGraphicsCapabilities covers the plumbing end to end:
// the capabilities a client detects at startup must survive the hello/attach
// handshake and reach the session that spawns its shells.
func TestAttachRecordsClientGraphicsCapabilities(t *testing.T) {
	d, _ := startTestDaemon(t)

	sess, err := d.manager.CreateSession("graphics", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if kitty, sixel := sess.GraphicsCapabilities(); kitty || sixel {
		t.Fatalf("fresh session reports kitty=%v sixel=%v, want both false", kitty, sixel)
	}

	c := NewTUIClient()
	caps := &ClientCapabilities{KittyGraphics: true, TerminalName: "kitty"}
	if err := c.ConnectWithCapabilities("test", 80, 24, caps); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.AttachSession("graphics", false, 80, 24); err != nil {
		t.Fatalf("attach: %v", err)
	}

	kitty, sixel := sess.GraphicsCapabilities()
	if !kitty {
		t.Error("attach did not record the client's kitty graphics support; guest shells would advertise TERM_PROGRAM=TUIOS and fall back to block art")
	}
	if sixel {
		t.Error("attach recorded sixel support the client never claimed")
	}

	if got := envValue(sess.buildEnv("", false), "TERM_PROGRAM"); got != "ghostty" {
		t.Errorf("TERM_PROGRAM = %q, want ghostty", got)
	}
}
