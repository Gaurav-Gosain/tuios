//go:build !windows

package agentproto

import (
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestProcessHasNoTerminal: the agent cannot open the pane's terminal, so
// nothing it writes reaches the pane except through the transcript. Skipped
// where the test itself has no terminal, since nothing is proved there.
func TestProcessHasNoTerminal(t *testing.T) {
	if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err != nil {
		t.Skip("the test has no controlling terminal")
	} else {
		_ = f.Close()
	}
	p, err := StartProcess([]string{"/bin/sh", "-c", "if (: > /dev/tty) 2>/dev/null; then echo tty; else echo none; fi"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	out, _ := io.ReadAll(p.Stdout)
	if got := strings.TrimSpace(string(out)); got != "none" {
		t.Fatalf("the agent could open /dev/tty (%q)", got)
	}
}

// TestProcessStopEndsWhatItStarted: Stop kills the agent's process group, so a
// command it left running goes with it.
func TestProcessStopEndsWhatItStarted(t *testing.T) {
	p, err := StartProcess([]string{"/bin/sh", "-c", "sleep 60 & echo $!; wait"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, _ := p.Stdout.Read(buf)
	pid := strings.TrimSpace(string(buf[:n]))
	p.Stop()
	deadline := time.Now().Add(5 * time.Second)
	n, err = strconv.Atoi(pid)
	if err != nil {
		t.Fatalf("pid %q: %v", pid, err)
	}
	for {
		// Signal 0 checks the process exists. A killed orphan is reaped
		// by init, so it stops existing shortly after.
		if syscall.Kill(n, 0) != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent's child %s outlived Stop", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
