package tuie2e

// A full-screen guest (yazi browsing a directory) draws a kitty image while
// the pane already has shell history. Placement lines are computed as
// ScrollbackLen()+cursorY, and the ghostty backend once answered with the
// alternate screen's empty history instead of the main screen's: every
// placement shifted by the pane's real history and the preview pane rendered
// blank. Panes without history were fine, which made it look intermittent.
// This guard runs that exact shape and asserts a placement reaches the host.

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

var placedAtRE = regexp.MustCompile(`\x1b\[(\d+);(\d+)H\x1b_G[^\x1b]*a=p[^\x1b]*\x1b\\`)

func TestKittyImageSurvivesAltScreenWithHistory(t *testing.T) {
	stream := &hostStream{}
	term, _ := start(t, startOpts{
		cols: 120, rows: 40,
		env: []string{"TUIOS_KITTY_GRAPHICS=1", "TUIOS_SIXEL_GRAPHICS=0", "TUIOS_DEBUG_INTERNAL=" + os.Getenv("TUIOS_DEBUG_INTERNAL")},
		out: stream,
	})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	// The pane earns real history first; without it half the bug hid.
	runInShell(t, term, "seq 1 200; echo HISTORYDONE", "HISTORYDONE", shellTimeout)

	// A yazi-shaped guest, compressed into one write the way a TUI paints
	// a frame: junk that parks the cursor at the bottom-right, the
	// alt-screen switch, a cursor move to the preview cell, and the image,
	// all in the same chunk. Any emulator state read at APC time that lags
	// the chunk (cursor, alt flag, history length) sends the placement to
	// wherever the previous frame finished, clipped in a corner - which on
	// screen is a blank preview pane.
	frame := kittyFrameFile(t, t.TempDir(), 64, 48)
	payload, err := os.ReadFile(frame)
	if err != nil {
		t.Fatal(err)
	}
	oneChunk := filepath.Join(t.TempDir(), "frame-chunk.bin")
	var buf bytes.Buffer
	buf.WriteString("\x1b[38;20Hjunk")
	buf.WriteString("\x1b[?1049h\x1b[2;2H")
	buf.Write(payload)
	buf.WriteString("ALTDRAWN\n")
	if err := os.WriteFile(oneChunk, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	stream.mark("altimage")
	typeLine(t, term, "cat "+oneChunk+"; sleep 5; printf '\\033[?1049l'")
	if err := term.WaitForText("ALTDRAWN", shellTimeout); err != nil {
		t.Fatalf("guest never drew: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(2 * time.Second)

	if dump := os.Getenv("TUIOS_KITTY_CAPTURE"); dump != "" {
		_ = os.WriteFile(dump, stream.bytes(), 0o644)
	}

	chunk := stream.bytes()
	if idx := markRE.FindAllIndex(chunk, -1); len(idx) > 0 {
		chunk = chunk[idx[len(idx)-1][1]:]
	}
	m := placedAtRE.FindSubmatch(chunk)
	if m == nil {
		t.Fatalf("no kitty placement reached the host after the alt-screen guest drew with pane history; the image pane is blank\n%s", term.Snapshot())
	}
	row, col := atoiB(t, m[1]), atoiB(t, m[2])
	// The guest drew at alt-screen row 2; the pane sits near the top of
	// the host screen. A placement computed against stale state lands at
	// the bottom-right, where the junk left the previous frame's cursor.
	if row > 20 || col > 60 {
		t.Fatalf("kitty placement landed at host row %d col %d, far from the pane's preview cell: placed against stale emulator state\n%q", row, col, m[0])
	}
}

func atoiB(t *testing.T, b []byte) int {
	t.Helper()
	n := 0
	for _, c := range b {
		n = n*10 + int(c-'0')
	}
	return n
}
