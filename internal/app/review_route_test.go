package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/review"
)

// TestReviewNoteGoesToTheLinesAuthor: a note on a line goes to its author, a
// note on a hunk to its main author, and a note on the person's own line
// stays with the person, but only in a diff an agent turn made lines of. An
// agent this client cannot reach falls back to the pane the review is on.
func TestReviewNoteGoesToTheLinesAuthor(t *testing.T) {
	m := shotOS(t)
	authors := []reviewAuthorInfo{
		{Kind: "person"},
		{Kind: "agent", Agent: "claude", Pane: "w0", Session: "s"},
		{Kind: "agent", Agent: "codex", Pane: "w9", Session: "other"},
	}
	m.review = reviewState{open: true, who: "w0", diff: &reviewDiffResult{Session: "s", Window: "w0", Authors: authors}}
	h := review.Hunk{Lines: []review.Line{
		{Op: review.OpAdd, Origin: 1, Text: "mine"},
		{Op: review.OpAdd, Origin: 2, Text: "claude's"},
		{Op: review.OpAdd, Origin: 2, Text: "claude's too"},
		{Op: review.OpAdd, Origin: 3, Text: "codex's, in another session"},
		{Op: review.OpAdd, Origin: 1, Unsure: true, Text: "maybe a tool's"},
		{Op: review.OpContext, Text: "context"},
	}}
	for _, c := range []struct {
		line int
		want string
	}{
		{0, review.NoteToPerson},
		{1, ""},
		{3, ""},
		{4, ""},
		{5, ""},  // context: the hunk's main author, claude
		{-1, ""}, // the hunk: claude
	} {
		if got := m.reviewDefaultReceiver(h, c.line); got != c.want {
			t.Errorf("a note on line %d goes to %q, want %q", c.line, got, c.want)
		}
	}
	if rs := m.reviewReceivers(); len(rs) != 2 || rs[0].name != "claude" || rs[1].to != review.NoteToPerson {
		t.Errorf("the receivers are %+v, want claude and you", rs)
	}

	// With no agent turn known, "you" is not evidence: the note goes to the
	// pane, as before authors were known.
	m.review.diff.Authors = authors[:1]
	if got := m.reviewDefaultReceiver(h, 0); got != "" {
		t.Errorf("with no agent known, a note on a line of you's goes to %q, want the pane", got)
	}

	m.review.diff.Authors = authors
	m.review.notes = []review.Note{{ID: "n1"}, {ID: "n2"}, {ID: "n3", To: review.NoteToPerson}}
	if got := m.reviewSendWords(); got != "send 2 notes" {
		t.Errorf("S says %q, want send 2 notes", got)
	}
	if n := m.review.unsentNotes(); n != 2 {
		t.Errorf("%d notes count as unsent, want 2: the person's is never sent", n)
	}
	m.review.notes = append(m.review.notes, review.Note{ID: "n4", To: "w5"})
	if got := m.reviewSendWords(); !strings.HasPrefix(got, "send 2 to claude, 1 to ") {
		t.Errorf("S says %q, want how many go to each", got)
	}
}
