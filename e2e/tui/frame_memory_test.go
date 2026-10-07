package tuie2e

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The daemon's memory against frames it is sent and does not get the rest of
// (internal/session/frame_budget.go). A frame header announces up to 16 MiB,
// and any process that can reach the socket can send one.
//
// daemonRSS is in perf_test.go.
//
// The wire numbers below are from internal/session's iota block, which is the
// wire format and cannot move. wireMsgError is in window_size_view_test.go.
const (
	wireMsgHello       = 1
	wireMsgList        = 5
	wireMsgInput       = 7
	wireMsgSessionList = 26
	wireMaxFrame       = 16 << 20
)

// frameSocket is the daemon socket of the isolation root base.
func frameSocket(base string) string {
	return filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
}

// rawFrame is a frame header of type typ announcing a payload of n bytes.
func rawFrame(typ byte, n int) []byte {
	h := make([]byte, 6)
	binary.BigEndian.PutUint32(h, uint32(2+n))
	h[4] = typ
	return h
}

// readReply reads one frame and returns its type and payload.
func readReply(conn net.Conn) (byte, []byte, error) {
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	head := make([]byte, 6)
	if _, err := io.ReadFull(conn, head); err != nil {
		return 0, nil, err
	}
	body := make([]byte, binary.BigEndian.Uint32(head)-2)
	if _, err := io.ReadFull(conn, body); err != nil {
		return 0, nil, err
	}
	return head[4], body, nil
}

// TestDaemonBoundsStalledFrames opens 32 connections that each announce a
// 16 MiB input frame, send 15 MiB of it and stall, and holds the daemon's
// resident memory to well under what they announced. Then it checks that a
// hello over its short limit is refused with the stream in step, that the
// daemon refuses connections past its cap, and that it still serves after.
//
// NEGATIVE CONTROL: on a build before frame_budget.go, the daemon allocates
// each announced body whole and the stalled frames take about 520 MB.
func TestDaemonBoundsStalledFrames(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads the daemon's memory from /proc")
	}
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "frames", "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	sock := frameSocket(base)
	var report strings.Builder
	defer func() {
		path := filepath.Join(artifactDir(t), "frame-memory.txt")
		_ = os.WriteFile(path, []byte(report.String()), 0o644)
		t.Logf("report: %s\n%s", path, report.String())
	}()

	// Stalled frames.
	const conns, sent = 32, 15 << 20
	before := int64(daemonRSS(t, base)) << 10
	fmt.Fprintf(&report, "rss before: %d MB\n", before>>20)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var held []net.Conn
	defer func() {
		for _, c := range held {
			_ = c.Close()
		}
	}()
	chunk := make([]byte, 64<<10)
	start := time.Now()
	for range conns {
		wg.Go(func() {
			c, err := net.Dial("unix", sock)
			if err != nil {
				t.Errorf("dial: %v", err)
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
			_ = c.SetWriteDeadline(time.Now().Add(20 * time.Second))
			if _, err := c.Write(rawFrame(wireMsgInput, wireMaxFrame-2)); err != nil {
				return
			}
			for left := sent; left > 0; left -= len(chunk) {
				if _, err := c.Write(chunk[:min(left, len(chunk))]); err != nil {
					return
				}
			}
		})
	}
	wg.Wait()
	fmt.Fprintf(&report, "the writes took %v\n", time.Since(start).Round(time.Millisecond))
	// The writes are done when the daemon has read the bytes, less what the
	// socket buffers hold. Sample over the budget wait and past it.
	var peak int64
	for range 16 {
		peak = max(peak, int64(daemonRSS(t, base))<<10)
		time.Sleep(250 * time.Millisecond)
	}
	grew := peak - before
	fmt.Fprintf(&report, "%d connections, each announcing 16 MiB and sending 15 MiB: peak rss %d MB, grew %d MB\n",
		conns, peak>>20, grew>>20)
	if grew > 200<<20 {
		t.Errorf("the daemon grew by %d MB holding %d stalled frames", grew>>20, conns)
	}
	for _, c := range held {
		_ = c.Close()
	}
	held = nil

	// A hello over the short limit is refused, and the frame after it read.
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	const hello = 1 << 20
	if _, err := c.Write(append(rawFrame(wireMsgHello, hello), make([]byte, hello)...)); err != nil {
		t.Fatalf("write the hello: %v", err)
	}
	if _, err := c.Write(rawFrame(wireMsgList, 0)); err != nil {
		t.Fatalf("write the list: %v", err)
	}
	typ, body, err := readReply(c)
	if err != nil || typ != wireMsgError || !strings.Contains(string(body), "refused") {
		t.Fatalf("a 1 MiB hello was answered with type %d %q, %v; want a refusal", typ, body, err)
	}
	if typ, _, err := readReply(c); err != nil || typ != wireMsgSessionList {
		t.Fatalf("the list after the refused hello was answered with type %d, %v", typ, err)
	}
	fmt.Fprintf(&report, "a 1 MiB hello: refused, and the next frame answered\n")

	// Connections past the cap are closed at once.
	capStart := time.Now()
	const many = 1100
	var open []net.Conn
	defer func() {
		for _, c := range open {
			_ = c.Close()
		}
	}()
	for range many {
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatalf("dial connection %d: %v", len(open)+1, err)
		}
		open = append(open, c)
	}
	var closedN atomic.Int64
	var reads sync.WaitGroup
	for _, c := range open {
		reads.Go(func() {
			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := c.Read(make([]byte, 1)); errors.Is(err, io.EOF) {
				closedN.Add(1)
			}
		})
	}
	reads.Wait()
	closed := int(closedN.Load())
	fmt.Fprintf(&report, "%d idle connections: %d closed by the daemon, rss %d MB, in %v\n", many, closed, daemonRSS(t, base)>>10, time.Since(capStart).Round(time.Millisecond))
	if closed < many-1024 {
		t.Errorf("the daemon closed %d of %d connections, want at least %d", closed, many, many-1024)
	}
	// The log is written behind the connections' own lines, so it is waited
	// for.
	logged := false
	for wait := time.Now().Add(10 * time.Second); !logged && time.Now().Before(wait); time.Sleep(100 * time.Millisecond) {
		log, _ := os.ReadFile(filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log"))
		logged = strings.Contains(string(log), "connections, which is the maximum")
	}
	if !logged {
		raw, _ := os.ReadFile(filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log"))
		_ = os.WriteFile(filepath.Join(artifactDir(t), "daemon.log"), raw, 0o644)
		t.Errorf("the daemon log says nothing of the refused connections\n%s", daemonLogTail(base, 20))
	}
	for _, c := range open {
		_ = c.Close()
	}
	open = nil

	lsStart := time.Now()
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := tuiosCLI(t, base, "ls")
		if err == nil && strings.Contains(out, "frames") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon does not serve after the connections closed: %v: %s", err, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Fprintf(&report, "after: tuios ls answers in %v\n", time.Since(lsStart).Round(time.Millisecond))
}
