package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The rail's files section for a pane on another machine follows the pane
// (issue #313, the follow-up report).
//
// A pane on another machine changed folder, its title and its terminals row
// moved with it, and the files list stayed on the folder the pane started in.
// The client took the pane's folder from the daemon once, the first time it
// heard of the window, and never again. Its own reading of the shell's OSC 7
// cannot stand in, because it judges the host in the report against the
// client's machine, and the pane is not on the client's machine.
//
// Both ways a pane can be on another machine are driven here: a session
// attached on a host, and a window on a host inside a session of this
// machine. In both the far machine also watches the listed folder, so a file
// removed over there leaves the list without the pane doing anything.

// farFolders makes the folders the far shells walk through: the start folder
// with one file of its own, and a folder inside it with two.
func farFolders(t *testing.T, remote string) (top, inner string) {
	t.Helper()
	top = workDirIn(t, remote)
	inner = filepath.Join(top, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	// Short names: the rail is about twenty-four cells wide.
	for path, body := range map[string]string{
		filepath.Join(top, "top-only.txt"):     "x",
		filepath.Join(inner, "inner-keep.txt"): "x",
		filepath.Join(inner, "inner-gone.txt"): "x",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return top, inner
}

// railLists waits until the screen holds every name in want and none in not.
func railLists(t *testing.T, term *tuitest.Terminal, why string, want, not []string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		for _, w := range want {
			if !strings.Contains(text, w) {
				return false
			}
		}
		for _, n := range not {
			if strings.Contains(text, n) {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s: want %q on screen and %q off it: %v\n%s", why, want, not, err, term.Snapshot())
	}
}

// followCdAndDelete is the part both tests share: the list follows a cd in
// the far shell, and then drops a file removed on the far machine.
// typing says whether keys already reach the shell: a client attached on a
// host starts in terminal mode.
func followCdAndDelete(t *testing.T, term *tuitest.Terminal, inner string, typing bool) {
	t.Helper()
	railLists(t, term, "the files list never showed the far start folder",
		[]string{"top-only.txt", "inner/"}, nil)
	saveArtifact(t, term, artifactDir(t), "before-cd")

	if !typing {
		enterTerminalMode(t, term)
	}
	runInShell(t, term, "cd inner && echo now-in-$((40+2))", "now-in-42", uiTimeout)
	if !typing {
		leaveTerminalMode(t, term)
	}

	railLists(t, term, "the files list did not follow the far shell's cd",
		[]string{"inner-keep.txt", "inner-gone.txt"}, []string{"top-only.txt"})
	saveArtifact(t, term, artifactDir(t), "after-cd")

	// Removed on the far machine by something other than the pane, so the
	// only way the list can learn of it is the far machine's own watch.
	if err := os.Remove(filepath.Join(inner, "inner-gone.txt")); err != nil {
		t.Fatal(err)
	}
	railLists(t, term, "a file removed on the far machine stayed on the list",
		[]string{"inner-keep.txt"}, []string{"inner-gone.txt"})
	saveArtifact(t, term, artifactDir(t), "after-delete")
}

// TestFilesFollowAPaneInASessionOnAHost is the reporter's setup: a session that
// lives on the other machine, attached in this client.
//
// Negative control: on main the list stays on the start folder after the cd.
func TestFilesFollowAPaneInASessionOnAHost(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	_, inner := farFolders(t, remote)

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "--host", "build", "far-shell"}, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "╰──")
	}, bootTimeout); err != nil {
		t.Fatalf("the client never drew the far session: %v\n%s", err, term.Snapshot())
	}
	toggleSidebarViaPalette(t, term)
	followCdAndDelete(t, term, inner, true)
	alive(t, term, "after following a far pane's folder")
}

// TestFilesFollowAWindowOnAHost is a window on the other machine inside a
// session of this one, where the daemon that owns the window asks the far
// machine where the pane is.
//
// Negative control: on main the list stays on the start folder after the cd.
func TestFilesFollowAWindowOnAHost(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	_, inner := farFolders(t, remote)

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon never reported build up")
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "faraway", "-s", "home", "--host", "build"); err != nil {
		t.Fatalf("create a window on build: %v\n%s", err, out)
	}
	toggleSidebarViaPalette(t, term)
	followCdAndDelete(t, term, inner, false)
	alive(t, term, "after following a far window's folder")
}

// TestFilesDoNotListALocalFolderForAnSSHShell is a pane of this machine whose
// shell is now on another machine, the plain `ssh host` case. The program in
// the foreground stands in for ssh: it reports a folder on another machine
// over OSC 7, as a shell over there does, and holds the terminal. The list
// must not show this machine's folder of the same name, and it says why it
// shows nothing. When the program ends, the list is back.
//
// Both kinds of pane: one the client runs itself, and one a daemon runs.
//
// Negative control: on main the list keeps the folder on this machine.
func TestFilesDoNotListALocalFolderForAnSSHShell(t *testing.T) {
	t.Run("client pane", func(t *testing.T) {
		term, _ := start(t, startOpts{})
		waitBoot(t, term)
		newWindow(t, term)
		waitWindowCount(t, term, 1, "opening a shell")
		filesForAnSSHShell(t, term)
	})
	t.Run("daemon pane", func(t *testing.T) {
		base := t.TempDir()
		term := startIn(t, base, startOpts{args: []string{"new", "home"}})
		waitBoot(t, term)
		newWindow(t, term)
		waitWindowCount(t, term, 1, "opening a shell")
		filesForAnSSHShell(t, term)
	})
}

func filesForAnSSHShell(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	dir := fileViewFixture(t)
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+" && printf 'in-the-%s\\n' dir", "in-the-dir", uiTimeout)
	leaveTerminalMode(t, term)
	toggleSidebarViaPalette(t, term)
	railLists(t, term, "the files list never showed the local folder",
		[]string{"alpha/", "brief.txt"}, nil)

	// The same path as the local folder, which is the case that must not list
	// local files.
	enterTerminalMode(t, term)
	runInShell(t, term,
		`sh -c 'printf "\033]7;file://farbox%s\033\\\\%s\n" "$PWD" ann""ounced; exec cat'`,
		"announced", uiTimeout)
	leaveTerminalMode(t, term)
	railLists(t, term, "the files list still shows this machine's folder for a shell on another machine",
		[]string{"on farbox"}, []string{"brief.txt"})
	saveArtifact(t, term, artifactDir(t), "ssh-shell")

	// The program ends and the shell has the terminal again.
	enterTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('d')); err != nil {
		t.Fatal(err)
	}
	runInShell(t, term, "echo back-$((1+1))", "back-2", uiTimeout)
	leaveTerminalMode(t, term)
	railLists(t, term, "the files list did not come back when the shell had the terminal again",
		[]string{"alpha/", "brief.txt"}, []string{"on farbox"})
	saveArtifact(t, term, artifactDir(t), "ssh-ended")
	alive(t, term, "after a shell moved to another machine and back")
}
