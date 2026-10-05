package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// ssh-aware splits (discussion #468), pressed on a real client of a daemon
// session. A fake ssh on PATH records its arguments and then acts as a shell,
// so the pane that runs it looks to tuios exactly like a pane in ssh.
//
// How these could pass wrongly, written down first:
//   - The new pane could run ssh because the test typed it there. Every check
//     reads the argument file the new pane's fake ssh wrote, numbered by run,
//     and the test types ssh only in the first pane.
//   - The new pane could be a shell that merely shows the old pane's text. The
//     fake ssh prints its own run number, which only a new run can print.
//   - The fallback could pass because nothing happened. It waits for the
//     window count to rise and for the new pane's shell to compute a marker.
//   - The remote folder could be passed for a report that names another
//     machine. The ordinary case passes a report from the destination host,
//     and the parser tests in internal/session cover the mismatch.

// fakeSSH writes an ssh stand-in into dir/bin and returns the bin directory
// and the directory its runs record their arguments in.
func fakeSSH(t *testing.T, dir string) (bin, runs string) {
	t.Helper()
	bin = filepath.Join(dir, "bin")
	runs = filepath.Join(dir, "ssh-runs")
	for _, d := range []string{bin, runs} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Each run takes the next number, writes one argument per line, says it
	// connected, and then runs what is typed into it as a shell would.
	script := fmt.Sprintf(`#!/bin/sh
runs=%q
n=$(ls "$runs" | wc -l | tr -d ' ')
: > "$runs/$n.tmp"
for a in "$@"; do printf '%%s\n' "$a" >> "$runs/$n.tmp"; done
mv "$runs/$n.tmp" "$runs/$n.argv"
echo "FAKESSH-RUN-$n-UP"
while IFS= read -r line; do eval "$line"; done
`, runs)
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, runs
}

// sshRunArgs waits for run n of the fake ssh and returns its arguments.
func sshRunArgs(t *testing.T, term *tuitest.Terminal, runs string, n int) []string {
	t.Helper()
	path := filepath.Join(runs, fmt.Sprintf("%d.argv", n))
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the fake ssh never ran a run %d\n%s", n, term.Snapshot())
	return nil
}

// sshRunCount is how many times the fake ssh has run.
func sshRunCount(runs string) int {
	m, _ := filepath.Glob(filepath.Join(runs, "*.argv"))
	return len(m)
}

// startSSHSplit starts a tiled daemon session with the fake ssh on PATH, the
// ssh actions bound, and extra config added, and returns at one pane in
// window-management mode.
func startSSHSplit(t *testing.T, extra string) (*tuitest.Terminal, string, string) {
	t.Helper()
	base := t.TempDir()
	bin, runs := fakeSSH(t, base)
	writeConfig(t, base, `
[keybindings.layout]
split_ssh_vertical = ["alt+v"]
split_ssh_horizontal = ["alt+s"]
new_window_ssh = ["alt+w"]
`+extra)
	term := startIn(t, base, startOpts{
		cols: 140, rows: 40,
		args: []string{"new", "work"},
		env:  []string{"PATH=" + bin + ":" + os.Getenv("PATH")},
	})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")
	enableTiling(t, term)
	return term, base, runs
}

// sshIn types an ssh command into the focused pane and waits until the fake
// ssh says run n connected.
func sshIn(t *testing.T, term *tuitest.Terminal, cmd string, n int) {
	t.Helper()
	enterTerminalMode(t, term)
	// Typed once: typed again, the line would run in the fake ssh's shell
	// and start a second run.
	runInShell(t, term, cmd, fmt.Sprintf("FAKESSH-RUN-%d-UP", n), uiTimeout)
	leaveTerminalMode(t, term)
}

// pressAndCount presses keys and waits for the window count to reach n.
func pressAndCount(t *testing.T, term *tuitest.Terminal, n int, what string, keys ...any) {
	t.Helper()
	if err := term.SendKeys(keys...); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	waitWindowCount(t, term, n, what)
}

func wantArgs(t *testing.T, term *tuitest.Terminal, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s: the new pane ran ssh with\n %q\nwant\n %q\n%s", what, got, want, term.Snapshot())
	}
}

// An ssh split runs the same destination and options, with the remote
// command, the forward and -N gone. The new pane is beside the old one, and a
// new window from the ssh pane does the same.
func TestSSHSplitRunsTheSameSSH(t *testing.T) {
	term, _, runs := startSSHSplit(t, "")
	sshIn(t, term, "ssh -p 2222 -i /tmp/key -o ServerAliveInterval=5 -L 8080:localhost:80 -N pollen@fakehost tail -f /var/log/x", 0)
	wantArgs(t, term, "the typed ssh", sshRunArgs(t, term, runs, 0),
		[]string{"-p", "2222", "-i", "/tmp/key", "-o", "ServerAliveInterval=5", "-L", "8080:localhost:80", "-N", "pollen@fakehost", "tail", "-f", "/var/log/x"})

	pressAndCount(t, term, 2, "split_ssh_vertical", tuitest.Alt('v'))
	if err := term.WaitForText("FAKESSH-RUN-1-UP", uiTimeout); err != nil {
		t.Fatalf("the split pane never ran ssh: %v\n%s", err, term.Snapshot())
	}
	want := []string{"-p", "2222", "-i", "/tmp/key", "-o", "ServerAliveInterval=5", "pollen@fakehost"}
	wantArgs(t, term, "split_ssh_vertical", sshRunArgs(t, term, runs, 1), want)
	t.Logf("after split_ssh_vertical:\n%s", term.Snapshot())

	// The new pane has the focus and runs ssh too, so a second split from
	// it follows the same destination.
	pressAndCount(t, term, 3, "split_ssh_horizontal", tuitest.Alt('s'))
	wantArgs(t, term, "split_ssh_horizontal", sshRunArgs(t, term, runs, 2), want)

	pressAndCount(t, term, 4, "new_window_ssh", tuitest.Alt('w'))
	wantArgs(t, term, "new_window_ssh", sshRunArgs(t, term, runs, 3), want)
	alive(t, term, "after the ssh splits")
}

// With appearance.new_window_follow_ssh on, the ordinary split key follows
// the pane into ssh. The positive half: the same key in a pane with no ssh
// opens a shell.
func TestSSHSplitFollowOption(t *testing.T) {
	term, _, runs := startSSHSplit(t, "\n[appearance]\nnew_window_follow_ssh = true\n")

	// No ssh yet: the ordinary split is a shell.
	pressAndCount(t, term, 2, "split_vertical with no ssh", "|")
	enterTerminalMode(t, term)
	typeUntil(t, term, "echo LOCAL-$((6*7))", "LOCAL-42")
	if n := sshRunCount(runs); n != 0 {
		t.Fatalf("a split of a pane with no ssh ran ssh %d times", n)
	}

	runInShell(t, term, "ssh -l pollen fakehost uptime", "FAKESSH-RUN-0-UP", uiTimeout)
	leaveTerminalMode(t, term)
	pressAndCount(t, term, 3, "split_vertical in ssh", "|")
	wantArgs(t, term, "split_vertical with new_window_follow_ssh", sshRunArgs(t, term, runs, 1),
		[]string{"-l", "pollen", "fakehost"})
	alive(t, term, "after the followed split")
}

// A remote shell that reports its folder with OSC 7 puts the new pane in that
// folder: ssh gets -t and a cd in the remote shell, quoted.
func TestSSHSplitKeepsTheRemoteFolder(t *testing.T) {
	term, _, runs := startSSHSplit(t, "")
	sshIn(t, term, "ssh pollen@fakehost", 0)

	// The fake ssh evals what is typed, so this is the remote shell's report.
	enterTerminalMode(t, term)
	typeUntil(t, term, `printf '\033]7;file://fakehost/srv/it'"'"'s here\033\\'; echo REPORT""ED`, "REPORTED")
	leaveTerminalMode(t, term)

	pressAndCount(t, term, 2, "split_ssh_vertical", tuitest.Alt('v'))
	wantArgs(t, term, "split with a reported folder", sshRunArgs(t, term, runs, 1),
		[]string{"-t", "pollen@fakehost", `cd '/srv/it'"'"'s here' 2>/dev/null; exec "$SHELL" -l`})
	alive(t, term, "after the split with a folder")
}

// The ssh actions in a pane with no ssh open an ordinary pane with a shell.
func TestSSHSplitFallsBackToAShell(t *testing.T) {
	term, _, runs := startSSHSplit(t, "")
	pressAndCount(t, term, 2, "split_ssh_vertical with no ssh", tuitest.Alt('v'))
	enterTerminalMode(t, term)
	typeUntil(t, term, "echo LOCAL-$((6*7))", "LOCAL-42")
	leaveTerminalMode(t, term)
	pressAndCount(t, term, 3, "new_window_ssh with no ssh", tuitest.Alt('w'))
	enterTerminalMode(t, term)
	typeUntil(t, term, "echo LOCAL-$((7*8))", "LOCAL-56")
	if n := sshRunCount(runs); n != 0 {
		t.Fatalf("the ssh actions ran ssh %d times in panes with no ssh", n)
	}
	alive(t, term, "after the fallback")
}

// ssh run by a shell inside the pane's shell is found: the pane's foreground
// is the inner shell, and ssh is under it.
func TestSSHSplitFindsSSHUnderANestedShell(t *testing.T) {
	term, _, runs := startSSHSplit(t, "")
	sshIn(t, term, "sh -c 'ssh -J jump nested@fakehost; true'", 0)
	pressAndCount(t, term, 2, "split_ssh_vertical", tuitest.Alt('v'))
	wantArgs(t, term, "split of a nested ssh", sshRunArgs(t, term, runs, 1),
		[]string{"-J", "jump", "nested@fakehost"})
	alive(t, term, "after the nested split")
}
