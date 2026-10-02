//go:build linux || darwin

package session

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The helper process these tests start: this test binary, run again with
// TUIOS_HELPER_SOCK set, as a stand in for an agent calling the socket. It is
// started inside a pane, or detached from everything, and writes what the
// daemon told it to TUIOS_HELPER_OUT.
const (
	helperSockEnv = "TUIOS_HELPER_SOCK"
	helperOutEnv  = "TUIOS_HELPER_OUT"
	helperModeEnv = "TUIOS_HELPER_MODE"
	helperArgsEnv = "TUIOS_HELPER_ARGS"
	// helperDetachEnv makes the helper wait until it has been reparented.
	helperDetachEnv = "TUIOS_HELPER_DETACH"
	// helperSizeEnv is the size an attaching helper reports, WxH.
	helperSizeEnv = "TUIOS_HELPER_SIZE"
)

// ppidAtStart is the helper's parent when the binary started.
var ppidAtStart = os.Getppid()

// TestHelperSocketCaller is not a test. It is the body of the helper process,
// and skips when it is not one.
func TestHelperSocketCaller(t *testing.T) {
	sock := os.Getenv(helperSockEnv)
	if sock == "" {
		t.Skip("only runs as a helper process")
	}
	out := os.Getenv(helperOutEnv)
	// A detached helper waits for the shell that started it to exit, so it
	// dials as an orphan and not as that shell's child.
	if os.Getenv(helperDetachEnv) != "" && ppidAtStart != 1 {
		deadline := time.Now().Add(5 * time.Second)
		for os.Getppid() == ppidAtStart && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	var result string
	switch os.Getenv(helperModeEnv) {
	case "attach-host":
		result = helperAttachHost(sock)
	case "attach", "attach-served", "attach-force", "attach-probe", "attach-served-probe", "attach-hold", "attach-late-probe":
		mode := os.Getenv(helperModeEnv)
		// The size the client attaches at, WxH, 80x24 when unset.
		width, height := 80, 24
		if size := os.Getenv(helperSizeEnv); size != "" {
			_, _ = fmt.Sscanf(size, "%dx%d", &width, &height)
		}
		c := NewTUIClient()
		c.Served = strings.HasPrefix(mode, "attach-served")
		c.AllowNested = mode == "attach-force"
		if strings.HasSuffix(mode, "-probe") {
			// Written to this process's terminal, as a client writes it.
			c.SetNestProbe(WriteNestProbe(os.Stdout))
		}
		conn, err := net.DialTimeout("unix", sock, 3*time.Second)
		if err != nil {
			result = "dial: " + err.Error()
			break
		}
		c.conn = conn
		if err := c.handshake("test", width, height, nil); err != nil {
			result = "handshake: " + err.Error()
			break
		}
		if _, err := c.AttachSession(os.Getenv(helperArgsEnv), false, width, height); err != nil {
			result = "attach: " + err.Error()
			break
		}
		result = "nonce:" + c.HumanNonce()
		if strings.HasSuffix(mode, "-probe") {
			// The daemon does not wait for the probe, so a client can attach
			// before its probe reaches a pane and be taken off after. Report
			// the attach, then wait to be taken off, and report the reason to
			// out.ended.
			ended := make(chan string, 1)
			c.OnSessionEnded(func(_, reason string) { ended <- reason })
			c.StartReadLoop()
			if err := os.WriteFile(out+".tmp", []byte(result), 0o600); err == nil {
				_ = os.Rename(out+".tmp", out)
			}
			select {
			case reason := <-ended:
				_ = os.WriteFile(out+".ended", []byte(reason+"|"+c.NestedRefusal()), 0o600)
			case <-time.After(time.Minute):
			}
			return
		}
		if mode == "attach-hold" {
			// Stays attached, so a later attach sees this client in the
			// chain. The pane closing with its daemon ends it.
			if err := os.WriteFile(out+".tmp", []byte(result), 0o600); err == nil {
				_ = os.Rename(out+".tmp", out)
			}
			time.Sleep(time.Minute)
		}
		_ = c.Close()
	default:
		conn, err := net.DialTimeout("unix", sock, 3*time.Second)
		if err != nil {
			result = "dial: " + err.Error()
			break
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte(os.Getenv(helperArgsEnv) + "\n")); err != nil {
			result = "write: " + err.Error()
			break
		}
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			result = "read: " + err.Error()
			break
		}
		result = line
		_ = conn.Close()
	}
	if err := os.WriteFile(out+".tmp", []byte(result), 0o600); err == nil {
		_ = os.Rename(out+".tmp", out)
	}
}

// helperCommand is the shell line that runs the helper with mode and args,
// writing to out.
func helperCommand(t *testing.T, sock, out, mode, args string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	return helperSockEnv + "=" + q(sock) + " " + helperOutEnv + "=" + q(out) + " " +
		helperModeEnv + "=" + q(mode) + " " + helperArgsEnv + "=" + q(args) + " " +
		q(exe) + " -test.run='^TestHelperSocketCaller$'"
}

// waitHelper waits for the helper to write its result.
func waitHelper(t *testing.T, out string) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the helper process wrote nothing")
	return ""
}

func skipWithoutPeerPID(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the peer pid is read on Linux and macOS only")
	}
}

// TestPeerPIDIsTheCaller checks the kernel's record: a connection made from
// this process reports this process.
func TestPeerPIDIsTheCaller(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	c.call(t, `{"id":1,"verb":"hello","params":{"protocol":1}}`)
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	found := false
	for _, cs := range d.clients {
		if cs.peerPID == os.Getpid() {
			found = true
		}
	}
	if !found {
		t.Errorf("no connection reports pid %d, the process that dialed", os.Getpid())
	}
}

// TestAPaneAttachIsIssuedNoNonce covers the route around the refusal: an agent
// that attaches from its pane to get the nonce the person's replies carry. The
// attach is to another session, because an attach to the pane's own session is
// refused outright (nested_attach.go).
func TestAPaneAttachIsIssuedNoNonce(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	sess, _, b := twoWindowSession(t, d, "nonce")
	makeSessionWithWindow(t, d, "nonce-other")
	out := filepath.Join(t.TempDir(), "out")
	runInPane(t, d, sess, b, helperCommand(t, sp, out, "attach", "nonce-other"))
	if got := waitHelper(t, out); got != "nonce:" {
		t.Errorf("an attach from a pane got %q, want no nonce", got)
	}
}

// TestPaneOriginOfThisProcess: the daemon's own process, which is where an
// in-process client runs, is not inside a pane, and a pane's shell is.
func TestPaneOriginOfThisProcess(t *testing.T) {
	skipWithoutPeerPID(t)
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "origin")
	if from, why := d.paneOrigin(os.Getpid()); from {
		t.Errorf("the daemon's own process counts as a pane: %s", why)
	}
	pty := sess.GetPTY(sess.GetState().Windows[0].PTYID)
	if pty == nil || pty.ShellPID() <= 0 {
		t.Fatal("the window has no shell")
	}
	if from, _ := d.paneOrigin(pty.ShellPID()); !from {
		t.Error("a pane's shell does not count as inside a pane")
	}
	// A pid that is not running fails closed.
	if from, why := d.paneOrigin(1 << 30); !from || why != paneOriginUnreadable {
		t.Errorf("an unreadable pid: from %v (%s), want inside a pane, unreadable", from, why)
	}
}
