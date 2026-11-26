package app

import (
	"strconv"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/review"
)

// longFileDiff is sampleDiff with long.txt: the lines in top, then n lines
// "row 1" to "row n", all added.
func longFileDiff(top []string, n int) map[string]any {
	d := sampleDiff()
	var lines []review.Line
	for _, s := range top {
		lines = append(lines, review.Line{Op: "add", New: len(lines) + 1, Text: s})
	}
	for i := range n {
		lines = append(lines, review.Line{Op: "add", New: len(lines) + 1, Text: "row " + strconv.Itoa(i+1)})
	}
	long := review.File{Path: "long.txt", Status: "A", Added: len(lines), Hunks: []review.Hunk{{
		Header: "@@ -0,0 +1," + strconv.Itoa(len(lines)) + " @@", NewStart: 1, NewLines: len(lines), Lines: lines,
	}}}
	d["files"] = append(d["files"].([]review.File), long)
	return d
}

// TestReviewLiveReadKeepsTheCursorRow: lines added above the cursor by a live
// read leave the cursor on the row it was on, at the same place on screen.
// A read that lands while a note is typed changes nothing, and runs once the
// note is dropped.
func TestReviewLiveReadKeepsTheCursorRow(t *testing.T) {
	m, f := reviewOS(t)
	f.diff = longFileDiff(nil, 200)
	openReviewed(t, m)
	reviewFrameText(t, m)
	g := m.reviewGeometry()
	m.ReviewMouseClick(g.listX, reviewPaneTop+2)
	reviewFrameText(t, m)
	m.ReviewMouseClick(g.diffX+3, reviewPaneTop+10)
	lines := reviewFrameText(t, m)
	at := m.review.cursor - m.review.scroll
	if rowWith(lines, "row 10") != reviewPaneTop+at {
		t.Fatalf("the cursor is on screen row %d, not on row 10's:\n%v", reviewPaneTop+at, lines)
	}

	f.diff = longFileDiff([]string{"new 1", "new 2", "new 3", "new 4", "new 5"}, 200)
	runMsg(t, m, m.reviewLiveStart())
	lines = reviewFrameText(t, m)
	rows := m.reviewRows(m.reviewRowsWidth())
	e := m.review.currentFile()
	row := rows[m.review.cursor]
	if got := e.file.Hunks[row.hunk].Lines[row.line].Text; got != "row 10" {
		t.Errorf("after a live read the cursor is on %q, want row 10", got)
	}
	if got := m.review.cursor - m.review.scroll; got != at {
		t.Errorf("after a live read the cursor is %d rows down the diff, was %d", got, at)
	}
	if rowWith(lines, "row 10") != reviewPaneTop+at {
		t.Errorf("row 10 moved on screen after a live read:\n%v", lines)
	}

	// A read while a note is typed is held.
	m.ReviewNote(false)
	if m.review.editor == nil {
		t.Fatal("c opened no note editor")
	}
	f.diff = longFileDiff([]string{"held 1"}, 200)
	runMsg(t, m, m.reviewLiveStart())
	if m.review.editor == nil {
		t.Fatal("a live read closed the note editor")
	}
	if rowWith(reviewFrameText(t, m), "held 1") >= 0 {
		t.Fatal("a live read changed the rows while a note was typed")
	}
	m.ReviewEditorCancel()
	cmd := m.reviewLiveRead()
	if cmd == nil {
		t.Fatal("a held read did not run once the note was dropped")
	}
	m.review.live.lastEnd = m.review.live.lastEnd.Add(-reviewLiveGap)
	runMsg(t, m, m.reviewLiveStart())
	if e := m.review.currentFile(); e == nil || e.file.Hunks[0].Lines[0].Text != "held 1" {
		t.Fatal("the held read never showed the new rows")
	}
}
