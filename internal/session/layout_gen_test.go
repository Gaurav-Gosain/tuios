package session

import "testing"

// A push tiled in a layout generation older than the session's arrives after
// the session moved on, so its rectangles describe a box no client draws any
// more. The session keeps its own. A push from the current generation, and
// one from a client too old to name a generation, are taken as sent.
//
// The E2E test TestDaemonKeepsTheLayoutTheClientsDraw cannot reach this: each
// client pushes again for every newer generation, on one ordered connection,
// so the last push to land is always a current one. What it guards is a
// client whose current push does not go out (a push dropped behind a window
// intent), where the stale one would otherwise stand.
//
// NEGATIVE CONTROL: with keepRectsOfStaleLayout returning before it compares
// generations, the stale push's rectangles replace the session's.
func TestAPushTiledInAnOlderLayoutKeepsTheSessionsRectangles(t *testing.T) {
	sess, err := NewSession("gen", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)
	win := func(x int) []WindowState {
		return []WindowState{{ID: "w1", X: x, Y: 2, Width: 40, Height: 20, Workspace: 1}}
	}
	sess.SettleLayout(120, 40, LayoutReserve{})
	sess.UpdateState(&SessionState{Name: "gen", CurrentWorkspace: 1, Windows: win(0), BaseVersion: 1})
	gen := sess.SettleLayout(120, 40, LayoutReserve{Left: 28})

	for _, c := range []struct {
		what  string
		gen   uint64
		wantX int
	}{
		{"a push from the older generation", gen - 1, 0},
		{"a push from the current generation", gen, 28},
		{"a push that names no generation", 0, 30},
	} {
		st := &SessionState{Name: "gen", CurrentWorkspace: 1, Windows: win(c.wantX), LayoutGen: c.gen,
			BaseVersion: sess.GetState().Version}
		if c.gen == gen-1 {
			st.Windows = win(16)
		}
		sess.keepRectsOfStaleLayout(st)
		sess.UpdateState(st)
		if got := sess.GetState().Windows[0].X; got != c.wantX {
			t.Errorf("%s: the session holds the window at column %d, want %d", c.what, got, c.wantX)
		}
		if st.LayoutGen != 0 {
			t.Errorf("%s: the generation stayed on the state as %d", c.what, st.LayoutGen)
		}
	}
}
