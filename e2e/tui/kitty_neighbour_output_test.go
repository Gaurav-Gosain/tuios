package tuie2e

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// hostStream records the raw PTY traffic with phase markers written into it, so
// a graphics command can be attributed to what the pane beside it was doing.
type hostStream struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

const phaseMark = "\x00\x00PHASE:"

func (h *hostStream) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf.Write(p)
	return len(p), nil
}

func (h *hostStream) mark(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf.WriteString(phaseMark + name + "\x00\x00")
}

func (h *hostStream) bytes() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]byte(nil), h.buf.Bytes()...)
}

var (
	placementRE = regexp.MustCompile(`\x1b\[(\d+);(\d+)H\x1b_G(a=p[^\x1b]*)\x1b\\`)
	markRE      = regexp.MustCompile(phaseMark + `([a-z-]+)\x00\x00`)
)

// placementsByPhase returns, per phase, the distinct placements emitted in it.
// A placement is its cursor position plus its parameters, which together are the
// whole of what the host is told about where an image goes and how big it is.
func placementsByPhase(stream []byte) map[string][]string {
	out := map[string][]string{}
	prevEnd, prevName := 0, "boot"
	add := func(name string, chunk []byte) {
		seen := map[string]bool{}
		for _, m := range placementRE.FindAllSubmatch(chunk, -1) {
			p := fmt.Sprintf("@%s,%s %s", m[1], m[2], m[3])
			if !seen[p] {
				seen[p] = true
				out[name] = append(out[name], p)
			}
		}
		sort.Strings(out[name])
	}
	for _, m := range markRE.FindAllSubmatchIndex(stream, -1) {
		add(prevName, stream[prevEnd:m[0]])
		prevEnd, prevName = m[1], string(stream[m[2]:m[3]])
	}
	add(prevName, stream[prevEnd:])
	return out
}

// kittyFrameFile writes one kitty direct-transmission frame (a=T, reused image
// id, zlib RGBA), the shape terminal-browser emits for every rendered frame.
func kittyFrameFile(t *testing.T, dir string, wpx, hpx int) string {
	t.Helper()
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	px := []byte{0x30, 0x60, 0xc0, 0xff}
	for range wpx * hpx {
		_, _ = w.Write(px)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("compress frame: %v", err)
	}
	payload := base64.StdEncoding.EncodeToString(z.Bytes())

	var out strings.Builder
	const chunk = 4096
	for i := 0; i < len(payload); i += chunk {
		end := min(i+chunk, len(payload))
		more := 0
		if end < len(payload) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(&out, "\x1b_Ga=T,f=32,o=z,s=%d,v=%d,t=d,i=1,p=1,C=1,q=2,m=%d;%s\x1b\\",
				wpx, hpx, more, payload[i:end])
			continue
		}
		fmt.Fprintf(&out, "\x1b_Gm=%d;%s\x1b\\", more, payload[i:end])
	}
	path := filepath.Join(dir, "frame.esc")
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	return path
}

// TestKittyPlacementSurvivesNeighbourOutput is the reported scenario: an image
// live in one tiled pane while the pane beside it prints. The placement the host
// is given while the neighbour is busy must be the one it is given when nothing
// else is happening, and the one a click's full redraw produces. Anything else
// is an image drawn to the wrong rectangle - stretched, or spilling over the
// pane beside it - which is what the report describes and what only a click
// repaired.
func TestKittyPlacementSurvivesNeighbourOutput(t *testing.T) {
	stream := &hostStream{}
	term, _ := start(t, startOpts{
		cols: 120, rows: 40,
		env: []string{"TUIOS_KITTY_GRAPHICS=1", "TUIOS_SIXEL_GRAPHICS=0"},
		out: stream,
	})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	waitWindowCount(t, term, 2, "two tiled panes")

	// The image pane is the right one, as in the report.
	frame := kittyFrameFile(t, t.TempDir(), 522, 720)
	mouseClick(t, term, 90, 12, tuitest.MouseLeft, 0)
	time.Sleep(400 * time.Millisecond)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo IMAGEPANE", "IMAGEPANE", shellTimeout)
	typeLine(t, term, "while :; do cat "+frame+"; sleep 0.05; done")
	leaveTerminalMode(t, term)

	stream.mark("image-only")
	time.Sleep(3 * time.Second)

	// The neighbour starts printing.
	mouseClick(t, term, 20, 12, tuitest.MouseLeft, 0)
	time.Sleep(400 * time.Millisecond)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo TEXTPANE", "TEXTPANE", shellTimeout)
	typeLine(t, term, "while :; do seq 1 60; sleep 0.05; done")
	leaveTerminalMode(t, term)

	stream.mark("neighbour-busy")
	time.Sleep(4 * time.Second)

	// The repair in the report: click back into the image pane.
	mouseClick(t, term, 90, 12, tuitest.MouseLeft, 0)
	stream.mark("after-click")
	time.Sleep(3 * time.Second)

	if dump := os.Getenv("TUIOS_KITTY_CAPTURE"); dump != "" {
		if err := os.WriteFile(dump, stream.bytes(), 0o644); err != nil {
			t.Fatalf("write capture: %v", err)
		}
	}

	byPhase := placementsByPhase(stream.bytes())
	quiet := byPhase["image-only"]
	if len(quiet) == 0 {
		t.Fatalf("no placement was emitted while the image pane was the only busy one; "+
			"phases: %v\n%s", byPhase, term.Snapshot())
	}
	if len(quiet) > 1 {
		t.Fatalf("the image pane alone emitted %d different placements with nothing "+
			"moving: %v", len(quiet), quiet)
	}
	for _, phase := range []string{"neighbour-busy", "after-click"} {
		got := byPhase[phase]
		if len(got) == 0 {
			continue // no frame reached the host in that window; nothing to compare
		}
		if len(got) != 1 || got[0] != quiet[0] {
			t.Fatalf("placement changed in phase %q with no geometry change:\n got=%v\nwant=%v",
				phase, got, quiet)
		}
	}
}

// typeLine types a command and runs it without waiting for output, for the
// endless loops these panes are given.
func typeLine(t *testing.T, term *tuitest.Terminal, cmd string) {
	t.Helper()
	if err := term.SendKeys(cmd); err != nil {
		t.Fatalf("type %q: %v", cmd, err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter after %q: %v", cmd, err)
	}
}
