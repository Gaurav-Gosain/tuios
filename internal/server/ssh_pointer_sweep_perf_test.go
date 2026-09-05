package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// TestPerfSSHPointerSweep measures what a pointer sweep costs the server
// process over a real SSH session: CPU time per motion event, and the bytes
// the session writes back. It is a measurement, gated behind TUIOS_PERF=1, and
// asserts nothing about time. Run it on a tree with and without the motion
// filter in the shared program options to see what the filter saves:
//
//	TUIOS_PERF=1 go test -run TestPerfSSHPointerSweep -v ./internal/server/
//
// The sweep crosses the dock row, which is chrome: no clause of the filter
// claims it, so with the filter every event is dropped before Update and
// without it every event composes a frame the renderer then finds unchanged.
func TestPerfSSHPointerSweep(t *testing.T) {
	if os.Getenv("TUIOS_PERF") != "1" {
		t.Skip("set TUIOS_PERF=1 to measure the pointer sweep over SSH")
	}

	port := freePort(t)
	hostKey := filepath.Join(t.TempDir(), "test_host_key")
	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- StartSSHServer(ctx, &SSHServerConfig{
			Host: "127.0.0.1", Port: port, KeyPath: hostKey, Ephemeral: true, Version: "test",
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-serveErr:
		case <-time.After(5 * time.Second):
		}
	})

	clientCfg := &gossh.ClientConfig{User: "tester", HostKeyCallback: gossh.InsecureIgnoreHostKey(), Timeout: 2 * time.Second}
	addr := net.JoinHostPort("127.0.0.1", port)
	var client *gossh.Client
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := gossh.Dial("tcp", addr, clientCfg)
		if err == nil {
			client = c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("could not connect: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer func() { _ = client.Close() }()
	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if err := sess.RequestPty("xterm-256color", 40, 120, gossh.TerminalModes{gossh.ECHO: 0}); err != nil {
		t.Fatal(err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}

	var written atomic.Int64
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, err := stdout.Read(buf)
			written.Add(int64(n))
			if err != nil {
				return
			}
		}
	}()
	// Let the first frame and the capability probe settle.
	time.Sleep(1500 * time.Millisecond)

	const events = 3000
	before := cpuTime()
	bytesBefore := written.Load()
	start := time.Now()
	for i := range events {
		// SGR any-motion report, button 35 = motion with no button held, on
		// the dock row. One report per cell, the way a terminal sends them.
		_, _ = io.WriteString(stdin, fmt.Sprintf("\x1b[<35;%d;40M", 1+i%120))
		time.Sleep(200 * time.Microsecond)
	}
	// Drain: whatever the sweep queued has to be handled before the clock stops.
	time.Sleep(1500 * time.Millisecond)
	elapsed := time.Since(start)
	cpu := cpuTime() - before
	bytes := written.Load() - bytesBefore

	t.Logf("pointer sweep over SSH: %d events in %v, server CPU %v (%v per event), %d bytes written back",
		events, elapsed.Round(time.Millisecond), cpu.Round(time.Millisecond), (cpu / events).Round(time.Microsecond), bytes)
}

// cpuTime is this process's user plus system time.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
