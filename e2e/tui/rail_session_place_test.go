package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRailLabelsAnUnnamedSessionByItsDirectory drives the whole path on a real
// daemon and client: a session nobody named is created, its shell reports a
// directory over OSC 7, and the rail labels the session with that directory
// and the branch checked out there. Leaving the checkout drops the branch and
// keeps the directory. The checkout is a layout written by hand in the test's
// own temp directory, so the test needs no git and touches no real repository.
func TestRailLabelsAnUnnamedSessionByItsDirectory(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "labelrepo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/feature-x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(base, "plaindir")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	// A generated name, exactly as `tuios new` with no argument hands out.
	if out, err := tuiosCLI(t, base, "new", "session-0", "--detach"); err != nil {
		t.Fatalf("create session-0: %v: %s", err, out)
	}
	term := attachIn(t, base, "session-0", startOpts{})

	toggleSidebarViaPalette(t, term)
	if err := term.WaitForText(sidebarHeader, uiTimeout); err != nil {
		t.Fatalf("sidebar did not open: %v\n%s", err, term.Snapshot())
	}

	// The shell reports where it is the way fish does on every cd. /bin/sh
	// does not, so the report is printed by hand: the daemon reads the bytes
	// either way.
	report := func(dir string) {
		t.Helper()
		enterTerminalMode(t, term)
		cmd := "cd " + dir + " && printf '\\033]7;file://localhost%s\\007' \"$PWD\" && echo MOVED-" + filepath.Base(dir)
		if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
			t.Fatalf("type %q: %v", cmd, err)
		}
		if err := term.WaitForText("MOVED-"+filepath.Base(dir), uiTimeout); err != nil {
			t.Fatalf("the shell never moved to %s: %v\n%s", dir, err, term.Snapshot())
		}
		leaveTerminalMode(t, term)
	}

	// railHas reports whether one line of the frame carries every marker.
	railHas := func(markers ...string) func(tuitest.Screen) bool {
		return func(s tuitest.Screen) bool {
			for _, line := range strings.Split(s.Text(), "\n") {
				ok := true
				for _, m := range markers {
					if !strings.Contains(line, m) {
						ok = false
						break
					}
				}
				if ok {
					return true
				}
			}
			return false
		}
	}

	report(repo)
	// The listing behind the rail refreshes every few seconds while the rail is
	// open, so the label lands within that, not on the keystroke.
	if err := term.WaitFor(railHas("labelrepo", "feature-x"), 15*time.Second); err != nil {
		t.Fatalf("the rail never labelled the session by its checkout: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Snapshot(), "session-0") {
		t.Fatalf("the generated name is still on screen next to the label:\n%s", term.Snapshot())
	}
	t.Logf("labelled by a checkout:\n%s", term.Snapshot())

	report(plain)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railHas("plaindir")(s) && !strings.Contains(s.Text(), "feature-x")
	}, 15*time.Second); err != nil {
		t.Fatalf("leaving the checkout did not relabel the session: %v\n%s", err, term.Snapshot())
	}
	t.Logf("labelled by a plain directory:\n%s", term.Snapshot())
}
