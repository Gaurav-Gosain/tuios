package app

import (
	"os"
	"strings"
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

// The animation probe used to be a one-pixel image with a one-pixel patch, and
// it could not fail. The patch covered the whole image, so a host that read s=
// and v= as the patch rectangle and a host that read them as the image's new
// size both answered OK, and the second one is exactly the host the probe
// existed to catch. These pin the two answers it now takes.

// hostReply builds the graphics half of a probe response: an answer to the
// direct-transmission probe, then to each half of the animation probe, then
// DA1.
func hostReply(accept, reject string) string {
	r := "\x1b_Gi=1;OK\x1b\\"
	if accept != "" {
		r += "\x1b_Gi=3,r=1;" + accept + "\x1b\\"
	}
	if reject != "" {
		r += "\x1b_Gi=4,r=1;" + reject + "\x1b\\"
	}
	return r + "\x1b[?62;4c"
}

func TestAnimationProbeTakesTwoAnswers(t *testing.T) {
	const outOfBounds = "EINVAL:Frame width 9 larger than image width: 4"
	cases := []struct {
		name    string
		reply   string
		want    bool
		because string
	}{
		{
			name:    "kitty",
			reply:   hostReply("OK", outOfBounds),
			want:    true,
			because: "it patched a rectangle and still refused a frame wider than the image",
		},
		{
			name:  "host that resizes the image to the patch",
			reply: hostReply("EINVAL:Frame width 4 larger than image width: 1", outOfBounds),
			want:  false,
			because: "the one-pixel patch left it holding a one-pixel image, " +
				"so a four-pixel frame no longer fits",
		},
		{
			name:    "relay that acknowledges everything",
			reply:   hostReply("OK", "OK"),
			want:    false,
			because: "a frame nine pixels wide cannot fit an image four pixels wide, so OK is a rubber stamp",
		},
		{
			name:    "host that ignores frame edits",
			reply:   hostReply("", ""),
			want:    false,
			because: "silence is not a claim of support",
		},
		{
			name:    "host that answers only the half it likes",
			reply:   hostReply("OK", ""),
			want:    false,
			because: "an unanswered refusal is not a refusal",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var caps HostCapabilities
			parseGraphicsSupport(&caps, tc.reply, false)
			if !caps.KittyGraphics {
				t.Fatalf("probe reply did not even claim kitty graphics: %q", tc.reply)
			}
			if caps.KittyAnimation != tc.want {
				t.Errorf("KittyAnimation = %v, want %v: %s", caps.KittyAnimation, tc.want, tc.because)
			}
		})
	}
}

// TestAnimationProbeAcceptAloneIsNotEnough is the negative control written out.
// Both of these replies pass the old rule, which was a single OK for id 3, and
// only one of them is a host that can carry a frame edit.
func TestAnimationProbeAcceptAloneIsNotEnough(t *testing.T) {
	rubberStamp := hostReply("OK", "OK")
	if !kittyProbeOK(rubberStamp, animationProbeAccept) {
		t.Fatal("a relay that answers OK to everything answers OK to the accept half too")
	}
	var caps HostCapabilities
	parseGraphicsSupport(&caps, rubberStamp, false)
	if caps.KittyAnimation {
		t.Error("claimed animation support from a relay that rubber-stamps every command")
	}
}

// TestCellSizeOverrideReplacesTheHostsAnswer pins TUIOS_CELL_SIZE. A host that
// does not answer the pixel-geometry query gets a guessed cell, and everything
// tuios draws in cells is then the wrong shape. This is how a user says the
// real number, and it is what the end-to-end tests set so an assertion about a
// placement box is arithmetic rather than a guess about the terminal running
// them.
//
// Negative control: with the applyCellSizeOverride call removed from
// applyEnvironmentOverrides, the first case keeps the guessed 9x20 and fails.
func TestCellSizeOverrideReplacesTheHostsAnswer(t *testing.T) {
	for _, tc := range []struct {
		spec   string
		w, h   int
		accept bool
	}{
		{spec: "10x22", w: 10, h: 22, accept: true},
		{spec: " 7 x 15 ", w: 7, h: 15, accept: true},
		{spec: "10X22", w: 10, h: 22, accept: true},
		{spec: "", accept: false},
		{spec: "10", accept: false},
		{spec: "0x22", accept: false},
		{spec: "-4x22", accept: false},
		{spec: "wide x tall", accept: false},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			caps := &HostCapabilities{CellWidth: 9, CellHeight: 20, Cols: 80, Rows: 24}
			applyCellSizeOverride(caps, tc.spec)
			if !tc.accept {
				if caps.CellWidth != 9 || caps.CellHeight != 20 {
					t.Fatalf("%q was accepted and set the cell to %dx%d",
						tc.spec, caps.CellWidth, caps.CellHeight)
				}
				return
			}
			if caps.CellWidth != tc.w || caps.CellHeight != tc.h {
				t.Fatalf("%q gave a %dx%d cell, want %dx%d",
					tc.spec, caps.CellWidth, caps.CellHeight, tc.w, tc.h)
			}
			// The window's pixel size is derived from it, so the two cannot
			// disagree about how big the screen is.
			if caps.PixelWidth != tc.w*caps.Cols || caps.PixelHeight != tc.h*caps.Rows {
				t.Errorf("the window is %dx%d px for a %dx%d cell on %dx%d cells",
					caps.PixelWidth, caps.PixelHeight, tc.w, tc.h, caps.Cols, caps.Rows)
			}
		})
	}
}

// TestAnimationProbeFitsOneEscapeEach checks the probe's own escapes, because a
// payload split across m= continuations is not a frame edit: the continuation
// carries no a= key, so the terminal routes it to the transmit handler.
func TestAnimationProbeFitsOneEscapeEach(t *testing.T) {
	var q strings.Builder
	writeAnimationProbe(&q)
	seqs := strings.SplitSeq(strings.TrimSuffix(q.String(), "\x1b\\"), "\x1b\\")
	n := 0
	for seq := range seqs {
		n++
		params, payload, ok := strings.Cut(strings.TrimPrefix(seq, "\x1b_G"), ";")
		if !ok {
			params, payload = strings.TrimPrefix(seq, "\x1b_G"), ""
		}
		if strings.Contains(params, "m=") {
			t.Errorf("probe escape %q is chunked", params)
		}
		if len(payload) > 4096 {
			t.Errorf("probe escape %q carries %d base64 bytes, more than one escape holds",
				params, len(payload))
		}
	}
	if n == 0 {
		t.Fatal("probe wrote nothing")
	}
}
