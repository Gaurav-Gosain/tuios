package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestRailLabelsAnUnnamedSessionByItsDirectory drives the whole path on a real
// daemon and client: a session nobody named is created, its shell reports a
// directory over OSC 7, and the rail labels the session with that directory
// and the branch checked out there. Leaving the checkout drops the branch and
// keeps the directory. The checkout is a layout written by hand in the test's
// own temp directory, so the test needs no git and touches no real repository.
//
// Then an agent in the pane sets a task summary as its title, as Claude Code
// does, and the title takes the directory's place: it follows a new title, and
// the directory comes back when the agent ends. A session a person named keeps
// its name in the rail and in tuios ls while an agent there sets a title too.
//
// How the agent half could pass wrongly, written down first:
//   - The pane's own row already shows an agent's title, so finding the title
//     on screen proves nothing. The session row's label is the directory, so
//     the directory has to leave the screen while the title is on it.
//   - A rule that took any pane's title would pass on the agent pane. The
//     title is printed before the pane is marked an agent, and the rail is
//     read with no agent first, so a shell's title alone must not move it.
//   - A gate on the made-up name that let every session through would still
//     show the named session's agent title somewhere. Its row has to say its
//     own name, and tuios ls has to list it without the title.
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
	// A session a person named, with an agent in it that sets a title.
	if out, err := tuiosCLI(t, base, "new", "billing", "--detach"); err != nil {
		t.Fatalf("create billing: %v: %s", err, out)
	}
	setTitle(t, base, "billing", "Ship the invoices")
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "billing", "working", "--harness", "claude-code"); err != nil {
		t.Fatalf("mark the billing pane an agent: %v\n%s", err, out)
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
			for line := range strings.SplitSeq(s.Text(), "\n") {
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

	// sessionRow reports whether a row of the rail's sessions section, and not
	// the files section under it, which names the directory too, says want.
	sessionRow := func(want string) func(tuitest.Screen) bool {
		return func(s tuitest.Screen) bool {
			in := false
			for line := range strings.SplitSeq(s.Text(), "\n") {
				rail, _, _ := strings.Cut(line, "│")
				switch {
				case strings.HasPrefix(strings.TrimSpace(rail), sidebarHeader):
					in = true
				case !in:
				case strings.TrimSpace(rail) == "" || strings.HasPrefix(strings.TrimSpace(rail), "files"):
					return false
				case strings.Contains(rail, want):
					return true
				}
			}
			return false
		}
	}
	if !sessionRow("plaindir")(term.Screen()) {
		t.Fatalf("the sessions section does not show the directory:\n%s", term.Snapshot())
	}

	// A title from a pane that is not an agent's leaves the label alone.
	setTitle(t, base, "session-0", "Fix login redirect")
	waitForWindowTitle(t, base, "session-0", "Fix login redirect")
	if !sessionRow("plaindir")(term.Screen()) {
		t.Fatalf("a shell's title moved the label before any agent ran:\n%s", term.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "session-0", "working", "--harness", "claude-code"); err != nil {
		t.Fatalf("mark the pane an agent: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return sessionRow("Fix login redirect")(s) && !sessionRow("plaindir")(s)
	}, 15*time.Second); err != nil {
		t.Fatalf("the rail never labelled the session by its agent's title: %v\n%s", err, term.Snapshot())
	}
	// The named session keeps its name. The listing behind its row refreshes on
	// the same clock, so its row is waited for, not read once.
	if err := term.WaitFor(sessionRow("billing"), 15*time.Second); err != nil {
		t.Fatalf("the named session lost its name in the rail: %v\n%s", err, term.Snapshot())
	}
	for line := range strings.SplitSeq(term.Snapshot(), "\n") {
		if strings.Contains(line, "billing") && strings.Contains(line, "Ship the invoices") {
			t.Fatalf("the named session's row carries its agent's title: %q", line)
		}
	}
	// The frame waits for the agents section to name the pane by the same
	// title, which it adopts on its own clock, so the picture is settled.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		n := 0
		for line := range strings.SplitSeq(s.Text(), "\n") {
			if rail, _, _ := strings.Cut(line, "│"); strings.Contains(rail, "Fix login redirect") {
				n++
			}
		}
		return n >= 2
	}, 15*time.Second); err != nil {
		t.Fatalf("the agents section never named the pane by its title: %v\n%s", err, term.Snapshot())
	}
	t.Logf("labelled by the agent's title:\n%s", term.Snapshot())
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "rail-agent-title")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "rail-agent-title")

	out, err := tuiosCLI(t, base, "ls")
	if err != nil {
		t.Fatalf("tuios ls: %v\n%s", err, out)
	}
	if !strings.Contains(out, "session-0 (Fix login redirect)") {
		t.Errorf("tuios ls does not show the agent's title beside the made-up name:\n%s", out)
	}
	if strings.Contains(out, "Ship the invoices") {
		t.Errorf("tuios ls shows an agent's title for the named session:\n%s", out)
	}

	// The label follows a new title.
	setTitle(t, base, "session-0", "Add rate limits")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return sessionRow("Add rate limits")(s) && !sessionRow("Fix login redirect")(s)
	}, 15*time.Second); err != nil {
		t.Fatalf("the label did not follow the agent's new title: %v\n%s", err, term.Snapshot())
	}

	// The agent ends, and the directory comes back.
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "session-0", "none"); err != nil {
		t.Fatalf("end the agent: %v\n%s", err, out)
	}
	if err := term.WaitFor(sessionRow("plaindir"), 15*time.Second); err != nil {
		t.Fatalf("the directory did not come back when the agent ended: %v\n%s", err, term.Snapshot())
	}
}

// setTitle has the shell in the session's pane set its terminal title, with
// the escape Claude Code writes.
func setTitle(t *testing.T, base, session, title string) {
	t.Helper()
	cmd := "printf '\\033]2;" + title + "\\007'\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, cmd); err != nil {
		t.Fatalf("set the title in %s: %v\n%s", session, err, out)
	}
	waitForWindowTitle(t, base, session, title)
}

// waitForWindowTitle waits until the daemon lists the session's pane with the
// title.
func waitForWindowTitle(t *testing.T, base, session, title string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(t, base, "list-windows", "-s", session)
		if err == nil && strings.Contains(out, title) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane in %s never took the title %q: %v\n%s", session, title, err, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
