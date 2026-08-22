package vt

// Backend throughput benchmarks. They construct the emulator through vt.New,
// so the same benchmark measures whichever backend the build selected:
//
//	go test ./internal/vt/ -bench BenchmarkBackend                 # pure Go
//	go test -tags ghostty ./internal/vt/ -bench BenchmarkBackend   # libghostty
//
// The streams are synthesized, not stored: a DOOM-fire style full-screen
// truecolor repaint (the maintainer's own stress test), a plain log scroll,
// and an editor-style partial redraw.

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func synthDoomFire(cols, rows, frames int) []byte {
	var b strings.Builder
	rng := rand.New(rand.NewSource(42))
	pal := [][3]int{{7, 7, 7}, {31, 7, 7}, {103, 31, 7}, {175, 63, 7}, {223, 95, 7}, {255, 143, 7}, {255, 191, 7}, {255, 255, 255}}
	for f := 0; f < frames; f++ {
		b.WriteString("\x1b[H")
		for y := 0; y < rows; y++ {
			for x := 0; x < cols; x++ {
				t := pal[rng.Intn(len(pal))]
				bo := pal[rng.Intn(len(pal))]
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", t[0], t[1], t[2], bo[0], bo[1], bo[2])
			}
			if y < rows-1 {
				b.WriteString("\r\n")
			}
		}
	}
	return []byte(b.String())
}

func synthScroll(lines int) []byte {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "%09d INFO request handled path=/api/v1/items/%d status=200 dur=%dms bytes=%d\r\n", i, i, i%90, 1000+i%9000)
	}
	return []byte(b.String())
}

func synthTUI(cols, rows, frames int) []byte {
	var b strings.Builder
	rng := rand.New(rand.NewSource(7))
	b.WriteString("\x1b[2J\x1b[H")
	for y := 1; y <= rows; y++ {
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[38;5;250mline %3d  %s", y, y, strings.Repeat("lorem ipsum dolor ", 4))
	}
	for f := 0; f < frames; f++ {
		for k := 0; k < 6; k++ {
			fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[1;38;5;%dmupdated %d\x1b[0m", 1+rng.Intn(rows), 1+rng.Intn(cols/2), 30+rng.Intn(200), f)
		}
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[7m frame %5d \x1b[0m", rows, f)
	}
	return []byte(b.String())
}

// benchBackendStream measures Write throughput in PTY-sized chunks, plus a
// per-frame read of every cell the way a renderer consumes the grid.
func benchBackendStream(b *testing.B, cols, rows int, data []byte, readEvery int) {
	term := New(cols, rows)
	defer func() { _ = term.Close() }()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chunk := 32 * 1024
		since := 0
		for off := 0; off < len(data); off += chunk {
			end := min(off+chunk, len(data))
			if _, err := term.Write(data[off:end]); err != nil {
				b.Fatal(err)
			}
			since += end - off
			if readEvery > 0 && since >= readEvery {
				since = 0
				for y := 0; y < rows; y++ {
					for x := 0; x < cols; x++ {
						_ = term.CellAt(x, y)
					}
				}
				_ = term.CursorPosition()
			}
		}
	}
}

func BenchmarkBackendDoomFire158x40(b *testing.B) {
	data := synthDoomFire(158, 40, 30)
	benchBackendStream(b, 158, 40, data, 0)
}

func BenchmarkBackendDoomFire207x55(b *testing.B) {
	data := synthDoomFire(207, 55, 20)
	benchBackendStream(b, 207, 55, data, 0)
}

// The render variant reads the whole grid back once per ~frame, which is what
// the client render loop costs on top of parsing.
func BenchmarkBackendDoomFireRender158x40(b *testing.B) {
	data := synthDoomFire(158, 40, 30)
	benchBackendStream(b, 158, 40, data, 220*1024)
}

func BenchmarkBackendScroll(b *testing.B) {
	data := synthScroll(40000)
	benchBackendStream(b, 207, 55, data, 0)
}

func BenchmarkBackendTUI(b *testing.B) {
	data := synthTUI(120, 40, 3000)
	benchBackendStream(b, 120, 40, data, 0)
}
