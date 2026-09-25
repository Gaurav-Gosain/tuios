package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
)

// These tests pin the cost of the diff drawing: only the hunks drawn are
// tokenised, a closed review keeps nothing, and a frame of the largest diff
// stays inside its benchmark. TestReviewShots writes screenshots on request.

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
