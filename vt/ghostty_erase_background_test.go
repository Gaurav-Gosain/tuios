//go:build ghostty

package vt

import (
	"fmt"
	"testing"
)

// TestGhosttyDiffErasedCellsKeepThePenBackground: the library erases under a
// coloured pen with cells that hold the colour in their content and have style
// ID 0, and reading them through the style ID left every cell a full-screen
// program painted by clearing on the host's background (#1436).
//
// Ways it could go wrong: only the RGB arm or only the palette arm is read; a
// palette colour skips the guest's OSC 4; the decoder and the getter path
// disagree; or the screen reader is fixed and the history reader is not.
func TestGhosttyDiffErasedCellsKeepThePenBackground(t *testing.T) {
	pens := map[string]string{
		"rgb":        "\x1b[48;2;34;36;54m",
		"basic":      "\x1b[44m",
		"bright":     "\x1b[104m",
		"indexed":    "\x1b[48;5;200m",
		"with-attrs": "\x1b[1;4;38;2;9;9;9;48;2;34;36;54m",
	}
	erases := map[string]string{
		"ED 2":      "\x1b[2J\x1b[Hhello",
		"ED 0":      "\x1b[3;3H\x1b[J",
		"ED 1":      "\x1b[3;3H\x1b[1J",
		"EL 0":      "\x1b[2;3H\x1b[K",
		"EL 2":      "\x1b[2;3H\x1b[2K",
		"ECH":       "\x1b[2;3H\x1b[5X",
		"ICH":       "\x1b[2;3H\x1b[2@",
		"DCH":       "\x1b[2;3H\x1b[2P",
		"IL":        "\x1b[2;1H\x1b[2L",
		"DL":        "\x1b[2;1H\x1b[2M",
		"scroll up": "\x1b[2S",
		"LF":        "\x1b[6;1H\n\n",
	}
	for penName, pen := range pens {
		for eraseName, erase := range erases {
			for _, getters := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/getters=%v", penName, eraseName, getters), func(t *testing.T) {
					p := newDiffPair(t, 20, 6)
					if getters {
						p.gh.dec.ok = false
					}
					p.write(t, []byte("aaaaaaaa\r\nbbbbbbbb\r\ncccccccc"))
					p.write(t, []byte(pen+erase+"\x1b[0m"))
					p.compareScreens(t, "screen")
					p.compareRender(t, "render")
				})
			}
		}
	}

	// The fast path has to be the one that shipped: a library whose manifest
	// stopped naming the colour fields would drop every cell to cgo getters.
	t.Run("the decoder reads the manifest", func(t *testing.T) {
		g := NewGhosttyTerminal(20, 6)
		defer func() { _ = g.Close() }()
		if !g.dec.ok {
			t.Fatal("the decoder did not understand the library's manifest")
		}
		if _, err := g.Write([]byte("\x1b[48;2;34;36;54m\x1b[2J\x1b[Hhello\x1b[0m")); err != nil {
			t.Fatal(err)
		}
		c := g.CellAt(10, 4)
		if c == nil || c.Style.Bg == nil {
			t.Fatalf("a cell only the clear painted has no background: %s", ghDiffCellText(c))
		}
		if r, gr, b, _ := c.Style.Bg.RGBA(); r>>8 != 34 || gr>>8 != 36 || b>>8 != 54 {
			t.Fatalf("a cell only the clear painted has background %v, want rgb(34,36,54)", c.Style.Bg)
		}
	})

	// A palette slot the guest set with OSC 4 has to reach a cell the same way
	// on an erase as on a write.
	t.Run("osc 4", func(t *testing.T) {
		g := NewGhosttyTerminal(20, 6)
		defer func() { _ = g.Close() }()
		if _, err := g.Write([]byte("\x1b]4;200;rgb:12/34/56\x1b\\\x1b[48;5;200m\x1b[2J\x1b[Hx\x1b[0m")); err != nil {
			t.Fatal(err)
		}
		written, erased := g.CellAt(0, 0), g.CellAt(10, 4)
		if written == nil || erased == nil || !colorEquivalent(written.Style.Bg, erased.Style.Bg) {
			t.Fatalf("under one pen\n written %s\n erased  %s", ghDiffCellText(written), ghDiffCellText(erased))
		}
		if r, gr, b, _ := erased.Style.Bg.RGBA(); r>>8 != 0x12 || gr>>8 != 0x34 || b>>8 != 0x56 {
			t.Fatalf("an erased cell has background %v, want the guest's rgb:12/34/56", erased.Style.Bg)
		}
	})

	// compareScrollback reads text only, so this reads the cells.
	t.Run("history", func(t *testing.T) {
		p := newDiffPair(t, 20, 4)
		p.write(t, []byte("\x1b[48;2;34;36;54m\x1b[2J\x1b[H\x1b[48;5;200m\x1b[K\r\n\x1b[44m\x1b[K\r\n\r\n\r\n\r\n\r\n\x1b[0m"))
		n := p.gh.ScrollbackLen()
		if n == 0 || n != p.pure.ScrollbackLen() {
			t.Fatalf("history length: ghostty %d, pure %d", n, p.pure.ScrollbackLen())
		}
		for i := range n {
			pl, gl := p.pure.ScrollbackLine(i), p.gh.ScrollbackLine(i)
			for x := range min(len(pl), len(gl)) {
				if !cellsEquivalent(&pl[x], &gl[x]) {
					t.Errorf("history line %d col %d\n pure    %s\n ghostty %s", i, x, ghDiffCellText(&pl[x]), ghDiffCellText(&gl[x]))
				}
			}
		}
	})
}
