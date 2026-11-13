package tuie2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// A [[keybindings.command]] entry with a scope runs inside a leader submenu:
// ctrl+b m alt+j. A shell command writes a file, so the assertion is that the
// third press ran it, not merely that a row showed up in a listing.
//
// How this could pass wrongly, written down first:
//   - Wired into the leader's own menu, the command would run on two presses.
//     ctrl+b alt+j is sent first and must not make the file.
//   - Wired globally, a bare alt+j would run it. That is sent too.
//   - Neither press may be the reason the file appears later; both are
//     checked before the real chord.
//   - The three-press chord is the positive half, and the old prefix+ form is
//     the positive half for the wiring this change must not break.

func waitForSubmenuFile(t *testing.T, path, want, what string) {
	t.Helper()
	waitUntil(t, what+" writes "+strconv.Quote(want)+" to "+path, uiTimeout, func() (bool, string) {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, "read: " + err.Error()
		}
		return strings.Contains(string(data), want), "read " + strconv.Quote(string(data))
	})
}

func TestCommandKeyInsideASubmenu(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "submenu-ran")
	leaderOut := filepath.Join(base, "leader-ran")
	writeConfig(t, base, `
[[keybindings.command]]
scope = "prefix.minimize"
key = "alt+j"
type = "shell"
command = "echo SUBMENU > `+out+`"

[[keybindings.command]]
key = "prefix+alt+y"
type = "shell"
command = "echo LEADER > `+leaderOut+`"
`)
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "work"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")

	// The two shorter chords are the negative halves. alt+j is a preselect
	// motion in window mode, so neither press has a side effect that could
	// stand in for the command.
	sendKeys(t, term, tuitest.Ctrl('b'), tuitest.Alt('j'))
	sendKeys(t, term, tuitest.Alt('j'))
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		data, _ := os.ReadFile(out)
		t.Fatalf("the submenu command ran before its chord: stat = %v, file = %q", err, data)
	}

	// The positive half: leader, minimize menu, then the command's key.
	sendKeys(t, term, tuitest.Ctrl('b'), "m", tuitest.Alt('j'))
	waitForSubmenuFile(t, out, "SUBMENU", "the submenu command")

	// The form that shipped before scope still works: the positive control
	// for a change that must not take the old wiring away.
	pressCommand(t, term, 'y')
	waitForSubmenuFile(t, leaderOut, "LEADER", "the leader command")

	alive(t, term, "after the submenu command key")
}
