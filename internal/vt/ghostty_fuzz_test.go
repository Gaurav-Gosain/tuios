//go:build ghostty

package vt

// Crash-safety fuzz for the cgo boundary. A segfault inside libghostty-vt
// cannot be recovered the way a Go panic can, and the emulator runs inside
// the daemon where a crash takes every session with it. This target feeds
// arbitrary bytes through the full adapter write path in adversarial
// chunkings, interleaved with the reads and resizes the app performs, so the
// scanner, the library parser and the render-state sync all see torn
// sequences.

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzGhosttyTerminalWrite(f *testing.F) {
	f.Add([]byte("hello\x1b[1;31mworld\x1b[0m\r\n"), uint8(7), uint8(3))
	f.Add([]byte("\x1b[?1049h\x1b[2J\x1b[H\x1b]0;t\a\x1bP0q##\x1b\\\x1b_Gf=24;AAAA\x1b\\"), uint8(1), uint8(1))
	f.Add([]byte("\x1b[38;2;1;2;3m日本\x1b[3;10r\x1b[?6h\x1b[H\x1b(0qq"), uint8(2), uint8(0))
	f.Add([]byte("\x1b]52;c;?\a\x1b]4;1;rgb:aa/bb/cc\a\x1b[>5u\x1b[<u"), uint8(9), uint8(2))
	// Truncated introducers and raw C1 bytes.
	f.Add([]byte("\x1b"), uint8(1), uint8(0))
	f.Add([]byte("\x1b_G"), uint8(1), uint8(0))
	f.Add([]byte{0x9b, 0x90, 0x9d, 0x9c, 0x1b, 0xff, 0xfe, 0x00}, uint8(3), uint8(1))
	if files, err := filepath.Glob("testdata/corpus/*.bin"); err == nil {
		for _, fn := range files {
			if data, err := os.ReadFile(fn); err == nil && len(data) > 4096 {
				f.Add(data[:4096], uint8(13), uint8(2))
			}
		}
	}

	f.Fuzz(func(t *testing.T, data []byte, chunkSeed, opSeed uint8) {
		term := NewGhosttyTerminal(24, 8)
		defer func() { _ = term.Close() }()

		chunk := int(chunkSeed)%97 + 1
		ops := 0
		for off := 0; off < len(data); off += chunk {
			end := min(off+chunk, len(data))
			if _, err := term.Write(data[off:end]); err != nil {
				t.Fatalf("write: %v", err)
			}
			ops++
			switch (int(opSeed) + ops) % 7 {
			case 0:
				_ = term.CellAt(ops%24, ops%8)
			case 1:
				_ = term.CursorPosition()
			case 2:
				if n := term.ScrollbackLen(); n > 0 {
					_ = term.ScrollbackLine(n - 1)
				}
			case 3:
				term.Resize(1+ops%40, 1+ops%20)
			case 4:
				_ = term.Render()
			case 5:
				_ = term.GetModes()
			}
		}
		_ = term.String()
		_ = term.TailText(3)
	})
}
