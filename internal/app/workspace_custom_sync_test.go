package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// customClient is a client with two panes on workspace 3, under the
// master-stack layout the custom flag is most visible in. The panes are real
// because these cases are about where a rectangle ends up, not only about which
// flag a map holds.
func customClient(t *testing.T, name string) *OS {
	t.Helper()
	m := &OS{
		Settings:             config.Global,
		SharedBorders:        config.Global.SharedBorders,
		PaneGap:              config.Global.PaneGap,
		AutoTiling:           true,
		UseBSPLayout:         false,
		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceFocus:       map[int]int{},
		WorkspaceTrees:       map[int]*layout.BSPTree{},
		CurrentWorkspace:     1,
		NumWorkspaces:        9,
		Width:                160,
		Height:               40,
		MasterRatio:          config.Global.MasterRatioFraction(),
		FocusedWindow:        -1,
	}
	for i := range 2 {
		w := benchWindow(t, fmt.Sprintf("%s-w%d", name, i), 80, 40)
		w.Workspace, w.Width, w.Height = 3, 80, 40
		m.Windows = append(m.Windows, w)
	}
	return m
}

// paneWidths reports every pane's width, which is what a retile changes and a
// preserved layout does not.
func paneWidths(m *OS) string {
	out := ""
	for _, w := range m.Windows {
		if out != "" {
			out += "/"
		}
		out += fmt.Sprint(w.Width)
	}
	return out
}

// pushedWidths is the same reading taken off a state about to go on the wire.
func pushedWidths(s *session.SessionState) string {
	out := ""
	for _, w := range s.Windows {
		if out != "" {
			out += "/"
		}
		out += fmt.Sprint(w.Width)
	}
	return out
}

// arrangeCustom gives A a layout the tiler would not have chosen: a 40 column
// pane beside a 120 column one, where tiling gives 80 and 80.
func arrangeCustom(t *testing.T, a *OS) {
	t.Helper()
	a.CurrentWorkspace = 3
	a.TileVisibleWorkspaceWindows()
	if got := paneWidths(a); got != "80/80" {
		t.Fatalf("the tiler gave %s, want 80/80: the fixture is not measuring what it says", got)
	}
	a.Windows[0].X, a.Windows[0].Width = 0, 40
	a.Windows[1].X, a.Windows[1].Width = 40, 120
	a.MarkLayoutCustom()
}

// noAnimations stops tiling applying its geometry through an animation, which
// would leave every pane at its old size for the length of the test.
func noAnimations(t *testing.T) {
	t.Helper()
	prev := config.Global.AnimationsEnabled
	t.Cleanup(func() { config.Global.AnimationsEnabled = prev })
	config.Global.AnimationsEnabled = false
}

// TestACustomLayoutSurvivesAPeersFirstVisit is the two client case the field
// exists for.
//
// The custom flag used to live only in each client's own WorkspaceHasCustom
// map, which nothing ever sent. A client that had never been to a workspace had
// no entry for it, RestoreWorkspaceLayout read the missing entry as "not
// custom", the workspace switch retiled, and the client pushed the tiler's
// rectangles - taking away, for every client, the layout another client had
// arranged by hand.
//
// NEGATIVE CONTROL: drop the WorkspaceHasCustom block from BuildSessionState,
// or the adopt call from RestoreFromState, and B comes back at 80/80 and pushes
// 80/80, which is the bug verbatim.
func TestACustomLayoutSurvivesAPeersFirstVisit(t *testing.T) {
	noAnimations(t)

	a := customClient(t, "a")
	arrangeCustom(t, a)

	pushed := a.BuildSessionState()
	if !pushed.WorkspaceHasCustom[3] {
		t.Fatalf("A pushed WorkspaceHasCustom = %v, want workspace 3 marked custom", pushed.WorkspaceHasCustom)
	}
	if got := pushedWidths(pushed); got != "40/120" {
		t.Fatalf("A pushed widths %s, want the 40/120 it arranged", got)
	}

	// B has never been to workspace 3. It adopts the session.
	b := customClient(t, "b")
	if err := b.RestoreFromState(pushed); err != nil {
		t.Fatalf("RestoreFromState: %v", err)
	}
	if !b.WorkspaceHasCustom[3] {
		t.Fatalf("B holds WorkspaceHasCustom = %v after adopting the session, want workspace 3 marked custom",
			b.WorkspaceHasCustom)
	}
	b.CurrentWorkspace = 1

	// B's user presses the workspace key.
	b.SwitchToWorkspace(3)
	if got := paneWidths(b); got != "40/120" {
		t.Errorf("B's first visit to workspace 3 retiled to %s over A's 40/120", got)
	}

	// And B's own push carries the layout rather than replacing it.
	if got := pushedWidths(b.BuildSessionState()); got != "40/120" {
		t.Errorf("B pushed %s, want A's 40/120: B is overwriting a layout it never touched", got)
	}
}

// TestACustomLayoutSurvivesAWorkspaceSwitchThatArrivesOverTheWire is the other
// door into the same failure, and the one that makes the field worth carrying.
//
// The current workspace is session state, so only the client whose user pressed
// the key runs SwitchToWorkspace. Every other client adopts the switch through
// ApplyStateSync, which retiled on any workspace change to settle the border
// allowance. That retile ran whatever the flag said, so a peer holding the flag
// still destroyed the layout, and the fix above would have saved nothing.
//
// NEGATIVE CONTROL: restore the plain workspaceChanged in the retile condition
// at the end of ApplyStateSync and B comes out at 80/80.
func TestACustomLayoutSurvivesAWorkspaceSwitchThatArrivesOverTheWire(t *testing.T) {
	noAnimations(t)

	a := customClient(t, "a")
	arrangeCustom(t, a)
	pushed := a.BuildSessionState()

	b := customClient(t, "b")
	if err := b.RestoreFromState(pushed); err != nil {
		t.Fatalf("RestoreFromState: %v", err)
	}
	b.CurrentWorkspace = 1

	// The session moves to workspace 3 and B is told rather than asked.
	pushed.Version = b.DaemonStateVersion + 1
	if err := b.ApplyStateSync(pushed); err != nil {
		t.Fatalf("ApplyStateSync: %v", err)
	}
	if b.CurrentWorkspace != 3 {
		t.Fatalf("B is on workspace %d after the sync, want 3: the case is not exercising the switch", b.CurrentWorkspace)
	}
	if got := paneWidths(b); got != "40/120" {
		t.Errorf("the sync-driven workspace switch retiled to %s over A's 40/120", got)
	}
	if got := pushedWidths(b.BuildSessionState()); got != "40/120" {
		t.Errorf("B pushed %s, want A's 40/120", got)
	}
}

// TestASyncThatSaysNothingLeavesTheCustomFlagsAlone is the compatibility
// contract. A peer too old to know the field, and every state file written
// before it existed, carry no map at all; gob also drops an empty one, so absent
// and empty have to mean the same thing. Either way the client keeps what it
// holds, and a workspace nobody has an entry for is still the tiler's.
//
// NEGATIVE CONTROL: make adoptWorkspaceHasCustom replace the map instead of
// merging into it and workspace 4's flag disappears.
func TestASyncThatSaysNothingLeavesTheCustomFlagsAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags map[int]bool
	}{
		{"absent", nil},
		{"empty", map[int]bool{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := customClient(t, "m")
			m.WorkspaceHasCustom[4] = true

			m.adoptWorkspaceHasCustom(&session.SessionState{
				CurrentWorkspace:   1,
				WorkspaceHasCustom: tc.flags,
			})
			if !m.WorkspaceHasCustom[4] {
				t.Errorf("workspace 4 lost its custom flag to a sync that said nothing")
			}
			if m.WorkspaceHasCustom[5] {
				t.Errorf("workspace 5 came out custom; a workspace nobody has an entry for is the tiler's")
			}
		})
	}
}

// TestALaggingSyncCannotUndoALayoutJustArranged is the other direction of the
// merge: the daemon echoes a client's own push back, and an echo built before
// the resize must not roll the flag back.
//
// NEGATIVE CONTROL: replace the map in adoptWorkspaceHasCustom rather than
// merging and workspace 6 loses its flag.
func TestALaggingSyncCannotUndoALayoutJustArranged(t *testing.T) {
	m := customClient(t, "m")
	m.WorkspaceHasCustom[6] = true

	m.adoptWorkspaceHasCustom(&session.SessionState{
		CurrentWorkspace:   1,
		WorkspaceHasCustom: map[int]bool{1: true},
	})
	if !m.WorkspaceHasCustom[6] {
		t.Errorf("workspace 6 lost the flag this client set, to a sync that never heard of it")
	}
	if !m.WorkspaceHasCustom[1] {
		t.Errorf("workspace 1 is not custom, want the flag the sync carried")
	}
}

// TestAPeerCanStillClearACustomLayout is the half a union of trues would lose.
// Moving a pane off a workspace retiles it and clears the flag, and that has to
// reach the other clients or they never auto-tile the workspace again.
//
// NEGATIVE CONTROL: skip present-and-false entries in adoptWorkspaceHasCustom
// and workspace 3 stays custom here.
func TestAPeerCanStillClearACustomLayout(t *testing.T) {
	m := customClient(t, "m")
	m.WorkspaceHasCustom[3] = true

	m.adoptWorkspaceHasCustom(&session.SessionState{
		CurrentWorkspace:   1,
		WorkspaceHasCustom: map[int]bool{3: false},
	})
	if m.WorkspaceHasCustom[3] {
		t.Errorf("workspace 3 is still custom after a peer said it is not")
	}
}

// TestAdoptingAnotherSessionDropsTheCustomFlagsOfTheOneBeingLeft:
// RestoreFromState is also the session switch, and one session's layouts say
// nothing about another's.
//
// NEGATIVE CONTROL: drop the map reset in RestoreFromState and workspace 8's
// flag follows the client into the new session.
func TestAdoptingAnotherSessionDropsTheCustomFlagsOfTheOneBeingLeft(t *testing.T) {
	m := customClient(t, "m")
	m.WorkspaceHasCustom[8] = true

	if err := m.RestoreFromState(&session.SessionState{
		Name:               "other",
		CurrentWorkspace:   1,
		AutoTiling:         true,
		MasterRatio:        0.5,
		WorkspaceHasCustom: map[int]bool{2: true},
	}); err != nil {
		t.Fatalf("RestoreFromState: %v", err)
	}
	if m.WorkspaceHasCustom[8] {
		t.Errorf("workspace 8's custom flag survived a switch to another session")
	}
	if !m.WorkspaceHasCustom[2] {
		t.Errorf("workspace 2 is not custom in the adopted session, want the flag it carried")
	}
}

// TestAPeerSyncCarriesTheCustomFlags covers the other adoption path. A client
// that is already attached takes the session's state through ApplyStateSync, not
// through RestoreFromState, so a layout arranged on another client reaches it
// there or not at all.
//
// NEGATIVE CONTROL: drop the adopt call from ApplyStateSync and workspace 3
// comes back with no entry, which sends the next visit through a retile.
func TestAPeerSyncCarriesTheCustomFlags(t *testing.T) {
	noAnimations(t)

	m := customClient(t, "m")
	m.CurrentWorkspace = 1

	if err := m.ApplyStateSync(&session.SessionState{
		Name:               "peer",
		CurrentWorkspace:   1,
		MasterRatio:        0.5,
		AutoTiling:         true,
		WorkspaceHasCustom: map[int]bool{3: true},
	}); err != nil {
		t.Fatalf("ApplyStateSync: %v", err)
	}
	if !m.WorkspaceHasCustom[3] {
		t.Fatalf("workspace 3 is not custom after a peer sync, want the flag the peer set")
	}
}

// TestAWorkspaceNobodyArrangedIsStillTheTilers is the behaviour the fix must not
// take away: a plain workspace switch still retiles.
//
// NEGATIVE CONTROL: mark workspace 3 custom in the fixture and the retile is
// skipped, which is the case above.
func TestAWorkspaceNobodyArrangedIsStillTheTilers(t *testing.T) {
	noAnimations(t)

	m := customClient(t, "m")
	m.CurrentWorkspace = 1
	m.Windows[0].X, m.Windows[0].Width = 0, 40
	m.Windows[1].X, m.Windows[1].Width = 40, 120

	m.SwitchToWorkspace(3)
	if got := paneWidths(m); got != "80/80" {
		t.Errorf("the switch left the panes at %s, want the tiler's 80/80: a workspace nobody arranged is retiled", got)
	}
}

// TestTwoRealClientsKeepACustomLayout is the whole thing through the daemon:
// two clients on one session over real sockets, one of which arranges a layout
// by hand and one of which has never been to that workspace.
//
// It is the case the report names, played by the route a user plays it: A drags
// a border, B leaves the workspace and comes back, and A's layout has to still
// be there afterwards - on B's screen, on A's, and in what B pushes.
//
// NEGATIVE CONTROL: measured with both halves of the fix reverted - the flag
// cleared again in RestoreWorkspaceLayout and the retile back on any workspace
// change in ApplyStateSync. The arranging client comes out at 60x38 and 60x38,
// the tiler's answer, over the 40 and 80 it was left at.
func TestTwoRealClientsKeepACustomLayout(t *testing.T) {
	noAnimations(t)

	r := newRigSized(t, 2, holderCols, holderRows)
	r.tile()
	p := joinPeerOS(t, r, holderCols, holderRows)
	p.m.AutoTiling = true

	ex := &exchange{t: t}
	ex.route(r.client, r.m, "local")
	ex.route(p.c, p.m, "peer")
	r.m.AnnounceLayoutReserve()
	p.m.AnnounceLayoutReserve()
	ex.settleBox(r, p)
	p.m.TileAllWindows()
	ex.settle(40, 200*time.Millisecond)

	// A arranges the panes by hand, which is what a border drag ends in.
	left, right := r.m.Windows[0], r.m.Windows[1]
	if left.X > right.X {
		left, right = right, left
	}
	span := right.X + right.Width - left.X
	left.Width = span / 3
	right.X = left.X + left.Width
	right.Width = span - left.Width
	r.m.MarkLayoutCustom()
	r.m.SyncStateToDaemon()
	ex.settle(80, 300*time.Millisecond)

	want := left.Width
	if got := p.m.Windows[0].Width; got != want && p.m.Windows[1].Width != want {
		t.Fatalf("the peer never received the arranged layout: %s", rects(p.m))
	}
	if !p.m.WorkspaceHasCustom[r.m.CurrentWorkspace] {
		t.Fatalf("the peer does not know workspace %d is custom: %v",
			r.m.CurrentWorkspace, p.m.WorkspaceHasCustom)
	}

	// B leaves the workspace and comes back. Before this change its first visit
	// retiled and pushed the tiler's rectangles over A's layout.
	p.m.SwitchToWorkspace(2)
	ex.settle(200, 300*time.Millisecond)
	p.m.SwitchToWorkspace(1)
	ex.settle(200, 400*time.Millisecond)

	narrowest := func(m *OS) int {
		w := m.Windows[0].Width
		for _, o := range m.Windows[1:] {
			w = min(w, o.Width)
		}
		return w
	}
	t.Logf("local rects: %s", rects(r.m))
	t.Logf("peer  rects: %s", rects(p.m))
	if got := narrowest(p.m); got != want {
		t.Errorf("the peer's narrowest pane is %d wide, want the %d it was arranged at", got, want)
	}
	if got := narrowest(r.m); got != want {
		t.Errorf("the arranging client's narrowest pane is %d wide, want the %d it arranged: "+
			"the peer pushed the tiler's rectangles back", got, want)
	}
}
