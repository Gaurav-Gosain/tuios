package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// [[keybindings.command]] entries, pressed on a real client of a daemon
// session: two scratch entries, a popup, a pane and a shell command.
//
// How these could pass wrongly, written down first:
//   - Text on screen could be from the other scratch. Each marker is printed
//     by its own shell, and the other scratch's marker must be gone.
//   - A scratch could be made again on each show. The window id must stay.
//   - The popup could stay open. It must leave list-windows when its
//     command exits.
//   - The shell command could run in a pane. The window count must not move.

// commandRows is list-windows --json for session work.
type commandRow struct {
	ID          string `json:"window_id"`
	Name        string `json:"display_name"`
	Minimized   bool   `json:"minimized"`
	Scratch     bool   `json:"scratch"`
	ScratchName string `json:"scratch_name"`
}

func commandRows(t *testing.T, base string) []commandRow {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", "work")
	if err != nil {
		return nil
	}
	var res struct {
		Windows []commandRow `json:"windows"`
	}
	_ = json.Unmarshal([]byte(out), &res)
	return res.Windows
}

// waitRow waits until a row matches, and returns it.
func waitRow(t *testing.T, term *tuitest.Terminal, base, what string, ok func(commandRow) bool) commandRow {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		for _, r := range commandRows(t, base) {
			if ok(r) {
				return r
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: no window matched\n%v\n%s", what, commandRows(t, base), term.Snapshot())
	return commandRow{}
}

// scratchNamed matches the scratch pane of name, shown or hidden.
func scratchNamed(name string, hidden bool) func(commandRow) bool {
	return func(r commandRow) bool { return r.Scratch && r.ScratchName == name && r.Minimized == hidden }
}

// pressCommand presses the leader and alt+k.
func pressCommand(t *testing.T, term *tuitest.Terminal, k rune) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), tuitest.Alt(k)); err != nil {
		t.Fatalf("send leader alt+%c: %v", k, err)
	}
}

func TestCommandKeybindings(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "shell-ran")
	writeConfig(t, base, `
[[keybindings.command]]
key = "prefix+alt+y"
type = "scratch"
name = "first"

[[keybindings.command]]
key = "prefix+alt+u"
type = "scratch"
command = "echo SECOND; exec sh"
description = "Second"

[[keybindings.command]]
key = "prefix+alt+o"
type = "popup"
command = "echo POPUP-UP; sleep 2"

[[keybindings.command]]
key = "prefix+alt+n"
type = "pane"
command = "echo PANE-$((4*4)); exec sh"
description = "Pane job"

[[keybindings.command]]
key = "prefix+alt+j"
type = "shell"
command = "echo \"$TUIOS_SESSION $TUIOS_ACTIVE_PANE_ID\" > `+out+`"
`)
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "work"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")

	// The first scratch: the user's shell.
	pressCommand(t, term, 'y')
	first := waitRow(t, term, base, "first shown", scratchNamed("first", false))
	typeUntil(t, term, "echo ONE-$((6*7))", "ONE-42")

	// The second hides the first and shows its own command.
	pressCommand(t, term, 'u')
	second := waitRow(t, term, base, "second shown", scratchNamed("second", false))
	if err := term.WaitForText("SECOND", uiTimeout); err != nil {
		t.Fatalf("the second scratch did not run its command: %v\n%s", err, term.Snapshot())
	}
	waitRow(t, term, base, "first hidden by second", scratchNamed("first", true))
	waitGone(t, term, "the first scratch behind the second", "ONE-42")
	typeUntil(t, term, "echo TWO-$((7*8))", "TWO-56")
	t.Logf("the second scratch over the layout:\n%s", term.Snapshot())

	// Back to the first: its text is kept, and the second's is gone.
	pressCommand(t, term, 'y')
	if err := term.WaitForText("ONE-42", uiTimeout); err != nil {
		t.Fatalf("the first scratch lost its text: %v\n%s", err, term.Snapshot())
	}
	waitGone(t, term, "the second scratch behind the first", "TWO-56")
	if r := waitRow(t, term, base, "first again", scratchNamed("first", false)); r.ID != first.ID {
		t.Fatalf("the first scratch was made again: %s, then %s", first.ID, r.ID)
	}
	pressCommand(t, term, 'u')
	if err := term.WaitForText("TWO-56", uiTimeout); err != nil {
		t.Fatalf("the second scratch lost its text: %v\n%s", err, term.Snapshot())
	}
	if r := waitRow(t, term, base, "second again", scratchNamed("second", false)); r.ID != second.ID {
		t.Fatalf("the second scratch was made again: %s, then %s", second.ID, r.ID)
	}
	pressCommand(t, term, 'u')
	waitRow(t, term, base, "second hidden", scratchNamed("second", true))

	// A popup closes when its command exits.
	pressCommand(t, term, 'o')
	if err := term.WaitForText("POPUP-UP", uiTimeout); err != nil {
		t.Fatalf("the popup did not open: %v\n%s", err, term.Snapshot())
	}
	t.Logf("the popup:\n%s", term.Snapshot())
	waitGone(t, term, "the popup after its command", "POPUP-UP")
	for _, r := range commandRows(t, base) {
		if strings.Contains(r.Name, "POPUP") {
			t.Fatalf("the popup is still listed: %+v", r)
		}
	}

	// A pane runs its command in the layout.
	pressCommand(t, term, 'n')
	waitRow(t, term, base, "the pane", func(r commandRow) bool { return r.Name == "Pane job" && !r.Scratch })
	if err := term.WaitForText("PANE-16", uiTimeout); err != nil {
		t.Fatalf("the pane did not run its command: %v\n%s", err, term.Snapshot())
	}
	t.Logf("the pane:\n%s", term.Snapshot())

	// A shell command runs with no window.
	before := len(commandRows(t, base))
	pressCommand(t, term, 'j')
	deadline := time.Now().Add(uiTimeout)
	var data []byte
	for time.Now().Before(deadline) {
		if data, _ = os.ReadFile(out); len(data) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "work ") || len(strings.Fields(string(data))) != 2 {
		t.Fatalf("the shell command wrote %q, want the session and the pane id", data)
	}
	time.Sleep(300 * time.Millisecond)
	if after := len(commandRows(t, base)); after != before {
		t.Fatalf("the shell command changed the windows from %d to %d", before, after)
	}
	alive(t, term, "after the command keys")
}

// A scratch entry whose command exits at once says so on the dock, with the
// exit code, and the next press tries again instead of waiting out a create
// that never arrives.
func TestScratchEntryThatStopsAtOnce(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[[keybindings.command]]\nkey = \"prefix+alt+y\"\ntype = \"scratch\"\ncommand = \"exit 7\"\ndescription = \"Broken\"\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "work"}})
	waitBoot(t, term)
	newWindow(t, term)
	// The dock counts its messages after "esc": "+2". A second report
	// raises it, which a press still blocked by the first create would not.
	count := func(s tuitest.Screen) int {
		m := regexp.MustCompile(`esc\s+\+(\d+)`).FindStringSubmatch(s.Text())
		if m == nil {
			return 0
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	last := 0
	for try := 1; try <= 2; try++ {
		pressCommand(t, term, 'y')
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(s.Text(), "stopped with exit code 7") && count(s) > last
		}, uiTimeout); err != nil {
			t.Fatalf("press %d: no new report (count %d): %v\n%s", try, count(term.Screen()), err, term.Snapshot())
		}
		last = count(term.Screen())
		t.Logf("press %d:\n%s", try, term.Snapshot())
	}
	alive(t, term, "after a scratch that stops at once")
}
