//go:build !slim

package session

import "testing"

// TestHoldsMoveWithARename: open questions and approval holds are answered
// by session name. Left on the old name, an answer after a rename was lost.
func TestHoldsMoveWithARename(t *testing.T) {
	a := newAttentionStore(func(streamEvent) {}, func() uint64 { return 0 })
	h, err := a.openAsk(AttentionItem{Session: "a", Window: "w1", Summary: "ok?"}, false)
	if err != nil {
		t.Fatal(err)
	}
	a.holds = map[string]*approvalHold{"r1": {id: "r1", session: "a", window: "w1"}}
	a.renameSession("a", "renamed")
	if h.session != "renamed" || a.holds["r1"].session != "renamed" {
		t.Errorf("after the rename the ask is on %q and the approval on %q, want renamed", h.session, a.holds["r1"].session)
	}
	for _, it := range a.items {
		if it.Session != "renamed" {
			t.Errorf("Inbox item %s is still on %q", it.ID, it.Session)
		}
	}
}
