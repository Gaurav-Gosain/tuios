package tuie2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// ptyLogLimit is the most a client's raw PTY log holds. The log mirrors both
// directions of the PTY, and a client attached to a pane that streams kitty
// graphics wrote 473 MB of it in 19 s.
const ptyLogLimit = 4 << 20

// ptyLogTailOnFailure is how much of the log a failed test prints.
const ptyLogTailOnFailure = 2 << 10

// boundedLog is an append-only log file that keeps only its newest bytes.
//
// When the file grows past limit, it is rewritten to its last limit/2 bytes
// under a one-line header that counts what was dropped. The rewrite goes to a
// temporary file that is renamed over the log, so a test reading the log by
// path while the client writes sees the old file or the new one, never a
// half-written one. Rewriting half the limit each time keeps the extra I/O to
// about one and a half times what is written.
type boundedLog struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	size    int64
	limit   int64
	dropped int64
}

func newBoundedLog(path string, limit int64) (*boundedLog, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &boundedLog{path: path, f: f, limit: limit}, nil
}

func (l *boundedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return len(p), nil
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	if err != nil {
		return n, err
	}
	if l.size > l.limit {
		if err := l.compact(); err != nil {
			return n, fmt.Errorf("compact pty log: %w", err)
		}
	}
	return n, nil
}

// compact rewrites the log to its newest limit/2 bytes. l.mu is held.
func (l *boundedLog) compact() error {
	keep := l.limit / 2
	tail := make([]byte, keep)
	if _, err := l.f.ReadAt(tail, l.size-keep); err != nil {
		return err
	}
	l.dropped += l.size - keep
	header := fmt.Sprintf("[pty.log: %d older bytes dropped, the log keeps at most %d]\n", l.dropped, l.limit)
	tmp := l.path + ".tmp"
	nf, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := nf.WriteString(header); err != nil {
		_ = nf.Close()
		return err
	}
	if _, err := nf.Write(tail); err != nil {
		_ = nf.Close()
		return err
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = nf.Close()
		return err
	}
	_ = l.f.Close()
	l.f = nf
	l.size = int64(len(header)) + keep
	return nil
}

// Tail is the last n bytes of the log.
func (l *boundedLog) Tail(n int64) []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, _ := os.ReadFile(l.path)
	if int64(len(b)) > n {
		b = b[int64(len(b))-n:]
	}
	return b
}

func (l *boundedLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// shmPrefixEnv names the variable that gives the kitty stand-ins (./frameloop,
// ./placeholders, ./placeonce and ./shmstream) the prefix for the shared
// memory objects they create.
const shmPrefixEnv = "TUIOS_E2E_SHM_PREFIX"

var (
	shmPrefixes sync.Map // *testing.T to its prefix
	shmSeq      atomic.Int64
)

// standInShmPrefix is the prefix for the /dev/shm objects a stand-in creates
// in this test. The first call registers a cleanup that removes every object
// with the prefix.
//
// A stand-in removes its objects in a deferred call, which does not run when
// the process dies by a signal, and a signal is how the client's teardown ends
// it. Without this every run of a shared memory test left an object of a few
// megabytes in /dev/shm, which is RAM.
func standInShmPrefix(t *testing.T) string {
	if p, ok := shmPrefixes.Load(t); ok {
		return p.(string)
	}
	p := fmt.Sprintf("tuios-e2e-%d-%d-", os.Getpid(), shmSeq.Add(1))
	shmPrefixes.Store(t, p)
	t.Cleanup(func() {
		shmPrefixes.Delete(t)
		removeStandInShm(p)
	})
	return p
}

// removeStandInShm kills the stand-ins that own objects with prefix and then
// removes the objects. The cleanup can run before the client's teardown, while
// a stand-in still streams, so the owner dies first and cannot create another.
// The owner's pid is the number after the prefix in each object's name.
func removeStandInShm(prefix string) {
	matches, _ := filepath.Glob(filepath.Join("/dev/shm", prefix+"*"))
	for _, path := range matches {
		rest := strings.TrimPrefix(filepath.Base(path), prefix)
		head, _, _ := strings.Cut(rest, "-")
		pid, err := strconv.Atoi(head)
		if err != nil || pid <= 1 {
			continue
		}
		// Only a stand-in: a pid can be reused after its owner exits.
		cmdline, _ := os.ReadFile(filepath.Join("/proc", head, "cmdline"))
		for _, standIn := range []string{"frameloop", "placeholders", "placeonce", "shmstream", "framepace"} {
			if bytes.Contains(cmdline, []byte(standIn)) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				break
			}
		}
	}
	matches, _ = filepath.Glob(filepath.Join("/dev/shm", prefix+"*"))
	for _, path := range matches {
		_ = os.Remove(path)
	}
}

// startStream boots tuios against a kitty host and starts the frameloop
// stand-in at 60 frames a second in one tiled pane, still in terminal mode. It
// returns the path of the stand-in's geometry file, which is in its argv.
func startStream(t *testing.T, o startOpts, transport string) (*tuitest.Terminal, *kittyHost, string) {
	t.Helper()
	host := newKittyHost()
	o.cols, o.rows = 120, 40
	o.env = append(o.env, "TUIOS_SIXEL_GRAPHICS=0")
	o.out = host
	term, _ := start(t, o)
	host.answerProbe(t, term)
	waitBoot(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	waitWindowCount(t, term, 1, "one pane")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo IMAG\"\"EPANE", "IMAGEPANE", shellTimeout)
	geom, _, _, _, _ := startFrameloopOpts(t, term, 0, 60, transport)
	return term, host, geom
}

// killByArg sends SIGKILL to every process whose argv contains arg.
func killByArg(t *testing.T, arg string) {
	t.Helper()
	entries, _ := os.ReadDir("/proc")
	killed := 0
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if bytes.Contains(cmdline, []byte(arg)) {
			if syscall.Kill(pid, syscall.SIGKILL) == nil {
				killed++
			}
		}
	}
	if killed == 0 {
		t.Fatalf("no process has %q in its argv", arg)
	}
}

// A client's raw PTY log keeps its newest bytes and stays under its limit,
// however much the client writes. Inline base64 frames at 60 frames a second
// write megabytes a second into the log.
//
// NEGATIVE CONTROL: with startIn handing tuitest a plain os.Create file, the
// log grows past the limit and this fails. A log that drops every write past
// the limit fails it too: the newest bytes are missing.
func TestPtyLogStaysBounded(t *testing.T) {
	const limit = 256 << 10
	var logPath string
	_, host, geom := startStream(t, startOpts{logPath: &logPath, logLimit: limit}, "b64")
	time.Sleep(3 * time.Second)

	// End the stand-in and wait for the client to go quiet. With no input
	// sent after that, the newest bytes of the log are the newest bytes the
	// host was sent.
	killByArg(t, geom)
	var stream []byte
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		before := len(host.bytes())
		time.Sleep(500 * time.Millisecond)
		stream = host.bytes()
		if len(stream) == before {
			break
		}
	}
	newest := stream[max(0, len(stream)-4096):]
	time.Sleep(200 * time.Millisecond)

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat pty log: %v", err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read pty log: %v", err)
	}
	t.Logf("pty log is %d bytes with a limit of %d", info.Size(), limit)
	if info.Size() > limit {
		t.Fatalf("the pty log is %d bytes, over its limit of %d", info.Size(), limit)
	}
	if !bytes.Contains(raw, newest) {
		t.Fatalf("the log lost its newest bytes: the last %d bytes sent to the host are not in it", len(newest))
	}
	if !bytes.HasPrefix(raw, []byte("[pty.log: ")) {
		t.Fatalf("the log never dropped bytes, so the stream did not test the limit: it starts %q", raw[:min(len(raw), 80)])
	}
}

// The shared memory objects of a kitty stand-in are gone after the test, even
// when the stand-in dies by SIGKILL and its own deferred removal never runs.
//
// NEGATIVE CONTROL: with the cleanup in standInShmPrefix not registered, the
// object of the killed stand-in stays in /dev/shm and this fails.
func TestKittyStandInShmRemovedAfterTest(t *testing.T) {
	requireDevShm(t)
	var prefix string
	t.Run("stream", func(t *testing.T) {
		startStream(t, startOpts{}, "shm")
		prefix = standInShmPrefix(t)
		// The stand-in reports its geometry, which is what startStream waits
		// for, and only then creates its object. Wait for the object itself.
		var matches []string
		deadline := time.Now().Add(shellTimeout)
		for {
			matches, _ = filepath.Glob(filepath.Join("/dev/shm", prefix+"*"))
			if len(matches) > 0 || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(matches) == 0 {
			t.Fatalf("the stand-in made no object with prefix %q", prefix)
		}
		rest := strings.TrimPrefix(filepath.Base(matches[0]), prefix)
		head, _, _ := strings.Cut(rest, "-")
		pid, err := strconv.Atoi(head)
		if err != nil {
			t.Fatalf("no pid in object name %q", matches[0])
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			t.Fatalf("kill stand-in %d: %v", pid, err)
		}
		deadline = time.Now().Add(shellTimeout)
		for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		// The stand-in is gone and its object is not: the harness must
		// remove it.
		if _, err := os.Stat(matches[0]); err != nil {
			t.Fatalf("the object went away with the stand-in, so this test shows nothing: %v", err)
		}
	})
	if prefix == "" {
		t.Fatal("the subtest never started the stand-in")
	}
	if left, _ := filepath.Glob(filepath.Join("/dev/shm", prefix+"*")); len(left) > 0 {
		for _, p := range left {
			_ = os.Remove(p)
		}
		t.Fatalf("%d shared memory objects stayed in /dev/shm after the test: %v", len(left), left)
	}
}
