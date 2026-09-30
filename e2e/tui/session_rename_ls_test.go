package tuie2e

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSessionPickerRenameReachesLs is issue #266 as it was reported: open the
// session picker, press ctrl+r on the attached session, type a new name, save,
// and run `tuios ls`. The picker showed the new name and ls kept the old one,
// because the rename only set a display label and left the session's name
// alone.
//
// How this could pass wrongly: the rename might land as a label that ls then
// prints beside the old name, so the old name is required to be gone from the
// names ls lists, not only the new one to appear. The old name must still
// reach the session for a process that was started with it (a pane's
// TUIOS_SESSION cannot change), and `tuios attach` by the old name must say
// what the session is called now instead of a bare "not found".
//
// Negative control: with CommitRename sending set-session-name again, ls
// never lists "work" and this fails at the first ls check.
func TestSessionPickerRenameReachesLs(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "test", "--detach"); err != nil {
		t.Fatalf("create test: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "test"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	if err := term.SendKeys(tuitest.Ctrl('b'), "S"); err != nil {
		t.Fatalf("open the session picker: %v", err)
	}
	if err := term.WaitForText("Sessions", uiTimeout); err != nil {
		t.Fatalf("the session picker did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('r')); err != nil {
		t.Fatalf("start the rename: %v", err)
	}
	if err := term.WaitForText("rename session test", uiTimeout); err != nil {
		t.Fatalf("the rename editor did not open on test: %v\n%s", err, term.Snapshot())
	}
	// The editor may be seeded with the current name. Clear it either way.
	if err := term.SendKeys(strings.Repeat("\x7f", 8)); err != nil {
		t.Fatalf("clear the editor: %v", err)
	}
	if err := term.SendKeys("work"); err != nil {
		t.Fatalf("type the new name: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("commit the rename: %v", err)
	}

	var names []string
	deadline := time.Now().Add(uiTimeout)
	for {
		names = sessionNames(t, base)
		if slices.Equal(names, []string{"work"}) || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !slices.Equal(names, []string{"work"}) {
		t.Fatalf("tuios ls lists %v after renaming test to work in the picker, want [work]\n%s", names, term.Snapshot())
	}

	// A process started in the session before the rename still has
	// TUIOS_SESSION=test, and a tuios command it runs must still find it.
	out, err := tuiosCLIEnv(t, base, []string{"TUIOS_SESSION=test"}, "list-windows", "-s", "test")
	if err != nil {
		t.Errorf("a command addressed by the old name lost the session: %v\n%s", err, out)
	}

	// attach by the old name refuses, and says what the session is called now.
	var raw syncBuffer
	old := startIn(t, base, startOpts{args: []string{"attach", "test"}, out: &raw})
	if _, err := old.WaitExit(15 * time.Second); err != nil {
		t.Fatalf("attach by the old name did not exit: %v\n%s", err, old.Snapshot())
	}
	if msg := raw.String(); !strings.Contains(msg, "work") {
		t.Errorf("attach by the old name did not name the new one:\n%s", msg)
	}

	// The attached client shows the name the daemon holds. The picker is
	// still open, and its row for the attached session reads the new name
	// once the daemon's push lands.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "work") && !strings.Contains(text, "test")
	}, uiTimeout); err != nil {
		t.Fatalf("the client does not show the daemon's new name: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after renaming the attached session")
}
