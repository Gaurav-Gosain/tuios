package server

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// releaseReading is one look at what the server process holds.
type releaseReading struct {
	goroutines int
	heap       uint64
}

// readRelease collects garbage twice, so what is left is what something still
// reaches, and reads the goroutine count and the live heap.
func readRelease() releaseReading {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return releaseReading{goroutines: runtime.NumGoroutine(), heap: ms.HeapAlloc}
}

func (r releaseReading) String() string {
	return fmt.Sprintf("goroutines=%d heap=%s", r.goroutines, mib(int64(r.heap)))
}

// mib renders a byte count in MiB.
func mib(n int64) string { return fmt.Sprintf("%.2f MiB", float64(n)/(1<<20)) }

// openReleaseTerm opens one more interactive session with a PTY on client,
// attached to the server's default tuios session, and returns it with a writer
// for its keyboard. What the far end writes is kept the same way
// startSSHExitRun keeps it, so waitForOutput works on it.
func openReleaseTerm(t *testing.T, client *gossh.Client, name string) (*sshExitRun, io.Writer) {
	t.Helper()
	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := sess.RequestPty("xterm-256color", 40, 120, gossh.TerminalModes{gossh.ECHO: 0}); err != nil {
		t.Fatalf("request pty: %v", err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatalf("start shell: %v", err)
	}
	term := &sshExitRun{session: sess, client: client, name: name,
		done: make(chan error, 1), readDone: make(chan struct{})}
	go func() {
		defer close(term.readDone)
		buf := make([]byte, 8192)
		for {
			n, rerr := stdout.Read(buf)
			if n > 0 {
				term.mu.Lock()
				term.seen.Write(buf[:n])
				term.mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
	go func() { term.done <- sess.Wait() }()
	return term, stdin
}

// waitForText waits, bounded, until want appears in what the far end wrote.
func waitForText(t *testing.T, term *sshExitRun, d time.Duration, want string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(term.text(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%q never reached the client within %s\n--- last output ---\n%s", want, d, tailPrintable(term.text()))
}

func typeLine(t *testing.T, stdin io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(stdin, line+"\r"); err != nil {
		t.Fatalf("type %q: %v", line, err)
	}
}

// TestSSHSessionsWithTheScreensaverGiveBackWhenTheyEnd runs the release check
// of the test below with the screen saver on at its shortest idle time, so
// every session arms a one-minute timer when it starts. Eight ended sessions
// must still be given back within 10 s, long before any of those timers fires.
// It runs first so that a run on code that still holds ended sessions until
// their timers fire stays readable: this test's sessions, held a minute, change
// nothing during the test below, while the test below's sessions, released over
// the following 30 s, would drop below this test's baseline.
func TestSSHSessionsWithTheScreensaverGiveBackWhenTheyEnd(t *testing.T) {
	const (
		clients        = 8
		goroutineSlack = 40
		heapSlack      = 8 << 20
		saverBound     = 50 * time.Second
	)
	// Registered before startSSHExitRun, so it runs after the server stops.
	t.Cleanup(func() { pinStartupConfig(os.Getenv("XDG_CONFIG_HOME")) })
	cfg := "[startup]\nopen_default_window = true\nstart_in_terminal_mode = true\n" +
		"[screensaver]\nenabled = true\nidle_minutes = 1\n"
	if err := os.WriteFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "tuios", "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	started := time.Now()
	run := startSSHExitRun(t, "release-saver")

	warm, warmIn := openReleaseTerm(t, run.client, run.name)
	warm.waitForOutput(t, 15*time.Second, "the warm-up client never painted a frame")
	warmBy := time.Now().Add(30 * time.Second)
	for !strings.Contains(warm.text(), "warm-0") {
		if time.Now().After(warmBy) {
			t.Fatalf("the warm-up line never printed warm-0\n--- last output ---\n%s", tailPrintable(warm.text()))
		}
		typeLine(t, warmIn, "\x15sleep 2; echo warm-$((0))")
		retypeAt := time.Now().Add(5 * time.Second)
		for time.Now().Before(retypeAt) && !strings.Contains(warm.text(), "warm-0") {
			time.Sleep(20 * time.Millisecond)
		}
	}
	base := readRelease()
	t.Logf("baseline: %s", base)

	cfgSSH := &gossh.ClientConfig{
		User:            "tuios",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}
	addr := run.client.RemoteAddr().String()
	conns := make([]*gossh.Client, 0, clients)
	terms := make([]*sshExitRun, 0, clients)
	var probeIn io.Writer
	for i := range clients {
		c, err := gossh.Dial("tcp", addr, cfgSSH)
		if err != nil {
			t.Fatalf("dial client %d: %v", i+1, err)
		}
		t.Cleanup(func() { _ = c.Close() })
		conns = append(conns, c)
		term, stdin := openReleaseTerm(t, c, run.name)
		terms = append(terms, term)
		if i == 0 {
			probeIn = stdin
		}
	}
	for i, term := range terms {
		term.waitForOutput(t, 15*time.Second, fmt.Sprintf("client %d never painted a frame", i+1))
	}

	// Positive half: a live client of this server still gets its pane's output.
	typeLine(t, probeIn, "sleep 2; echo probe-$((1))")
	waitForText(t, terms[0], 15*time.Second, "probe-1")
	t.Log("live session painted probe-1")
	t.Logf("live, %d clients up: %s", clients, readRelease())

	for _, c := range conns {
		_ = c.Close()
	}

	// Negative half: everything the eight sessions held is given back within
	// 10 s. The kept sessions' savers start a minute after the server did; a
	// run that reaches the last poll past saverBound measures them too.
	deadline := time.Now().Add(10 * time.Second)
	for poll := 1; ; poll++ {
		now := readRelease()
		dg := now.goroutines - base.goroutines
		dh := int64(now.heap) - int64(base.heap)
		t.Logf("poll %d: %s (goroutines %+d, heap %s over baseline)", poll, now, dg, mib(dh))
		released := dg <= goroutineSlack && dh <= heapSlack
		if released || time.Now().After(deadline) {
			elapsed := time.Since(started)
			t.Logf("last poll at %s after the server started", elapsed.Round(time.Millisecond))
			if elapsed > saverBound {
				t.Fatalf("test defect: the last poll came %s after the server started, past %s, so a kept session's screen saver may have run during the polls",
					elapsed.Round(time.Millisecond), saverBound)
			}
			if released {
				return
			}
			t.Fatalf("after %d clients ended, the server still holds %+d goroutines and %s of heap over the baseline (%s); allowed %d goroutines and %s",
				clients, dg, mib(dh), base, goroutineSlack, mib(heapSlack))
		}
		time.Sleep(time.Second)
	}
}

// TestSSHSessionsGiveBackTheirGoroutinesAndMemoryWhenTheyEnd attaches eight real
// SSH clients to one daemon session, proves the pane's output still reaches a
// live one twice, drops all eight, and waits for the server's goroutines and
// heap to come back to what they were before the eight connected.
func TestSSHSessionsGiveBackTheirGoroutinesAndMemoryWhenTheyEnd(t *testing.T) {
	const (
		clients        = 8
		goroutineSlack = 40
		heapSlack      = 8 << 20
	)
	form := "inside the whole package run"
	if f := flag.Lookup("test.run"); f != nil && f.Value.String() != "" {
		form = "alone, -run " + f.Value.String()
	}
	t.Logf("form: %s", form)

	run := startSSHExitRun(t, "release")

	// Warm up through the helper's own client, which stays connected, so the
	// daemon's and the first client's caches and queues have grown before the
	// baseline. A shell that is still starting can drop type-ahead, so the
	// line is typed again, cleared first with ctrl+u, until its output shows.
	warm, warmIn := openReleaseTerm(t, run.client, run.name)
	warm.waitForOutput(t, 15*time.Second, "the warm-up client never painted a frame")
	warmBy := time.Now().Add(30 * time.Second)
	for !strings.Contains(warm.text(), "warm-0") {
		if time.Now().After(warmBy) {
			t.Fatalf("the warm-up line never printed warm-0\n--- last output ---\n%s", tailPrintable(warm.text()))
		}
		typeLine(t, warmIn, "\x15sleep 2; echo warm-$((0))")
		retypeAt := time.Now().Add(5 * time.Second)
		for time.Now().Before(retypeAt) && !strings.Contains(warm.text(), "warm-0") {
			time.Sleep(20 * time.Millisecond)
		}
	}
	base := readRelease()
	t.Logf("baseline: %s", base)

	cfg := &gossh.ClientConfig{
		User:            "tuios",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}
	addr := run.client.RemoteAddr().String()
	conns := make([]*gossh.Client, 0, clients)
	terms := make([]*sshExitRun, 0, clients)
	var probeIn io.Writer
	for i := range clients {
		c, err := gossh.Dial("tcp", addr, cfg)
		if err != nil {
			t.Fatalf("dial client %d: %v", i+1, err)
		}
		t.Cleanup(func() { _ = c.Close() })
		conns = append(conns, c)
		term, stdin := openReleaseTerm(t, c, run.name)
		terms = append(terms, term)
		if i == 0 {
			probeIn = stdin
		}
	}
	for i, term := range terms {
		term.waitForOutput(t, 15*time.Second, fmt.Sprintf("client %d never painted a frame", i+1))
	}

	// Positive half: the line is printed two seconds after its Enter key's
	// frame, when the client has gone idle, so only pane output can paint it.
	// The second one proves the pane listener was armed again after the first.
	probe := terms[0]
	typeLine(t, probeIn, "sleep 2; echo probe-$((1))")
	waitForText(t, probe, 15*time.Second, "probe-1")
	t.Log("live session painted probe-1")
	typeLine(t, probeIn, "sleep 2; echo probe-$((2))")
	waitForText(t, probe, 15*time.Second, "probe-2")
	t.Log("live session painted probe-2")
	t.Logf("live, %d clients up: %s", clients, readRelease())

	for _, c := range conns {
		_ = c.Close()
	}

	// Negative half: everything the eight sessions held is given back within
	// 10 s of their clients closing.
	deadline := time.Now().Add(10 * time.Second)
	for poll := 1; ; poll++ {
		now := readRelease()
		dg := now.goroutines - base.goroutines
		dh := int64(now.heap) - int64(base.heap)
		t.Logf("poll %d: %s (goroutines %+d, heap %s over baseline)", poll, now, dg, mib(dh))
		if dg <= goroutineSlack && dh <= heapSlack {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %d clients ended, the server still holds %+d goroutines and %s of heap over the baseline (%s); allowed %d goroutines and %s",
				clients, dg, mib(dh), base, goroutineSlack, mib(heapSlack))
		}
		time.Sleep(time.Second)
	}
}
