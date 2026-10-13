package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNewSpaceGivesItsPaneTheCallersTerm proves the hello verb's term and
// color_term fields, which a JSON verb connection (tuios call, and so new
// --space) sends and the daemon stores on cs.hello exactly as the binary
// protocol's MsgHello does. Before this, cs.hello was always nil on that
// connection, so every session a verb created -- new-session (and so new
// --space), switch-session's create -- inherited the daemon's own terminal
// defaults rather than the caller's, even when the caller had a real one.
//
// TestSessionMadeWithoutATerminalGivesItsPanesARealTerm (pane_term_headless_
// test.go) covers the other half: a caller with no terminal of its own still
// gets a real one, from the daemon's own fallback. This test is the caller
// that does have one, and should keep it.
//
// How this could pass wrongly, written down first:
//   - The environment could already hold the daemon's own fallback
//     (xterm-256color, no COLORTERM), which would equal a real caller's TERM
//     by coincidence. The test's TERM (xterm-ghostty) differs from every
//     fallback this daemon has, so a match proves the real value, not luck.
//   - A daemon started earlier with another environment could answer. The
//     test has its own base, and the first command it runs starts the daemon.
//   - The line read could be the shell's echo of the typed command rather
//     than its output. The shell prints the value through a format, so only
//     its output has a TERM= line without %s in it.
//
// Negative control: with cs.hello left nil by the hello verb (either the
// client not sending term/color_term, or the handler not storing them), this
// fails with the daemon's fallback, xterm-256color and no COLORTERM, instead
// of xterm-ghostty and truecolor.
func TestNewSpaceGivesItsPaneTheCallersTerm(t *testing.T) {
	const session = "e2e-term-space"
	base := t.TempDir()
	killDaemon(t, base)
	callerEnv := []string{"TERM=xterm-ghostty", "COLORTERM=truecolor"}

	if out, err := tuiosCLIEnv(t, base, callerEnv, "new", session, "--space", "term-check", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, base, callerEnv, "send-keys", "-s", session, "-l",
		`printf 'TERM=%s COLORTERM=%s END\n' "$TERM" "$COLORTERM"`+"\r"); err != nil {
		t.Fatalf("ask the shell for its TERM: %v\n%s", err, out)
	}

	var pane, got string
	deadline := time.Now().Add(shellTimeout)
	for got == "" {
		pane, _ = tuiosCLIEnv(t, base, callerEnv, "capture-pane", "-s", session)
		for _, line := range strings.Split(pane, "\n") {
			if line = strings.TrimSpace(line); strings.HasPrefix(line, "TERM=") && strings.HasSuffix(line, " END") &&
				!strings.Contains(line, "%s") {
				got = strings.TrimSuffix(line, " END")
			}
		}
		if got == "" {
			if time.Now().After(deadline) {
				t.Fatalf("the shell never printed its TERM:\n%s", pane)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "pane.txt"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}

	if want := "TERM=xterm-ghostty COLORTERM=truecolor"; got != want {
		t.Errorf("a session made by new --space, from a caller with a real terminal, gave its pane %q, want %q: "+
			"the hello verb's term and color_term did not reach the session\n%s", got, want, pane)
	}
}
