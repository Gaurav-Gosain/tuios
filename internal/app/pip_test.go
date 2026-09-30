package app

import (
	"image"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// How the picture-in-picture view could be wrong, written down before the
// tests:
//   - The box could land on the region's edge or outside it, on the rail or
//     the dock, or keep its full size on a screen too small for it.
//   - The box could cover the focused pane's cursor, or jump back and forth as
//     the cursor moves near it.
//   - The body could show the bottom of a screen that has text only at the
//     top, which is a blank box for every fresh shell.
//   - The view could be drawn while its own pane has the focus, keep drawing
//     after its pane closed, or survive an unpin.
//   - It could re-read the emulator on frames where nothing was written.

func TestPiPBoxPlacement(t *testing.T) {
	region := image.Rect(10, 1, 110, 41) // 100x40, a rail on the left, a dock row above
	cases := []struct {
		corner pipCorner
		want   image.Rectangle
	}{
		{pipBottomRight, image.Rect(69, 28, 109, 40)},
		{pipBottomLeft, image.Rect(11, 28, 51, 40)},
		{pipTopRight, image.Rect(69, 2, 109, 14)},
		{pipTopLeft, image.Rect(11, 2, 51, 14)},
	}
	for _, tc := range cases {
		got, ok := pipBox(region, 40, 12, tc.corner)
		if !ok || got != tc.want {
			t.Errorf("corner %d: box %v ok %v, want %v", tc.corner, got, ok, tc.want)
		}
		if !got.In(region.Inset(pipMargin)) {
			t.Errorf("corner %d: box %v leaves the region less its margin %v", tc.corner, got, region.Inset(pipMargin))
		}
	}

	// A region smaller than the box shrinks it to the region less the margin.
	small := image.Rect(0, 0, 30, 10)
	got, ok := pipBox(small, 40, 12, pipBottomRight)
	if !ok || got != image.Rect(1, 1, 29, 9) {
		t.Errorf("small region: box %v ok %v, want (1,1)-(29,9)", got, ok)
	}

	// A region that cannot hold the smallest box draws nothing.
	if got, ok := pipBox(image.Rect(0, 0, config.PiPMinWidth+1, 20), 40, 12, pipBottomRight); ok {
		t.Errorf("a region %d wide gave a box %v", config.PiPMinWidth+1, got)
	}
	if got, ok := pipBox(image.Rect(0, 0, 80, config.PiPMinHeight+1), 40, 12, pipBottomRight); ok {
		t.Errorf("a region %d tall gave a box %v", config.PiPMinHeight+1, got)
	}
}

func TestPiPCornerAvoidsTheCursor(t *testing.T) {
	region := image.Rect(0, 0, 120, 40)
	br, _ := pipBox(region, 40, 12, pipBottomRight)
	bl, _ := pipBox(region, 40, 12, pipBottomLeft)
	tr, _ := pipBox(region, 40, 12, pipTopRight)
	cell := func(p image.Point) image.Rectangle { return image.Rect(p.X, p.Y, p.X+1, p.Y+1) }
	inBR := image.Pt(br.Min.X+3, br.Max.Y-1)
	inBL := image.Pt(bl.Min.X+3, bl.Max.Y-1)
	// A line typed at the bottom of a full-width pane, long enough to reach
	// the bottom-right box: both bottom corners would cover part of it.
	longLine := image.Rect(1, br.Max.Y-1, br.Min.X+5, br.Max.Y)
	// The same line in a left-hand pane that stops short of the box.
	shortLine := image.Rect(1, br.Max.Y-1, 50, br.Max.Y)

	cases := []struct {
		name               string
		current, preferred pipCorner
		clear              image.Rectangle
		want               pipCorner
	}{
		{"no cursor stays", pipBottomRight, pipBottomRight, image.Rectangle{}, pipBottomRight},
		{"cursor elsewhere stays", pipBottomRight, pipBottomRight, cell(image.Pt(60, 20)), pipBottomRight},
		{"cursor in the box moves along the edge", pipBottomRight, pipBottomRight, cell(inBR), pipBottomLeft},
		{"cursor on the box's corner cell moves", pipBottomRight, pipBottomRight, cell(br.Min), pipBottomLeft},
		{"cursor next to the box stays", pipBottomRight, pipBottomRight, cell(image.Pt(br.Min.X-1, br.Min.Y)), pipBottomRight},
		// Sticky: back in the preferred corner's area is no reason to move
		// while the current corner is clear.
		{"moved box does not jump back", pipBottomLeft, pipBottomRight, cell(image.Pt(60, 20)), pipBottomLeft},
		{"moved box moves on when reached", pipBottomLeft, pipBottomRight, cell(inBL), pipBottomRight},
		{"top-left preference mirrors", pipTopLeft, pipTopLeft, cell(image.Pt(2, 3)), pipTopRight},
		{"cursor in top-right from bottom-right is fine", pipBottomRight, pipBottomRight, cell(tr.Min), pipBottomRight},
		{"a typed line that reaches the box goes over the top", pipBottomRight, pipBottomRight, longLine, pipTopRight},
		{"a typed line short of the box stays", pipBottomRight, pipBottomRight, shortLine, pipBottomRight},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pipChooseCorner(region, 40, 12, tc.current, tc.preferred, tc.clear)
			if !ok || got != tc.want {
				t.Fatalf("corner %d ok %v, want %d", got, ok, tc.want)
			}
			box, _ := pipBox(region, 40, 12, got)
			if box.Overlaps(tc.clear) {
				t.Fatalf("the box %v covers %v", box, tc.clear)
			}
		})
	}

	// A screen where every corner's box meets the cursor: the view hides.
	tight := image.Rect(0, 0, 30, 10)
	if c, ok := pipChooseCorner(tight, 40, 12, pipBottomRight, pipBottomRight, cell(image.Pt(15, 5))); ok {
		t.Fatalf("every corner covers the cursor, yet corner %d was chosen", c)
	}
}

// fakePiPScreen is a pipScreen made of plain lines.
type fakePiPScreen struct {
	w, h   int
	lines  []string
	cursor uv.Position
}

func (s fakePiPScreen) Width() int                  { return s.w }
func (s fakePiPScreen) Height() int                 { return s.h }
func (s fakePiPScreen) CursorPosition() uv.Position { return s.cursor }
func (s fakePiPScreen) CellAt(x, y int) *uv.Cell {
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		return nil
	}
	c := uv.EmptyCell
	if y < len(s.lines) {
		row := []rune(s.lines[y])
		if x < len(row) {
			c = uv.Cell{Content: string(row[x]), Width: 1}
		}
	}
	return &c
}

func TestPiPCellsShowTheLatestRows(t *testing.T) {
	var lines []string
	for i := range 10 {
		lines = append(lines, "line "+string(rune('0'+i)))
	}
	// Ten lines at the top of a thirty-row pane, the cursor under them: the
	// view shows the last lines written, not the blank bottom of the screen.
	scr := fakePiPScreen{w: 40, h: 30, lines: lines, cursor: uv.Position{X: 0, Y: 10}}
	got := strings.Split(ansi.Strip(pipCells(scr, 12, 4)), "\n")
	want := []string{"line 7", "line 8", "line 9", ""}
	if len(got) != 4 {
		t.Fatalf("got %d rows, want 4: %q", len(got), got)
	}
	for i := range want {
		if strings.TrimRight(got[i], " ") != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
		if w := ansi.StringWidth(got[i]); w != 12 {
			t.Errorf("row %d is %d cells wide, want 12", i, w)
		}
	}

	// A screen with less text than the view shows its top.
	few := fakePiPScreen{w: 40, h: 30, lines: []string{"$ echo hi", "hi"}, cursor: uv.Position{X: 2, Y: 2}}
	got = strings.Split(ansi.Strip(pipCells(few, 12, 4)), "\n")
	if strings.TrimRight(got[0], " ") != "$ echo hi" || strings.TrimRight(got[1], " ") != "hi" {
		t.Errorf("a short screen shows %q, want its first rows", got)
	}

	// Text below the cursor (a full-screen program's status line) counts.
	tui := fakePiPScreen{w: 20, h: 10, lines: []string{"top", "", "", "", "", "", "", "", "", "status"}, cursor: uv.Position{Y: 0}}
	got = strings.Split(ansi.Strip(pipCells(tui, 10, 3)), "\n")
	if strings.TrimRight(got[2], " ") != "status" {
		t.Errorf("the status line on the last row is not shown: %q", got)
	}
}

func TestPiPCellsBlankAWideGlyphCutByTheEdge(t *testing.T) {
	scr := wideEdgeScreen{}
	got := pipCells(scr, 4, 1)
	if w := ansi.StringWidth(got); w != 4 {
		t.Fatalf("row is %d cells wide, want 4: %q", w, got)
	}
	if strings.Contains(got, "世") {
		t.Fatalf("a wide glyph cut by the right edge was drawn: %q", got)
	}
}

// wideEdgeScreen holds "abc" and then a wide glyph on columns 3 and 4.
type wideEdgeScreen struct{}

func (wideEdgeScreen) Width() int                  { return 8 }
func (wideEdgeScreen) Height() int                 { return 1 }
func (wideEdgeScreen) CursorPosition() uv.Position { return uv.Position{} }
func (wideEdgeScreen) CellAt(x, y int) *uv.Cell {
	c := uv.EmptyCell
	switch {
	case x < 3:
		c = uv.Cell{Content: string(rune('a' + x)), Width: 1}
	case x == 3:
		c = uv.Cell{Content: "世", Width: 2}
	case x == 4:
		c = uv.Cell{}
	}
	return &c
}

// pipOS is a local client with two tiled panes, a on the left and b on the
// right, 120x40, a focused.
func pipOS(t *testing.T) *OS {
	t.Helper()
	m := gapTestOS(t, 2)
	m.Width, m.Height = 120, 40
	m.UserConfig = config.DefaultConfig()
	m.Windows[0].CustomName, m.Windows[1].CustomName = "alpha", "bravo"
	m.TileAllWindows()
	m.FocusWindow(0)
	return m
}

func TestPiPLifecycle(t *testing.T) {
	m := pipOS(t)
	a, b := m.Windows[0], m.Windows[1]
	b.WriteOutput([]byte("BRAVO-FIRST\r\n"))
	m.MarkTerminalsWithNewContent()

	// Pinning the focused pane draws nothing until the focus moves.
	m.FocusWindow(1)
	m.TogglePiP()
	if m.PiPWindowID() != b.ID {
		t.Fatalf("pinned %q, want %q", m.PiPWindowID(), b.ID)
	}
	if l := m.renderPiP(); l != nil {
		t.Fatal("the view is drawn while its own pane has the focus")
	}

	m.FocusWindow(0)
	l := m.renderPiP()
	if l == nil {
		t.Fatal("the view is not drawn with the other pane focused")
	}
	if !strings.Contains(ansi.Strip(l.GetContent()), "BRAVO-FIRST") || !strings.Contains(ansi.Strip(l.GetContent()), "bravo") {
		t.Fatalf("the view does not show the pinned pane:\n%s", ansi.Strip(l.GetContent()))
	}
	if l.GetZ() != config.ZIndexPiP || config.ZIndexPiP <= config.ZIndexAnimating || config.ZIndexPiP >= config.ZIndexFloating {
		t.Fatalf("the view is at z %d, want above the tiles and a zoomed pane and below the floating band", l.GetZ())
	}

	// Nothing written: the next frame reuses the box it built.
	before := m.pip.box
	m.MarkTerminalsWithNewContent()
	if m.pip.dirty {
		t.Fatal("the view is marked dirty with no output from its pane")
	}
	_ = m.renderPiP()
	if m.pip.box != before {
		t.Fatal("the box was rebuilt on a frame where nothing changed")
	}

	// Output from the pane: the next frame reads it.
	// An unfocused pane repaints every third pass, and the view with it.
	b.WriteOutput([]byte("BRAVO-SECOND\r\n"))
	pumpPiP(m)
	if !m.pip.dirty {
		t.Fatal("output from the pinned pane did not mark the view dirty")
	}
	l = m.renderPiP()
	if !strings.Contains(ansi.Strip(l.GetContent()), "BRAVO-SECOND") {
		t.Fatalf("the view did not follow the pane's output:\n%s", ansi.Strip(l.GetContent()))
	}

	// A pane on another workspace keeps feeding the view.
	m.MoveWindowToWorkspace(1, 2)
	b.WriteOutput([]byte("BRAVO-AWAY\r\n"))
	pumpPiP(m)
	l = m.renderPiP()
	if l == nil || !strings.Contains(ansi.Strip(l.GetContent()), "BRAVO-AWAY") {
		t.Fatal("the view stopped following a pane on another workspace")
	}

	// A click on the view goes to the pane, on its workspace, and the view
	// is not drawn while it has the focus.
	r := m.pip.rect
	if !m.PiPAt(r.Min.X+2, r.Min.Y+2) || m.PiPAt(r.Min.X-1, r.Min.Y) {
		t.Fatalf("PiPAt does not match the drawn box %v", r)
	}
	if !m.JumpToPiP() {
		t.Fatal("the jump found no pane")
	}
	if m.CurrentWorkspace != 2 || m.GetFocusedWindow() != b {
		t.Fatalf("the jump left workspace %d focused on %v, want workspace 2 on bravo", m.CurrentWorkspace, m.GetFocusedWindow())
	}
	if l := m.renderPiP(); l != nil || !m.pip.rect.Empty() {
		t.Fatal("the view is drawn while its pane has the focus after the jump")
	}

	// The key unpins from any pane.
	m.FocusWindow(0)
	m.TogglePiP()
	if m.PiPWindowID() != "" || m.renderPiP() != nil {
		t.Fatal("the key did not unpin the view")
	}
	_ = a
}

// pumpPiP runs the output pass until it marks the view dirty. A pane without
// the focus repaints every third pass, and the view of it does too.
func pumpPiP(m *OS) {
	for range 3 {
		if m.MarkTerminalsWithNewContent(); m.pip.dirty {
			return
		}
	}
}

func TestPiPGoesWithItsPane(t *testing.T) {
	m := pipOS(t)
	b := m.Windows[1]
	if err := m.PinPiP(b.ID); err != nil {
		t.Fatal(err)
	}
	if m.renderPiP() == nil {
		t.Fatal("the view is not drawn")
	}
	notes := len(m.Notifications)
	m.DeleteWindow(1)
	if m.PiPWindowID() != "" || m.renderPiP() != nil {
		t.Fatal("the view outlived its pane")
	}
	if len(m.Notifications) != notes+1 || !strings.Contains(m.Notifications[len(m.Notifications)-1].Message, "bravo closed") {
		t.Fatalf("no dock note says the pane closed: %+v", m.Notifications)
	}
}

func TestPiPVerbToggles(t *testing.T) {
	m := pipOS(t)
	b := m.Windows[1]
	if pinned, id, err := m.SetPiP(b.ID, false); err != nil || !pinned || id != b.ID {
		t.Fatalf("pin: %v %q %v", pinned, id, err)
	}
	if pinned, _, err := m.SetPiP(b.ID, false); err != nil || pinned {
		t.Fatalf("naming the pinned pane again: pinned %v err %v, want unpinned", pinned, err)
	}
	if _, _, err := m.SetPiP("no-such-pane", false); err == nil {
		t.Fatal("pinning an unknown pane did not fail")
	}
	_, _, _ = m.SetPiP(b.ID, false)
	if pinned, _, _ := m.SetPiP("", true); pinned || m.PiPWindowID() != "" {
		t.Fatal("off did not unpin")
	}
}

func TestPiPJumpRestoresAMinimizedPane(t *testing.T) {
	m := pipOS(t)
	b := m.Windows[1]
	if err := m.PinPiP(b.ID); err != nil {
		t.Fatal(err)
	}
	m.MinimizeWindow(1)
	m.FocusWindow(0)
	b.WriteOutput([]byte("BRAVO-HIDDEN\r\n"))
	pumpPiP(m)
	l := m.renderPiP()
	if l == nil || !strings.Contains(ansi.Strip(l.GetContent()), "BRAVO-HIDDEN") {
		t.Fatal("the view does not follow a minimized pane")
	}
	if !m.JumpToPiP() {
		t.Fatal("the jump found no pane")
	}
	if b.Minimized || m.GetFocusedWindow() != b {
		t.Fatalf("the jump left the pane minimized %v, focused %v", b.Minimized, m.GetFocusedWindow() == b)
	}
}
