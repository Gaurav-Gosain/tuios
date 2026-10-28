package tuie2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The limits TestSSHServerLimits configures. The handshake timeout is short so
// the test can wait it out.
const (
	sshLimitHandshake     = 3 * time.Second
	sshLimitMaxHandshakes = 4
)

// idleSSHConn is a client that sends its version line and nothing more. closed
// receives the time the server closed the connection.
type idleSSHConn struct {
	conn   net.Conn
	opened time.Time
	closed chan time.Time
}

// openIdleSSHConn connects to addr and sends only the version line. With
// wantBanner it waits for the server's version line, which the server sends
// only to a connection it admitted. Without, it returns at once.
func openIdleSSHConn(t *testing.T, addr string, wantBanner bool) (*idleSSHConn, string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ic := &idleSSHConn{conn: c, opened: time.Now(), closed: make(chan time.Time, 1)}
	if _, err := io.WriteString(c, "SSH-2.0-idle\r\n"); err != nil {
		t.Fatalf("write the version line: %v", err)
	}
	banner := ""
	if wantBanner {
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := bufio.NewReader(c).ReadString('\n')
		if err != nil {
			t.Fatalf("ASSERTION: an admitted connection got no version line from the server: %v", err)
		}
		banner = strings.TrimSpace(line)
		_ = c.SetReadDeadline(time.Time{})
	}
	go func() {
		_, _ = io.Copy(io.Discard, c)
		ic.closed <- time.Now()
	}()
	return ic, banner
}

// procStats is the number of open file descriptors, the number of PTY
// masters among them, and the resident set size in KiB, of process pid.
type procStats struct {
	fds, ptys int
	rssKiB    int
}

func readProcStats(t *testing.T, pid int) procStats {
	t.Helper()
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var st procStats
	st.fds = len(entries)
	for _, e := range entries {
		link, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err == nil && (link == "/dev/ptmx" || link == "/dev/pts/ptmx") {
			st.ptys++
		}
	}
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		t.Fatalf("read status of %d: %v", pid, err)
	}
	for line := range strings.SplitSeq(string(status), "\n") {
		if v, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			st.rssKiB, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(v), " kB"))
		}
	}
	return st
}

// sshKeyArgs are the ssh client flags for a login with key and nothing from
// the machine's own ssh config or known hosts.
func sshKeyArgs(key string, port int, user string) []string {
	return []string{"-tt", "-p", strconv.Itoa(port), "-i", key,
		"-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR", "-o", "BatchMode=yes", "-F", "/dev/null",
		user + "@127.0.0.1"}
}

// TestSSHServerLimits runs tuios ssh in daemon mode with short limits in the
// [ssh] section and key authentication, and checks the three limits a client
// that never logs in, or one session too many, runs into:
//
//   - Connections that send only the version line hold a slot each. One past
//     max_handshakes is closed before the server sends its own version line.
//     The ones admitted are closed after handshake_timeout.
//   - A key login still works after that, and stays open past the handshake
//     timeout, because the deadline ends when the login finishes.
//   - With max_sessions = 1 and that login open, a second login gets a message
//     and no session: the daemon has no session of that name and the server
//     process holds no new PTY.
//
// The descriptor counts and the resident size of the server are logged at
// each step.
func TestSSHServerLimits(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads the server's descriptors from /proc")
	}
	for _, bin := range []string{"ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("no %s", bin)
		}
	}

	base := t.TempDir()
	writeConfig(t, base, fmt.Sprintf(`[startup]
open_default_window = true

[ssh]
handshake_timeout = "%s"
max_handshakes = %d
max_sessions = 1
`, sshLimitHandshake, sshLimitMaxHandshakes))
	key := filepath.Join(base, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	authKeys := filepath.Join(base, "authorized_keys")
	if err := os.WriteFile(authKeys, pub, 0o600); err != nil {
		t.Fatal(err)
	}

	srv := startSSHServerIn(t, base, "daemon", "--authorized-keys", authKeys)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(srv.port))
	serverLog := func() string { data, _ := os.ReadFile(srv.log); return string(data) }

	// The probe connections of startSSHServerIn close at once; give their
	// slots time to come back before the count starts.
	time.Sleep(500 * time.Millisecond)
	baseline := readProcStats(t, srv.pid)
	t.Logf("baseline: %d fds, %d PTYs, %d KiB RSS", baseline.fds, baseline.ptys, baseline.rssKiB)

	// 1. Fill every handshake slot with a client that only says hello.
	idle := make([]*idleSSHConn, 0, sshLimitMaxHandshakes)
	for range sshLimitMaxHandshakes {
		ic, banner := openIdleSSHConn(t, addr, true)
		if !strings.HasPrefix(banner, "SSH-2.0-") {
			t.Fatalf("ASSERTION: an admitted connection got %q, not a version line", banner)
		}
		idle = append(idle, ic)
	}
	held := readProcStats(t, srv.pid)
	t.Logf("with %d idle connections: %d fds, %d KiB RSS", len(idle), held.fds, held.rssKiB)

	// 2. One more is closed at once, before the server says anything.
	extra, _ := openIdleSSHConn(t, addr, false)
	select {
	case <-extra.closed:
	case <-time.After(sshLimitHandshake / 2):
		t.Fatalf("ASSERTION: connection %d of %d with max_handshakes = %d was still open after %s\nserver log:\n%s",
			sshLimitMaxHandshakes+1, sshLimitMaxHandshakes+1, sshLimitMaxHandshakes, sshLimitHandshake/2, serverLog())
	}
	for i, ic := range idle {
		select {
		case <-ic.closed:
			t.Fatalf("ASSERTION: idle connection %d closed before the handshake timeout, at once with the extra one", i)
		default:
		}
	}

	// 3. The admitted ones are closed after the handshake timeout, not before.
	for i, ic := range idle {
		select {
		case at := <-ic.closed:
			after := at.Sub(ic.opened)
			t.Logf("idle connection %d closed after %s", i, after.Round(10*time.Millisecond))
			if after < sshLimitHandshake-500*time.Millisecond {
				t.Fatalf("ASSERTION: idle connection %d closed after %s, before the %s handshake timeout", i, after, sshLimitHandshake)
			}
		case <-time.After(time.Until(ic.opened.Add(sshLimitHandshake + 5*time.Second))):
			t.Fatalf("ASSERTION: idle connection %d was still open %s after it connected, with handshake_timeout = %s\nserver log:\n%s",
				i, time.Since(ic.opened).Round(time.Second), sshLimitHandshake, serverLog())
		}
	}
	time.Sleep(200 * time.Millisecond)
	after := readProcStats(t, srv.pid)
	t.Logf("after the timeout: %d fds, %d KiB RSS", after.fds, after.rssKiB)
	if after.fds > baseline.fds+2 {
		t.Fatalf("ASSERTION: the server holds %d fds after the idle connections closed, %d before they opened", after.fds, baseline.fds)
	}

	// 4. A key login works, and outlives the handshake timeout.
	home := xdgDir(base, "HOME")
	first := tuitest.StartT(t, append([]string{"ssh"}, sshKeyArgs(key, srv.port, "first")...),
		tuitest.WithSize(120, 40),
		tuitest.WithTerm("xterm-256color"),
		tuitest.WithEnv("HOME="+home),
		tuitest.WithDir(workDirIn(t, base)))
	if err := first.WaitForText("1:1", bootTimeout); err != nil {
		t.Fatalf("ASSERTION: the key login never drew the session: %v\n%s\nserver log:\n%s", err, first.Snapshot(), serverLog())
	}
	loggedIn := time.Now()
	deadline := time.Now().Add(bootTimeout)
	var withFirst procStats
	for {
		withFirst = readProcStats(t, srv.pid)
		if withFirst.ptys > baseline.ptys || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("with one session: %d fds, %d PTYs, %d KiB RSS", withFirst.fds, withFirst.ptys, withFirst.rssKiB)
	if withFirst.ptys <= baseline.ptys {
		t.Fatalf("ASSERTION: the first session opened no pane, so the PTY count proves nothing (%d before, %d now)",
			baseline.ptys, withFirst.ptys)
	}

	// 5. A second session is refused with a message, before it has a PTY.
	ctx, cancel := context.WithTimeout(context.Background(), bootTimeout)
	defer cancel()
	second := exec.CommandContext(ctx, "ssh", sshKeyArgs(key, srv.port, "second")...)
	second.Env = append(os.Environ(), "HOME="+home)
	out, err := second.CombinedOutput()
	t.Logf("second login: %v\n%s", err, out)
	if err == nil {
		t.Fatalf("ASSERTION: a second session with max_sessions = 1 ended without an error")
	}
	if !strings.Contains(string(out), "maximum number of sessions") {
		t.Fatalf("ASSERTION: a second session with max_sessions = 1 got no message about the limit:\n%s", out)
	}
	if got := readProcStats(t, srv.pid); got.ptys != withFirst.ptys {
		t.Fatalf("ASSERTION: the refused session changed the PTY count from %d to %d", withFirst.ptys, got.ptys)
	}
	ls, err := tuiosCLI(t, base, "ls", "--json")
	if err != nil {
		t.Fatalf("ls: %v: %s", err, ls)
	}
	var sessions []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(ls), &sessions); err != nil {
		t.Fatalf("ls --json: %v: %s", err, ls)
	}
	names := make([]string, 0, len(sessions))
	for _, s := range sessions {
		names = append(names, s.Name)
	}
	t.Logf("daemon sessions: %v", names)
	for _, n := range names {
		if n == "second" {
			t.Fatalf("ASSERTION: the refused login made the daemon session %q", n)
		}
	}

	// The first session is still open past the handshake timeout.
	time.Sleep(time.Until(loggedIn.Add(sshLimitHandshake + time.Second)))
	if st, exited := first.ExitStatus(); exited {
		t.Fatalf("ASSERTION: the first session ended (%v) after the handshake timeout\n%s", st, first.Snapshot())
	}
	if !strings.Contains(first.Screen().Text(), "1:1") {
		t.Fatalf("ASSERTION: the first session no longer draws after the handshake timeout\n%s", first.Snapshot())
	}
}
