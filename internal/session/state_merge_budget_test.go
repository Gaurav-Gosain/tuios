package session

import (
	"reflect"
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// leastAllocsPerRun is the least of three testing.AllocsPerRun readings of f,
// 100 runs each. AllocsPerRun counts every allocation in the process: an
// allocation the merge makes is in every reading, one a goroutine an earlier
// test left winding down makes is not.
func leastAllocsPerRun(f func()) float64 {
	allocs := testing.AllocsPerRun(100, f)
	for range 2 {
		allocs = min(allocs, testing.AllocsPerRun(100, f))
	}
	return allocs
}

// mergeFixture is a session of n windows and a push of the same windows in
// the same order, as benchState builds them. Each of the session's windows
// holds a directory, a shell pid, a note, an agent state and an agent message
// no other window has; the push holds none of them.
func mergeFixture(n int) (canon, inc *SessionState) {
	canon, inc = benchState(n), benchState(n)
	for i := range canon.Windows {
		w := &canon.Windows[i]
		w.Cwd = "/work/" + w.ID
		w.ShellPID = 1000 + i
		w.Note = "note " + w.ID
		w.AgentState = AgentStateWorking
		w.AgentMessage = "busy " + w.ID
	}
	return canon, inc
}

// TestMergingAPushBuildsNoTablePerField: every client state push is merged
// with the session's state under the session's lock, and the merge built one
// table keyed by window ID for each field only the daemon writes, most of them
// at the session's full size, filled them in one walk and read each back for
// every window of the push. A push of 100 windows in the session's order took
// 44 allocations and about 76 KB that way, and a push of one window 2, for a
// drag step or a focus move as much as for anything else.
//
// A push whose windows are in the session's order finds each window's copy at
// the same position, so the merge needs no table at all: exactly 0. A push in
// another order needs one index from ID to position, built once at the first
// window not found in place: at most 8, which a table per field cannot meet.
//
// It could pass wrongly in three ways, and each has a row:
//   - The merge did nothing, so it built nothing. Every row checks that each
//     window of the push holds its session window's five values after the call.
//   - The window at the same position is taken without comparing its ID. "another
//     order" swaps windows 0 and 99 and checks the values by ID.
//   - The index is built again at every window not found in place. "the reverse
//     order" misses at nearly every window.
//
// A window the session does not hold keeps the directory, pid and machine the
// push sent (a restore hands them in that way) and takes none of the session's
// records.
func TestMergingAPushBuildsNoTablePerField(t *testing.T) {
	sameFive := func(t *testing.T, row string, got, want WindowState) {
		t.Helper()
		if got.Cwd != want.Cwd || got.ShellPID != want.ShellPID || got.Note != want.Note ||
			got.AgentState != want.AgentState || got.AgentMessage != want.AgentMessage {
			t.Errorf("ASSERTION: %s: after the merge window %s holds cwd %q, pid %d, note %q, agent state %q, message %q; the session's copy holds %q, %d, %q, %q, %q",
				row, got.ID, got.Cwd, got.ShellPID, got.Note, got.AgentState, got.AgentMessage,
				want.Cwd, want.ShellPID, want.Note, want.AgentState, want.AgentMessage)
		}
	}
	inPlace := func(t *testing.T, row string, inc, canon *SessionState) {
		t.Helper()
		for i := range canon.Windows {
			sameFive(t, row, inc.Windows[i], canon.Windows[i])
		}
	}
	byID := func(t *testing.T, row string, inc, canon *SessionState) {
		t.Helper()
		if len(inc.Windows) != len(canon.Windows) {
			t.Fatalf("ASSERTION: %s: the push holds %d windows after the merge, want %d", row, len(inc.Windows), len(canon.Windows))
		}
		for _, w := range inc.Windows {
			cw, ok := findWindowState(canon, w.ID)
			if !ok {
				t.Fatalf("ASSERTION: %s: the push holds window %s, which the session does not", row, w.ID)
			}
			sameFive(t, row, w, cw)
		}
	}

	t.Run("the session's order, 100 windows", func(t *testing.T) {
		canon, inc := mergeFixture(100)
		allocs := leastAllocsPerRun(func() { retainDaemonExclusive(inc, canon) })
		t.Logf("%.0f allocations a merge (least of three readings)", allocs)
		if allocs != 0 {
			t.Errorf("ASSERTION: merging a push of 100 windows in the session's order allocated %.0f times (least of three readings), want 0: the merge builds tables again", allocs)
		}
		inPlace(t, "the session's order", inc, canon)
	})

	t.Run("one window", func(t *testing.T) {
		canon, inc := mergeFixture(1)
		allocs := leastAllocsPerRun(func() { retainDaemonExclusive(inc, canon) })
		t.Logf("%.0f allocations a merge (least of three readings)", allocs)
		if allocs != 0 {
			t.Errorf("ASSERTION: merging a push of one window allocated %.0f times (least of three readings), want 0: the merge builds tables again", allocs)
		}
		inPlace(t, "one window", inc, canon)
	})

	t.Run("another order, 100 windows", func(t *testing.T) {
		canon, inc := mergeFixture(100)
		inc.Windows[0], inc.Windows[99] = inc.Windows[99], inc.Windows[0]
		allocs := leastAllocsPerRun(func() { retainDaemonExclusive(inc, canon) })
		t.Logf("%.0f allocations a merge (least of three readings)", allocs)
		if allocs > 8 {
			t.Errorf("ASSERTION: merging a push of 100 windows with windows 0 and 99 swapped allocated %.0f times (least of three readings), want at most 8: the merge builds more than one index", allocs)
		}
		byID(t, "another order", inc, canon)
	})

	t.Run("the reverse order, 100 windows", func(t *testing.T) {
		canon, inc := mergeFixture(100)
		slices.Reverse(inc.Windows)
		allocs := leastAllocsPerRun(func() { retainDaemonExclusive(inc, canon) })
		t.Logf("%.0f allocations a merge (least of three readings)", allocs)
		if allocs > 8 {
			t.Errorf("ASSERTION: merging a push of 100 windows in the reverse order allocated %.0f times (least of three readings), want at most 8: the merge builds more than one index", allocs)
		}
		byID(t, "the reverse order", inc, canon)
	})

	t.Run("a window the session does not hold", func(t *testing.T) {
		canon, inc := mergeFixture(100)
		inc.Windows = append(inc.Windows, WindowState{
			ID: "window-not-held", Cwd: "/restored", ShellPID: 77, Host: "far",
			Note: "from the push", AgentState: AgentStateWorking,
		})
		retainDaemonExclusive(inc, canon)
		w := inc.Windows[100]
		if w.Cwd != "/restored" || w.ShellPID != 77 || w.Host != "far" {
			t.Errorf("ASSERTION: a window the session does not hold has cwd %q, pid %d, machine %q after the merge, want the push's \"/restored\", 77, \"far\"", w.Cwd, w.ShellPID, w.Host)
		}
		if w.Note != "" || w.AgentState != AgentStateNone {
			t.Errorf("ASSERTION: a window the session does not hold has note %q and agent state %q after the merge, want none of either: only the daemon writes them", w.Note, w.AgentState)
		}
		inPlace(t, "beside a window the session does not hold", inc, canon)
	})
}

// TestAClientPushCannotMoveWhatTheDaemonWrites: a client push is merged with
// the session's state, and the fields of a window only the daemon writes (a
// pane's grants and the start folder they are held to, who started a pane, its
// agent link and delegation, the dim override, the agent's resume record, the
// note, when it was minimized) are taken from the session. A push can neither
// set one on a pane that has none nor clear one the session holds. Each row is
// a session's windows, a push, and the push's windows after the merge.
func TestAClientPushCannotMoveWhatTheDaemonWrites(t *testing.T) {
	started, link, deleg := &PaneRef{}, &AgentLink{}, &Delegation{}
	dim := &config.PopupDim{}
	full := WindowState{
		ID: "w1", MinimizedAt: 3, StartDir: "/start", Note: "x", ForegroundCmd: "nvim", SSHOffer: &SSHOffer{}, Rail: "r",
		Kind: "web", URI: "https://example.invalid", Scratch: true, ScratchName: "s",
		AgentKind: "approval", AgentSessionID: "s", AgentSessionHarness: "h", AgentFlags: &KeptFlags{},
		AgentState: AgentStateWorking, AgentMessage: "m", AgentHarness: "h", AgentStateAt: 5, AgentReason: "r",
		AgentMeta: []AgentMetaToken{{}}, AgentQueued: 2, AgentDraftHeld: true, AgentSettled: true, AgentQuiet: true,
		AgentSubagents: 1, AgentSubagentList: []SubagentInfo{{}}, ProgramStatus: []ProgramStatusRecord{{}},
		Grants: []string{"admin"}, StartedBy: started, AgentLink: link, Delegation: deleg,
		Cwd: "/now", ShellPID: 42, Host: "far", CompletionSeq: 4,
	}
	for _, tc := range []struct {
		name                      string
		canonical, incoming, want []WindowState
	}{
		{
			name:      "the turn count does not go down",
			canonical: []WindowState{{ID: "w1", CompletionSeq: 5}},
			incoming:  []WindowState{{ID: "w1", CompletionSeq: 2}},
			want:      []WindowState{{ID: "w1", CompletionSeq: 5}},
		},
		{
			name:      "the turn count goes up",
			canonical: []WindowState{{ID: "w1", CompletionSeq: 2}},
			incoming:  []WindowState{{ID: "w1", CompletionSeq: 5}},
			want:      []WindowState{{ID: "w1", CompletionSeq: 5}},
		},
		{
			name:      "a push cannot set the dim override",
			canonical: []WindowState{{ID: "w1"}},
			incoming:  []WindowState{{ID: "w1", PopupDim: dim}},
			want:      []WindowState{{ID: "w1"}},
		},
		{
			name:      "a reason without an agent state is dropped",
			canonical: []WindowState{{ID: "w1", AgentReason: "r"}},
			incoming:  []WindowState{{ID: "w1"}},
			want:      []WindowState{{ID: "w1"}},
		},
		{
			name:      "who started a pane, its link and delegation are kept",
			canonical: []WindowState{{ID: "w1", StartedBy: started, AgentLink: link, Delegation: deleg}},
			incoming:  []WindowState{{ID: "w1"}},
			want:      []WindowState{{ID: "w1", StartedBy: started, AgentLink: link, Delegation: deleg}},
		},
		{
			name:      "a push cannot set who started a pane, its link or delegation",
			canonical: []WindowState{{ID: "w1"}},
			incoming:  []WindowState{{ID: "w1", StartedBy: started, AgentLink: link, Delegation: deleg}},
			want:      []WindowState{{ID: "w1"}},
		},
		{
			name:      "when a pane was minimized is the session's",
			canonical: []WindowState{{ID: "w1", MinimizedAt: 7}},
			incoming:  []WindowState{{ID: "w1", MinimizedAt: 3}},
			want:      []WindowState{{ID: "w1", MinimizedAt: 7}},
		},
		{
			name:      "a session that holds no window keeps the push's minimized stamp",
			canonical: nil,
			incoming:  []WindowState{{ID: "w1", MinimizedAt: 3}},
			want:      []WindowState{{ID: "w1", MinimizedAt: 3}},
		},
		{
			name:      "the note is kept",
			canonical: []WindowState{{ID: "w1", Note: "n"}},
			incoming:  []WindowState{{ID: "w1"}},
			want:      []WindowState{{ID: "w1", Note: "n"}},
		},
		{
			name:      "a push cannot set the note",
			canonical: []WindowState{{ID: "w1"}},
			incoming:  []WindowState{{ID: "w1", Note: "x"}},
			want:      []WindowState{{ID: "w1"}},
		},
		{
			name:      "a window the session does not hold keeps its scratch workspace",
			canonical: []WindowState{{ID: "w1"}},
			incoming:  []WindowState{{ID: "w2", Workspace: ScratchWorkspaceBase}},
			want:      []WindowState{{ID: "w2", Workspace: ScratchWorkspaceBase}},
		},
		{
			name:      "a push cannot set a pane's grants",
			canonical: []WindowState{{ID: "w1"}},
			incoming:  []WindowState{{ID: "w1", Grants: []string{"admin"}}},
			want:      []WindowState{{ID: "w1"}},
		},
		{
			name:      "a push cannot set a program's status records",
			canonical: []WindowState{{ID: "w1"}},
			incoming:  []WindowState{{ID: "w1", ProgramStatus: []ProgramStatusRecord{{}}}},
			want:      []WindowState{{ID: "w1"}},
		},
		{
			name:      "a push cannot set the start folder",
			canonical: []WindowState{{ID: "w1"}},
			incoming:  []WindowState{{ID: "w1", StartDir: "/elsewhere"}},
			want:      []WindowState{{ID: "w1"}},
		},
		{
			name:      "a push cannot set any field only the daemon writes on a pane that has none",
			canonical: []WindowState{{ID: "w1"}},
			incoming: []WindowState{{
				ID: "w1", MinimizedAt: 3, StartDir: "/elsewhere", Note: "x", ForegroundCmd: "nvim", SSHOffer: &SSHOffer{}, Rail: "r",
				Kind: "web", URI: "https://example.invalid", Scratch: true, ScratchName: "s", PopupDim: dim,
				AgentKind: "approval", AgentSessionID: "s", AgentSessionHarness: "h", AgentFlags: &KeptFlags{},
				AgentState: AgentStateWorking, AgentMessage: "m", AgentHarness: "h", AgentStateAt: 5, AgentReason: "r",
				AgentMeta: []AgentMetaToken{{}}, AgentQueued: 2, AgentDraftHeld: true, AgentSettled: true, AgentQuiet: true,
				AgentSubagents: 1, AgentSubagentList: []SubagentInfo{{}}, ProgramStatus: []ProgramStatusRecord{{}},
				Grants: []string{"admin"}, StartedBy: started, AgentLink: link, Delegation: deleg,
			}},
			want: []WindowState{{ID: "w1"}},
		},
		{
			name:      "a scratch pane stays on its scratch workspace",
			canonical: []WindowState{{ID: "w1", Scratch: true, Workspace: ScratchWorkspaceBase}},
			incoming:  []WindowState{{ID: "w1", Workspace: 2}},
			want:      []WindowState{{ID: "w1", Scratch: true, Workspace: ScratchWorkspaceBase}},
		},
		{
			name:      "a push cannot move a pane the session holds to a scratch workspace",
			canonical: []WindowState{{ID: "w1"}, {ID: "w2", Workspace: 3}},
			incoming:  []WindowState{{ID: "w2", Workspace: ScratchWorkspaceBase}, {ID: "w1"}},
			want:      []WindowState{{ID: "w2", Workspace: 3}, {ID: "w1"}},
		},
		{
			name:      "a scratch name without the scratch mark is dropped",
			canonical: []WindowState{{ID: "w1", ScratchName: "s"}},
			incoming:  []WindowState{{ID: "w1"}},
			want:      []WindowState{{ID: "w1"}},
		},
		{
			name:      "every field only the daemon writes is kept when the push holds none",
			canonical: []WindowState{full},
			incoming:  []WindowState{{ID: "w1"}},
			want:      []WindowState{full},
		},
		{
			name:      "a reason beside an agent message is kept",
			canonical: []WindowState{{ID: "w1", AgentMessage: "m", AgentReason: "r"}},
			incoming:  []WindowState{{ID: "w1"}},
			want:      []WindowState{{ID: "w1", AgentMessage: "m", AgentReason: "r"}},
		},
		{
			name:      "a reason beside an agent harness is kept",
			canonical: []WindowState{{ID: "w1", AgentHarness: "h", AgentReason: "r"}},
			incoming:  []WindowState{{ID: "w1"}},
			want:      []WindowState{{ID: "w1", AgentHarness: "h", AgentReason: "r"}},
		},
		{
			name:      "a reason beside an agent state stamp is kept",
			canonical: []WindowState{{ID: "w1", AgentStateAt: 5, AgentReason: "r"}},
			incoming:  []WindowState{{ID: "w1"}},
			want:      []WindowState{{ID: "w1", AgentStateAt: 5, AgentReason: "r"}},
		},
		{
			name:      "a reason beside an agent state alone is kept",
			canonical: []WindowState{{ID: "w1", AgentState: AgentStateWorking, AgentReason: "r"}},
			incoming:  []WindowState{{ID: "w1"}},
			want:      []WindowState{{ID: "w1", AgentState: AgentStateWorking, AgentReason: "r"}},
		},
		{
			name:      "a window the session does not hold, beside one it holds, does not keep the push's minimized stamp",
			canonical: []WindowState{{ID: "w1"}},
			incoming:  []WindowState{{ID: "w2", MinimizedAt: 3}},
			want:      []WindowState{{ID: "w2"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			incoming := &SessionState{Windows: tc.incoming}
			retainDaemonExclusive(incoming, &SessionState{Windows: tc.canonical})
			if got := incoming.Windows; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ASSERTION: %s: the push's windows after the merge differ:\n got %+v\nwant %+v", tc.name, got, tc.want)
			}
		})
	}
}
