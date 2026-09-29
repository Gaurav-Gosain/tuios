package session

import "testing"

// The mark is the daemon's. A push that omits it keeps it, and a push that
// claims it for another pane does not get it.
func TestScratchPopupMarkIsDaemonOwned(t *testing.T) {
	canonical := &SessionState{Windows: []WindowState{
		{ID: "scratch", Popup: true, IsFloating: true, ScratchPopup: true},
		{ID: "picker", Popup: true, IsFloating: true},
		{ID: "pane"},
	}}
	incoming := &SessionState{Windows: []WindowState{
		{ID: "scratch"},
		{ID: "picker", Popup: true, ScratchPopup: true},
		{ID: "pane", ScratchPopup: true},
	}}
	retainDaemonExclusive(incoming, canonical)
	got := map[string]bool{}
	for _, w := range incoming.Windows {
		got[w.ID] = w.ScratchPopup
	}
	if !got["scratch"] || got["picker"] || got["pane"] {
		t.Fatalf("scratch marks after the merge = %v, want only the daemon's scratch popup", got)
	}
}
