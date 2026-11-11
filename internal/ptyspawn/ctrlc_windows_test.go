//go:build windows

package ptyspawn

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestPaneGetsCtrlCInANewProcessGroup is issue #1668: Ctrl+C in a pane did
// not stop the pane's program when a daemon ran the pane, and did with
// --standalone. The daemon starts with CREATE_NEW_PROCESS_GROUP, which turns
// Ctrl+C handling off for it, and the programs it starts in panes inherit
// that.
//
// The test runs this test binary the way the daemon is started, in a new
// process group with a console of its own and no window, and the child opens
// a pane running ping -t and types Ctrl+C into it. Without AllowCtrlC ping must
// keep running, which shows the child has the daemon's problem. With it ping
// must stop.
func TestPaneGetsCtrlCInANewProcessGroup(t *testing.T) {
	for _, tc := range []struct {
		name  string
		allow bool
		want  string
	}{
		{"without AllowCtrlC the pane ignores Ctrl+C", false, "PANE-STOPPED=false"},
		{"with AllowCtrlC the pane stops on Ctrl+C", true, "PANE-STOPPED=true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestPaneCtrlCChild$", "-test.v", "-test.count=1")
			cmd.Env = append(os.Environ(), "TUIOS_CTRLC_CHILD=1", "TUIOS_CTRLC_ALLOW="+strconv.FormatBool(tc.allow))
			cmd.SysProcAttr = &syscall.SysProcAttr{
				CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
				HideWindow:    true,
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("the child failed: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("ASSERTION: the child did not report %s\n%s", tc.want, out)
			}
		})
	}
}

// TestPaneCtrlCChild is the child TestPaneGetsCtrlCInANewProcessGroup starts.
// It prints PANE-STOPPED=true when ping ended within five seconds of Ctrl+C,
// and PANE-STOPPED=false when it was still running.
func TestPaneCtrlCChild(t *testing.T) {
	if os.Getenv("TUIOS_CTRLC_CHILD") != "1" {
		t.Skip("run by TestPaneGetsCtrlCInANewProcessGroup")
	}
	if os.Getenv("TUIOS_CTRLC_ALLOW") == "true" {
		if err := AllowCtrlC(); err != nil {
			t.Fatalf("AllowCtrlC: %v", err)
		}
	}
	pty, cmd, err := Spawn(80, 24, command("ping", "-t", "127.0.0.1"), func(string, ...any) {})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = pty.Close()
	})

	// Read the pane for as long as it runs: ping blocks on a full pane, and
	// it prints its totals when Ctrl+C stops it.
	var (
		mu  sync.Mutex
		out []byte
	)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pty.Read(buf)
			mu.Lock()
			out = append(out, buf[:n]...)
			mu.Unlock()
			if bytes.Contains(buf[:n], []byte("\x1b[c")) {
				_, _ = pty.Write([]byte("\x1b[?62;4;22c"))
			}
			if err != nil {
				return
			}
		}
	}()
	printed := func() []byte {
		mu.Lock()
		defer mu.Unlock()
		return bytes.Clone(out)
	}

	// Two lines that name the address: ping's header and its first reply, so
	// ping is in its loop before Ctrl+C is typed.
	deadline := time.Now().Add(15 * time.Second)
	for bytes.Count(printed(), []byte("127.0.0.1")) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("ping never printed a reply: %q", printed())
		}
		time.Sleep(50 * time.Millisecond)
	}

	exited := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(exited)
	}()
	if _, err := pty.Write([]byte{0x03}); err != nil {
		t.Fatalf("typing Ctrl+C: %v", err)
	}
	select {
	case <-exited:
		t.Log("PANE-STOPPED=true")
	case <-time.After(5 * time.Second):
		t.Log("PANE-STOPPED=false")
	}
}
