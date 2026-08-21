package vt_test

// Conformance for what a resize does to a double-width character it cuts.
//
// wide_cell_edge_test.go pins the visible screen. The same cut can land on a
// row that has already scrolled into the scrollback, on the alternate screen
// while the guest is not looking at the primary, and on a grapheme cluster
// rather than a bare wide rune. Each of those is read by something that walks
// cells and assumes a row is worth no more display columns than the screen has,
// so a lead half left standing anywhere draws over the pane next door.
//
// Sizes here are small on purpose. A pane in a tiled layout is narrow, and the
// squeeze that produces a cut is a layout change rather than a window resize.

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// rowDisplayWidth is what a reader that walks the row and draws each cell whole
// would produce.
func rowDisplayWidth(cells func(x int) *uv.Cell, width int) int {
	n := 0
	for x := range width {
		if c := cells(x); c != nil && c.Content != "" && c.Content != " " {
			n += max(c.Width, 1)
		}
	}
	return n
}

func TestConform_ResizeCuttingAWideCharacter(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       string
		from, to int
	}{
		{"a wide rune whose tail is dropped", "世世世", 6, 5},
		{"a wide rune at the new edge", "a世世", 6, 4},
		{"a cluster carrying a mark", "世́世́世́", 6, 5},
		{"an emoji with a presentation selector", "☝️☝️☝️", 6, 5},
		{"a zero width joiner sequence", "\U0001f469‍\U0001f4bb\U0001f469‍\U0001f4bb", 6, 5},
		{"a flag", "\U0001f1fa\U0001f1f8\U0001f1fa\U0001f1f8", 6, 5},
		{"a narrowing to one column", "世世世", 6, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(tc.from, 3)
			if _, err := emu.WriteString(tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			emu.Resize(tc.to, 3)

			got := rowDisplayWidth(func(x int) *uv.Cell { return emu.CellAt(x, 0) }, emu.Width())
			if got > emu.Width() {
				t.Errorf("after narrowing %d to %d the row is worth %d display columns on a %d-column screen",
					tc.from, tc.to, got, emu.Width())
			}
			// Widening again must not resurrect the half that was cut.
			emu.Resize(tc.from, 3)
			got = rowDisplayWidth(func(x int) *uv.Cell { return emu.CellAt(x, 0) }, emu.Width())
			if got > emu.Width() {
				t.Errorf("after widening back to %d the row is worth %d display columns", tc.from, got)
			}
		})
	}
}

// TestConform_ResizeCuttingAWideCharacterInScrollback is the same claim for the
// rows the user has to scroll up to see. They are drawn by the same reader.
func TestConform_ResizeCuttingAWideCharacterInScrollback(t *testing.T) {
	emu := vt.NewEmulator(6, 2)
	// Eight rows of wide characters through a two-row screen, so six of them
	// end up in the scrollback with a lead in the last column.
	if _, err := emu.WriteString(strings.Repeat("世世世\r\n", 8)); err != nil {
		t.Fatalf("write: %v", err)
	}
	emu.Resize(5, 2)

	if emu.ScrollbackLen() == 0 {
		t.Fatal("nothing scrolled back, so this is not testing what it claims")
	}
	for i := range emu.ScrollbackLen() {
		line := emu.ScrollbackLine(i)
		got := rowDisplayWidth(func(x int) *uv.Cell {
			if x >= len(line) {
				return nil
			}
			return &line[x]
		}, min(len(line), emu.Width()))
		if got > emu.Width() {
			t.Errorf("scrollback line %d is worth %d display columns on a %d-column screen",
				i, got, emu.Width())
		}
	}
}

// TestConform_ResizeCuttingAWideCharacterOnTheAlternateScreen covers the screen
// the guest is not looking at when the pane is squeezed.
//
// A full-screen program sits on the alternate screen while the layout changes
// around it. If only the active screen is repaired, the damage waits until the
// program exits and the primary comes back, which is the worst time to find it.
func TestConform_ResizeCuttingAWideCharacterOnTheAlternateScreen(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"cut on the alternate screen, checked there", "\x1b[?1049h世世世"},
		{"cut on the primary, checked after coming back", "世世世\x1b[?1049h\x1b[?1049l"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(6, 3)
			if _, err := emu.WriteString(tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			emu.Resize(5, 3)
			if tc.name == "cut on the primary, checked after coming back" {
				if _, err := emu.WriteString("\x1b[?1049l"); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			got := rowDisplayWidth(func(x int) *uv.Cell { return emu.CellAt(x, 0) }, emu.Width())
			if got > emu.Width() {
				t.Errorf("the row is worth %d display columns on a %d-column screen", got, emu.Width())
			}
		})
	}
}

// TestConform_ResizeWhileAClusterIsOpen covers the boundary case the split-write
// path creates.
//
// A cluster stays open across a write so that combining marks arriving in the
// next read still reach the cell they belong to. A resize between the two
// writes moves that cell, or removes it. The mark has to land somewhere on the
// grid or nowhere at all.
func TestConform_ResizeWhileAClusterIsOpen(t *testing.T) {
	for _, tc := range []struct {
		name     string
		first    string
		second   string
		from, to int
	}{
		{"narrowing between a base and its mark", "abc世", "́", 6, 4},
		{"narrowing onto the open cell", "abcd世", "́", 7, 5},
		{"widening between a base and its mark", "世", "́", 4, 8},
		{"narrowing to one column", "世", "́", 6, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(tc.from, 3)
			if _, err := emu.WriteString(tc.first); err != nil {
				t.Fatalf("first write: %v", err)
			}
			emu.Resize(tc.to, 3)
			if _, err := emu.WriteString(tc.second); err != nil {
				t.Fatalf("second write: %v", err)
			}

			for y := range emu.Height() {
				got := rowDisplayWidth(func(x int) *uv.Cell { return emu.CellAt(x, y) }, emu.Width())
				if got > emu.Width() {
					t.Errorf("row %d is worth %d display columns on a %d-column screen", y, got, emu.Width())
				}
			}
			p := emu.CursorPosition()
			if p.X < 0 || p.X >= emu.Width() || p.Y < 0 || p.Y >= emu.Height() {
				t.Errorf("the cursor is at %d,%d on a %dx%d screen", p.X, p.Y, emu.Width(), emu.Height())
			}
		})
	}
}
