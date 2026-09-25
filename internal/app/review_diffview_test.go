package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/review"
)

// The benchmark here pins the cost of the diff drawing: a frame of the largest
// diff stays inside its budget.

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
