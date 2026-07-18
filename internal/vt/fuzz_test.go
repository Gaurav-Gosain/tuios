package vt_test

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// fuzzSeeds are byte streams drawn from sequences a real guest program emits,
// plus the hostile shapes a previous review flagged: unbounded repeat counts,
// oversized graphics payloads and truncated introducers that leave the parser
// mid-state.
var fuzzSeeds = []string{
	// Plain text and control characters.
	"hello world",
	"a\tb\tc\r\n",
	"\x07\x08\x0b\x0c\x0e\x0f",
	// UTF-8: multibyte, combining marks, wide characters, invalid encodings.
	"\xe4\xb8\x96\xe7\x95\x8c",
	"e\xcc\x81\xcc\x82\xcc\x83",
	"\xf0\x9f\x91\x8d\xf0\x9f\x8f\xbd",
	"\xff\xfe\xfd",
	"\xc0\x80\xe0\x80\x80\xf0\x80\x80\x80",
	"\xf4\x90\x80\x80",
	// SGR.
	"\x1b[0m\x1b[1;4;7m\x1b[31;42m",
	"\x1b[38;2;255;128;0m\x1b[48;5;123m",
	"\x1b[38;2;99999999;99999999;99999999m",
	// Cursor motion and erase.
	"\x1b[10;20H\x1b[2J\x1b[K\x1b[3J",
	"\x1b[999999999;999999999H",
	"\x1b[-1;-1H",
	// Scroll regions.
	"\x1b[5;10r\x1b[1;1r\x1b[0;0r",
	// Insert/delete/repeat: unbounded counts were flagged as a hazard.
	"\x1b[999999999b",
	"X\x1b[2147483647b",
	"\x1b[999999999L\x1b[999999999M",
	"\x1b[999999999@\x1b[999999999P",
	"\x1b[999999999S\x1b[999999999T",
	"\x1b[999999999X",
	// XTWINOPS.
	"\x1b[8;500;500t\x1b[18t\x1b[14t",
	// Modes: alt screen, bracketed paste, mouse, in-band resize.
	"\x1b[?1049h\x1b[?1049l",
	"\x1b[?2004h\x1b[?2004l",
	"\x1b[?1000h\x1b[?1002h\x1b[?1006h\x1b[?1003h",
	"\x1b[?2048h",
	"\x1b[?25l\x1b[?25h\x1b[?7h\x1b[?7l",
	// Device queries: each produces a response into the bounded pipe.
	"\x1b[c\x1b[>c\x1b[5n\x1b[6n\x1b[?6n",
	strings.Repeat("\x1b[6n", 4096),
	// Charset designation.
	"\x1b(0\x1b)B\x1b*A\x1b+0\x0e\x0f",
	"\x1b(0lqwqk\x1b(B",
	"\x1b%G\x1b%@",
	// ESC dispatch.
	"\x1b7\x1b8\x1bD\x1bM\x1bE\x1bH\x1bc\x1b#8",
	// OSC: title, colors, hyperlink, clipboard, shell integration.
	"\x1b]0;window title\x07",
	"\x1b]2;title\x1b\\",
	"\x1b]4;1;rgb:ff/00/00\x07",
	"\x1b]10;#ffffff\x07\x1b]11;#000000\x07\x1b]12;red\x07",
	"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
	"\x1b]52;c;aGVsbG8=\x07",
	"\x1b]133;A\x07prompt\x1b]133;B\x07cmd\x1b]133;C\x07out\x1b]133;D;0\x07",
	"\x1b]66;s=2;big\x1b\\",
	"\x1b]" + strings.Repeat("9", 4096) + ";x\x07",
	// Unterminated OSC: accumulator must stay bounded.
	"\x1b]0;" + strings.Repeat("A", 100000),
	// DCS.
	"\x1bP+q544e\x1b\\",
	"\x1bP$q m\x1b\\",
	"\x1bP" + strings.Repeat("z", 65536) + "\x1b\\",
	// Sixel.
	"\x1bPq#0;2;0;0;0#1;2;100;100;100#1~~@@vv@@~~$-#1??}}GG}}??-\x1b\\",
	"\x1bPq" + strings.Repeat("!999999999~", 64) + "\x1b\\",
	"\x1bP0;0;0q#0#1#2#3$-$-\x1b\\",
	// Kitty graphics: direct payload, chunked, zlib-compressed, file transfer.
	"\x1b_Ga=T,f=24,s=1,v=1;AAAA\x1b\\",
	"\x1b_Ga=T,f=32,s=2,v=2,m=1;AAAA\x1b\\\x1b_Gm=0;BBBB\x1b\\",
	"\x1b_Ga=T,f=100,o=z;eJwBAAD//wAAAAE=\x1b\\",
	"\x1b_Ga=T,t=f,f=24,s=1,v=1;L2V0Yy9wYXNzd2Q=\x1b\\",
	"\x1b_Ga=T,t=s,f=24,s=1,v=1;L3RtcC9zaG0=\x1b\\",
	"\x1b_Ga=d\x1b\\\x1b_Ga=q,i=1\x1b\\\x1b_Ga=p,i=1,c=999999999,r=999999999\x1b\\",
	"\x1b_Ga=T,f=24,s=999999999,v=999999999;AA\x1b\\",
	"\x1b_G" + strings.Repeat("a=T,", 4096) + ";AA\x1b\\",
	// Kitty keyboard protocol.
	"\x1b[>1u\x1b[>31u\x1b[<u\x1b[=1;1u\x1b[?u",
	"\x1b[>999999999u" + strings.Repeat("\x1b[>1u", 1024),
	// APC/PM/SOS that never terminate.
	"\x1b_" + strings.Repeat("q", 100000),
	"\x1b^" + strings.Repeat("q", 100000),
	"\x1bX" + strings.Repeat("q", 100000),
	// Truncated introducers.
	"\x1b",
	"\x1b[",
	"\x1b]",
	"\x1bP",
	"\x1b[?",
	"\x1b[1;",
	// Excessive parameters.
	"\x1b[" + strings.Repeat("1;", 8192) + "m",
	// Sustained newline flood to exercise scrollback trimming.
	strings.Repeat("line\r\n", 4096),
	// Wide characters straddling the right margin.
	"\x1b[1;79H\xe4\xb8\x96\xe4\xb8\x96",
	"\x1b[1;80H\xf0\x9f\x91\x8d",
}

// addCapturedCorpus seeds a target with output captured from real programs
// running under a PTY (see testdata/corpus). Hand-written seeds cover the
// sequences we thought to write down; these cover the ones programs actually
// emit, including the interleavings a colourised pager or progress line
// produces.
func addCapturedCorpus(f *testing.F, add func([]byte)) {
	f.Helper()
	matches, err := filepath.Glob(filepath.Join("testdata", "corpus", "*.bin"))
	if err != nil {
		return
	}
	for _, m := range matches {
		b, err := os.ReadFile(m) //nolint:gosec // fixed test corpus path
		if err != nil {
			f.Fatalf("reading corpus %s: %v", m, err)
		}
		add(b)
	}
}

// writeWithBudget feeds data to the emulator on a goroutine and fails if the
// write has not returned within budget. A hostile repeat count that drives an
// O(n) loop per parameter shows up here as a hang rather than as a panic.
func writeWithBudget(t *testing.T, emu *vt.Emulator, data []byte, budget time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = emu.Write(data)
	}()
	select {
	case <-done:
	case <-time.After(budget):
		t.Fatalf("emulator write did not return within %s for %d bytes", budget, len(data))
	}
}

// drain consumes emulator query responses so a seed full of device queries does
// not simply fill the response pipe and stop being interesting.
func drain(emu *vt.Emulator) func() {
	stop := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	return func() { close(stop) }
}

// FuzzEmulatorWrite feeds arbitrary byte streams to the emulator and asserts
// the invariants that matter to a multiplexer: parsing never panics, never
// hangs, never changes the screen dimensions the host configured, always
// leaves the cursor inside the screen, and never grows the heap without bound.
func FuzzEmulatorWrite(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	addCapturedCorpus(f, func(b []byte) { f.Add(b) })

	const (
		width  = 80
		height = 24
	)

	f.Fuzz(func(t *testing.T, data []byte) {
		// Keep inputs to a size a PTY read could plausibly deliver so the
		// budget below measures parser behaviour, not input size.
		if len(data) > 1<<16 {
			data = data[:1<<16]
		}

		emu := vt.NewEmulator(width, height)
		defer emu.Close()
		stopDrain := drain(emu)
		defer stopDrain()

		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		writeWithBudget(t, emu, data, 30*time.Second)

		if got := emu.Width(); got != width {
			t.Fatalf("parsing changed screen width: got %d, want %d", got, width)
		}
		if got := emu.Height(); got != height {
			t.Fatalf("parsing changed screen height: got %d, want %d", got, height)
		}

		pos := emu.CursorPosition()
		if pos.X < 0 || pos.X >= width {
			t.Fatalf("cursor X out of bounds: %d not in [0,%d)", pos.X, width)
		}
		if pos.Y < 0 || pos.Y >= height {
			t.Fatalf("cursor Y out of bounds: %d not in [0,%d)", pos.Y, height)
		}

		// Rendering the screen must succeed and must stay proportional to the
		// screen size, not to the length of the input.
		out := emu.String()
		if !utf8.ValidString(out) {
			t.Fatalf("emulator rendered invalid UTF-8")
		}
		if lines := strings.Count(out, "\n") + 1; lines > height {
			t.Fatalf("rendered %d lines for a %d-row screen", lines, height)
		}

		// A bounded input must not produce unbounded retained heap. The
		// emulator's own caps (scrollback ring, 4 MiB response pipe, graphics
		// stores) put a ceiling well under this.
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		const heapBudget = 512 << 20
		if after.HeapAlloc > before.HeapAlloc && after.HeapAlloc-before.HeapAlloc > heapBudget {
			t.Fatalf("parsing %d bytes retained %d bytes of heap", len(data),
				after.HeapAlloc-before.HeapAlloc)
		}
	})
}

// FuzzEmulatorWriteChunked feeds the same inputs one byte at a time. The
// emulator's grapheme flush logic keys off the parser state transition and the
// end of the current Write, so a sequence split across Write calls takes a
// different path than the same bytes delivered at once. A PTY read boundary can
// land anywhere, so both paths must reach the same screen contents.
func FuzzEmulatorWriteChunked(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s), uint8(1))
	}
	addCapturedCorpus(f, func(b []byte) { f.Add(b, uint8(1)) })
	f.Add([]byte("\xe4\xb8\x96"), uint8(1))
	f.Add([]byte("\x1b[31mred"), uint8(2))

	f.Fuzz(func(t *testing.T, data []byte, chunk uint8) {
		if len(data) > 1<<15 {
			data = data[:1<<15]
		}
		size := int(chunk)
		if size == 0 {
			size = 1
		}

		whole := vt.NewEmulator(80, 24)
		defer whole.Close()
		stopWhole := drain(whole)
		defer stopWhole()
		writeWithBudget(t, whole, data, 30*time.Second)

		split := vt.NewEmulator(80, 24)
		defer split.Close()
		stopSplit := drain(split)
		defer stopSplit()
		for off := 0; off < len(data); off += size {
			end := min(off+size, len(data))
			writeWithBudget(t, split, data[off:end], 30*time.Second)
		}

		if a, b := whole.String(), split.String(); a != b {
			t.Fatalf("chunk size %d changed the rendered screen:\nwhole: %q\nsplit: %q",
				size, a, b)
		}
	})
}

// FuzzEmulatorResize interleaves parsing with resizes. The resize path rewrites
// both screens, reflows scrollback and reclamps the cursor, so it is where an
// out-of-bounds cursor or a torn cell buffer would surface.
func FuzzEmulatorResize(f *testing.F) {
	f.Add([]byte("hello\r\nworld"), uint8(80), uint8(24))
	f.Add([]byte(strings.Repeat("wrap me around the margin ", 64)), uint8(1), uint8(1))
	f.Add([]byte("\x1b[?1049h\x1b[10;10Hx"), uint8(5), uint8(3))
	f.Add([]byte("\x1b[1;1r\x1b[999H"), uint8(200), uint8(200))
	f.Add([]byte("\xe4\xb8\x96\xe4\xb8\x96\xe4\xb8\x96"), uint8(2), uint8(1))
	addCapturedCorpus(f, func(b []byte) { f.Add(b, uint8(80), uint8(24)) })

	f.Fuzz(func(t *testing.T, data []byte, w, h uint8) {
		if len(data) > 1<<15 {
			data = data[:1<<15]
		}
		// The emulator is documented for positive dimensions; the host clamps
		// before calling, so fuzz the positive domain it actually sees.
		width := int(w)%200 + 1
		height := int(h)%200 + 1

		emu := vt.NewEmulator(80, 24)
		defer emu.Close()
		stop := drain(emu)
		defer stop()

		half := len(data) / 2
		writeWithBudget(t, emu, data[:half], 30*time.Second)
		emu.Resize(width, height)
		writeWithBudget(t, emu, data[half:], 30*time.Second)

		if got := emu.Width(); got != width {
			t.Fatalf("width after resize: got %d, want %d", got, width)
		}
		if got := emu.Height(); got != height {
			t.Fatalf("height after resize: got %d, want %d", got, height)
		}
		pos := emu.CursorPosition()
		if pos.X < 0 || pos.X >= width || pos.Y < 0 || pos.Y >= height {
			t.Fatalf("cursor (%d,%d) outside %dx%d screen after resize",
				pos.X, pos.Y, width, height)
		}
		if _ = emu.String(); false {
			_ = io.Discard
		}
	})
}
