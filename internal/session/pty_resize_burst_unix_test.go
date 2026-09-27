//go:build !windows

package session

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A new pane is resized several times in its first milliseconds, because
// every attached client announces the layout it drew. Each size change is a
// TIOCSWINSZ and so a SIGWINCH for the guest. macOS /bin/bash is bash 3.2.
// While it starts, readline sets LINES and COLUMNS through malloc, and the
// shell's SIGWINCH handler sets them again, also through malloc. A signal
// that lands in that moment deadlocks the shell on its own allocator lock,
// libplatform aborts it ("Trying to recursively lock an os_unfair_lock") and
// the kernel kills it. The pane dies a few milliseconds after it opened.
//
// The PTY now coalesces a burst of resizes: the first one reaches the kernel
// at once, and the rest reach it as one write of the last size once the shell
// has had time to start.
//
// The ways this could fail, written before the change:
//
//  1. The burst still reaches the guest as one SIGWINCH per resize, and bash
//     3.2 dies (the bug). Measured by counting shells killed by a signal.
//  2. The coalesced write never happens, so the guest keeps a size from the
//     middle of the burst while the emulator and every client use the last.
//  3. The first resize is held back, so a pane spawned at the wrong size waits
//     for the burst to end before its guest hears the right one.
//  4. Input typed after a resize reaches the guest before the held size does,
//     so `stty size` run straight after a resize prints the old size.
//  5. The held write fires after Close, against a closed pty.
//  6. Two clients resizing at once race the held size (run under -race).
//
// 1 is TestResizeBurstAtStartupLeavesBash32Alive. 2, 3 and 4 are
// TestResizeBurstReachesTheGuestFirstAndLast. 5 and 6 are the Close after
// each burst and the concurrent resizers in both tests.

// bash32 returns the path of a bash 3.x, the one whose SIGWINCH handling is not
// async-signal-safe, or "" when this machine has none.
func bash32() string {
	for _, path := range []string{"/bin/bash", "/usr/bin/bash", "/usr/local/bin/bash"} {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		out, err := exec.Command(path, "-c", "echo ${BASH_VERSINFO[0]}").Output()
		if err == nil && strings.TrimSpace(string(out)) == "3" {
			return path
		}
	}
	return ""
}

// signalled reports whether the pane's process was ended by a signal, which
// ExitStatus reports as -1.
func signalled(p *PTY) bool {
	code, exited := p.ExitStatus()
	return exited && code == -1
}

// resizeBurst resizes p the way a pane is resized as it opens: clients
// announcing sizes that disagree by a cell, several of them at once, for as
// long as span, gap apart. It returns the size the burst ended on.
func resizeBurst(p *PTY, clients int, span, gap time.Duration) (int, int) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	lastW, lastH := 0, 0
	end := time.Now().Add(span)
	for c := range clients {
		wg.Go(func() {
			for r := 0; time.Now().Before(end); r++ {
				w, h := 60+(r+c)%3, 16+(r+c)%2
				mu.Lock()
				_ = p.Resize(w, h)
				lastW, lastH = w, h
				mu.Unlock()
				if gap > 0 {
					time.Sleep(gap)
				}
			}
		})
	}
	wg.Wait()
	return lastW, lastH
}

// TestResizeBurstAtStartupLeavesBash32Alive opens bash 3.2 panes and resizes
// each one as fast as three clients can while it starts, then counts the
// shells a signal killed.
//
// The moment that kills the shell is a few microseconds long, so this is a
// count over many panes rather than one reproduction. Resizing without pause
// keeps signals arriving through the whole startup and loads the machine, which
// stretches that moment the way the loaded machine the crash was first seen
// on did. Before the change it killed 2 to 5 of every 400 to 1000 shells; see
// e2e/tui/NEGATIVE_CONTROLS.md. TUIOS_RESIZE_BURST_PANES raises the count for
// a longer campaign.
func TestResizeBurstAtStartupLeavesBash32Alive(t *testing.T) {
	if testing.Short() {
		t.Skip("opens hundreds of shells and resizes them without pause")
	}
	bash := bash32()
	if bash == "" {
		t.Skip("no bash 3.x on this machine; its SIGWINCH handling is what this reproduces")
	}
	panes := 400
	if n, err := strconv.Atoi(os.Getenv("TUIOS_RESIZE_BURST_PANES")); err == nil && n > 0 {
		panes = n
	}
	const parallel = 32

	d := NewDaemon(&DaemonConfig{})
	sess, err := d.manager.CreateSession("burst", &SessionConfig{Shell: bash}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(d.manager.Shutdown)

	var killed atomic.Int32
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i := range panes {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			p, err := sess.CreatePTY(fmt.Sprintf("burst-%d", i), 60, 16, nil)
			if err != nil {
				t.Errorf("CreatePTY: %v", err)
				return
			}
			resizeBurst(p, 3, 100*time.Millisecond, 0)
			// Long enough for the shell to finish starting and for a held
			// size to be written, so a signal it sends is counted too.
			time.Sleep(winsizeStartup + 50*time.Millisecond)
			if signalled(p) {
				killed.Add(1)
			}
			_ = p.Close()
		})
	}
	wg.Wait()

	t.Logf("%s: %d of %d shells killed by a signal during a startup resize burst", bash, killed.Load(), panes)
	if n := killed.Load(); n > 0 {
		t.Errorf("ASSERTION: %d of %d bash 3.2 shells died of a signal while their pane was resized at startup", n, panes)
	}
}

// TestResizeBurstReachesTheGuestFirstAndLast is what the coalescing must not
// cost: the first size at once, the last size in the end, and no keystroke
// ahead of a size asked for before it.
func TestResizeBurstReachesTheGuestFirstAndLast(t *testing.T) {
	_, sess := newTestDaemonSession(t)
	p, err := sess.CreatePTY("win-burst", 40, 12, func(string) {})
	if err != nil {
		t.Fatalf("CreatePTY: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	// 3: the first resize reaches the kernel before Resize returns.
	if err := p.Resize(50, 14); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if ws := readWinsize(t, p); ws.Col != 50 || ws.Row != 14 {
		t.Fatalf("ASSERTION: the first resize was held back: the guest sees %dx%d, want 50x14", ws.Col, ws.Row)
	}

	// 2: the rest of a burst lands, as its last size.
	lastW, lastH := resizeBurst(p, 3, 20*time.Millisecond, time.Millisecond)
	if w, h := p.Size(); w != lastW || h != lastH {
		t.Fatalf("the pane records %dx%d, want the burst's last %dx%d", w, h, lastW, lastH)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		ws := readWinsize(t, p)
		if int(ws.Col) == lastW && int(ws.Row) == lastH {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the guest was left at %dx%d, never told the burst's last size %dx%d", ws.Col, ws.Row, lastW, lastH)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 4: input written after a resize finds the guest at that size. Two
	// resizes back to back, so the second is one a burst holds.
	_ = p.Resize(30, 9)
	_ = p.Resize(31, 10)
	if _, err := p.Write([]byte("stty size\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if ws := readWinsize(t, p); ws.Col != 31 || ws.Row != 10 {
		t.Fatalf("ASSERTION: input reached the guest while it was still at %dx%d, before the held 31x10", ws.Col, ws.Row)
	}
}
