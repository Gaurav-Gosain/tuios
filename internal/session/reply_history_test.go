package session

import (
	"strings"
	"testing"
)

// TestReplyHistoryIsThePersonsAlone: the reply history holds what the person
// pasted, so a caller without a live nonce, and a pane, neither reads nor
// adds to it.
func TestReplyHistoryIsThePersonsAlone(t *testing.T) {
	d := &Daemon{}
	for _, cs := range []*connState{nil, {paneOnly: true}} {
		if _, verr := d.verbReplyHistory(cs, []byte(`{"target":"t","human_nonce":"made-up"}`)); verr == nil || verr.Code != ErrVerbNotHuman {
			t.Errorf("reply-history for %+v: %v, want not_human", cs, verr)
		}
		if _, verr := d.verbAddReplyHistory(cs, []byte(`{"target":"t","parts":[{"kind":"text","text":"x"}],"human_nonce":"made-up"}`)); verr == nil || verr.Code != ErrVerbNotHuman {
			t.Errorf("add-reply-history for %+v: %v, want not_human", cs, verr)
		}
	}
	if got := d.replyHistory.list("t"); len(got) != 0 {
		t.Fatalf("a refused add was kept: %v", got)
	}
}

// TestReplyHistoryNeedsANonceForEverySession: a presence made for one session
// proves the person for that session alone, and the history holds what was
// sent to the agents of every session, so it neither reads nor adds. An
// attach nonce does both.
func TestReplyHistoryNeedsANonceForEverySession(t *testing.T) {
	d := &Daemon{clients: map[string]*connState{
		"phone":  {clientID: "phone", presenceNonce: "for-one", presenceSession: "s1"},
		"laptop": {clientID: "laptop", attached: true, isTUIClient: true, humanNonce: "attached"},
	}}
	if _, verr := d.verbReplyHistory(nil, []byte(`{"target":"t","human_nonce":"for-one"}`)); verr == nil || verr.Code != ErrVerbNotHuman {
		t.Errorf("reply-history with a one-session presence: %v, want not_human", verr)
	}
	if _, verr := d.verbAddReplyHistory(nil, []byte(`{"target":"t","parts":[{"kind":"text","text":"x"}],"human_nonce":"for-one"}`)); verr == nil || verr.Code != ErrVerbNotHuman {
		t.Errorf("add-reply-history with a one-session presence: %v, want not_human", verr)
	}
	if _, verr := d.verbAddReplyHistory(nil, []byte(`{"target":"t","parts":[{"kind":"text","text":"x"}],"human_nonce":"attached"}`)); verr != nil {
		t.Fatalf("add-reply-history with an attach nonce: %v", verr)
	}
	if _, verr := d.verbReplyHistory(nil, []byte(`{"target":"t","human_nonce":"attached"}`)); verr != nil {
		t.Fatalf("reply-history with an attach nonce: %v", verr)
	}
}

// TestReplyHistoryBounds: each target keeps its newest messages, the same
// message moves to the end, a message's blocks are bounded, and the least
// used target goes first.
func TestReplyHistoryBounds(t *testing.T) {
	var h replyHistory
	for i := range replyHistoryEntries + 5 {
		h.add("a", replyHistoryEntry{Parts: []queuePart{{Kind: queuePartText, Text: strings.Repeat("x", i+1)}}})
	}
	list := h.list("a")
	if len(list) != replyHistoryEntries || len(list[0].Parts[0].Text) != 6 {
		t.Fatalf("kept %d, oldest %d bytes", len(list), len(list[0].Parts[0].Text))
	}
	h.add("a", replyHistoryEntry{Parts: list[0].Parts})
	if list = h.list("a"); len(list[len(list)-1].Parts[0].Text) != 6 || len(list) != replyHistoryEntries {
		t.Fatal("a message sent again did not move to the end")
	}

	parts, dropped := boundHistoryParts([]queuePart{
		{Kind: queuePartText, Text: "see "},
		{Kind: queuePartPaste, Text: strings.Repeat("y", replyHistoryEntryBytes), N: 1},
		{Kind: queuePartPaste, Text: "small", N: 2},
	})
	if len(parts) != 2 || len(dropped) != 1 || dropped[0] != "Pasted text #1" {
		t.Fatalf("bounded to %v, dropped %v", parts, dropped)
	}

	for i := range replyHistoryTargets {
		h.add("t"+string(rune('A'+i%26))+string(rune('a'+i/26)), replyHistoryEntry{Parts: []queuePart{{Kind: queuePartText, Text: "x"}}})
	}
	if h.list("a") != nil {
		t.Error("the least used target was kept past the bound")
	}
}

// TestReplyHistoryWholeBound: the whole history stays under its bytes, the
// least used target losing its oldest messages first.
func TestReplyHistoryWholeBound(t *testing.T) {
	var h replyHistory
	block := strings.Repeat("z", replyHistoryEntryBytes-16)
	for i := range 3 * replyHistoryBytes / replyHistoryEntryBytes {
		target := "t" + string(rune('a'+i%8))
		h.add(target, replyHistoryEntry{Parts: []queuePart{{Kind: queuePartPaste, Text: block + strings.Repeat("y", i%7), N: 1}, {Kind: queuePartText, Text: string(rune('a' + i%26))}}})
		if h.bytes > replyHistoryBytes {
			t.Fatalf("after %d adds the history holds %d bytes, over %d", i+1, h.bytes, replyHistoryBytes)
		}
	}
	total := 0
	for _, l := range h.targets {
		for _, e := range l {
			total += entryBytes(e)
		}
	}
	if total != h.bytes {
		t.Fatalf("counted %d bytes, holds %d", h.bytes, total)
	}
}
