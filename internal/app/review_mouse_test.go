package app

import (
	"strconv"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/review"
)

// longReviewDiff is sampleDiff with a third file of n added lines, so its
// diff is taller than the panes.
func longReviewDiff(n int) map[string]any {
	d := sampleDiff()
	lines := make([]review.Line, n)
	for i := range lines {
		lines[i] = review.Line{Op: "add", New: i + 1, Text: "row " + strconv.Itoa(i+1)}
	}
	long := review.File{Path: "long.txt", Status: "A", Added: n, Hunks: []review.Hunk{{
		Header: "@@ -0,0 +1," + strconv.Itoa(n) + " @@", NewStart: 1, NewLines: n, Lines: lines,
	}}}
	d["files"] = append(d["files"].([]review.File), long)
	return d
}

// openLong opens the review on longReviewDiff and shows long.txt.
func openLong(t *testing.T) *OS {
	t.Helper()
	m, f := reviewOS(t)
	f.diff = longReviewDiff(200)
	openReviewed(t, m)
	reviewFrameText(t, m)
	g := m.reviewGeometry()
	m.ReviewMouseClick(g.listX, reviewPaneTop+2)
	reviewFrameText(t, m)
	if e := m.review.currentFile(); e == nil || e.path != "long.txt" {
		t.Fatalf("a click on the third list row did not open long.txt: %+v", e)
	}
	return m
}

// TestReviewScrollBarDragsToBothEnds presses the thumb, drags it to the
// bottom of the bar and back to the top, and reads where the diff is.
func TestReviewScrollBarDragsToBothEnds(t *testing.T) {
	m := openLong(t)
	b := m.review.bar
	if !b.shown {
		t.Fatalf("no scroll bar on a diff of %d rows", len(m.reviewRows(m.reviewRowsWidth())))
	}
	hidden := b.total - b.rows
	m.ReviewMouseClick(b.x, b.top+b.thumbTop)
	for y := b.top + b.thumbTop; y <= b.top+b.rows-1; y++ {
		m.ReviewMouseDrag(b.x, y)
		reviewFrameText(t, m)
	}
	m.ReviewMouseRelease()
	if m.review.scroll != hidden {
		t.Fatalf("the bar dragged to the bottom shows the diff from row %d, want %d", m.review.scroll, hidden)
	}
	b = m.review.bar
	m.ReviewMouseClick(b.x, b.top+b.thumbTop)
	m.ReviewMouseDrag(b.x, b.top)
	reviewFrameText(t, m)
	m.ReviewMouseRelease()
	if m.review.scroll != 0 {
		t.Fatalf("the bar dragged to the top shows the diff from row %d, want 0", m.review.scroll)
	}
}

// TestReviewClickFoldsAHunk clicks the hunk's header twice.
func TestReviewClickFoldsAHunk(t *testing.T) {
	m := openLong(t)
	g := m.reviewGeometry()
	before := len(m.reviewRows(m.reviewRowsWidth()))
	m.ReviewMouseClick(g.diffX+3, reviewPaneTop)
	lines := reviewFrameText(t, m)
	if got := len(m.reviewRows(m.reviewRowsWidth())); got != 1 {
		t.Fatalf("the folded hunk is %d rows, want its header only (was %d)", got, before)
	}
	if rowWith(lines, "200 lines folded") < 0 {
		t.Fatalf("the folded header does not say what it hides:\n%v", lines)
	}
	m.ReviewMouseClick(g.diffX+3, reviewPaneTop)
	reviewFrameText(t, m)
	if got := len(m.reviewRows(m.reviewRowsWidth())); got != before {
		t.Fatalf("the unfolded hunk is %d rows, want %d", got, before)
	}
}

// TestReviewRuleDragSetsTheListWidth drags the rule right and reads the
// layout.
func TestReviewRuleDragSetsTheListWidth(t *testing.T) {
	m := openLong(t)
	g := m.reviewGeometry()
	m.ReviewMouseClick(g.ruleX, reviewPaneTop+1)
	m.ReviewMouseDrag(g.ruleX+7, reviewPaneTop+1)
	m.ReviewMouseRelease()
	if got := m.reviewGeometry().ruleX; got != g.ruleX+7 {
		t.Fatalf("the rule is at %d after a drag of 7 from %d", got, g.ruleX)
	}
	m.ReviewMouseClick(g.ruleX+7, reviewPaneTop+1)
	m.ReviewMouseDrag(1000, reviewPaneTop+1)
	m.ReviewMouseRelease()
	if got := m.reviewGeometry().diffW; got != reviewDiffMin {
		t.Fatalf("a drag past the right edge leaves a diff column of %d, want %d", got, reviewDiffMin)
	}
}

// TestReviewHintsAreWhereTheyAreDrawn reads each recorded hint's cells on
// the frame.
func TestReviewHintsAreWhereTheyAreDrawn(t *testing.T) {
	m := openLong(t)
	lines := reviewFrameText(t, m)
	if len(m.review.hintHits) == 0 {
		t.Fatal("the footer recorded no hints")
	}
	row := []rune(lines[m.review.hintRow])
	for _, s := range m.review.hintHits {
		got := string(row[s.x0:s.x1])
		key := s.key
		if key == "enter" {
			continue
		}
		if len(got) < len(key) || got[:len(key)] != key {
			t.Errorf("the hint for %q is drawn as %q", key, got)
		}
		if k := m.ReviewHintKeyAt(s.x0, m.review.hintRow); k != key {
			t.Errorf("ReviewHintKeyAt(%d) = %q, want %q", s.x0, k, key)
		}
	}
}
