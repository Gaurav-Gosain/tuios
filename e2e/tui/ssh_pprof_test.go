package tuie2e

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sshPprofModes are the two ways tuios ssh serves: with the daemon in its own
// process, and standalone with --ephemeral.
var sshPprofModes = []string{"daemon", "ephemeral"}

// sshPprofServer is a tuios ssh process started by startSSHPprofServer.
type sshPprofServer struct {
	pid  int
	port int
	log  string
}

// startSSHPprofServer starts tuios ssh for a fresh isolation root on a free
// loopback port, with no authentication, in the given mode, and with --pprof
// on pprofAddr when that is not empty. It returns once the SSH port accepts.
// The server's output is kept in the test's temp dir.
func startSSHPprofServer(t *testing.T, mode, pprofAddr string) sshPprofServer {
	t.Helper()
	args := []string{"--no-auth"}
	if pprofAddr != "" {
		args = append(args, "--pprof", pprofAddr)
	}
	return startSSHServerIn(t, t.TempDir(), mode, args...)
}

// startSSHServerIn starts tuios ssh for the isolation root base on a free
// loopback port, in the given mode, with extra after the address flags. The
// caller puts any config file in base first. It returns once the SSH port
// accepts.
func startSSHServerIn(t *testing.T, base, mode string, extra ...string) sshPprofServer {
	t.Helper()
	killDaemon(t, base)
	port := freePort(t)
	env := append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(base, key))
	}
	args := []string{"ssh", "--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"--key-path", filepath.Join(base, "hostkey")}
	if mode == "ephemeral" {
		args = append(args, "--ephemeral")
	}
	args = append(args, extra...)
	srv := exec.Command(tuiosBin, args...)
	srv.Env = env
	srv.Dir = workDirIn(t, base)
	logf, err := os.Create(filepath.Join(t.TempDir(), "ssh-server.log"))
	if err != nil {
		t.Fatal(err)
	}
	srv.Stdout, srv.Stderr = logf, logf
	if err := srv.Start(); err != nil {
		t.Fatalf("start tuios ssh: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Process.Kill()
		_ = srv.Wait()
		_ = logf.Close()
	})
	t.Logf("tuios ssh %s, log: %s", strings.Join(args[1:], " "), logf.Name())

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(bootTimeout)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(logf.Name())
			t.Fatalf("tuios ssh (%s) never listened on %s:\n%s", mode, addr, data)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The server falls back to ephemeral when it cannot start the daemon, and
	// a daemon-mode run that fell back would test the other mode twice.
	data, _ := os.ReadFile(logf.Name())
	if !strings.Contains(string(data), "(mode: "+mode+")") {
		t.Fatalf("tuios ssh did not start in %s mode:\n%s", mode, data)
	}
	return sshPprofServer{pid: srv.Process.Pid, port: port, log: logf.Name()}
}

// tcpListenPorts is the set of TCP ports, sorted, that the process pid holds
// a listening socket on. It matches the socket inodes under /proc/pid/fd to
// the LISTEN rows of /proc/pid/net/tcp and tcp6.
func tcpListenPorts(t *testing.T, pid int) []int {
	t.Helper()
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	fds, err := os.ReadDir(fdDir)
	if err != nil {
		t.Fatalf("read %s: %v", fdDir, err)
	}
	inodes := map[string]bool{}
	for _, fd := range fds {
		link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
		if err != nil {
			continue
		}
		if inode, ok := strings.CutPrefix(link, "socket:["); ok {
			inodes[strings.TrimSuffix(inode, "]")] = true
		}
	}
	var ports []int
	for _, table := range []string{"tcp", "tcp6"} {
		path := fmt.Sprintf("/proc/%d/net/%s", pid, table)
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		sc := bufio.NewScanner(f)
		sc.Scan() // the header
		for sc.Scan() {
			// sl local_address rem_address st tx:rx tr:when retrnsmt uid timeout inode
			fields := strings.Fields(sc.Text())
			if len(fields) < 10 || fields[3] != "0A" || !inodes[fields[9]] {
				continue
			}
			_, hexPort, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			p, err := strconv.ParseUint(hexPort, 16, 16)
			if err != nil {
				continue
			}
			if !slices.Contains(ports, int(p)) {
				ports = append(ports, int(p))
			}
		}
		err = sc.Err()
		_ = f.Close()
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
	}
	slices.Sort(ports)
	return ports
}

// TestSSHServesPprof runs tuios ssh with --pprof in each mode and reads its
// heap profile. The SSH server holds an emulator, a compositor and frame
// buffers for every connection, so it is a process a memory question is
// about; --pprof was a persistent flag that "tuios ssh" accepted and ignored.
// Once the profile is served, the process listens on the SSH port and the
// profile port and nothing else.
func TestSSHServesPprof(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads the server's listeners from /proc")
	}
	for _, mode := range sshPprofModes {
		t.Run(mode, func(t *testing.T) {
			pprofPort := freePort(t)
			addr := fmt.Sprintf("127.0.0.1:%d", pprofPort)
			srv := startSSHPprofServer(t, mode, addr)

			deadline := time.Now().Add(15 * time.Second)
			for {
				heap, err := pprofHeapInUse(addr)
				if err == nil {
					if heap == 0 {
						t.Fatalf("ASSERTION: tuios ssh (%s) reports an in-use heap of 0 bytes on %s", mode, addr)
					}
					break
				}
				if time.Now().After(deadline) {
					data, _ := os.ReadFile(srv.log)
					t.Fatalf("ASSERTION: tuios ssh (%s) never served /debug/pprof on %s: %v\n%s", mode, addr, err, data)
				}
				time.Sleep(100 * time.Millisecond)
			}

			ports := tcpListenPorts(t, srv.pid)
			t.Logf("tuios ssh (%s) with --pprof listens on %v", mode, ports)
			want := []int{srv.port, pprofPort}
			slices.Sort(want)
			if !slices.Equal(ports, want) {
				t.Fatalf("ASSERTION: tuios ssh (%s) with --pprof listens on %v, want the SSH port %d and the profile port %d",
					mode, ports, srv.port, pprofPort)
			}
		})
	}
}

// TestSSHWithoutPprofOpensNoProfileListener runs tuios ssh without --pprof in
// each mode and reads the TCP ports the process listens on, at once and a
// second later. The profiles have no authentication, and in daemon mode this
// process holds the host key and every remote user's panes, so a profile
// listener is opened only when asked for. TestSSHServesPprof is the positive
// half: the same fixture and listener probe, with the flag.
func TestSSHWithoutPprofOpensNoProfileListener(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads the server's listeners from /proc")
	}
	for _, mode := range sshPprofModes {
		t.Run(mode, func(t *testing.T) {
			srv := startSSHPprofServer(t, mode, "")
			want := []int{srv.port}
			for i := range 2 {
				if i > 0 {
					time.Sleep(time.Second)
				}
				ports := tcpListenPorts(t, srv.pid)
				t.Logf("tuios ssh (%s) without --pprof listens on %v", mode, ports)
				if !slices.Equal(ports, want) {
					t.Fatalf("ASSERTION: tuios ssh (%s) without --pprof listens on %v, want only the SSH port %d",
						mode, ports, srv.port)
				}
			}
		})
	}
}
