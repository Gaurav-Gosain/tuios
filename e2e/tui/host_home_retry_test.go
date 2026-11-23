package tuie2e

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestComingHomeFromAHostAsksForNoRetry: a click on this machine's session
// while the client is on a linked machine brings it home, and asks this
// machine's daemon to dial no link for it. This machine has no link. The
// daemon answers a retry for it without a word in its log, so the test reads
// what the client sends: the client gets a runtime folder of its own, and a
// recorder on the socket there passes every connection on to the daemon and
// keeps what the client wrote. The way out to build must still work, and the
// session at home must be the one the rail marks.
//
// How this could pass wrongly, written down first:
//   - The recorder could be out of the client's path. The rail asks for
//     list-host-sessions, and that line must be in the record.
//   - The recorder could miss a retry. The click out to build sends
//     retry-host for build, and that line must be in the record.
//   - The retry could be sent late and land after the check. It is sent at
//     the click; the check starts only after the switch home has landed and a
//     later call from a new process (tuios call list-keys) has reached the
//     record, then holds for a second. That is a margin of time, not an order.
//   - The click could never leave home, so there is no way home to look at.
//     The rail must mark far-shell before the click back.
//
// Negative controls: NEGATIVE_CONTROLS.md, "Coming home from a host asks for
// no retry".
func TestComingHomeFromAHostAsksForNoRetry(t *testing.T) {
	requireReal(t, "another tuios daemon on its own isolation root, reached through the ssh stand-in")
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	// This machine's daemon, on the isolation root's own runtime folder.
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "start-server"); err != nil {
		t.Fatalf("start-server: %v\n%s", err, out)
	}
	daemonSock := hubSocket(base)
	waitUntil(t, "this machine's daemon listens", uiTimeout, func() (bool, string) {
		conn, err := net.DialTimeout("unix", daemonSock, time.Second)
		if err != nil {
			return false, err.Error()
		}
		_ = conn.Close()
		return true, ""
	})

	// The client's own runtime folder, with the recorder on its socket.
	clientRuntime := t.TempDir()
	if err := os.Mkdir(filepath.Join(clientRuntime, "tuios"), 0o700); err != nil {
		t.Fatalf("make the client's socket folder: %v", err)
	}
	rec := startTrafficRecorder(t, filepath.Join(clientRuntime, "tuios", "tuios.sock"), daemonSock)
	clientEnv := []string{"XDG_RUNTIME_DIR=" + clientRuntime}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: append(append([]string{}, env...), clientEnv...)})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)
	names := []string{hostOpen + " local", "home", hostOpen + " build", "far-shell"}
	for _, want := range names {
		railShows(t, term, want)
	}
	waitRailCurrent(t, term, "home")
	rows := railRows(term.Screen(), names...)

	waitUntil(t, "the recorder holds the rail's list-host-sessions", uiTimeout, func() (bool, string) {
		return len(rec.linesWith(`"verb":"list-host-sessions"`)) > 0, rec.calls()
	})

	// The way out works, and its retry for build is seen.
	mouseClick(t, term, 3, rows["far-shell"], tuitest.MouseLeft, 0)
	waitRailCurrent(t, term, "far-shell")
	waitUntil(t, "the recorder holds the retry-host for build the click out sent", uiTimeout, func() (bool, string) {
		return len(rec.linesWith(`"verb":"retry-host"`, `"host":"build"`)) > 0, rec.calls()
	})

	// And home, through this machine's row on the rail.
	mouseClick(t, term, 3, rows["home"], tuitest.MouseLeft, 0)
	waitRailCurrent(t, term, "home")
	alive(t, term, "after coming home from build")

	// A call of the test's own, from a new process, after the click home.
	out, err := tuiosCLIEnv(t, base, clientEnv, "call", "list-keys")
	if err != nil {
		t.Fatalf("tuios call list-keys through the recorder: %v\n%s", err, out)
	}
	waitUntil(t, "the recorder holds the test's list-keys, sent after the click home", uiTimeout, func() (bool, string) {
		return len(rec.linesWith(`"verb":"list-keys"`)) > 0, rec.calls()
	})

	stayTrue(t, "this machine's daemon was asked for no retry of a link on the way home", time.Second, func() (bool, string) {
		found := rec.linesWith(`"verb":"retry-host"`, `"host":"local"`)
		return len(found) == 0, strings.Join(found, "\n")
	})

	saveFrame(t, term, "home-from-host")
	if !railCurrentIs(term.Screen(), "home") {
		t.Fatalf("ASSERTION: the rail no longer marks home as the attached session:\n%s", term.Snapshot())
	}
	alive(t, term, "after the checks on the recorded calls")
}

// trafficRecorder listens on a socket and passes each connection on to a
// daemon's socket, keeping every byte the connecting side wrote, one record
// for each connection, so lines of two connections never mix.
type trafficRecorder struct {
	mu    sync.Mutex
	sent  []*bytes.Buffer
	conns []net.Conn
}

// startTrafficRecorder listens at path and dials daemonSock once for each
// connection it accepts. The test's cleanup closes the listener and every
// connection, and the copies end with their connections.
func startTrafficRecorder(t *testing.T, path, daemonSock string) *trafficRecorder {
	t.Helper()
	if len(path) > unixSocketPathMax {
		t.Fatalf("the recorder's socket %s is %d bytes, over the %d the suite lets a socket have", path, len(path), unixSocketPathMax)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on the recorder's socket: %v", err)
	}
	r := &trafficRecorder{}
	t.Cleanup(func() {
		_ = ln.Close()
		calls := r.calls()
		r.mu.Lock()
		defer r.mu.Unlock()
		t.Logf("the recorder passed on %d connections, with these calls:\n%s", len(r.sent), calls)
		for _, c := range r.conns {
			_ = c.Close()
		}
	})
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			daemon, err := net.Dial("unix", daemonSock)
			if err != nil {
				_ = client.Close()
				continue
			}
			rec := &recordedConn{r: r, buf: &bytes.Buffer{}}
			r.mu.Lock()
			r.conns = append(r.conns, client, daemon)
			r.sent = append(r.sent, rec.buf)
			r.mu.Unlock()
			go func() {
				_, _ = io.Copy(client, daemon)
				_ = client.Close()
			}()
			go func() {
				_, _ = io.Copy(daemon, io.TeeReader(client, rec))
				_ = daemon.Close()
			}()
		}
	}()
	return r
}

// recordedConn keeps what one client connection sent.
type recordedConn struct {
	r   *trafficRecorder
	buf *bytes.Buffer
}

func (c *recordedConn) Write(p []byte) (int, error) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	return c.buf.Write(p)
}

// linesWith is every recorded line that holds all of subs. A connection that
// carries an attach also carries binary frames, so a line is cut to start at
// the call it holds.
func (r *trafficRecorder) linesWith(subs ...string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var found []string
	for _, buf := range r.sent {
		for line := range strings.SplitSeq(buf.String(), "\n") {
			if i := strings.Index(line, `{"id":`); i > 0 {
				line = line[i:]
			}
			all := true
			for _, s := range subs {
				all = all && strings.Contains(line, s)
			}
			if all {
				found = append(found, line)
			}
		}
	}
	return found
}

// calls is every recorded verb call, for a failure or the log to show.
func (r *trafficRecorder) calls() string {
	return strings.Join(r.linesWith(`"verb":`), "\n")
}
