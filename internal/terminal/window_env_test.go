package terminal

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// detectTermWithStdout runs the pane TERM detection afresh with stdout
// replaced, and puts the cache and stdout back afterwards.
func detectTermWithStdout(t *testing.T, stdout *os.File) (termType, colorTerm string) {
	t.Helper()
	saved := os.Stdout
	localEnvOnce = sync.Once{}
	t.Cleanup(func() {
		os.Stdout = saved
		localEnvOnce = sync.Once{}
		localTermType, localColorTerm = "", ""
	})
	os.Stdout = stdout
	termType, colorTerm = getTerminalEnv()
	os.Stdout = saved
	return termType, colorTerm
}

// TestHeadlessServerPanesAreNotDumb is the SSH server under a service manager,
// nohup or a log file. Its stdout is no terminal, and detection answered
// TERM=dumb with no COLORTERM for every ephemeral pane, although each is drawn
// on a client's real terminal.
func TestHeadlessServerPanesAreNotDumb(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "")
	t.Cleanup(func() { headlessGuestTerm.Store(nil) })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

	if got, _ := detectTermWithStdout(t, w); got != "dumb" {
		t.Fatalf("with no default set, a non-terminal stdout gave TERM=%q; the premise of this test is dumb", got)
	}

	SetHeadlessGuestTerm("xterm-256color", "truecolor")
	termType, colorTerm := detectTermWithStdout(t, w)
	if termType != "xterm-256color" || colorTerm != "truecolor" {
		t.Errorf("a headless server's pane got TERM=%q COLORTERM=%q, want xterm-256color and truecolor", termType, colorTerm)
	}
}

// TestHeadlessGuestTermLeavesATrustedEnvironmentAlone keeps the default out of
// the way of an environment that already says what it is, which is how
// tuios-web pins its panes.
func TestHeadlessGuestTermLeavesATrustedEnvironmentAlone(t *testing.T) {
	t.Setenv("TERM", "screen-256color")
	t.Setenv("COLORTERM", "truecolor")
	t.Cleanup(func() { headlessGuestTerm.Store(nil) })
	SetHeadlessGuestTerm("xterm-256color", "truecolor")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

	if termType, _ := detectTermWithStdout(t, w); termType != "screen-256color" {
		t.Errorf("TERM=%q, want the environment's screen-256color", termType)
	}
}

// A locally spawned shell must advertise the graphics protocols tuios can
// forward to the host terminal. Hardcoding TERM_PROGRAM=TUIOS meant image
// tools inside a window fell back to block art even when tuios was passing
// kitty graphics straight through to a capable terminal.
func TestGuestTermProgramFollowsGraphicsCapabilities(t *testing.T) {
	t.Cleanup(func() { SetGraphicsCapabilities(false, false, false) })

	tests := []struct {
		name  string
		kitty bool
		sixel bool
		want  string
	}{
		{name: "no passthrough", want: "TUIOS"},
		{name: "kitty passthrough", kitty: true, want: "ghostty"},
		{name: "sixel passthrough", sixel: true, want: "WezTerm"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			SetGraphicsCapabilities(tc.kitty, tc.sixel, false)
			if got := guestTermProgram(); got != tc.want {
				t.Errorf("guestTermProgram() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGuestBaseEnvDropsHostTmux covers tuios started from inside tmux: a
// standalone pane must not inherit the variables that make it read as a tmux
// pane.
func TestGuestBaseEnvDropsHostTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("TUIOS_TEST_KEEP", "1")
	kept := false
	for _, kv := range guestBaseEnv() {
		switch {
		case strings.HasPrefix(kv, "TMUX="), strings.HasPrefix(kv, "TMUX_PANE="):
			t.Errorf("standalone pane environment carries %q from the enclosing tmux", kv)
		case kv == "TUIOS_TEST_KEEP=1":
			kept = true
		}
	}
	if !kept {
		t.Error("the rest of the environment was not passed through")
	}
}

// TestGuestKittyAnimation pins what a pane is told about frame edits.
//
// A guest cannot work this out for itself: tuios does not relay the host's
// answer to an a=f back into the pane, and TERM and KITTY_WINDOW_ID are
// inherited straight through, so they name the host terminal rather than the
// pane. wlterm reads this variable to decide whether its cheap transport is
// safe, so a wrong "1" here is a frozen picture in somebody's pane.
func TestGuestKittyAnimation(t *testing.T) {
	t.Cleanup(func() { SetGraphicsCapabilities(false, false, false) })
	for _, tc := range []struct {
		name      string
		kitty     bool
		animation bool
		want      string
	}{
		{name: "nothing forwarded", want: "TUIOS_KITTY_ANIMATION=0"},
		{name: "graphics but no frame edits", kitty: true, want: "TUIOS_KITTY_ANIMATION=0"},
		{name: "frame edits without graphics", animation: true, want: "TUIOS_KITTY_ANIMATION=0"},
		{name: "both", kitty: true, animation: true, want: "TUIOS_KITTY_ANIMATION=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetGraphicsCapabilities(tc.kitty, false, tc.animation)
			if got := guestKittyAnimation(); got != tc.want {
				t.Errorf("guestKittyAnimation() = %q, want %q", got, tc.want)
			}
		})
	}
}
