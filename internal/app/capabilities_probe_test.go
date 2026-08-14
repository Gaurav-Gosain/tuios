package app

import (
	"os"
	"testing"
	"time"
)

// The capability probe used to cost a fixed half second of every start, and
// the reason was invisible from the outside: it waited for a count of
// terminator bytes, and two of the three replies it asked for never contain
// one. These pin the two properties that fixes it, that the read stops on the
// reply rather than on the clock, and that every answer is still read out of
// the single response that gets.

// probeReply feeds a canned terminal response through a pipe and returns what
// readTTYResponse made of it, and how long it waited. A pipe stands in for the
// tty because readTTYResponse only ever polls and reads a file descriptor.
//
// The write end is held open for the whole read, because a closed one reports
// EOF and would end the read for a reason a real terminal never supplies. That
// is exactly what the backstop case has to avoid being fooled by.
func probeReply(t *testing.T, reply string, timeout time.Duration) (string, time.Duration) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()

	if _, err := w.WriteString(reply); err != nil {
		t.Fatalf("write reply: %v", err)
	}

	start := time.Now()
	got := readTTYResponse(r, timeout, da1Response.MatchString)
	return got, time.Since(start)
}

func TestProbeStopsOnDA1(t *testing.T) {
	// The order a terminal answers in: the XTWINOPS replies it understood, the
	// kitty reply, then DA1 last because it was asked last.
	reply := "\x1b[4;1080;1920t\x1b[6;20;9t\x1b_Gi=1;OK\x1b\\\x1b[?62;4c"

	got, elapsed := probeReply(t, reply, probeTimeout)
	if got != reply {
		t.Fatalf("probe lost part of the reply:\n got %q\nwant %q", got, reply)
	}
	// The whole point is that the answer ends the read. A tenth of the backstop
	// is generous for a pipe and still fails loudly if the clock is back in
	// charge.
	if limit := probeTimeout / 10; elapsed > limit {
		t.Errorf("probe waited %s for a complete reply, expected well under %s", elapsed, limit)
	}
}

// TestProbeWithoutDA1SpendsBackstop documents the other side: a host that never
// identifies itself is the only case that costs the full timeout.
func TestProbeWithoutDA1SpendsBackstop(t *testing.T) {
	const short = 40 * time.Millisecond
	got, elapsed := probeReply(t, "\x1b[4;1080;1920t", short)
	if got != "\x1b[4;1080;1920t" {
		t.Fatalf("probe lost the reply it did get: %q", got)
	}
	// Not the timeout exactly: the poll wakes on the deadline it was handed, so
	// the last iteration can return a hair early.
	if floor := short - short/10; elapsed < floor {
		t.Errorf("probe returned after %s without a DA1 reply, expected it to wait out %s", elapsed, short)
	}
}

// TestProbeParsesOneResponse checks that folding four queries into one round
// trip did not cost any of the answers, which is the risk the merge carries.
func TestProbeParsesOneResponse(t *testing.T) {
	const reply = "\x1b[4;1080;1920t\x1b[6;20;9t\x1b_Gi=1;OK\x1b\\\x1b_Gi=2;OK\x1b\\\x1b[?62;4c"

	caps := &HostCapabilities{Cols: 207, Rows: 55}
	parsePixelGeometry(caps, reply)
	parseGraphicsSupport(caps, reply, true)

	for _, c := range []struct {
		what      string
		got, want int
	}{
		{"pixel width", caps.PixelWidth, 1920},
		{"pixel height", caps.PixelHeight, 1080},
		{"cell width", caps.CellWidth, 9},
		{"cell height", caps.CellHeight, 20},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.what, c.got, c.want)
		}
	}
	if !caps.SixelGraphics {
		t.Error("sixel not detected from DA1 parameter 4")
	}
	if !caps.KittyGraphics {
		t.Error("kitty graphics not detected from the i=1 reply")
	}
	if !caps.KittyFileTransfer {
		t.Error("kitty file transfer not detected from the i=2 reply")
	}
}

// TestProbeSilentHostFallsBack pins the behaviour of a terminal that answers
// nothing: no graphics claimed, and a cell size to render with anyway.
func TestProbeSilentHostFallsBack(t *testing.T) {
	caps := &HostCapabilities{Cols: 207, Rows: 55}
	parsePixelGeometry(caps, "")
	parseGraphicsSupport(caps, "", false)

	if caps.SixelGraphics || caps.KittyGraphics || caps.KittyFileTransfer {
		t.Errorf("silent host had graphics claimed for it: %+v", caps)
	}
	if caps.CellWidth == 0 || caps.CellHeight == 0 {
		t.Errorf("silent host left no cell size to render with: %dx%d", caps.CellWidth, caps.CellHeight)
	}
}
