package tuie2e

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The bridge's output path (#598 on main): an output frame is queued as a
// head and the daemon's own payload, not copied into one buffer, and the
// model's emulator of each pane drops its history, which the renderer keeps.
//
// What would pass a weaker test and fail this one: a frame whose body is
// lost, or written before its head, or interleaved with another frame (the
// stream of seq is not whole), and an emulator that stops following the pane
// once it drops its history (the title after the flood never arrives).

// TestGUIBridgeFloodArrivesWhole runs seq 1 100000 in a pane and reads the
// pane's output frames back: every line must be there, in order, with nothing
// between. A title set after the flood must reach the state.
func TestGUIBridgeFloodArrivesWhole(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	b.tiledPanes(1)
	st := b.waitState(func(s *wireState) bool {
		for _, w := range s.Windows {
			if w.PTY != "" {
				return true
			}
		}
		return false
	}, "a pane")
	var pty string
	for _, w := range st.Windows {
		if w.PTY != "" {
			pty = w.PTY
			break
		}
	}
	const n = 100000
	b.input(pty, "seq 1 "+strconv.Itoa(n)+"; printf '\\033]2;AFTER-FLOOD\\007'\r")

	var want strings.Builder
	for i := 1; i <= n; i++ {
		want.WriteString(strconv.Itoa(i))
		want.WriteString("\r\n")
	}
	tail := []byte("\r\n" + strconv.Itoa(n-1) + "\r\n" + strconv.Itoa(n) + "\r\n")
	b.waitFor(func() bool { return bytes.Contains(b.outputs[pty], tail) }, 60*time.Second, "the last line of seq")
	b.mu.Lock()
	got := append([]byte(nil), b.outputs[pty]...)
	b.mu.Unlock()
	if !bytes.Contains(got, []byte(want.String())) {
		at := bytes.Index(got, []byte("\r\n1\r\n2\r\n"))
		t.Fatalf("ASSERTION: the %d lines of seq did not arrive whole and in order (%d bytes of output, first line at %d)", n, len(got), at)
	}
	b.waitState(func(s *wireState) bool {
		for _, w := range s.Windows {
			if w.PTY == pty && w.Title == "AFTER-FLOOD" {
				return true
			}
		}
		return false
	}, "the title set after the flood")
	b.saveLog("flood-arrives-whole")
}
