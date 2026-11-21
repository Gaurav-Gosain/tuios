package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func fakeSSH(t *testing.T, dir string) (bin, runs string) {
	t.Helper()
	bin = filepath.Join(dir, "bin")
	runs = filepath.Join(dir, "ssh-runs")
	for _, d := range []string{bin, runs} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	script := fmt.Sprintf(`#!/bin/sh
runs=%q
if [ "$1" = "-G" ]; then
	h=""; prev=""; last=""
	for a in "$@"; do
		case "$prev$a" in -oHostName=*) h=${a#HostName=} ;; esac
		prev=$a; last=$a
	done
	[ -n "$h" ] || h=$last
	echo "hostname $h"
	exit 0
fi
n=$(ls "$runs" | grep -c '[.]argv$')
: > "$runs/$n.tmp"
printf '%%s\n' "$SSH_AUTH_SOCK" > "$runs/$n.sock"
for a in "$@"; do printf '%%s\n' "$a" >> "$runs/$n.tmp"; done
mv "$runs/$n.tmp" "$runs/$n.argv"
echo "FAKESSH-RUN-$n-UP"
while IFS= read -r line; do eval "$line"; done
`, runs)
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	scp := "#!/bin/sh\nssh \"$1\" scp -t /tmp\necho SCP-DONE\n"
	if err := os.WriteFile(filepath.Join(bin, "scp"), []byte(scp), 0o755); err != nil {
		t.Fatal(err)
	}
	tt := "#!/bin/sh\n\"$@\"\necho TT-DONE\n"
	if err := os.WriteFile(filepath.Join(bin, "tt"), []byte(tt), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, runs
}

var (
	fakeSSHBinOnce sync.Once
	fakeSSHBin     string
	fakeSSHBinErr  error
)

func compiledFakeSSH(t *testing.T, path string) {
	t.Helper()
	fakeSSHBinOnce.Do(func() {
		dir, err := runScratchDir("fakessh")
		if err != nil {
			fakeSSHBinErr = err
			return
		}
		fakeSSHBin = filepath.Join(dir, "fakessh")
		if out, err := exec.Command("go", "build", "-o", fakeSSHBin, "./fakessh").CombinedOutput(); err != nil {
			fakeSSHBinErr = fmt.Errorf("build fakessh: %v\n%s", err, out)
		}
	})
	if fakeSSHBinErr != nil {
		t.Fatal(fakeSSHBinErr)
	}
	data, err := os.ReadFile(fakeSSHBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func sshRunArgv0(t *testing.T, runs string, n int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runs, fmt.Sprintf("%d.argv0", n)))
	if err != nil {
		t.Fatalf("run %d wrote no argv0: %v", n, err)
	}
	return strings.TrimSpace(string(data))
}

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

func sshRunCount(runs string) int {
	m, _ := filepath.Glob(filepath.Join(runs, "*.argv"))
	return len(m)
}

func startSSHSplit(t *testing.T, extra string) (*tuitest.Terminal, string, string) {
	t.Helper()
	return startSSHSplitWith(t, extra, nil)
}

func startSSHSplitWith(t *testing.T, extra string, prepare func(base, bin string)) (*tuitest.Terminal, string, string) {
	t.Helper()
	base := t.TempDir()
	bin, runs := fakeSSH(t, base)
	if prepare != nil {
		prepare(base, bin)
	}
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

func sshIn(t *testing.T, term *tuitest.Terminal, cmd string, n int) {
	t.Helper()
	enterTerminalMode(t, term)
	runInShell(t, term, cmd, fmt.Sprintf("FAKESSH-RUN-%d-UP", n), uiTimeout)
	leaveTerminalMode(t, term)
}

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
	requireReal(t, "OS process or perf topic")
	term, _, runs := startSSHSplit(t, "")
	sshIn(t, term, "ssh -p 2222 -i /tmp/key -o ServerAliveInterval=5 -L 8080:localhost:80 -N pollen@fakehost tail -f /var/log/x", 0)
	wantArgs(t, term, "the typed ssh", sshRunArgs(t, term, runs, 0),
		[]string{"-p", "2222", "-i", "/tmp/key", "-o", "ServerAliveInterval=5", "-L", "8080:localhost:80", "-N", "pollen@fakehost", "tail", "-f", "/var/log/x"})

	pressAndCount(t, term, 2, "split_ssh_vertical", tuitest.Alt('v'))
	if err := term.WaitForText("FAKESSH-RUN-1-UP", uiTimeout); err != nil {
		t.Fatalf("the split pane never ran ssh: %v\n%s", err, term.Snapshot())
	}
	want := []string{"-p", "2222", "-i", "/tmp/key", "-o", "ServerAliveInterval=5", "-o", "ControlMaster=no", "pollen@fakehost"}
	wantArgs(t, term, "split_ssh_vertical", sshRunArgs(t, term, runs, 1), want)
	t.Logf("after split_ssh_vertical:\n%s", term.Snapshot())

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
	requireReal(t, "OS process or perf topic")
	term, _, runs := startSSHSplit(t, "\n[appearance]\nnew_window_follow_ssh = true\n")

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
		[]string{"-l", "pollen", "-o", "ControlMaster=no", "fakehost"})
	alive(t, term, "after the followed split")
}

// A remote shell that reports its folder with OSC 7 puts the new pane in that
// folder: ssh gets -t and a cd in the remote shell, quoted.
func TestSSHSplitKeepsTheRemoteFolder(t *testing.T) {
	requireReal(t, "OS process or perf topic")
	term, _, runs := startSSHSplit(t, "")
	sshIn(t, term, "ssh pollen@fakehost", 0)

	enterTerminalMode(t, term)
	typeUntil(t, term, `printf '\033]7;file://fakehost/srv/my app\033\\'; echo REPORT""ED`, "REPORTED")
	leaveTerminalMode(t, term)

	pressAndCount(t, term, 2, "split_ssh_vertical", tuitest.Alt('v'))
	wantArgs(t, term, "split with a reported folder", sshRunArgs(t, term, runs, 1),
		[]string{"-o", "ControlMaster=no", "-t", "-o", "RemoteCommand=none", "pollen@fakehost",
			`exec sh -c 'cd "/srv/my app" 2>/dev/null; exec "$SHELL" -l'`})
	alive(t, term, "after the split with a folder")
}

// Issue #491: a native Windows shell reports file://HOST/C:/x. The folder is
// C:/x, and tuios kept the slash in front of the drive, so a split ran
// cd "/C:/x" on the far machine, which is no folder there. The cd goes
// through sh, which a Windows ssh server does not run, so a drive folder
// gets no cd at all: the new pane opens the far machine's login shell.
//
// A shell in a UNC folder reports file://HOST//srv/share. That is no folder
// sh can cd to either, so it gets no cd as well.
func TestSSHSplitIntoWindowsSendsNoDriveFolder(t *testing.T) {
	requireReal(t, "OS process or perf topic")
	for _, tc := range []struct{ name, url string }{
		{"drive", "file://fakehost/C:/Users/pollen/my%%20app"},
		{"unc", "file://fakehost//srv/share"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term, _, runs := startSSHSplit(t, "")
			sshIn(t, term, "ssh pollen@fakehost", 0)

			enterTerminalMode(t, term)
			typeUntil(t, term, `printf '\033]7;`+tc.url+`\033\\'; echo REPORT""ED`, "REPORTED")
			leaveTerminalMode(t, term)

			pressAndCount(t, term, 2, "split_ssh_vertical", tuitest.Alt('v'))
			wantArgs(t, term, "split with a reported "+tc.name+" folder", sshRunArgs(t, term, runs, 1),
				[]string{"-o", "ControlMaster=no", "pollen@fakehost"})
			alive(t, term, "after the split with a "+tc.name+" folder")
		})
	}
}

// The ssh actions in a pane with no ssh open an ordinary pane with a shell.
func TestSSHSplitFallsBackToAShell(t *testing.T) {
	requireReal(t, "OS process or perf topic")
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

// ssh run by a program inside the pane's shell is found: the pane's
// foreground is that program, and ssh is under it. Through a shell any line
// is followed. Through a helper tuios does not know (discussion #468), only
// an interactive login is, and the dock says why a split did not follow.
func TestSSHSplitFindsSSHUnderANestedShell(t *testing.T) {
	requireReal(t, "OS process or perf topic")
	for _, tc := range []struct {
		name, cmd string
		want      []string
		notice    string
	}{
		{"shell", "sh -c 'ssh -J jump nested@fakehost; true'",
			[]string{"-J", "jump", "-o", "ControlMaster=no", "nested@fakehost"}, ""},
		{"helper", "tt ssh -J jump nested@fakehost",
			[]string{"-J", "jump", "-o", "ControlMaster=no", "nested@fakehost"}, ""},
		{"helper with a remote command", "tt ssh nested@fakehost uptime", nil,
			"The ssh in this pane runs a remote command."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term, _, runs := startSSHSplit(t, "")
			sshIn(t, term, tc.cmd, 0)
			pressAndCount(t, term, 2, "split_ssh_vertical", tuitest.Alt('v'))
			if tc.want != nil {
				wantArgs(t, term, "split under "+tc.name, sshRunArgs(t, term, runs, 1), tc.want)
				alive(t, term, "after the split under "+tc.name)
				return
			}
			if err := term.WaitForText(tc.notice, uiTimeout); err != nil {
				t.Fatalf("the dock never said why the split did not follow ssh: %v\n%s", err, term.Snapshot())
			}
			t.Logf("the notice for %s:\n%s", tc.name, term.Snapshot())
			enterTerminalMode(t, term)
			typeUntil(t, term, "echo LOCAL-$((6*7))", "LOCAL-42")
			if n := sshRunCount(runs); n != 1 {
				t.Fatalf("a split under %s ran ssh: %d runs, want 1", tc.name, n)
			}
			alive(t, term, "after the split under "+tc.name)
		})
	}
}

// A compiled ssh run from the project folder (./ssh) is followed with the ssh
// on the daemon's PATH, never with the binary the pane ran.
func TestSSHSplitRunsTheSSHOnPath(t *testing.T) {
	requireReal(t, "OS process or perf topic")
	var pathSSH string
	term, _, runs := startSSHSplitWith(t, "", func(base, bin string) {
		pathSSH = filepath.Join(bin, "ssh")
		compiledFakeSSH(t, pathSSH)
		compiledFakeSSH(t, filepath.Join(workDirIn(t, base), "ssh"))
	})
	sshIn(t, term, "./ssh -p 2222 pollen@fakehost", 0)
	if got := sshRunArgv0(t, runs, 0); got != "./ssh" {
		t.Fatalf("the first pane ran %q, want ./ssh", got)
	}
	pressAndCount(t, term, 2, "split_ssh_vertical", tuitest.Alt('v'))
	wantArgs(t, term, "split of a compiled ssh", sshRunArgs(t, term, runs, 1),
		[]string{"-p", "2222", "-o", "ControlMaster=no", "pollen@fakehost"})
	if got := sshRunArgv0(t, runs, 1); got != pathSSH {
		t.Fatalf("the split ran %q, want the ssh on PATH, %q", got, pathSSH)
	}
	alive(t, term, "after the compiled split")
}

// An ssh line with an option that runs code on this machine is not followed:
// the split is an ordinary shell.
func TestSSHSplitRefusesAProxyCommand(t *testing.T) {
	requireReal(t, "OS process or perf topic")
	term, _, runs := startSSHSplit(t, "")
	sshIn(t, term, "ssh -o ProxyCommand='nc %h %p' pollen@fakehost", 0)
	pressAndCount(t, term, 2, "split_ssh_vertical with a ProxyCommand", tuitest.Alt('v'))
	enterTerminalMode(t, term)
	typeUntil(t, term, "echo LOCAL-$((6*7))", "LOCAL-42")
	if n := sshRunCount(runs); n != 1 {
		t.Fatalf("a split of an ssh with a ProxyCommand ran ssh: %d runs, want 1", n)
	}
	alive(t, term, "after the refused split")
}

// ssh started by scp is a transfer, not a login: the split is an ordinary
// shell.
func TestSSHSplitIgnoresTheSSHOfATransfer(t *testing.T) {
	requireReal(t, "OS process or perf topic")
	term, _, runs := startSSHSplit(t, "")
	sshIn(t, term, "scp pollen@fakehost", 0)
	wantArgs(t, term, "the transfer's ssh", sshRunArgs(t, term, runs, 0),
		[]string{"pollen@fakehost", "scp", "-t", "/tmp"})
	pressAndCount(t, term, 2, "split_ssh_vertical under scp", tuitest.Alt('v'))
	enterTerminalMode(t, term)
	typeUntil(t, term, "echo LOCAL-$((6*7))", "LOCAL-42")
	if n := sshRunCount(runs); n != 1 {
		t.Fatalf("a split of scp's ssh ran ssh: %d runs, want 1", n)
	}
	alive(t, term, "after the transfer split")
}

// An -o value with a line break is two config lines to ssh, so a
// ProxyCommand can hide behind it. Such a line is not followed: the split is
// an ordinary shell.
func TestSSHSplitRefusesALineBreakInAnOption(t *testing.T) {
	requireReal(t, "OS process or perf topic")
	term, _, runs := startSSHSplit(t, "")
	sshIn(t, term, `ssh -o "$(printf 'User=pollen\nProxyCommand=echo PWNED')" fakehost`, 0)
	pressAndCount(t, term, 2, "split_ssh_vertical with a line break", tuitest.Alt('v'))
	enterTerminalMode(t, term)
	typeUntil(t, term, "echo LOCAL-$((6*7))", "LOCAL-42")
	if n := sshRunCount(runs); n != 1 {
		t.Fatalf("a split of an ssh with a line break in -o ran ssh: %d runs, want 1", n)
	}
	alive(t, term, "after the refused split")
}

// A destination that is an alias keeps the remote folder when the reported
// host is the host name ssh -G resolves the alias to.
func TestSSHSplitKeepsTheFolderOfAnAlias(t *testing.T) {
	requireReal(t, "OS process or perf topic")
	term, _, runs := startSSHSplit(t, "")
	sshIn(t, term, "ssh -o HostName=fakehost.example.com pollen@prod", 0)
	enterTerminalMode(t, term)
	typeUntil(t, term, `printf '\033]7;file://fakehost/srv/app\033\\'; echo REPORT""ED`, "REPORTED")
	leaveTerminalMode(t, term)
	pressAndCount(t, term, 2, "split_ssh_vertical", tuitest.Alt('v'))
	wantArgs(t, term, "split of an alias with a reported folder", sshRunArgs(t, term, runs, 1),
		[]string{"-o", "HostName=fakehost.example.com", "-o", "ControlMaster=no", "-t", "-o", "RemoteCommand=none", "pollen@prod",
			`exec sh -c 'cd "/srv/app" 2>/dev/null; exec "$SHELL" -l'`})
	alive(t, term, "after the alias split")
}

// The split's ssh gets the agent socket the followed ssh had, when the pane's
// shell started that agent itself. An ordinary pane starts with the daemon's
// environment, which has no such socket.
func TestSSHSplitPassesTheAgentSocket(t *testing.T) {
	requireReal(t, "OS process or perf topic")
	if _, err := exec.LookPath("ssh-agent"); err != nil {
		t.Skip("no ssh-agent on PATH")
	}
	term, base, runs := startSSHSplit(t, "")
	pidFile := filepath.Join(base, "agent.pid")
	t.Cleanup(func() {
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 1 {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
	})
	sockDir, err := os.MkdirTemp("", "ag")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sshIn(t, term, `eval "$(ssh-agent -s -a `+filepath.Join(sockDir, "s")+`)" >/dev/null; echo "$SSH_AGENT_PID" > `+pidFile+`; ssh pollen@fakehost`, 0)
	sock := func(n int) string {
		data, err := os.ReadFile(filepath.Join(runs, fmt.Sprintf("%d.sock", n)))
		if err != nil {
			t.Fatalf("run %d wrote no socket: %v", n, err)
		}
		return strings.TrimSpace(string(data))
	}
	first := sock(0)
	if first == "" {
		t.Fatalf("the agent in the pane set no SSH_AUTH_SOCK\n%s", term.Snapshot())
	}
	pressAndCount(t, term, 2, "split_ssh_vertical", tuitest.Alt('v'))
	sshRunArgs(t, term, runs, 1)
	if got := sock(1); got != first {
		t.Fatalf("the split's ssh had SSH_AUTH_SOCK %q, want the followed ssh's %q", got, first)
	}
	alive(t, term, "after the agent split")
}

// -E makes ssh append its log to a file the line names, so a line with it is
// not followed: the split is an ordinary shell.
func TestSSHSplitRefusesALogFile(t *testing.T) {
	requireReal(t, "OS process or perf topic")
	term, base, runs := startSSHSplit(t, "")
	sshIn(t, term, "ssh -E "+filepath.Join(base, "ssh.log")+" pollen@fakehost", 0)
	pressAndCount(t, term, 2, "split_ssh_vertical with -E", tuitest.Alt('v'))
	enterTerminalMode(t, term)
	typeUntil(t, term, "echo LOCAL-$((6*7))", "LOCAL-42")
	if n := sshRunCount(runs); n != 1 {
		t.Fatalf("a split of an ssh with -E ran ssh: %d runs, want 1", n)
	}
	alive(t, term, "after the refused split")
}
