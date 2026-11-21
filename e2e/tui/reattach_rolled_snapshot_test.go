package tuie2e

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	rolledHeaderTop  = 1
	rolledBodyTop    = 5
	rolledBodyBottom = 12
	rolledBodyCols   = 30
)

const rolledFloodBytes = 24 << 20

const rolledBodyMark = "BODYMARK-issue123"

var rolledPalette = []int{0, 52, 88, 124, 160, 196, 202, 208, 214, 220, 226, 231}

func writeRolledFlood(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create flood fixture: %v", err)
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriterSize(f, 1<<20)
	rng := rand.New(rand.NewSource(11))
	frames, written := 0, 0
	for written < rolledFloodBytes {
		frames++
		var b strings.Builder
		for y := rolledBodyTop; y < rolledBodyBottom; y++ {
			fmt.Fprintf(&b, "\x1b[%d;1H", y)
			last := -1
			for range rolledBodyCols {
				c := rolledPalette[rng.Intn(len(rolledPalette))]
				if c != last {
					fmt.Fprintf(&b, "\x1b[48;5;%dm", c)
					last = c
				}
				b.WriteByte(' ')
			}
			b.WriteString("\x1b[0m")
		}
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[0m%s", rolledBodyBottom, rolledBodyMark)
		fmt.Fprintf(&b, "\x1b[%d;1H", rolledBodyTop)
		n, err := w.WriteString(b.String())
		if err != nil {
			t.Fatalf("write flood fixture: %v", err)
		}
		written += n
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush flood fixture: %v", err)
	}
	return frames
}

// TestReattachKeepsSnapshotRowsWhenRingRolled is the issue #123 reproduction.
func TestReattachKeepsSnapshotRowsWhenRingRolled(t *testing.T) {
	const (
		hdrA = "HDRAAA-issue123"
		hdrB = "HDRBBB-issue123"
		hdrC = "HDRCCC-issue123"
	)

	base := t.TempDir()
	killDaemon(t, base)

	fixture := filepath.Join(t.TempDir(), "flood.bin")
	frames := writeRolledFlood(t, fixture)
	t.Logf("flood fixture: %d frames, %d bytes", frames, rolledFloodBytes)

	if out, err := tuiosCLI(t, base, "new", "e2e-rolled", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	first := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "e2e-rolled"}})
	if err := first.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("first client never attached: %v\n%s", err, first.Snapshot())
	}

	header := fmt.Sprintf(`printf '\033[%d;1H%s\033[%d;1H%s\033[%d;1H%s'`,
		rolledHeaderTop, hdrA, rolledHeaderTop+1, hdrB, rolledHeaderTop+2, hdrC)
	cmd := header + "; while :; do cat " + fixture + "; done"
	if err := first.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("start the flood: %v", err)
	}

	if err := first.WaitForText(hdrA, shellTimeout); err != nil {
		t.Fatalf("the header never appeared before the detach: %v\n%s", err, first.Snapshot())
	}
	if err := first.WaitForText(rolledBodyMark, soakTimeout); err != nil {
		t.Fatalf("the flood never started: %v\n%s", err, first.Snapshot())
	}
	if !strings.Contains(first.Screen().Text(), hdrA) {
		t.Fatalf("the flood scrolled the header off before any detach; the fixture geometry does not fit the pane\n%s",
			first.Snapshot())
	}

	time.Sleep(4 * time.Second)

	if err := first.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("send leader d: %v", err)
	}
	waitExit(t, first, "after leader d")

	if !sessionListed(t, base, "e2e-rolled") {
		out, _ := tuiosCLI(t, base, "ls")
		t.Fatalf("the session did not survive the detach\nls:\n%s", out)
	}

	second := startIn(t, base, startOpts{cols: 110, rows: 40, args: []string{"attach", "e2e-rolled"}})
	if err := second.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the reattached client never got its window back: %v\n%s", err, second.Snapshot())
	}

	if err := second.WaitForText(rolledBodyMark, soakTimeout); err != nil {
		t.Fatalf("the reattached pane never painted its body: %v\n%s", err, second.Snapshot())
	}

	deadline := time.Now().Add(uiTimeout)
	var missing []string
	for time.Now().Before(deadline) {
		text := second.Screen().Text()
		missing = missing[:0]
		for _, want := range []string{hdrA, hdrB, hdrC} {
			if !strings.Contains(text, want) {
				missing = append(missing, want)
			}
		}
		if len(missing) == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(missing) > 0 {
		t.Fatalf("the reattached pane lost header rows the snapshot held (issue #123): missing %v\n%s",
			missing, second.Snapshot())
	}
	alive(t, second, "after reattach over a rolled ring")
}
