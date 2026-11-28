package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestFilesViewWriteAccess shows a side on a linked host that lets this
// machine write only in ~/out:
//
//   - on ~ the machine row says "read only here", the footer says where
//     this machine may write and how many names build's read rules hide,
//     and a (new) is refused with that reason, before a call;
//   - on ~/out the machine row says "you can write here";
//   - d moves a file to build's trash, and u brings it back with the same
//     sha256.
//
// Frames of the read only side, the side that takes writes, and the file
// back after the undo are the artifact.
//
// Negative control: with the access part of the file-list reply not read,
// the badge says "write access unknown" and the wait for "read only here"
// times out.
func TestFilesViewWriteAccess(t *testing.T) {
	requireReal(t, "links a second daemon as a host")
	base := t.TempDir()
	remote := remoteMachine(t)
	writeRemoteConfig(t, remote, relayFilesPolicy+`files_roots = ["~/out"]`+"\n")
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	farHome := filepath.Join(remote, "HOME")
	out := filepath.Join(farHome, "out")
	for _, d := range []string{out, filepath.Join(farHome, ".ssh")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(out, "old.log"):                "the old log\n",
		filepath.Join(farHome, "notes.txt"):          "notes\n",
		filepath.Join(farHome, ".ssh", "id_ed25519"): "not a real key\n",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sum := fileSHA(t, filepath.Join(out, "old.log"))
	if res, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, res)
	}
	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: []string{"TUIOS_SSH=" + ssh}, cols: 160, rows: 44})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")
	newWindow(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "f")
	railLists(t, term, "Ctrl+B f did not open the view", []string{"this machine"}, nil)

	// ~ on build: read only, with the reason, and the hidden names counted.
	sendKeys(t, term, ":", "build:~", tuitest.Enter)
	railLists(t, term, "the side did not say build's home is read only", []string{"build", "notes.txt", "read only here", "build lets this machine write only in ~/out."}, []string{"write access unknown"})
	if !regexp.MustCompile(`[1-9][0-9]* hidden by build's read rules`).MatchString(term.Screen().Text()) {
		t.Fatalf("ASSERTION: the footer does not count the names build's read rules hide:\n%s", term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), ".ssh") {
		t.Fatalf("ASSERTION: build's .ssh is listed:\n%s", term.Snapshot())
	}
	sendKeys(t, term, "a")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "write only in ~/out") >= 2
	}, uiTimeout); err != nil {
		t.Fatalf("a on a read only side did not say why: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "a name that ends in / makes a folder") {
		t.Fatalf("ASSERTION: a on a read only side opened the new prompt:\n%s", term.Snapshot())
	}
	copiesFrame(t, term, "files-access-read-only")

	// ~/out on build: this machine may write there.
	sendKeys(t, term, ":", "build:~/out", tuitest.Enter)
	railLists(t, term, "the side did not say ~/out takes writes", []string{"old.log", "you can write here"}, []string{"read only here"})
	copiesFrame(t, term, "files-access-write")

	// d moves old.log to build's trash, and u brings it back.
	sendKeys(t, term, "d")
	filesWaitDisk(t, uiTimeout, "old.log to go to build's trash", func() bool {
		_, err := os.Stat(filepath.Join(out, "old.log"))
		return os.IsNotExist(err)
	})
	railLists(t, term, "the trash did not say where the file went", []string{"to the trash on build"}, nil)
	sendKeys(t, term, "u")
	filesWaitDisk(t, uiTimeout, "u to bring old.log back", func() bool {
		_, err := os.Stat(filepath.Join(out, "old.log"))
		return err == nil
	})
	if got := fileSHA(t, filepath.Join(out, "old.log")); got != sum {
		t.Fatalf("ASSERTION: old.log came back with sha256 %s, want %s", got, sum)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "old.log") }, 10*time.Second); err != nil {
		t.Fatalf("the side did not list old.log again: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "files-access-undo")
}
