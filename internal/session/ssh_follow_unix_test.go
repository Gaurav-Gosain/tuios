//go:build linux || darwin

package session

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The agent socket a split passes on comes from another process's
// environment. Ways it could be wrong: a link that points at another user's
// socket, or a socket in a folder another user can write to, where the name
// can be swapped. Only a socket of this user, in a folder only this user can
// write, is passed.
func TestOwnedSocket(t *testing.T) {
	owned := func(path string) bool {
		_, ok := ownedSocket(path)
		return ok
	}
	listen := func(dir string) string {
		path := filepath.Join(dir, "s")
		l, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		return path
	}
	private := filepath.Join(t.TempDir(), "p")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := listen(private)
	if !owned(sock) {
		t.Fatalf("a socket in a private folder was refused")
	}
	link := filepath.Join(private, "link")
	if err := os.Symlink(sock, link); err != nil {
		t.Fatal(err)
	}
	if owned(link) {
		t.Fatalf("a link to the socket was passed")
	}
	open := filepath.Join(t.TempDir(), "o")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	openSock := listen(open)
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if owned(openSock) {
		t.Fatalf("a socket in a folder others can write to was passed")
	}
	file := filepath.Join(private, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if owned(file) || owned("relative/s") {
		t.Fatalf("a plain file or a relative path was passed")
	}
}

// A symlink ancestor sits in every standard macOS temp path (/var, a symlink
// to /private/var), so rejecting any path that passes through one would
// refuse the socket in the common case, not just the suspicious one.
// ownedSocket resolves the socket's folder once up front, so the walk itself
// never sees the symlink — it checks whatever the symlink resolves to, the
// same as a real directory at that position would be checked, and still
// rejects a resolved target that is not actually safe.
func TestOwnedSocketFollowsASafeSymlinkAncestor(t *testing.T) {
	listen := func(dir string) string {
		path := filepath.Join(dir, "s")
		l, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		return path
	}
	// A short-named root, not t.TempDir(): a Unix domain socket's sun_path is
	// capped (104 bytes on macOS/BSD), and t.TempDir() embeds this test's own
	// (long) function name in the directory it hands back.
	root, err := os.MkdirTemp("", "owned-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	safeTarget := filepath.Join(root, "real")
	if err := os.Mkdir(safeTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	safeLink := filepath.Join(root, "link-to-real")
	if err := os.Symlink(safeTarget, safeLink); err != nil {
		t.Fatal(err)
	}
	sock := listen(safeTarget)
	got, ok := ownedSocket(filepath.Join(safeLink, "s"))
	if !ok {
		t.Fatalf("a socket reached through a symlink to a safe folder was refused: %s", sock)
	}
	// The path handed on is the one that was checked, so nothing after the
	// check goes back through the link.
	realTarget, err := filepath.EvalSymlinks(safeTarget)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(realTarget, "s"); got != want {
		t.Fatalf("ownedSocket returned %q for a socket reached through a link, want the resolved %q", got, want)
	}

	unsafeTarget := filepath.Join(root, "unsafe")
	if err := os.Mkdir(unsafeTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	// Mkdir's mode is masked by umask; Chmod sets the exact bits, same as
	// TestOwnedSocket's "open" case above does for the same reason.
	if err := os.Chmod(unsafeTarget, 0o777); err != nil {
		t.Fatal(err)
	}
	unsafeLink := filepath.Join(root, "link-to-unsafe")
	if err := os.Symlink(unsafeTarget, unsafeLink); err != nil {
		t.Fatal(err)
	}
	sock = listen(unsafeTarget)
	if _, ok := ownedSocket(filepath.Join(unsafeLink, "s")); ok {
		t.Fatalf("a socket reached through a symlink to a world-writable folder was passed: %s", sock)
	}
}

// ssh -G reads ~/.ssh/config, where a Match exec can start a child that keeps
// ssh's output open. The lookup must still return at its timeout.
func TestResolveSSHHostNameIsBounded(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "ssh")
	script := "#!/bin/sh\nsleep 30 &\nsleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got := resolveSSHHostName(remoteLogin{bin: bin, dest: "h"})
	if took := time.Since(start); took > sshResolveTimeout+2*time.Second {
		t.Fatalf("ssh -G took %v, want at most about %v", took, sshResolveTimeout)
	}
	if got != "" {
		t.Fatalf("got %q from an ssh that printed nothing", got)
	}
}

// A client started through a program tuios does not know (a person's own
// helper, such as `tt ssh box`, discussion #468) is followed only when its line
// is an interactive login. Ways this could go wrong: a transfer's ssh is
// followed because its program is now an unknown wrapper; a wrapper's ssh
// with a remote command or a session that is not a shell is followed, and
// the split repeats a job; an option that runs a local program is followed
// because the wrapper path skips the parser; a known launcher's line with a
// remote command stops being followed. Each case runs a real process chain:
// the pane's shell, the wrapper, and an ssh stand-in that writes its pid.
func TestCheckRemoteClientThroughAWrapper(t *testing.T) {
	old := lookPath
	lookPath = func(name string) (string, error) { return "/path/" + name, nil }
	t.Cleanup(func() { lookPath = old })

	bin := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The trailing : keeps sh from exec'ing the last command, so each
	// script stays the parent of what it runs.
	write("ssh", "echo $$ > \"$SSHPID\"\nsleep 30\n:\n")
	for _, name := range []string{"tt", "rsync", "git", "scp"} {
		write(name, "\"$@\"\n:\n")
	}

	cases := []struct {
		name, line, why string
	}{
		{"wrapper login", "tt ssh -p 2222 u@box", ""},
		{"wrapper login after --", "tt ssh -- u@box", ""},
		{"wrapper with a remote command", "tt ssh u@box uptime", sshWhyCommand},
		{"wrapper with a remote command after --", "tt ssh u@box -- uptime", sshWhyCommand},
		{"wrapper with a RemoteCommand option", "tt ssh -o RemoteCommand=top u@box", sshWhyCommand},
		{"wrapper with -N", "tt ssh -N u@box", sshWhyNoLogin},
		{"wrapper with -T", "tt ssh -T u@box", sshWhyNoLogin},
		{"wrapper with -W", "tt ssh -W x:22 u@box", sshWhyNoLogin},
		{"wrapper with a subsystem", "tt ssh -s u@box sftp", sshWhyCommand},
		{"wrapper with a ProxyCommand", "tt ssh -o ProxyCommand=nc u@box", sshWhyOptions},
		{"rsync", "rsync ssh u@box", sshWhyTransfer},
		{"git", "git ssh u@box", sshWhyTransfer},
		{"scp under a wrapper", "tt scp ssh u@box", sshWhyTransfer},
		{"shell with a remote command", "sh -c 'ssh u@box uptime; :'", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pid")
			shell := exec.Command("sh", "-c", tc.line+"; :")
			shell.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SSHPID="+pidFile)
			shell.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := shell.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = syscall.Kill(-shell.Process.Pid, syscall.SIGKILL)
				_ = shell.Wait()
			})
			var pid int
			for deadline := time.Now().Add(5 * time.Second); pid == 0 && time.Now().Before(deadline); {
				data, _ := os.ReadFile(pidFile)
				pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
				time.Sleep(10 * time.Millisecond)
			}
			if pid == 0 {
				t.Fatal("the ssh stand-in never started")
			}
			argv := readArgvExact(pid)
			if remoteClientName(argv) != "ssh" {
				t.Fatalf("pid %d runs %q, not the ssh stand-in", pid, argv)
			}
			login, why := checkRemoteClient(pid, shell.Process.Pid, argv)
			if why != tc.why {
				t.Fatalf("%s: got why %q, want %q", tc.line, why, tc.why)
			}
			if why == "" && (login.dest != "u@box" || login.bin != "/path/ssh") {
				t.Fatalf("%s: followed %+v", tc.line, login)
			}
		})
	}
}
