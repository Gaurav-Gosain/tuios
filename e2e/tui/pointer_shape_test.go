package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// The host terminal's pointer shape is not part of the grid, so no screen
// assertion can reach it. What the host is told is OSC 22 on the wire, and the
// shape it is showing at any moment is the last OSC 22 it was sent. These tests
// read that off the recorded PTY stream, the same thing the user's eye reports.
var osc22RE = regexp.MustCompile(`\x1b\]22;([a-z-]+)\x1b\\`)

// hostPointerShape is the shape the host is currently showing: the last OSC 22
// anywhere in the stream so far, or "" if the host was never sent one.
func hostPointerShape(stream []byte) string {
	m := osc22RE.FindAllSubmatch(stream, -1)
	if len(m) == 0 {
		return ""
	}
	return string(m[len(m)-1][1])
}

// waitPointerShape polls the host stream until the last OSC 22 is want.
func waitPointerShape(t *testing.T, stream *hostStream, want, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	got := ""
	for time.Now().Before(deadline) {
		if got = hostPointerShape(stream.bytes()); got == want {
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatalf("%s: the host pointer shape is %q, want %q\nOSC 22 the host was sent: %q",
		what, got, want, osc22RE.FindAllString(string(stream.bytes()), -1))
}

// TestBlurRetiresThePointerShape is the reported bug: hovering a divider sends
// OSC 22 resize, and the terminal paints the last shape it was sent until a new
// one arrives. Clicking another application and coming back left the resize
// arrows standing over pane content, because nothing restated the shape and no
// motion had come to do it either.
//
// The fix retires the shape on blur. The assertion is the last OSC 22 on the
// wire: after focus is lost it must be default, so a terminal that repaints on
// refocus shows arrows over nothing.
func TestBlurRetiresThePointerShape(t *testing.T) {
	// focus-follows-mouse is what lets bare motion over a divider reach the
	// pointer-shape code at all: the motion whitelist passes it so hover can
	// move focus, and the same pass is what puts the resize shape on the wire.
	base := t.TempDir()
	cfgDir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	cfg := "[appearance]\nfocus_follows_mouse = true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	stream := &hostStream{}
	term := startIn(t, base, startOpts{cols: 120, rows: 40, out: stream})
	waitBoot(t, term)
	time.Sleep(1 * time.Second)

	// A press that lands in the boot churn was observed to vanish, leaving one
	// window where two were asked for, so press until the dock agrees and let
	// waitWindowCount have the final word.
	deadline := time.Now().Add(3 * uiTimeout)
	for countWindows(term.Screen()) < 2 && time.Now().Before(deadline) {
		if err := term.Type("n"); err != nil {
			t.Fatalf("pressing new window: %v", err)
		}
		time.Sleep(700 * time.Millisecond)
	}
	enableTiling(t, term)
	waitWindowCount(t, term, 2, "pointer-blur setup")

	// Put a resize shape on the wire: hover the panes' edges, where
	// UpdatePointerForPosition offers a resize shape. The layout's exact edge
	// cells are its own, so sweep the border columns and rows and settle for
	// whatever resize shape lands.
	shape := ""
	for _, cell := range [][2]int{
		{0, 0}, {0, 10}, {119, 10}, {60, 0}, {30, 0}, {90, 0},
		{0, 1}, {119, 1}, {60, 1}, {30, 1}, {90, 1},
	} {
		col, row := cell[0], cell[1]
		mouseHover(t, term, col, row)
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if got := hostPointerShape(stream.bytes()); got != "" && got != "default" {
				shape = got
				break
			}
			time.Sleep(60 * time.Millisecond)
		}
		if shape != "" {
			break
		}
	}
	if shape == "" {
		t.Fatalf("no hover produced a resize shape\nOSC 22 the host was sent: %q",
			osc22RE.FindAllString(string(stream.bytes()), -1))
	}

	// The terminal loses focus, as a click into another application does.
	if err := term.Type("\x1b[O"); err != nil {
		t.Fatalf("sending focus lost: %v", err)
	}
	waitPointerShape(t, stream, "default", "after the host lost focus")
}
