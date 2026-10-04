package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestLinkOpensFromARehydratedPane: a second client attaching to a daemon
// session gets the pane from the daemon's snapshot, not from the guest. The
// snapshot has to carry the OSC 8 target with the cells, or the label reaches
// the new client as plain text and the click opens nothing.
func TestLinkOpensFromARehydratedPane(t *testing.T) {
	base := t.TempDir()
	record := filepath.Join(base, "opened.txt")
	opener := filepath.Join(base, "opener.sh")
	if err := os.WriteFile(opener, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+record+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(base, "links.sh")
	if err := os.WriteFile(script, []byte(linkScriptBody("x")), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, fmt.Sprintf("[appearance]\nlink_opener = %q\n", opener))
	killDaemon(t, base)
	if o, err := tuiosCLI(t, base, "new", "e2e-links", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, o)
	}
	env := []string{"SSH_CONNECTION=", "SSH_CLIENT=", "SSH_TTY="}
	first := startIn(t, base, startOpts{args: []string{"attach", "e2e-links"}, env: env})
	if err := first.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the first client never attached: %v\n%s", err, first.Snapshot())
	}
	windowManagementMode(t, first)
	enterTerminalMode(t, first)
	runInShell(t, first, "sh "+script, "LINKSDONE", uiTimeout)
	leaveTerminalMode(t, first)

	second := startIn(t, base, startOpts{args: []string{"attach", "e2e-links"}, env: env})
	if err := second.WaitForText("click here", bootTimeout); err != nil {
		t.Fatalf("the second client never showed the pane: %v\n%s", err, second.Snapshot())
	}
	windowManagementMode(t, second)
	if err := second.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v\n%s", err, second.Snapshot())
	}
	col, row := mustFind(t, second, "click here")
	mouseClick(t, second, col+3, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{linkTarget}, "ctrl+click on a label in a rehydrated pane")
}
