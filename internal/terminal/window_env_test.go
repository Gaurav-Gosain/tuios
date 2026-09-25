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
