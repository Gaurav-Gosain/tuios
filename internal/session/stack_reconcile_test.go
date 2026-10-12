package session

import "testing"

// A deterministic race regression for stacks (the GUI's stacked panes). A
// stack edit is a tree op, which the daemon always takes, and a push, which
// it takes as stale when another change landed between the client's last
// state and the push. The ways the stale merge can break a stack:
//
//   - the member the edit collapsed comes back open, with no tile, so every
//     client's tiler gives it a tile beside the stack (seen on main in three
//     of four runs of TestGUIBridgeStack, under window_size latest);
//   - the member a stack-open opened comes back minimized while the tree has
//     its tile, and the old open member comes back open with none;
//   - the member unstack took out stays minimized with a tile of its own;
//   - a pane in no stack keeps a minimize the daemon undid, which would
//     undo the daemon's own restore.
//
// NEGATIVE CONTROL: with reconcileStale taking canonical's flag for every
// window again, the three stack cases fail and the plain pane passes.
func TestReconcileStaleKeepsAStackEditsMinimized(t *testing.T) {
	live := func(string) bool { return true }
	win := func(id, stack string, idx int, minimized bool) WindowState {
		return WindowState{ID: id, PTYID: "p-" + id, Stack: stack, StackIndex: idx, Minimized: minimized}
	}
	cases := []struct {
		name      string
		canonical []WindowState
		incoming  []WindowState
		want      map[string]bool
	}{
		{
			name:      "stack collapses a pane",
			canonical: []WindowState{win("b", "", 0, false), win("c", "", 0, false)},
			incoming:  []WindowState{win("b", "b", 0, false), win("c", "b", 1, true)},
			want:      map[string]bool{"b": false, "c": true},
		},
		{
			name:      "stack-open trades the open pane",
			canonical: []WindowState{win("b", "b", 0, false), win("c", "b", 1, true)},
			incoming:  []WindowState{win("b", "b", 0, true), win("c", "b", 1, false)},
			want:      map[string]bool{"b": true, "c": false},
		},
		{
			name:      "unstack opens a title row",
			canonical: []WindowState{win("b", "b", 0, false), win("c", "b", 1, true)},
			incoming:  []WindowState{win("b", "", 0, false), win("c", "", 0, false)},
			want:      map[string]bool{"b": false, "c": false},
		},
		{
			name:      "a pane in no stack keeps the daemon's flag",
			canonical: []WindowState{win("a", "", 0, false)},
			incoming:  []WindowState{win("a", "", 0, true)},
			want:      map[string]bool{"a": false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			incoming := &SessionState{Windows: tc.incoming}
			reconcileStale(incoming, &SessionState{Windows: tc.canonical}, live)
			for _, w := range incoming.Windows {
				if got, want := w.Minimized, tc.want[w.ID]; got != want {
					t.Errorf("%s: minimized %v after the stale merge, want %v", w.ID, got, want)
				}
			}
		})
	}
}
