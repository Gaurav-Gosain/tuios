package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSessionMadeWithoutATerminalGivesItsPanesARealTerm creates a session from
// a command with no terminal of its own, the way a script, a service, CI or an
// agent does, and reads the TERM its pane's shell was given.
//
// The command's hello to the daemon carried the TERM detected from its own
// stdout. With no terminal there, and no TERM and COLORTERM=truecolor in its
// environment to trust instead, detection answers dumb, and every pane of the
// session kept TERM=dumb for its life: clear did nothing, and less and vim ran
// without cursor movement, even after a person attached from a real terminal.
// The panes are drawn by tuios's emulator, not by the command's stdout, and a
// session made by attaching or by the new-session verb already got the daemon's
// xterm-256color. GitHub's runners set TERM=dumb, which is how
// TestScreenshotDrawsAStraightBorderWithoutNotches found it: its clear left the
// typed command on the first row.
//
// How this could pass wrongly, written down first:
//   - The environment could hold a TERM and COLORTERM=truecolor that detection
//     trusts as they are, so the no-terminal path never runs. The command is
//     given TERM=dumb and an empty COLORTERM, which is what a runner has.
//   - A daemon started earlier with another environment could answer. The
//     test has its own base, and the first command it runs starts the daemon.
//   - The line read could be the shell's echo of the typed command. The shell
//     prints the value through a format, so only its output has a TERM= line
//     without %s in it.
//   - The line could be read while the shell's output is still arriving, cut
//     after TERM. The output ends in a marker, and only a line ending in it
//     is read.
//
// The pane's text is saved under artifactDir.
func TestSessionMadeWithoutATerminalGivesItsPanesARealTerm(t *testing.T) {
	const session = "e2e-headless-term"
	base := t.TempDir()
	killDaemon(t, base)
	scriptEnv := []string{"TERM=dumb", "COLORTERM="}
	if out, err := tuiosCLIEnv(t, base, scriptEnv, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, base, scriptEnv, "send-keys", "-s", session, "-l",
		`printf 'TERM=%s COLORTERM=%s END\n' "$TERM" "$COLORTERM"`+"\r"); err != nil {
		t.Fatalf("ask the shell for its TERM: %v\n%s", err, out)
	}

	var pane, got string
	deadline := time.Now().Add(shellTimeout)
	for got == "" {
		pane, _ = tuiosCLIEnv(t, base, scriptEnv, "capture-pane", "-s", session)
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

	if want := "TERM=xterm-256color COLORTERM=truecolor"; got != want {
		t.Errorf("a session made by a command with no terminal gave its pane %q, want %q: "+
			"the pane is drawn by tuios, not by the command's stdout\n%s", got, want, pane)
	}
}
