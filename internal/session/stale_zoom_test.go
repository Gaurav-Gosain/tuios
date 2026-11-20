package session

import "testing"

// TestAStalePushKeepsTheDaemonsZoom holds the zoom that zoom-window sets with
// a client attached. The client's push built before it saw the zoom, here the
// push for a pane whose program exited, went through the stale merge, which
// took each window's fields from the push. The push said the pane was not
// zoomed, so the zoom came off at once and no client ever drew it.
// TestSplitWindowFromAZoomedPaneAnswersAtOnce failed on it in CI.
//
// NEGATIVE CONTROL: with the keepDaemonZoom call cut from updateStateFrom,
// the pane is not zoomed after the push and the first check fails.
func TestAStalePushKeepsTheDaemonsZoom(t *testing.T) {
	sess, first, second := twoPanes(t, "zoom")
	d := &Daemon{}

	// The client builds a push before it sees the zoom.
	push := clientSnapshot(sess)

	w, ok := findWindowState(sess.GetState(), first)
	if !ok {
		t.Fatalf("setup: no window %s", first)
	}
	if changed, verr := d.zoomPane(sess, w, true); verr != nil || !changed {
		t.Fatalf("zoomPane: changed %v, error %v", changed, verr)
	}

	sess.UpdateState(push)
	got := sess.GetState()
	if w := windowByID(t, got, first); w == nil || !w.Zoomed {
		t.Errorf("a push built before zoom-window took the zoom off %s: %+v", first, w)
	}
	if w := windowByID(t, got, second); w == nil || w.Zoomed {
		t.Errorf("the other pane %s is zoomed after the push: %+v", second, w)
	}

	// A push built after the zoom is the client's own view: a client that
	// ends the zoom with its key is taken at its word.
	after := clientSnapshot(sess)
	for i := range after.Windows {
		after.Windows[i].Zoomed = false
	}
	sess.UpdateState(after)
	if w := windowByID(t, sess.GetState(), first); w == nil || w.Zoomed {
		t.Errorf("a current push that ended the zoom left %s zoomed: %+v", first, w)
	}
}
