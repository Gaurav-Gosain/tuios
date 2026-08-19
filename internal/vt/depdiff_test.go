package vt

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// depDiffOut is the directory a dependency-bump differential run writes its
// dumps into. Empty means the harness is skipped.
func depDiffOut(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("TUIOS_DEPDIFF_OUT")
	if dir == "" {
		t.Skip("TUIOS_DEPDIFF_OUT unset")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

// dumpCells renders every occupied cell of an emulator as position, content
// and width, which is the shape a width-table change would move.
func dumpCells(e *Emulator) string {
	var b strings.Builder
	for y := 0; y < e.Height(); y++ {
		for x := 0; x < e.Width(); x++ {
			c := e.CellAt(x, y)
			if c == nil {
				fmt.Fprintf(&b, "  %d,%d nil\n", x, y)
				continue
			}
			if c.Content == " " && c.Width == 1 {
				continue
			}
			fmt.Fprintf(&b, "  %d,%d %q w=%d\n", x, y, c.Content, c.Width)
		}
	}
	cur := e.CursorPosition()
	fmt.Fprintf(&b, "  cursor %d,%d\n", cur.X, cur.Y)
	return b.String()
}

// depDiffVTCases are byte streams chosen to put width decisions under
// pressure: wide runes at and over the right margin, combining marks arriving
// after their base, and grapheme clusters split across writes.
var depDiffVTCases = []struct {
	name  string
	parts []string
}{
	{"ascii", []string{"hello world"}},
	{"cjk-inline", []string{"日本語のテキスト"}},
	{"cjk-at-margin", []string{"\x1b[1;79H世世"}},
	{"cjk-over-margin", []string{"\x1b[1;80H世"}},
	{"emoji-at-margin", []string{"\x1b[1;80H👍"}},
	{"emoji-run", []string{"👋👋👋👋👋"}},
	{"zwj-family", []string{"👨‍👩‍👧‍👦"}},
	{"zwj-split-writes", []string{"👨‍", "👩‍", "👧‍", "👦"}},
	{"skin-tone", []string{"👋🏽👋🏿"}},
	{"flag", []string{"🇯🇵🇺🇸"}},
	{"keycap", []string{"1️⃣2️⃣"}},
	{"combining-after-base", []string{"e", "́", "f"}},
	{"combining-stack", []string{"à́̂̃"}},
	{"hebrew-niqqud", []string{"שָׁלוֹם"}},
	{"arabic-harakat", []string{"مَرْحَبًا"}},
	{"devanagari", []string{"हिन्दी"}},
	{"thai", []string{"กันยายน"}},
	{"tibetan", []string{"བོད་སྐད"}},
	{"myanmar", []string{"မြန်မာ"}},
	{"khmer", []string{"ខ្មែរ"}},
	{"variation-selector", []string{"❤️❤︎"}},
	{"zero-width-space", []string{"a​b"}},
	{"soft-hyphen", []string{"a­b"}},
	{"wide-then-ich", []string{"世世世\x1b[1;1H\x1b[2@"}},
	{"wide-then-dch", []string{"世世世\x1b[1;1H\x1b[1P"}},
	{"wide-wrap", []string{strings.Repeat("世", 45)}},
	{"mixed-wrap", []string{strings.Repeat("a世", 30)}},
	{"ambiguous", []string{"αβγ абв ─│┌"}},
	{"cjk-ext-b", []string{"\U00020000\U00020001"}},
	{"powerline", []string{" seg "}},
}

// TestDepDiffVT dumps emulator cell placement for the synthetic cases and for
// every captured corpus file, so a dependency bump can be diffed rather than
// trusted.
func TestDepDiffVT(t *testing.T) {
	dir := depDiffOut(t)

	var b strings.Builder
	for _, c := range depDiffVTCases {
		e := NewEmulator(80, 24)
		for _, p := range c.parts {
			if _, err := e.WriteString(p); err != nil {
				t.Fatalf("%s: write: %v", c.name, err)
			}
		}
		fmt.Fprintf(&b, "### %s\n", c.name)
		fmt.Fprintf(&b, "-- string --\n%s\n", e.String())
		fmt.Fprintf(&b, "-- cells --\n%s", dumpCells(e))
		fmt.Fprintf(&b, "-- widthmethod %v --\n\n", e.WidthMethod())
		_ = e.Close()
	}

	// Every case again at a narrow width, where the wrap and the right margin
	// do most of the deciding.
	for _, c := range depDiffVTCases {
		e := NewEmulator(10, 6)
		for _, p := range c.parts {
			if _, err := e.WriteString(p); err != nil {
				t.Fatalf("%s narrow: write: %v", c.name, err)
			}
		}
		fmt.Fprintf(&b, "### narrow/%s\n", c.name)
		fmt.Fprintf(&b, "-- string --\n%s\n", e.String())
		fmt.Fprintf(&b, "-- cells --\n%s\n", dumpCells(e))
		_ = e.Close()
	}

	files, err := filepath.Glob(filepath.Join("testdata", "corpus", "*.bin"))
	if err != nil {
		t.Fatalf("glob corpus: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		e := NewEmulator(80, 24)
		go func() {
			buf := make([]byte, 4096)
			for {
				if _, err := e.Read(buf); err != nil {
					return
				}
			}
		}()
		if _, err := e.Write(raw); err != nil {
			t.Fatalf("%s: write: %v", f, err)
		}
		fmt.Fprintf(&b, "### corpus/%s\n", filepath.Base(f))
		fmt.Fprintf(&b, "-- string --\n%s\n", e.String())
		fmt.Fprintf(&b, "-- cells --\n%s\n", dumpCells(e))
		_ = e.Close()
	}

	path := filepath.Join(dir, "vt.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("wrote %s (%d bytes)", path, b.Len())
}

// TestDepDiffResize dumps the cell layout after narrowing and widening
// resizes, which is where a wide rune gets cut in half.
func TestDepDiffResize(t *testing.T) {
	dir := depDiffOut(t)

	var b strings.Builder
	for _, c := range depDiffVTCases {
		for _, to := range []int{79, 40, 20, 5, 80} {
			e := NewEmulator(80, 24)
			for _, p := range c.parts {
				if _, err := e.WriteString(p); err != nil {
					t.Fatalf("%s: write: %v", c.name, err)
				}
			}
			e.Resize(to, 24)
			fmt.Fprintf(&b, "### %s -> %d cols\n", c.name, to)
			fmt.Fprintf(&b, "%s\n", dumpCells(e))
			_ = e.Close()
		}
	}

	path := filepath.Join(dir, "resize.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("wrote %s (%d bytes)", path, b.Len())
}
