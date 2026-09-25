package app

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// These tests pin the diff drawing of the review overlay: code is coloured
// by its file type in the theme's colours, added and removed lines sit on
// their own grounds, the changed part of a changed line is marked, notes sit
// under their lines in both layouts, the split layout divides the column
// exactly, and no cell of the overlay is left without a background.

// richDiff is a diff of a Go file, with a comment, strings, numbers and a
// changed line, and a Markdown file.
func richDiff() map[string]any {
	retry := review.File{Path: "internal/retry/retry.go", Status: "M", Added: 7, Removed: 3, Hunks: []review.Hunk{{
		Header: "@@ -12,12 +12,16 @@ import (", OldStart: 12, OldLines: 12, NewStart: 12, NewLines: 16,
		Lines: []review.Line{
			{Op: "context", Old: 12, New: 12, Text: "// Do calls f until it succeeds or ctx is done, waiting longer"},
			{Op: "context", Old: 13, New: 13, Text: "// after each failure."},
			{Op: "delete", Old: 14, Text: "func Do(ctx context.Context, f func() error) error {"},
			{Op: "add", New: 14, Text: "func Do(ctx context.Context, attempts int, f func() error) error {"},
			{Op: "context", Old: 15, New: 15, Text: "\tdelay := 100 * time.Millisecond"},
			{Op: "delete", Old: 16, Text: "\tfor attempt := 0; ; attempt++ {"},
			{Op: "add", New: 16, Text: "\tfor attempt := 0; attempt < attempts; attempt++ {"},
			{Op: "context", Old: 17, New: 17, Text: "\t\tif err := f(); err == nil {"},
			{Op: "context", Old: 18, New: 18, Text: "\t\t\treturn nil"},
			{Op: "context", Old: 19, New: 19, Text: "\t\t}"},
			{Op: "delete", Old: 20, Text: "\t\ttime.Sleep(delay)"},
			{Op: "add", New: 20, Text: "\t\tlog.Printf(\"retry: attempt %d failed\", attempt+1)"},
			{Op: "add", New: 21, Text: "\t\tselect {"},
			{Op: "add", New: 22, Text: "\t\tcase <-ctx.Done():"},
			{Op: "add", New: 23, Text: "\t\t\treturn ctx.Err()"},
			{Op: "add", New: 24, Text: "\t\tcase <-time.After(delay):"},
			{Op: "add", New: 25, Text: "\t\t}"},
			{Op: "context", Old: 21, New: 26, Text: "\t\tdelay *= 2"},
			{Op: "context", Old: 22, New: 27, Text: "\t}"},
			{Op: "add", New: 28, Text: "\treturn ErrExhausted"},
			{Op: "context", Old: 23, New: 29, Text: "}"},
		},
	}}}
	doc := review.File{Path: "docs/retry.md", Status: "A", Added: 3, Hunks: []review.Hunk{{
		Header: "@@ -0,0 +1,3 @@", NewStart: 1, NewLines: 3,
		Lines: []review.Line{
			{Op: "add", New: 1, Text: "# Retry"},
			{Op: "add", New: 2, Text: ""},
			{Op: "add", New: 3, Text: "`Do` now takes the number of **attempts**.", NoNewline: true},
		},
	}}}
	big := review.File{Path: "testdata/big.json", Status: "M", Added: 9000, Removed: 12, Truncated: true}
	return map[string]any{
		"type": "review_diff", "session": "api-2", "window": "w-1", "base": "main", "base_sha": "abc",
		"files":  []review.File{retry, doc, big},
		"totals": review.Totals{Files: 3, Added: 9010, Removed: 15},
	}
}

// richNotes are a note on a changed line and one on the hunk.
func richNotes() []review.Note {
	return []review.Note{
		{ID: "n1", Path: "internal/retry/retry.go", Side: "new", Line: 16, Quote: "\tfor attempt := 0; attempt < attempts; attempt++ {", Text: "attempts of zero never calls f. Is that wanted?", By: "human"},
		{ID: "n2", Path: "internal/retry/retry.go", Side: "new", Line: 12, HunkHeader: "@@ -12,12 +12,16 @@ import (", Text: "add a test for the cancelled context", By: "w-9", SentAt: time.Now().Add(-4 * time.Minute).UnixNano()},
	}
}

// richReview is a review open on richDiff at w by h.
func richReview(t *testing.T, w, h int) (*OS, *reviewFake) {
	t.Helper()
	m, f := reviewOS(t)
	m.Width, m.Height = w, h
	f.diff = richDiff()
	f.notes = richNotes()
	openReviewed(t, m)
	return m, f
}

// overlayCells parses the drawn overlay into cells.
func overlayCells(t *testing.T, m *OS) uv.ScreenBuffer {
	t.Helper()
	out := m.renderReview()
	buf := uv.NewScreenBuffer(m.Width, m.Height)
	uv.NewStyledString(out).Draw(buf, buf.Bounds())
	return buf
}

// assertNoBareCells fails for any cell of the overlay with no background.
func assertNoBareCells(t *testing.T, m *OS, what string) {
	t.Helper()
	buf := overlayCells(t, m)
	for y := range m.Height {
		for x := range m.Width {
			c := buf.CellAt(x, y)
			if c == nil || c.Width == 0 {
				continue
			}
			if c.Style.Bg == nil {
				t.Fatalf("%s: cell (%d,%d) %q has no background", what, x, y, c.Content)
			}
		}
	}
}

// TestReviewEveryCellHasABackground: in both layouts, both sizes, with no
// theme, a dark theme, a light theme and a pane background, every cell of
// the overlay carries a background, the compare view's included.
func TestReviewEveryCellHasABackground(t *testing.T) {
	for _, th := range []string{"", "catppuccin_mocha", "catppuccin_latte"} {
		for _, size := range [][2]int{{80, 24}, {120, 40}} {
			for _, split := range []bool{false, true} {
				name := fmt.Sprintf("%s/%dx%d/split=%v", th, size[0], size[1], split)
				t.Run(name, func(t *testing.T) {
					withTheme(t, th)
					m, _ := richReview(t, size[0], size[1])
					m.review.split = split
					assertNoBareCells(t, m, "diff")
					m.ReviewMove(3)
					m.ReviewNote(false)
					assertNoBareCells(t, m, "editor")
					m.ReviewEditorCancel()
					m.ReviewToggleFocus()
					assertNoBareCells(t, m, "list")
					m.ReviewFile(1)
					m.ReviewFile(1)
					assertNoBareCells(t, m, "truncated placeholder")
				})
			}
		}
	}
	t.Run("pane background", func(t *testing.T) {
		withTheme(t, "")
		m, _ := richReview(t, 120, 40)
		m.Settings.PaneBackground = "#1d2b3a"
		assertNoBareCells(t, m, "pane background")
		buf := overlayCells(t, m)
		if got := buf.CellAt(0, 0).Style.Bg; !sameRGB(got, color.RGBA{0x1d, 0x2b, 0x3a, 0xff}) {
			t.Errorf("the frame is on %v, want the pane background", got)
		}
	})
	t.Run("compare", func(t *testing.T) {
		withTheme(t, "catppuccin_latte")
		m, f := reviewOS(t)
		m.Width, m.Height = 120, 40
		f.diff = richDiff()
		f.fan = map[string]any{"group": "g", "rows": []map[string]any{{"session": "api-1", "state": "done"}, {"session": "api-2", "state": "working"}}}
		openReviewed(t, m)
		runMsg(t, m, m.ReviewCompare())
		m.ReviewCompareMark()
		if !m.ReviewCompareShown() {
			t.Fatal("the compare view did not open")
		}
		assertNoBareCells(t, m, "compare")
	})
}

// sameRGB compares two colours by their 8-bit channels, nil only to nil.
func sameRGB(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == b
	}
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar>>8 == br>>8 && ag>>8 == bg>>8 && ab>>8 == bb>>8
}

// findText is the position of the first cell that starts text on screen.
func findText(buf uv.ScreenBuffer, w, h int, text string) (int, int, bool) {
	for y := range h {
		var line strings.Builder
		var xs []int
		for x := range w {
			c := buf.CellAt(x, y)
			if c == nil || c.Width == 0 {
				continue
			}
			for range len(c.Content) {
				xs = append(xs, x)
			}
			line.WriteString(c.Content)
		}
		if i := strings.Index(line.String(), text); i >= 0 {
			return xs[i], y, true
		}
	}
	return 0, 0, false
}

// TestReviewCodeIsHighlightedInTheThemeColours: a Go keyword, a string and a
// comment are drawn in the colours the theme's palette gives them, each
// measured against its line's ground, and plain text in the theme's
// foreground. An added line is on the add ground, a removed one on the
// delete ground, and a context line on the overlay's own.
func TestReviewCodeIsHighlightedInTheThemeColours(t *testing.T) {
	if !diffview.Enabled {
		t.Skip("this build does not highlight")
	}
	withTheme(t, "catppuccin_mocha")
	m, _ := richReview(t, 120, 40)
	m.ReviewMove(1) // off the hunk header, which the cursor tints
	buf := overlayCells(t, m)
	look := m.reviewLook()
	ansiPal := theme.GetANSIPalette()

	check := func(text string, kind diffview.Kind, slot int) {
		t.Helper()
		x, y, ok := findText(buf, m.Width, m.Height, text)
		if !ok {
			t.Fatalf("%q is not on screen", text)
		}
		c := buf.CellAt(x, y)
		bg := look.dv.Bg(kind, false, false)
		if !sameRGB(c.Style.Bg, bg) {
			t.Errorf("%q is on %v, want the %d ground %v", text, c.Style.Bg, kind, bg)
		}
		floor := overlay.ContrastFloor
		if slot == 8 {
			floor = overlay.MarkFloor
		}
		want := overlay.ReadableAt(solidColor(ansiPal[slot]), bg, floor)
		if !sameRGB(c.Style.Fg, want) {
			t.Errorf("%q is in %v, want palette slot %d on its ground, %v", text, c.Style.Fg, slot, want)
		}
	}
	check("// after each failure.", diffview.Context, 8)
	check("func", diffview.Delete, 5)
	check("\"retry: attempt %d failed\"", diffview.Add, 2)
	check("100", diffview.Context, 3)

	x, y, _ := findText(buf, m.Width, m.Height, "delay *= 2")
	if c := buf.CellAt(x, y); !sameRGB(c.Style.Fg, look.dv.Fg()) {
		t.Errorf("a plain name is in %v, want the theme's foreground %v", c.Style.Fg, look.dv.Fg())
	}
}

// TestReviewMarksTheChangedPart: of a line changed in place, the words that
// changed are on the stronger ground and the rest on the line's own.
func TestReviewMarksTheChangedPart(t *testing.T) {
	withTheme(t, "")
	m, _ := richReview(t, 120, 40)
	m.ReviewMove(1)
	buf := overlayCells(t, m)
	dv := m.reviewLook().dv
	x, y, ok := findText(buf, m.Width, m.Height, "attempts int, ")
	if !ok {
		t.Fatal("the changed line is not on screen")
	}
	if c := buf.CellAt(x, y); !sameRGB(c.Style.Bg, dv.Bg(diffview.Add, false, true)) {
		t.Errorf("the added words are on %v, want the changed-part ground", c.Style.Bg)
	}
	x, y, _ = findText(buf, m.Width, m.Height, "func Do(ctx context.Context, attempts")
	if c := buf.CellAt(x, y); !sameRGB(c.Style.Bg, dv.Bg(diffview.Add, false, false)) {
		t.Errorf("the unchanged start of the line is on %v, want the added line's ground", c.Style.Bg)
	}
}

// TestReviewSplitLayout: s lays the two sides next to each other where the
// column is wide enough: a removed line beside the line that replaced it, a
// note under the pair, and every row exactly the column's width. At 80
// columns the column is too narrow, the diff stays in one column, and the
// dock says why.
func TestReviewSplitLayout(t *testing.T) {
	withTheme(t, "")
	m, _ := richReview(t, 120, 40)
	m.ReviewMove(6) // the "for attempt" pair in one column
	m.ReviewToggleSplit()
	if !m.reviewSplitOn(m.reviewRowsWidth()) {
		t.Fatal("s did not split a 120 column review")
	}
	lines := reviewFrameText(t, m)
	row := rowWith(lines, "for attempt := 0; ; attempt+")
	if row < 0 || !strings.Contains(lines[row], "for attempt := 0; attempt < a") {
		t.Fatalf("the removed and added lines are not side by side:\n%s", strings.Join(lines, "\n"))
	}
	if note := rowWith(lines, "attempts of zero never calls f"); note != row+1 {
		t.Errorf("the note is on row %d, its line pair on row %d:\n%s", note, row, strings.Join(lines, "\n"))
	}
	if cur, ok := m.reviewRowUnderCursor(); !ok || cur.kind != reviewRowLine || !strings.Contains(m.review.diff.Files[0].Hunks[0].Lines[cur.line].Text, "attempt < attempts") {
		t.Errorf("the cursor left its line when the layout changed: %+v", cur)
	}
	// The two sides split the column between them exactly.
	width := m.reviewRowsWidth()
	rows := m.reviewRows(width)
	d := reviewDraw{m: m, look: m.reviewLook(), file: &m.review.diff.Files[0], width: width, numW: 3, split: true}
	for i, r := range rows {
		if w := ansi.StringWidth(d.row(r, i == 0)); w != width {
			t.Errorf("split row %d is %d cells, want %d", i, w, width)
		}
	}
	if !strings.Contains(strings.Join(lines, "\n"), "s unified") {
		t.Errorf("the footer does not offer the way back:\n%s", lines[len(lines)-2])
	}

	m.Width, m.Height = 80, 24
	if m.reviewSplitOn(m.reviewRowsWidth()) {
		t.Fatal("an 80 column review was split")
	}
	m.ReviewToggleSplit()
	m.ReviewToggleSplit()
	if n := m.Notifications; len(n) == 0 || !strings.Contains(n[len(n)-1].Message, "Too narrow") {
		t.Errorf("s on a narrow review said nothing: %v", n)
	}
	reviewFrameText(t, m)
}

// TestReviewScrollsSideways: l moves the code left and h back, and another
// file starts at the left edge.
func TestReviewScrollsSideways(t *testing.T) {
	withTheme(t, "")
	m, _ := richReview(t, 80, 24)
	m.ReviewScrollX(1)
	if m.review.xOff != reviewScrollStep {
		t.Fatalf("l scrolled to %d", m.review.xOff)
	}
	lines := reviewFrameText(t, m)
	if rowWith(lines, "-"+overlay.Ellipsis()+"ctx context.Context") < 0 || rowWith(lines, "func Do(ctx") >= 0 {
		t.Errorf("the code did not move left:\n%s", strings.Join(lines, "\n"))
	}
	m.ReviewScrollX(-5)
	if m.review.xOff != 0 {
		t.Errorf("h went past the left edge to %d", m.review.xOff)
	}
	m.ReviewScrollX(1)
	m.ReviewFile(1)
	reviewFrameText(t, m)
	if m.review.xOff != 0 {
		t.Errorf("the next file starts scrolled to %d", m.review.xOff)
	}
}

// TestReviewHighlightsOnlyWhatIsDrawn: a closed review keeps nothing, and an
// open one tokenises the hunks it drew and no others.
func TestReviewHighlightsOnlyWhatIsDrawn(t *testing.T) {
	withTheme(t, "")
	m, f := reviewOS(t)
	m.Width, m.Height = 120, 40
	var files []review.File
	for i := range 50 {
		files = append(files, review.File{Path: fmt.Sprintf("f%02d.go", i), Status: "M", Added: 1, Hunks: []review.Hunk{{
			Header: "@@ -1 +1 @@", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1,
			Lines: []review.Line{{Op: "add", New: 1, Text: "var x = 1"}},
		}}})
	}
	f.diff["files"] = files
	openReviewed(t, m)
	if m.review.hunks != nil {
		t.Error("the review tokenised before it drew")
	}
	m.renderReview()
	highlighted := 0
	for _, looks := range m.review.hunks {
		for _, hl := range looks {
			if hl != nil {
				highlighted += hl.highlighted()
			}
		}
	}
	if highlighted != 1 {
		t.Errorf("%d hunks were tokenised for one file on screen", highlighted)
	}
	m.CloseReview()
	if m.review.hunks != nil || m.review.look != nil {
		t.Error("a closed review kept its hunks or colours")
	}
	if m.renderReview() != "" {
		t.Error("a closed review drew")
	}
}

// TestReviewShots writes the overlay as PNG screenshots, rendered by
// internal/shot from the composed frame, for a person to look at. It runs
// only with TUIOS_REVIEW_SHOTS set to the directory to write them to.
func TestReviewShots(t *testing.T) {
	dir := os.Getenv("TUIOS_REVIEW_SHOTS")
	if dir == "" {
		t.Skip("TUIOS_REVIEW_SHOTS is not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, th := range []struct{ name, id, paneBg string }{
		{"default", "", ""},
		{"dark", "catppuccin_mocha", ""},
		{"light", "catppuccin_latte", ""},
		{"panebg", "tokyo_night", config.BackgroundTheme},
	} {
		for _, size := range [][2]int{{80, 24}, {120, 40}} {
			for _, split := range []bool{false, true} {
				withTheme(t, th.id)
				m, _ := richReview(t, size[0], size[1])
				m.Settings.PaneBackground = th.paneBg
				m.ReviewMove(6)
				if split {
					m.ReviewToggleSplit()
				}
				layout := "unified"
				if split {
					layout = "split"
				}
				g := m.composedGrid(0, 0, m.GetRenderWidth(), m.GetRenderHeight())
				if g == nil {
					t.Fatal("no frame")
				}
				data, err := shot.RenderPNG(g, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				name := fmt.Sprintf("review-%s-%dx%d-%s.png", th.name, size[0], size[1], layout)
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
					t.Fatal(err)
				}
				if size[0] != 120 || split {
					continue
				}
				// The Markdown file, then the placeholder of the file too
				// large to show, in one picture each.
				for _, what := range []string{"markdown", "truncated"} {
					m.ReviewFile(1)
					g := m.composedGrid(0, 0, m.GetRenderWidth(), m.GetRenderHeight())
					data, err := shot.RenderPNG(g, nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					name := fmt.Sprintf("review-%s-%dx%d-%s.png", th.name, size[0], size[1], what)
					if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
}

// bigReview is a review of 400 files, the first of 5000 lines in one hunk
// and every other a hunk of a dozen: the most review-diff hands over.
func bigReview(b *testing.B) *OS {
	b.Helper()
	m := shotOS(b)
	m.Width, m.Height = 120, 40
	m.IsDaemonSession = true
	var files []review.File
	for i := range 400 {
		n := 12
		if i == 0 {
			n = 5000
		}
		lines := make([]review.Line, n)
		for j := range n {
			text := fmt.Sprintf("\tif err := step%d(ctx, \"value %d\", %d); err != nil { // check %d", j, j, j, j)
			switch j % 3 {
			case 0:
				lines[j] = review.Line{Op: "add", New: j + 1, Text: text}
			case 1:
				lines[j] = review.Line{Op: "delete", Old: j + 1, Text: text + " old"}
			default:
				lines[j] = review.Line{Op: "context", Old: j + 1, New: j + 1, Text: text}
			}
		}
		files = append(files, review.File{Path: fmt.Sprintf("pkg/f%03d.go", i), Status: "M", Added: n / 3, Removed: n / 3, Hunks: []review.Hunk{{
			Header: fmt.Sprintf("@@ -1,%d +1,%d @@", n, n), OldStart: 1, OldLines: n, NewStart: 1, NewLines: n, Lines: lines,
		}}})
	}
	m.review = reviewState{open: true, diff: &reviewDiffResult{Session: "s", Files: files, Totals: review.Totals{Files: 400}}}
	m.review.rebuildFiles()
	return m
}

// BenchmarkReviewFrame measures one frame of the review on the largest diff
// it is handed: the first frame, which tokenises what shows, a frame with
// everything on screen already tokenised, a page down, and the jump to the
// end of the 5000 line file, in one column and side by side.
func BenchmarkReviewFrame(b *testing.B) {
	for _, split := range []bool{false, true} {
		layout := "unified"
		if split {
			layout = "split"
		}
		b.Run(layout+"/first", func(b *testing.B) {
			m := bigReview(b)
			m.review.split = split
			b.ResetTimer()
			for range b.N {
				m.review.hunks, m.review.look = nil, nil
				m.renderReview()
			}
		})
		b.Run(layout+"/cached", func(b *testing.B) {
			m := bigReview(b)
			m.review.split = split
			m.renderReview()
			b.ResetTimer()
			for range b.N {
				m.renderReview()
			}
		})
		b.Run(layout+"/page", func(b *testing.B) {
			m := bigReview(b)
			m.review.split = split
			m.renderReview()
			b.ResetTimer()
			for range b.N {
				m.ReviewPage(1)
				m.renderReview()
			}
		})
		b.Run(layout+"/end", func(b *testing.B) {
			m := bigReview(b)
			m.review.split = split
			m.renderReview()
			b.ResetTimer()
			for range b.N {
				m.review.hunks = nil
				m.ReviewEdge(true)
				m.renderReview()
				m.ReviewEdge(false)
			}
		})
	}
}
