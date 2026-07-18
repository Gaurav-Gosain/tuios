package overlay

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func testPalette() Palette {
	c := func(h string) color.Color { return lipgloss.Color(h) }
	return Palette{
		Canvas: c("#000000"), Panel: c("#111111"), Surface: c("#222222"),
		RowSel: c("#333333"), Card: c("#444444"), Selected: c("#5555ff"),
		Fg: c("#ffffff"), FgDim: c("#aaaaaa"), FgMute: c("#666666"),
		Accent: c("#6b50ff"), AccentBright: c("#8b75ff"), PillFg: c("#000000"),
		Warn: c("#ff0000"), Success: c("#00ff00"), Info: c("#0000ff"), Warning: c("#ffaa00"),
	}
}

func TestRectContains(t *testing.T) {
	r := Rect{X0: 2, Y0: 3, X1: 5, Y1: 6}
	cases := []struct {
		x, y int
		want bool
	}{
		{2, 3, true}, {4, 5, true}, {5, 6, false}, {1, 3, false}, {2, 2, false},
	}
	for _, c := range cases {
		if got := r.Contains(c.x, c.y); got != c.want {
			t.Errorf("Contains(%d,%d)=%v want %v", c.x, c.y, got, c.want)
		}
	}
	if !(Rect{}).Empty() {
		t.Error("zero rect should be empty")
	}
}

func TestPanelGeometry(t *testing.T) {
	pal := testPalette()
	p := Panel{
		Title:     "T",
		Width:     40,
		Tabs:      []string{"One", "Two", "Three"},
		ActiveTab: 1,
		Body:      strings.Join([]string{"a", "b", "c"}, "\n"),
		Hints:     []Hint{{Key: "esc", Label: "close"}},
	}
	out, geo := p.Render(pal)

	// Total width is inner + 2*sidePad, and every line matches it.
	if geo.Width != 44 {
		t.Errorf("Width=%d want 44", geo.Width)
	}
	for i, ln := range strings.Split(out, "\n") {
		if w := lipgloss.Width(ln); w != geo.Width {
			t.Errorf("line %d width %d != %d", i, w, geo.Width)
		}
	}

	// Title bar is row 1 and spans the full width.
	if geo.TitleBar.Y0 != 1 || geo.TitleBar.X0 != 0 || geo.TitleBar.X1 != geo.Width {
		t.Errorf("unexpected title bar %+v", geo.TitleBar)
	}

	// Three tab rects, contiguous, starting at the side pad, on the tab row.
	if len(geo.Tabs) != 3 {
		t.Fatalf("tabs=%d want 3", len(geo.Tabs))
	}
	if geo.Tabs[0].X0 != sidePad {
		t.Errorf("first tab X0=%d want %d", geo.Tabs[0].X0, sidePad)
	}
	for i := 1; i < len(geo.Tabs); i++ {
		if geo.Tabs[i].X0 != geo.Tabs[i-1].X1 {
			t.Errorf("tab %d not contiguous: %d vs %d", i, geo.Tabs[i].X0, geo.Tabs[i-1].X1)
		}
		if geo.Tabs[i].Y0 != geo.Tabs[0].Y0 {
			t.Errorf("tab %d on different row", i)
		}
	}

	// Body starts below the tab row + rule + blank.
	if geo.BodyY <= geo.Tabs[0].Y0 {
		t.Errorf("BodyY=%d should be below tabs at %d", geo.BodyY, geo.Tabs[0].Y0)
	}
	if geo.BodyX != sidePad || geo.InnerWidth != 40 {
		t.Errorf("BodyX=%d InnerWidth=%d", geo.BodyX, geo.InnerWidth)
	}
}
