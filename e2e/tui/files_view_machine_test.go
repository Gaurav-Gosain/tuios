package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestFilesViewMachinePicker opens the machine picker of a Files view side
// with a click on the side's machine row, and with M. The picker lists this
// machine, the linked host build, and under "Add with ssh" the ssh host
// devbox of ~/.ssh/config, which is no tuios host yet. A pattern host of the
// ssh config is no machine, so it is not listed. Picking build shows build's
// home folder on the side: build has no ~/project, so the side falls back
// to the home folder.
//
// How this could pass wrongly, written down first:
//   - The far listing could be this machine's. far-only.txt is only in
//     build's home folder.
//   - The picker's rows could be another picker's. Its title says what it
//     is for.
//   - devbox could come from somewhere other than the ssh config. Nothing
//     else in the test names it.
//
// NEGATIVE CONTROL: with filesMachineRowHit cut to return false, the click
// opens no picker and the test fails at the first check.
func TestFilesViewMachinePicker(t *testing.T) {
	requireReal(t, "links a second daemon as a host")
	base := t.TempDir()
	remote := remoteMachine(t)
	writeRemoteConfig(t, remote, relayFilesPolicy)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	if err := os.WriteFile(filepath.Join(remote, "HOME", "far-only.txt"), []byte("far\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	sshDir := filepath.Join(base, "HOME", ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sshConfig := "Host devbox\n    HostName 192.0.2.7\nHost *.example\n    User someone\n"
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(sshConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	filesViewFixtureIn(t, filepath.Join(base, "HOME", "project"))
	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: []string{"TUIOS_SSH=" + ssh}, cols: 160, rows: 44})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")
	newWindow(t, term)
	// The side shows ~/project, which build does not have, so the side
	// falls back to build's home folder.
	enterTerminalMode(t, term)
	runInShell(t, term, "cd ~/project && clear && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
	sendKeys(t, term, tuitest.Ctrl('b'), "f")
	railLists(t, term, "Ctrl+B f did not open the view on its list", []string{"this machine"}, []string{"Search files"})

	// A click on the machine row opens the picker.
	clickOnText(t, term, "this machine")
	railLists(t, term, "a click on the machine row did not open the machine picker",
		[]string{"Show this side on", "build", "linked", "Add with ssh", "devbox"}, []string{"example"})
	copiesFrame(t, term, "files-machine-picker")

	// Typing filters, and enter shows the machine on the side.
	sendKeys(t, term, "bui", tuitest.Enter)
	railLists(t, term, "the side did not show build's home folder",
		[]string{"build", "far-only.txt"}, []string{"Show this side on", "this machine"})
	copiesFrame(t, term, "files-machine-build")

	// M opens it too, and esc leaves the side as it is.
	sendKeys(t, term, "M")
	railLists(t, term, "M did not open the machine picker", []string{"Show this side on", "devbox"}, nil)
	sendKeys(t, term, tuitest.Esc)
	railLists(t, term, "esc did not close the machine picker", []string{"far-only.txt"}, []string{"Show this side on"})
}

// clickOnText clicks the first cell of the first place text is on screen.
func clickOnText(t *testing.T, term *tuitest.Terminal, text string) {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for r := range rows {
		if i := strings.Index(s.Line(r), text); i >= 0 {
			mouseClick(t, term, len([]rune(s.Line(r)[:i])), r, tuitest.MouseLeft, 0)
			return
		}
	}
	t.Fatalf("no %q on screen:\n%s", text, term.Snapshot())
}
