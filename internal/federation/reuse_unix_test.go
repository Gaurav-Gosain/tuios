//go:build !windows

package federation

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/sockpath"
)

// These guard the security and liveness of the shared ssh master: a link
// must never ride a socket someone else could have put there, must never
// break because of the folder, and must never hang on ssh -G.
//
// Ways it could fail, each with its test:
//   - ssh -G is held past its timeout by a child holding its pipes
//     (TestSSHConfigReturnsWhenAChildHoldsThePipes).
//   - ssh -G runs at every dial (TestSSHConfigIsAskedOncePerHost).
//   - The socket path is too long, ssh exits 255 and no link comes up
//     (TestReuseSkipsAControlPathTooLongForASocket).
//   - A host that forwards the agent loses it through the master
//     (TestReuseSkipsAHostThatForwardsTheAgent).
//   - A folder or parent that is a link, belongs to someone else, or that
//     others can write is trusted (TestReuseNeedsPrivateFolders,
//     TestPrivateDirRefusesAFolderOfAnotherUser).
//   - ssh's stale socket line is reported as the reason a link failed
//     (TestDiagnosticDropsTheStaleMasterLine).

// fakeSSH writes an ssh stand-in that answers -G with config, one option per
// line, appends a line to count for every -G it answers, and returns its
// path. TUIOS_SSH names it for the test.
func fakeSSH(t *testing.T, config string) (count string) {
	t.Helper()
	dir := t.TempDir()
	count = filepath.Join(dir, "count")
	path := filepath.Join(dir, "ssh")
	body := "#!/bin/sh\nif [ \"$1\" = -G ]; then echo run >>'" + count + "'; printf '%s\\n' 'user me' " + config + "; exit 0; fi\nexit 255\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUIOS_SSH", path)
	ForgetSSHConfig()
	t.Cleanup(ForgetSSHConfig)
	return count
}

// runtimeDir makes a runtime directory whose master folder path is exactly
// n bytes long, with tuios and tuios/cm of mode 0700, and points
// XDG_RUNTIME_DIR at it. It returns the master folder.
func runtimeDir(t *testing.T, n int) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "cm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	pad := n - len(filepath.Join(root, "r", "tuios", "cm"))
	if pad < 0 {
		t.Fatalf("the temp root %s is too long for a master folder of %d bytes", root, n)
	}
	rt := filepath.Join(root, "r"+strings.Repeat("x", pad))
	cm := filepath.Join(rt, "tuios", "cm")
	if err := os.MkdirAll(cm, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Dir(cm), cm} {
		if err := os.Chmod(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if len(cm) != n {
		t.Fatalf("built a master folder of %d bytes, want %d", len(cm), n)
	}
	t.Setenv("XDG_RUNTIME_DIR", rt)
	return cm
}

// fits is the longest master folder whose %C sockets fit sun_path.
func fits() int { return sockpath.MaxLen() - 1 - masterNameLen }

func rides(t *testing.T, h Host, cm string) bool {
	t.Helper()
	got := strings.Join(reuseOptions(h), " ")
	if got == "" {
		return false
	}
	if want := "-o ControlMaster=no -o ControlPath=" + cm + "/%C"; got != want {
		t.Fatalf("reuse options %q, want %q", got, want)
	}
	return true
}

func TestSSHConfigReturnsWhenAChildHoldsThePipes(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	path := filepath.Join(dir, "ssh")
	// A Match exec "sleep 30" in the person's config: ssh waits on a child
	// that holds ssh's stdout and stderr.
	body := "#!/bin/sh\nsleep 30 &\necho $! >'" + pidFile + "'\nwait\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUIOS_SSH", path)
	ForgetSSHConfig()
	t.Cleanup(func() {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})

	start := time.Now()
	_, ok := ReadSSHConfig(Host{Name: "b", Addr: "b"})
	took := time.Since(start)
	if ok {
		t.Errorf("a timed-out ssh -G gave an answer")
	}
	if limit := sshConfigTimeout + sshConfigWaitDelay + time.Second; took > limit {
		t.Fatalf("ssh -G took %v, want at most %v", took, limit)
	}
}

func TestSSHConfigIsAskedOncePerHost(t *testing.T) {
	count := fakeSSH(t, "'forwardagent yes'")
	h := Host{Name: "b", Addr: "b"}
	for range 3 {
		if c, ok := ReadSSHConfig(h); !ok || c.ForwardAgent != "yes" {
			t.Fatalf("ReadSSHConfig = %+v, %v", c, ok)
		}
	}
	// Other options are another question.
	h.SSHOptions = []string{"-p", "2222"}
	ReadSSHConfig(h)
	b, _ := os.ReadFile(count)
	if n := strings.Count(string(b), "run"); n != 2 {
		t.Fatalf("ssh -G ran %d times, want 2 (once per address and options)", n)
	}
	// A reload asks again.
	ForgetSSHConfig()
	ReadSSHConfig(h)
	b, _ = os.ReadFile(count)
	if n := strings.Count(string(b), "run"); n != 3 {
		t.Fatalf("ssh -G ran %d times after ForgetSSHConfig, want 3", n)
	}
}

func TestReuseSkipsAControlPathTooLongForASocket(t *testing.T) {
	fakeSSH(t, "")
	h := Host{Name: "b", Addr: "b"}
	if cm := runtimeDir(t, fits()); !rides(t, h, cm) {
		t.Fatalf("a master folder of %d bytes, whose sockets fit, is not used", fits())
	}
	if cm := runtimeDir(t, fits()+1); rides(t, h, cm) {
		t.Fatalf("a master folder of %d bytes is used, and its sockets are %d bytes, over the %d sun_path takes",
			len(cm), len(cm)+1+masterNameLen, sockpath.MaxLen())
	}
}

func TestReuseSkipsAHostThatForwardsTheAgent(t *testing.T) {
	cm := runtimeDir(t, fits())
	h := Host{Name: "b", Addr: "b"}
	for _, c := range []struct {
		config string
		rides  bool
	}{
		{"'forwardagent no'", true},
		{"'forwardagent yes'", false},
		{"'forwardagent /run/agent.sock'", false},
		{"'controlpath none'", true},
		{"'controlpath /home/me/.ssh/cm-%C'", false},
	} {
		fakeSSH(t, c.config)
		if got := rides(t, h, cm); got != c.rides {
			t.Errorf("with %s the link rides the master: %v, want %v", c.config, got, c.rides)
		}
	}
	// ssh -G fails: whether the host forwards is unknown.
	t.Setenv("TUIOS_SSH", "/nonexistent/ssh")
	ForgetSSHConfig()
	if rides(t, h, cm) {
		t.Errorf("a host ssh -G cannot answer for rides the master")
	}
}

func TestReuseNeedsPrivateFolders(t *testing.T) {
	fakeSSH(t, "")
	h := Host{Name: "b", Addr: "b"}

	cm := runtimeDir(t, fits())
	if !rides(t, h, cm) {
		t.Fatal("the user's own master folder is not used")
	}

	// cm is a link to a folder of the user's own.
	cm = runtimeDir(t, fits())
	target := filepath.Join(filepath.Dir(cm), "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cm); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, cm); err != nil {
		t.Fatal(err)
	}
	if rides(t, h, cm) {
		t.Error("a master folder that is a symbolic link is used")
	}

	// The parent, tuios, is open to the group.
	cm = runtimeDir(t, fits())
	if err := os.Chmod(filepath.Dir(cm), 0o770); err != nil {
		t.Fatal(err)
	}
	if rides(t, h, cm) {
		t.Error("a master folder whose parent the group can write is used")
	}

	// cm itself is open to the group.
	cm = runtimeDir(t, fits())
	if err := os.Chmod(cm, 0o750); err != nil {
		t.Fatal(err)
	}
	if rides(t, h, cm) {
		t.Error("a master folder the group can read is used")
	}
}

func TestPrivateDirRefusesAFolderOfAnotherUser(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root owns the folders this test could use")
	}
	// A folder that passes every check but the owner: closed to group and
	// others, and owned by someone else.
	for _, p := range []string{"/proc/1/fd", "/var/lib/private", "/root", "/var/root"} {
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsDir() || st.Mode().Perm()&0o077 != 0 {
			continue
		}
		if sys, ok := st.Sys().(*syscall.Stat_t); !ok || int(sys.Uid) == os.Getuid() {
			continue
		}
		if privateDir(p) {
			t.Fatalf("%s, owned by another user, counts as the user's own", p)
		}
		return
	}
	t.Skip("no folder of another user that only its owner can open")
}

func TestDiagnosticDropsTheStaleMasterLine(t *testing.T) {
	cm := runtimeDir(t, fits())
	stale := "Control socket connect(" + cm + "/0123456789abcdef0123456789abcdef01234567): Connection refused"
	other := "Control socket connect(/home/me/.ssh/cm-x): Connection refused"
	script := "printf '%s\\n' '" + stale + "' 'ssh: connect to host b port 22: No route to host' '" + other + "' >&2; exit 255"
	tr, err := CommandDialer("sh", "-c", script)(context.Background(), Host{Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	_, _ = tr.Read(make([]byte, 1))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if done, _ := tr.(exitReporter).Exited(); done || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := tr.Diagnostic()
	want := "ssh: connect to host b port 22: No route to host\n" + other
	if got != want {
		t.Fatalf("Diagnostic() = %q, want %q", got, want)
	}
}
