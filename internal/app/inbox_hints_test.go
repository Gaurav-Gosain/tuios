package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// hintText is a footer as "key label" pairs, for matching.
func hintText(hints []overlay.Hint) string {
	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = h.Key + " " + h.Label
	}
	return strings.Join(parts, " | ")
}

// TestInboxFooterFitsTheRow: the footer offers the keys that do something on
// the row under the cursor, the one that answers it first. It was the same
// eight hints on every row, "space peek" included on rows where space does
// nothing and "r reply" on rows that are not mail.
func TestInboxFooterFitsTheRow(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	for _, tc := range []struct {
		kind       string
		first      string
		want, lack []string
	}{
		{session.AttentionApproval, "space answer", []string{"↵ go", "d dismiss"}, []string{"reply", "mailbox", "peek"}},
		{session.AttentionQuestion, "space answer", nil, []string{"reply", "mailbox"}},
		{session.AttentionErrored, "↵ go", []string{"d dismiss", "f filter"}, []string{"space", "reply", "mailbox"}},
		{session.AttentionFinished, "↵ go", nil, []string{"space", "reply"}},
		{session.AttentionMail, "r reply", []string{"↵ open", "m mailbox"}, []string{"space"}},
		{session.AttentionResume, "y resume", nil, []string{"space", "reply"}},
	} {
		it := item("1", tc.kind, "here", "w-1", "", 1)
		got := m.inboxRowHints(it, true)
		text := hintText(got)
		if len(got) == 0 || got[0].Key+" "+got[0].Label != tc.first {
			t.Errorf("%s: footer %q, want it to lead with %q", tc.kind, text, tc.first)
		}
		for _, w := range tc.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s: footer %q lacks %q", tc.kind, text, w)
			}
		}
		for _, l := range tc.lack {
			if strings.Contains(text, l) {
				t.Errorf("%s: footer %q offers %q, which does nothing here", tc.kind, text, l)
			}
		}
	}
	// A footer fits the Inbox's width on one line on every non-mail row.
	it := item("1", session.AttentionApproval, "here", "w-1", "", 1)
	if n := overlay.HintRowCount(m.inboxRowHints(it, true), inboxWidth); n != 1 {
		t.Errorf("the approval footer takes %d lines at %d columns, want 1", n, inboxWidth)
	}
}

// TestPeekOffersOneWayToAnswer: a prompt with numbered options is answered by
// its digits, and the footer does not also offer a, A and d for it. A prompt
// with no options still offers them.
func TestPeekOffersOneWayToAnswer(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	numbered := &session.PromptPeek{
		Options: []harness.Option{{N: 1, Label: "Yes"}, {N: 2, Label: "Always"}, {N: 3, Label: "No"}},
		Actions: []string{harness.ActionChoose, harness.ActionApprove, harness.ActionApproveAlways, harness.ActionDeny},
	}
	text := hintText(m.inboxPeekHints(numbered))
	if !strings.HasPrefix(text, "1-3 choose") {
		t.Errorf("peek footer %q, want it to lead with the digits", text)
	}
	for _, l := range []string{"a approve", "A always", "d deny"} {
		if strings.Contains(text, l) {
			t.Errorf("peek footer %q also offers %q", text, l)
		}
	}

	bare := &session.PromptPeek{Actions: []string{harness.ActionApprove, harness.ActionDeny}}
	text = hintText(m.inboxPeekHints(bare))
	if !strings.Contains(text, "a approve") || !strings.Contains(text, "d deny") {
		t.Errorf("a prompt with no options offers %q", text)
	}
}
