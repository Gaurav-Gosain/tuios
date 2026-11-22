package tuie2e

import (
	"bufio"
	"io"
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

// farEcho is a TCP server on 127.0.0.1 that answers each line with "far:"
// and the line. It stands for a program that listens on build.
func farEcho(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					if _, err := io.WriteString(c, "far:"+sc.Text()+"\n"); err != nil {
						return
					}
				}
				_ = sc.Err() // a closed connection ends the loop, which is fine
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// startForward runs tuios forward and returns the process and the local
// address it prints first.
func startForward(t *testing.T, base string, env []string, args ...string) (*exec.Cmd, string) {
	t.Helper()
	cmd := exec.Command(tuiosBin, append([]string{"forward"}, args...)...)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	errs, err := os.Create(filepath.Join(t.TempDir(), "forward.stderr"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = errs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	lines := make(chan string, 1)
	go func() {
		br := bufio.NewReader(out)
		line, _ := br.ReadString('\n')
		lines <- strings.TrimSpace(line)
		_, _ = io.Copy(io.Discard, br)
	}()
	var addr string
	waitUntil(t, "tuios forward prints its address", 20*time.Second, func() (bool, string) {
		select {
		case addr = <-lines:
			return true, ""
		default:
			b, _ := os.ReadFile(errs.Name())
			return false, "stderr so far: " + string(b)
		}
	})
	if addr == "" {
		b, _ := os.ReadFile(errs.Name())
		t.Fatalf("ASSERTION: tuios forward printed no address; stderr %q", b)
	}
	return cmd, addr
}

// askThrough sends one line to addr and returns the answer line.
func askThrough(t *testing.T, addr, line string) string {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := io.WriteString(c, line+"\n"); err != nil {
		t.Fatalf("write to %s: %v", addr, err)
	}
	got, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatalf("read from %s: %v (got %q)", addr, err, got)
	}
	return strings.TrimSpace(got)
}

// listenAddrsOf is every TCP address in LISTEN that the process pid holds,
// from /proc/net/tcp and tcp6 matched to the inodes in /proc/PID/fd.
func listenAddrsOf(t *testing.T, pid int) []string {
	t.Helper()
	inodes := map[string]bool{}
	fds, _ := os.ReadDir("/proc/" + strconv.Itoa(pid) + "/fd")
	for _, fd := range fds {
		link, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/fd/" + fd.Name())
		if err == nil && strings.HasPrefix(link, "socket:[") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = true
		}
	}
	var out []string
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, _ := os.ReadFile(table)
		for _, line := range strings.Split(string(data), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) > 9 && f[3] == "0A" && inodes[f[9]] { // 0A is LISTEN
				out = append(out, f[1])
			}
		}
	}
	return out
}

// TestForwardReachesAPortOnAHost runs tuios forward over the real link: the
// ssh stand-in runs the real stdio-proxy against a second real daemon, and a
// program listens on 127.0.0.1 there.
//
// How this could fail, and what each subtest checks:
//   - bytes: a connection to the local address does not reach the far port,
//     or the answer does not come back. A line goes out and the far
//     program's answer must come back.
//   - through build: the forward dials the port on this machine and not
//     through the link. Both machines share one network in this test, so the
//     far daemon's log must name the connection.
//   - loopback: the forward listens on 0.0.0.0 or a public address. Every
//     LISTEN socket of the process must be 127.0.0.1 or ::1.
//   - closed: a port that nothing listens on gives a forward that accepts
//     and then drops every connection. tuios forward must fail at once and
//     say so.
//   - stop: Ctrl+C leaves the port bound or the process running.
//   - policy: a link without open still forwards. It must refuse and name
//     open.
//
// Negative control: on origin/main tuios has no forward command, so every
// subtest fails at its first step.
func TestForwardReachesAPortOnAHost(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	killDaemon(t, remote)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("start build's daemon: %v\n%s", err, out)
	}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start this machine's daemon: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")
	port := farEcho(t)

	cmd, addr := startForward(t, base, env, "build:"+strconv.Itoa(port), "--local", "0")

	t.Run("bytes", func(t *testing.T) {
		for _, word := range []string{"one", "two"} {
			if got := askThrough(t, addr, word); got != "far:"+word {
				dumpLinkLogs(t, base, remote)
				t.Fatalf("ASSERTION: through %s the far program answered %q, want %q", addr, got, "far:"+word)
			}
		}
	})

	t.Run("through build", func(t *testing.T) {
		log := filepath.Join(xdgDir(remote, "XDG_STATE_HOME"), "tuios", "daemon.log")
		want := "connected to port " + strconv.Itoa(port) + " on this machine"
		// One line for the check tuios forward makes at its start, and one
		// for each connection of the bytes subtest.
		waitUntil(t, "build's daemon logs each connection", 10*time.Second, func() (bool, string) {
			data, _ := os.ReadFile(log)
			n := strings.Count(string(data), want)
			return n >= 3, strconv.Itoa(n) + " lines " + strconv.Quote(want) + " in build's log, want 3"
		})
	})

	t.Run("loopback", func(t *testing.T) {
		addrs := listenAddrsOf(t, cmd.Process.Pid)
		if len(addrs) == 0 {
			t.Fatal("ASSERTION: tuios forward holds no LISTEN socket")
		}
		for _, a := range addrs {
			ip, _, _ := strings.Cut(a, ":")
			if ip != "0100007F" && ip != "00000000000000000000000001000000" {
				t.Fatalf("ASSERTION: tuios forward listens on %s, which is not 127.0.0.1 or ::1 (all: %v)", a, addrs)
			}
		}
	})

	t.Run("closed", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		closed := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		_, errs, code := runSplit(t, base, env, "forward", "build:"+strconv.Itoa(closed))
		if code == 0 || !strings.Contains(errs, "Nothing listens on port "+strconv.Itoa(closed)) {
			t.Fatalf("ASSERTION: a forward to a closed port exited %d with stderr %q; want a failure that names the port", code, errs)
		}
	})

	t.Run("stop", func(t *testing.T) {
		_ = cmd.Process.Signal(syscall.SIGINT)
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		var werr error
		waitUntil(t, "tuios forward exits after Ctrl+C", 10*time.Second, func() (bool, string) {
			select {
			case werr = <-exited:
				return true, ""
			default:
				return false, "still running"
			}
		})
		if werr != nil {
			t.Fatalf("ASSERTION: tuios forward exited with %v after Ctrl+C, want status 0", werr)
		}
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			_ = c.Close()
			t.Fatalf("ASSERTION: %s still takes connections after the forward stopped", addr)
		}
	})

	t.Run("policy", func(t *testing.T) {
		writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\"]\n")
		if out, err := tuiosCLI(t, remote, "config", "apply"); err != nil {
			t.Fatalf("config apply on build: %v\n%s", err, out)
		}
		_, errs, code := runSplit(t, base, env, "forward", "build:"+strconv.Itoa(port))
		if code == 0 || !strings.Contains(errs, "open") {
			t.Fatalf("ASSERTION: a link without open forwarded: exit %d, stderr %q", code, errs)
		}
	})
}
