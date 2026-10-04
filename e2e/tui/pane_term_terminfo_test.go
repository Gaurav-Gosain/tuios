package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestPaneTermNeedsATerminfoEntry creates a session from a client whose TERM
// names a terminal, and reads the TERM the pane's shell was given and what
// tput makes of it.
//
// The client's TERM went into every pane unchecked. A person in kitty or
// ghostty attaching to a machine with no xterm-kitty or xterm-ghostty entry
// got panes where clear, tput and every curses program failed with "unknown
// terminal", for the life of the session. A pane now keeps the client's TERM
// only when the daemon's machine has an entry for it, and gets xterm-256color
// otherwise.
//
// How this could pass wrongly, written down first:
//   - The fallback could be applied to every TERM, which passes the absent
//     case without any lookup. The host case is the positive half: an entry
//     the machine has, other than xterm-256color, must come through as it is.
//   - The lookup could find only the system directories. The home case
//     compiles an entry no system has into the test's own ~/.terminfo, and it
//     must come through.
//   - Detection could never see the client's TERM, so the pane gets the
//     daemon default whatever the client said. The client is given
//     COLORTERM=truecolor, which detection trusts with its TERM as they are,
//     and the positive halves prove that TERM reaches the pane.
//   - The line read could be the shell's echo of the typed command. The shell
//     prints through a format, so only its output has a line without %s.
//   - tput could fail for a reason other than the entry, which would make the
//     absent case pass on its TERM alone. tput must print a number in every
//     case, the positive halves included.
//
// The pane's text is saved under artifactDir.
func TestPaneTermNeedsATerminfoEntry(t *testing.T) {
	if _, err := exec.LookPath("tput"); err != nil {
		t.Skip("tput is not installed, so the pane cannot show its terminfo working")
	}

	t.Run("absent", func(t *testing.T) {
		base := t.TempDir()
		checkPaneTerm(t, base, "tuios-e2e-no-such-term", "xterm-256color")
	})

	t.Run("host", func(t *testing.T) {
		name := ""
		for _, candidate := range []string{"screen-256color", "xterm", "vt220", "vt100"} {
			if exec.Command("infocmp", candidate).Run() == nil {
				name = candidate
				break
			}
		}
		if name == "" {
			t.Skip("infocmp finds none of the usual entries on this machine")
		}
		base := t.TempDir()
		checkPaneTerm(t, base, name, name)
	})

	t.Run("home", func(t *testing.T) {
		src, err := exec.Command("infocmp", "-x", "xterm-256color").Output()
		if err != nil {
			t.Skipf("infocmp cannot print xterm-256color here: %v", err)
		}
		if _, err := exec.LookPath("tic"); err != nil {
			t.Skip("tic is not installed, so no entry can be compiled into the home")
		}
		// The entry's name line is the first line that is not a comment. It
		// is replaced, so the compiled file has a name no system ships.
		const name = "tuios-e2e-home-term"
		lines := strings.Split(string(src), "\n")
		for i, line := range lines {
			if line != "" && !strings.HasPrefix(line, "#") {
				lines[i] = name + "|an entry only the test home has,"
				break
			}
		}
		base := t.TempDir()
		home := xdgDir(base, "HOME")
		cmd := exec.Command("tic", "-x", "-o", filepath.Join(home, ".terminfo"), "-")
		cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile the home entry: %v\n%s", err, out)
		}
		checkPaneTerm(t, base, name, name)
	})
}

// paneTermLine is the line the pane's shell prints, read off the capture.
var paneTermLine = regexp.MustCompile(`TERM=(\S+) COLS=(.*) END`)

// checkPaneTerm makes a session under base from a client whose TERM is
// clientTerm, and fails unless the pane's TERM is want and tput reads a
// width from it.
func checkPaneTerm(t *testing.T, base, clientTerm, want string) {
	t.Helper()
	const session = "e2e-pane-term"
	killDaemon(t, base)
	clientEnv := []string{"TERM=" + clientTerm, "COLORTERM=truecolor"}
	if out, err := tuiosCLIEnv(t, base, clientEnv, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, base, clientEnv, "send-keys", "-s", session, "-l",
		`printf 'TERM=%s COLS=%s END\n' "$TERM" "$(tput cols 2>/dev/null || echo failed)"`+"\r"); err != nil {
		t.Fatalf("ask the shell for its TERM: %v\n%s", err, out)
	}

	var pane string
	var got []string
	deadline := time.Now().Add(shellTimeout)
	for got == nil {
		pane, _ = tuiosCLIEnv(t, base, clientEnv, "capture-pane", "-s", session)
		for _, line := range strings.Split(pane, "\n") {
			if line = strings.TrimSpace(line); !strings.Contains(line, "%s") {
				if m := paneTermLine.FindStringSubmatch(line); m != nil {
					got = m
				}
			}
		}
		if got == nil {
			if time.Now().After(deadline) {
				t.Fatalf("the shell never printed its TERM:\n%s", pane)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "pane.txt"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}

	if got[1] != want {
		t.Errorf("a client with TERM=%s gave its pane TERM=%s, want %s\n%s", clientTerm, got[1], want, pane)
	}
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(got[2]) {
		t.Errorf("tput in the pane could not read its terminal: %q\n%s", got[2], pane)
	}
}
