package session

// End-to-end cost of the wire: a real daemon on a real unix socket, a real
// TUIClient attached to it, and a real shell in a PTY. Everything between the
// PTY read and the client's output handler is measured, which is the whole of
// what this package owns on the output path. The client's emulator is left out
// on purpose: the handler counts bytes, so the number is the wire's and not the
// renderer's.
//
// The per-message benchmarks in wire_bench_test.go price the encoders; this
// prices the plumbing they sit in, which is where the syscalls are.

import (
	"bufio"
	"bytes"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// countingConn counts the reads and writes made on a connection, which on an
// unbuffered unix socket is the syscall count.
type countingConn struct {
	net.Conn
	reads, writes atomic.Int64
	readBytes     atomic.Int64
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.reads.Add(1)
	c.readBytes.Add(int64(n))
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	return c.Conn.Write(p)
}

// e2eRig is one daemon, one attached client and one pane running /bin/sh.
type e2eRig struct {
	d      *Daemon
	c      *TUIClient
	conn   *countingConn
	ptyID  string
	output chan []byte
}

// newE2ERig starts the daemon and attaches a client with a subscribed pane.
// The pane's handler forwards every batch it is handed to rig.output.
func newE2ERig(tb testing.TB) *e2eRig {
	tb.Helper()
	tb.Setenv("XDG_RUNTIME_DIR", tb.TempDir())
	tb.Setenv("SHELL", "/bin/sh")
	tb.Cleanup(useResurrectionDir(tb.TempDir()))

	d := NewDaemon(&DaemonConfig{Version: "bench", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		tb.Fatalf("daemon Start: %v", err)
	}
	tb.Cleanup(d.Stop)

	c := NewTUIClient()
	if err := c.Connect("bench", 200, 50); err != nil {
		tb.Fatalf("connect: %v", err)
	}
	tb.Cleanup(func() { _ = c.Close() })
	// Count at the socket. The reader is rebuilt over the counter, which is
	// safe only while it holds nothing, and at this point the daemon has
	// sent exactly the welcome the connect consumed.
	if n := c.br.Buffered(); n != 0 {
		tb.Fatalf("%d bytes buffered after connect; the counter would skip them", n)
	}
	cc := &countingConn{Conn: c.conn}
	c.conn = cc
	c.br = bufio.NewReaderSize(cc, clientReadBuffer)

	if _, err := c.AttachSession("bench", true, 200, 50); err != nil {
		tb.Fatalf("attach: %v", err)
	}
	c.StartReadLoop()

	ptyID, err := c.CreatePTY("bench", "", 200, 50)
	if err != nil {
		tb.Fatalf("create pty: %v", err)
	}
	rig := &e2eRig{d: d, c: c, conn: cc, ptyID: ptyID, output: make(chan []byte, 65536)}
	if err := c.SubscribePTY(ptyID, 0, false, func(data []byte) {
		cp := make([]byte, len(data))
		copy(cp, data)
		select {
		case rig.output <- cp:
		default:
		}
	}); err != nil {
		tb.Fatalf("subscribe: %v", err)
	}
	// Let the shell print its prompt and settle before anything is measured.
	rig.drain(300 * time.Millisecond)
	return rig
}

// drain discards output until the pane has been quiet for the given time.
func (r *e2eRig) drain(quiet time.Duration) {
	for {
		select {
		case <-r.output:
		case <-time.After(quiet):
			return
		}
	}
}

// run types a command and waits until marker appears in the output, returning
// how many bytes arrived and in how many batches. The shell echoes the command
// itself, so a caller spells the marker as two quoted halves that the shell
// joins: the echo then never contains it and only the output does.
func (r *e2eRig) run(tb testing.TB, cmd, marker string, timeout time.Duration) (bytesIn, batches int) {
	tb.Helper()
	if err := r.c.WritePTY(r.ptyID, []byte(cmd+"\n")); err != nil {
		tb.Fatalf("write: %v", err)
	}
	deadline := time.After(timeout)
	var tail []byte
	for {
		select {
		case b := <-r.output:
			bytesIn += len(b)
			batches++
			tail = append(tail, b...)
			if len(tail) > 4096 {
				tail = tail[len(tail)-4096:]
			}
			if bytes.Contains(tail, []byte(marker)) {
				return
			}
		case <-deadline:
			tb.Fatalf("no %q within %v (%d bytes in %d batches)", marker, timeout, bytesIn, batches)
		}
	}
}

// BenchmarkE2EFlood is a pane printing as fast as it can into one attached
// client: 16 MiB of 100-column lines per iteration. It reports the throughput
// the wire sustains, the batches the client took it in, and the syscalls the
// client spent reading it.
func BenchmarkE2EFlood(b *testing.B) {
	rig := newE2ERig(b)
	line := strings.Repeat("x", 99)
	const mib = 16
	cmd := "yes '" + line + "' | head -c " + itoaBench(mib<<20) + "; printf '\\nFLOOD''DONE\\n'"

	b.ResetTimer()
	var totalBytes, totalBatches int
	r0, w0 := rig.conn.reads.Load(), rig.conn.writes.Load()
	for b.Loop() {
		n, k := rig.run(b, cmd, "FLOODDONE", 60*time.Second)
		totalBytes += n
		totalBatches += k
	}
	b.StopTimer()
	reads := rig.conn.reads.Load() - r0
	writes := rig.conn.writes.Load() - w0
	b.SetBytes(int64(totalBytes / b.N))
	b.ReportMetric(float64(totalBatches)/float64(b.N), "batches/op")
	b.ReportMetric(float64(reads)/float64(totalBatches), "reads/batch")
	b.ReportMetric(float64(writes)/float64(b.N), "writes/op")
}

// BenchmarkE2EKeystroke is one keypress and its echo: the client writes one
// byte, the shell echoes it, and the daemon streams the echo back. The round
// trip is what a user waits for between pressing a key and seeing it, less the
// render. It reports the syscalls each side spent on it.
func BenchmarkE2EKeystroke(b *testing.B) {
	rig := newE2ERig(b)
	// Keep the shell's line buffer from growing: a line is cleared every 64
	// keys with ctrl-u, which itself echoes and is waited for like any key.
	b.ResetTimer()
	r0, w0 := rig.conn.reads.Load(), rig.conn.writes.Load()
	i := 0
	for b.Loop() {
		key := []byte("a")
		if i%64 == 63 {
			key = []byte{0x15}
		}
		i++
		if err := rig.c.WritePTY(rig.ptyID, key); err != nil {
			b.Fatal(err)
		}
		select {
		case <-rig.output:
		case <-time.After(5 * time.Second):
			b.Fatal("no echo")
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(rig.conn.reads.Load()-r0)/float64(b.N), "client-reads/key")
	b.ReportMetric(float64(rig.conn.writes.Load()-w0)/float64(b.N), "client-writes/key")
}

// TestE2ERigWorks keeps the rig honest: a change to attach, subscribe or the
// shell environment that breaks the fixture should fail here with a message
// rather than as a benchmark that never runs.
func TestE2ERigWorks(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not present")
	}
	rig := newE2ERig(t)
	n, _ := rig.run(t, "printf 'hello\\nRIG''DONE\\n'", "RIGDONE", 5*time.Second)
	if n == 0 {
		t.Fatal("rig delivered no output")
	}
}

// TestE2EIdleProbe holds the rig open and idle for TUIOS_PERF_IDLE_SECONDS,
// so a syscall counter run against the test binary can see what an attached
// client and its daemon do when nobody is typing. It is skipped unless asked
// for.
func TestE2EIdleProbe(t *testing.T) {
	secs := os.Getenv("TUIOS_PERF_IDLE_SECONDS")
	if secs == "" {
		t.Skip("set TUIOS_PERF_IDLE_SECONDS to run")
	}
	d, err := time.ParseDuration(secs + "s")
	if err != nil {
		t.Fatal(err)
	}
	rig := newE2ERig(t)
	rig.drain(500 * time.Millisecond)
	time.Sleep(d)
}

// TestIdleConnectionMakesNoReads pins the idle floor of the wire: a client
// attached to a daemon with nobody typing makes no read syscalls at all. Both
// read loops used to wake every 100 ms to look at their done channels, which
// was ten reads a second on each side of every connection, forever.
//
// The client side is counted at its socket. The daemon side is counted for
// the whole process through /proc/self/io, which is the only counter that
// sees the daemon's accepted socket: the two loops share one read helper, so
// a deadline put back on either side shows up here.
func TestIdleConnectionMakesNoReads(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell")
	}
	rig := newE2ERig(t)
	rig.drain(500 * time.Millisecond)

	const idle = 1500 * time.Millisecond
	r0 := rig.conn.reads.Load()
	s0, haveProc := processReadSyscalls()
	time.Sleep(idle)
	reads := rig.conn.reads.Load() - r0
	if reads != 0 {
		t.Errorf("the client read its socket %d times in %v with nothing arriving; an idle client must not poll", reads, idle)
	}
	if haveProc {
		s1, _ := processReadSyscalls()
		// A few reads are the runtime's own and the shell's; a 100 ms poll on
		// either loop is fifteen or more.
		if got := s1 - s0; got >= 10 {
			t.Errorf("the process made %d read syscalls in %v while idle; a read loop is polling", got, idle)
		}
	}
	// Silence is only the right answer from a connection that is still
	// there. A loop that timed out and gave up is silent too, and the
	// numbers above cannot tell the two apart.
	rig.run(t, "printf 'ali''ve\\n'", "alive", 5*time.Second)
}

// processReadSyscalls reads the process's read syscall count from
// /proc/self/io. The second result is false where that file does not exist.
func processReadSyscalls() (int64, bool) {
	data, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "syscr: "); ok {
			var n int64
			for _, c := range strings.TrimSpace(v) {
				n = n*10 + int64(c-'0')
			}
			return n, true
		}
	}
	return 0, false
}

// TestClientReadsAFrameInOneSyscall pins what the buffered reader is for: a
// frame that arrives whole is taken off the socket in one read. It used to be
// three, the length prefix, the two header bytes and the payload each read on
// their own, which for the echo of a keystroke was three syscalls where one
// does.
func TestClientReadsAFrameInOneSyscall(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell")
	}
	rig := newE2ERig(t)
	rig.drain(300 * time.Millisecond)

	const keys = 40
	r0 := rig.conn.reads.Load()
	batches := 0
	for range keys {
		if err := rig.c.WritePTY(rig.ptyID, []byte("a")); err != nil {
			t.Fatal(err)
		}
		select {
		case <-rig.output:
			batches++
		case <-time.After(5 * time.Second):
			t.Fatal("no echo")
		}
	}
	reads := rig.conn.reads.Load() - r0
	// One read per frame, with room for a frame the kernel split in two.
	// Three per frame is the shape being kept out.
	if reads > int64(batches)*2 {
		t.Errorf("%d frames took %d reads; a frame should take one", batches, reads)
	}
}
