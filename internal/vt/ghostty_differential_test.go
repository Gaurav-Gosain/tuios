//go:build ghostty

package vt

// The differential harness: the same bytes go to the pure emulator and the
// libghostty-backed one, and the observable surface must agree. This is the
// only place both implementations exist in one process; the shipped binary
// compiles exactly one.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// diffPair drives both emulators in lockstep.
type diffPair struct {
	pure *Emulator
	gh   *GhosttyTerminal
}

func newDiffPair(t *testing.T, w, h int) *diffPair {
	t.Helper()
	p := &diffPair{pure: NewEmulator(w, h), gh: NewGhosttyTerminal(w, h)}
	t.Cleanup(func() { _ = p.gh.Close() })
	return p
}

func (p *diffPair) write(t *testing.T, data []byte) {
	t.Helper()
	if _, err := p.pure.Write(data); err != nil {
		t.Fatalf("pure write: %v", err)
	}
	if _, err := p.gh.Write(data); err != nil {
		t.Fatalf("ghostty write: %v", err)
	}
}

// ghDiffCellText renders a cell for comparison messages.
func ghDiffCellText(c *uv.Cell) string {
	if c == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q w=%d fg=%v bg=%v attrs=%x ul=%d", c.Content, c.Width, c.Style.Fg, c.Style.Bg, c.Style.Attrs, c.Style.Underline)
}

// compareScreens asserts the visible grids agree cell by cell.
func (p *diffPair) compareScreens(t *testing.T, context string) {
	t.Helper()
	w, h := p.pure.Width(), p.pure.Height()
	if gw, gh_ := p.gh.Width(), p.gh.Height(); gw != w || gh_ != h {
		t.Fatalf("%s: size pure=%dx%d ghostty=%dx%d", context, w, h, gw, gh_)
	}
	bad := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			pc := p.pure.CellAt(x, y)
			gc := p.gh.CellAt(x, y)
			if !cellsEquivalent(pc, gc) {
				bad++
				if bad <= 8 {
					t.Errorf("%s: cell (%d,%d)\n pure    %s\n ghostty %s", context, x, y, ghDiffCellText(pc), ghDiffCellText(gc))
				}
			}
		}
	}
	if bad > 8 {
		t.Errorf("%s: %d differing cells total", context, bad)
	}
}

// cellsEquivalent compares what a renderer would draw. Blank forms (nil,
// empty content, space) are interchangeable when unstyled.
func cellsEquivalent(a, b *uv.Cell) bool {
	blank := func(c *uv.Cell) bool {
		return c == nil || ((c.Content == "" || c.Content == " ") && c.Style.IsZero() && c.Link.URL == "")
	}
	if blank(a) && blank(b) {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	ca, cb := a.Content, b.Content
	if ca == "" {
		ca = " "
	}
	if cb == "" {
		cb = " "
	}
	if ca != cb {
		return false
	}
	wa, wb := a.Width, b.Width
	if wa == 0 {
		wa = 1
	}
	if wb == 0 {
		wb = 1
	}
	if wa != wb {
		return false
	}
	if !styleEquivalent(&a.Style, &b.Style) {
		return false
	}
	return a.Link.URL == b.Link.URL
}

func styleEquivalent(a, b *uv.Style) bool {
	if a.Attrs != b.Attrs || a.Underline != b.Underline {
		return false
	}
	return colorEquivalent(a.Fg, b.Fg) && colorEquivalent(a.Bg, b.Bg) && colorEquivalent(a.UnderlineColor, b.UnderlineColor)
}

func colorEquivalent(a, b interface {
	RGBA() (uint32, uint32, uint32, uint32)
}) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab_, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar == br && ag == bg && ab_ == bb
}

func (p *diffPair) compareCursor(t *testing.T, context string) {
	t.Helper()
	pp, gp := p.pure.CursorPosition(), p.gh.CursorPosition()
	if pp != gp {
		t.Errorf("%s: cursor pure=%v ghostty=%v", context, pp, gp)
	}
	if ph, gh_ := p.pure.IsCursorHidden(), p.gh.IsCursorHidden(); ph != gh_ {
		t.Errorf("%s: cursor hidden pure=%v ghostty=%v", context, ph, gh_)
	}
}

func (p *diffPair) compareScrollback(t *testing.T, context string, maxLines int) {
	t.Helper()
	pl, gl := p.pure.ScrollbackLen(), p.gh.ScrollbackLen()
	if pl != gl {
		t.Errorf("%s: scrollback len pure=%d ghostty=%d", context, pl, gl)
		return
	}
	n := pl
	if maxLines > 0 && n > maxLines {
		n = maxLines
	}
	bad := 0
	for i := pl - n; i < pl; i++ {
		pline := p.pure.ScrollbackLine(i)
		gline := p.gh.ScrollbackLine(i)
		if lineToString(pline) != lineToString(gline) {
			bad++
			if bad <= 4 {
				t.Errorf("%s: scrollback line %d\n pure    %q\n ghostty %q", context, i, lineToString(pline), lineToString(gline))
			}
		}
	}
}

func lineToString(l uv.Line) string {
	var b strings.Builder
	for _, c := range l {
		if c.Width == 0 && c.Content == "" {
			continue
		}
		if c.Content == "" {
			b.WriteByte(' ')
		} else {
			b.WriteString(c.Content)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func TestGhosttyDiffSmoke(t *testing.T) {
	p := newDiffPair(t, 20, 5)
	p.write(t, []byte("hello \x1b[1;31mworld\x1b[0m"))
	p.compareScreens(t, "smoke")
	p.compareCursor(t, "smoke")
}

func TestGhosttyDiffBasicSequences(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"plain", "one\r\ntwo\r\nthree"},
		{"sgr16", "\x1b[31mred \x1b[42mongreen \x1b[1;4mbold-ul\x1b[0m done"},
		{"sgr256", "\x1b[38;5;42mx\x1b[48;5;200my\x1b[0m"},
		{"truecolor", "\x1b[38;2;1;2;3ma\x1b[48;2;9;8;7mb\x1b[0m"},
		{"cursor-move", "abc\x1b[2;2Hx\x1b[Hy\x1b[3Cz"},
		{"erase-line", "aaaaaa\x1b[3D\x1b[K"},
		{"erase-display", "line1\r\nline2\x1b[H\x1b[J"},
		{"clear", "junk\x1b[2J\x1b[Hfresh"},
		{"wide-chars", "日本語 中文\r\nかな"},
		{"combining", "é ä test"},
		{"wrap", strings.Repeat("x", 25)},
		{"scroll-up", "1\r\n2\r\n3\r\n4\r\n5\r\n6\r\n7"},
		{"tabs", "a\tb\tc"},
		{"reverse-video", "\x1b[7minv\x1b[27mnorm"},
		{"insert-line", "a\r\nb\r\nc\x1b[2;1H\x1b[L"},
		{"delete-char", "abcdef\x1b[1;2H\x1b[2P"},
		{"alt-screen", "main\x1b[?1049htop\x1b[?1049l"},
		{"scroll-region", "\x1b[2;4rA\r\nB\r\nC\r\nD\r\nE\x1b[r"},
		{"origin-mode", "\x1b[2;4r\x1b[?6h\x1b[Hx\x1b[?6l\x1b[r"},
		{"rep", "ab\x1b[3b"},
		{"underline-styles", "\x1b[4:3mcurly\x1b[4:0m \x1b[4:2mdouble\x1b[24m"},
		{"hidden-cursor", "\x1b[?25labc"},
		{"osc-title", "\x1b]0;my title\abody"},
		{"charset-linedraw", "\x1b(0qqqq\x1b(B done"},
		{"decsc-decrc", "A\x1b7\x1b[5;5HB\x1b8C"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newDiffPair(t, 20, 5)
			p.write(t, []byte(tc.in))
			p.compareScreens(t, tc.name)
			p.compareCursor(t, tc.name)
		})
	}
}

// TestGhosttyDiffCorpus replays the captured real-program corpus through
// both implementations.
func TestGhosttyDiffCorpus(t *testing.T) {
	files, err := filepath.Glob("testdata/corpus/*.bin")
	if err != nil || len(files) == 0 {
		t.Skip("no corpus")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			p := newDiffPair(t, 80, 24)
			// Feed in PTY-sized chunks to exercise boundary handling.
			for off := 0; off < len(data); off += 4096 {
				end := off + 4096
				if end > len(data) {
					end = len(data)
				}
				p.write(t, data[off:end])
			}
			p.compareScreens(t, filepath.Base(f))
			p.compareCursor(t, filepath.Base(f))
			p.compareScrollback(t, filepath.Base(f), 50)
		})
	}
}

// TestGhosttyKnownDivergences pins divergences that are understood and
// accepted, in the spirit of differential_tmux_test.go's allowlist: each
// entry states which side is right. If an entry starts agreeing, the pure
// emulator gained the behavior and the entry should be deleted.
func TestGhosttyKnownDivergences(t *testing.T) {
	t.Run("sgr21-double-underline", func(t *testing.T) {
		// ECMA-48 SGR 21 is double underline; kitty, xterm and ghostty
		// honor it, the pure emulator drops it. Ghostty is right.
		p := newDiffPair(t, 20, 5)
		p.write(t, []byte("\x1b[21mx"))
		pc := p.pure.CellAt(0, 0)
		gc := p.gh.CellAt(0, 0)
		if pc.Style.Underline == gc.Style.Underline {
			t.Fatalf("pure now agrees with ghostty on SGR 21 (ul=%d); delete this entry", pc.Style.Underline)
		}
	})
}

func TestGhosttyDiffScrollback(t *testing.T) {
	p := newDiffPair(t, 20, 5)
	var b strings.Builder
	for i := range 40 {
		fmt.Fprintf(&b, "line %d\r\n", i)
	}
	p.write(t, []byte(b.String()))
	p.compareScreens(t, "scrollback")
	p.compareScrollback(t, "scrollback", 0)
}

func TestGhosttyDiffModes(t *testing.T) {
	p := newDiffPair(t, 20, 5)
	p.write(t, []byte("\x1b[?1000h\x1b[?1006h\x1b[?2004h\x1b[?1h"))
	if a, b := p.pure.HasMouseMode(), p.gh.HasMouseMode(); a != b {
		t.Errorf("HasMouseMode pure=%v ghostty=%v", a, b)
	}
	if a, b := p.pure.BracketedPasteEnabled(), p.gh.BracketedPasteEnabled(); a != b {
		t.Errorf("BracketedPaste pure=%v ghostty=%v", a, b)
	}
	if a, b := p.pure.ApplicationCursorKeys(), p.gh.ApplicationCursorKeys(); a != b {
		t.Errorf("AppCursorKeys pure=%v ghostty=%v", a, b)
	}
	pm, gm := p.pure.GetModes(), p.gh.GetModes()
	for _, num := range []int{1000, 1006, 2004, 1} {
		if pm[num] != gm[num] {
			t.Errorf("mode %d pure=%v ghostty=%v", num, pm[num], gm[num])
		}
	}
}
