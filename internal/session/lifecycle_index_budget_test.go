package session

import (
	"slices"
	"testing"
)

// The most a lifecycle snapshot of 100 windows, and one state change of a
// session with 100 windows, may allocate. A snapshot that builds its window
// index takes 12 at 100 windows, and 5 with the index made at full size;
// without it, the window list and the empty index are 2. A daemon-side change
// takes two snapshots and took 50 allocations, a rename or a focus move, with
// the index built in each, and 36 with the index made at full size; without
// it, 30. 36 sits far below the first and a few above the last, so the
// daemon's own goroutines do not trip it; an index made at full size is held
// by the snapshot's 2 and by the change that must fill none.
const (
	lifecycleSnapshotAllocs = 2
	lifecycleChangeAllocs   = 36
)

// TestAStateChangeLooksAWindowUpAtItsOwnPlace: every state change takes a
// lifecycle snapshot before it and one after, and each snapshot built a map
// from window ID to place for the diff and the agent rules to look windows up
// in. Unless a window opened, closed or moved, the two snapshots hold the same
// windows in the same order, so the window a caller looks up sits at its own
// place in the other list and the map was built, grown and read for nothing.
// The snapshot alone took 12 allocations at 100 windows, a daemon-side change
// 50.
//
// "the snapshot, 100 windows" holds snapshotLifecycle to 2 allocations.
// "a daemon-side change, 100 windows" (a rename) and "a focus move, 100
// windows" hold a whole change on a daemon's session to 36. "a change that
// keeps the windows fills no index" runs what a daemon-side change runs (the
// snapshot, the five agent rules in their order, the second snapshot, the
// diff) on two states of the same windows, and wants neither index filled.
//
// It could pass wrongly in four ways, and each has a row:
//   - Nothing was looked up, so nothing was built. "a change that keeps the
//     windows fills no index" checks the events (a rename, a focus move with
//     its pane) and what each of the five rules wrote.
//   - The window at the caller's place is taken without checking its ID.
//     "a window closed in the middle fills it once" closes one window, so the
//     windows after it are one place off, and wants one close and no create;
//     "windows in another order keep their own past" puts five windows in
//     another order, four at other places and one at its own, beside a new
//     one, and checks what each rule wrote on each.
//   - The index is filled again at every miss. "a window closed in the middle
//     fills it once" then takes one window out of the filled index and wants
//     the diff to report that window created.
//   - The change never ran. The two daemon rows read the state back.
func TestAStateChangeLooksAWindowUpAtItsOwnPlace(t *testing.T) {
	// testing.AllocsPerRun counts every allocation in the process, so a
	// goroutine a neighbour left winding down is charged to f. An allocation f
	// makes itself is in every one of three readings, so the least still
	// counts it.
	least := func(f func()) float64 {
		n := testing.AllocsPerRun(50, f)
		for range 2 {
			n = min(n, testing.AllocsPerRun(50, f))
		}
		return n
	}

	// change runs what a daemon-side change runs between its two snapshots,
	// in the order mutateStateLocked runs it, with st1 as the state before and
	// st2 as the state after.
	change := func(st1, st2 *SessionState) (before, after lifecycleSnapshot, events []SessionEvent) {
		before = snapshotLifecycle(st1)
		s := &Session{state: st2}
		s.noteAgentTurnsLocked(before, 1)
		clearStaleSilenceLocked(before, st2)
		clearNowAtRestLocked(before, st2)
		wakeSettledLocked(before, st2)
		clearQuietOfLeftAgentLocked(before, st2)
		after = snapshotLifecycle(st2)
		events = diffLifecycle(before, after)
		return before, after, events
	}

	// agents gives every window an agent at work that went silent, settled
	// and quiet; every tenth from the second is at rest with a now key, every
	// tenth from the third has no agent, so each of the five rules reaches
	// its lookup.
	agents := func(st *SessionState) {
		for i := range st.Windows {
			w := &st.Windows[i]
			w.AgentState = AgentStateWorking
			w.AgentStateAt = int64(100 + i)
			w.AgentReason = "silent"
			w.AgentSettled = true
			w.AgentQuiet = true
			switch i % 10 {
			case 1:
				w.AgentState = AgentStateIdle
				w.AgentReason = ""
				w.AgentMeta = []AgentMetaToken{{Key: AgentMetaNow, Value: "x"}}
			case 2:
				w.AgentState = AgentStateNone
				w.AgentReason = ""
			}
		}
	}

	// session is a session of a fresh daemon with 100 windows and no pane
	// behind any of them.
	session := func(t *testing.T, name string) *Session {
		t.Helper()
		d, _ := startTestDaemon(t)
		sess, err := d.manager.CreateSession(name, &SessionConfig{}, 80, 24)
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		sess.stateMu.Lock()
		sess.state.Windows = benchState(100).Windows
		sess.stateMu.Unlock()
		return sess
	}

	t.Run("the snapshot, 100 windows", func(t *testing.T) {
		st := benchState(100)
		var snap lifecycleSnapshot
		allocs := least(func() { snap = snapshotLifecycle(st) })
		if len(snap.windows) != 100 || snap.windows[99].id != st.Windows[99].ID {
			t.Fatalf("ASSERTION: the snapshot holds %d windows, want 100 ending in %q: nothing was measured", len(snap.windows), st.Windows[99].ID)
		}
		if allocs > lifecycleSnapshotAllocs {
			t.Errorf("ASSERTION: a lifecycle snapshot of 100 windows took %.0f allocations (least of 3), want at most %d: the snapshot builds its window index",
				allocs, lifecycleSnapshotAllocs)
		}
	})

	t.Run("a change that keeps the windows fills no index", func(t *testing.T) {
		st1, st2 := benchState(100), benchState(100)
		agents(st1)
		agents(st2)
		st2.Windows[3].CustomName = "named"
		st2.Windows[5].X += 3
		st2.Windows[7].AgentState = AgentStateDone
		st2.FocusedWindowID = st2.Windows[50].ID

		before, after, events := change(st1, st2)

		if len(before.index) != 0 || len(after.index) != 0 {
			t.Errorf("ASSERTION: a change that keeps every window in its place filled %d entries of the index before and %d after, want none: a window is looked up in a map built for it",
				len(before.index), len(after.index))
		}
		retitled := slices.ContainsFunc(events, func(ev SessionEvent) bool {
			return ev.Type == EventWindowRetitled && ev.Window == st2.Windows[3].ID && ev.Title == "named"
		})
		if !retitled {
			t.Errorf("ASSERTION: no window-retitled event for %s, renamed \"named\"; events: %+v", st2.Windows[3].ID, events)
		}
		focused := slices.ContainsFunc(events, func(ev SessionEvent) bool {
			return ev.Type == EventWindowFocused && ev.Window == st2.Windows[50].ID && ev.PTYID == "pty-0050"
		})
		if !focused {
			t.Errorf("ASSERTION: no window-focused event for %s with its pane pty-0050; events: %+v", st2.Windows[50].ID, events)
		}
		if w := st2.Windows[7]; w.CompletionSeq != 1 || w.AgentReason != "" {
			t.Errorf("ASSERTION: window 7 went from working to done with CompletionSeq %d and reason %q, want 1 and none: its turn or its silence was not looked up",
				w.CompletionSeq, w.AgentReason)
		}
		if got := st2.Windows[8].AgentReason; got != "silent" {
			t.Errorf("ASSERTION: window 8, still working since the same moment, has reason %q, want silent", got)
		}
		if got := len(st2.Windows[21].AgentMeta); got != 1 {
			t.Errorf("ASSERTION: window 21, at rest before and after, holds %d meta tokens, want its now key kept", got)
		}
		if !st2.Windows[22].AgentQuiet {
			t.Errorf("ASSERTION: window 22, with no agent before and after, lost its quiet mark")
		}
		if !st2.Windows[23].AgentSettled {
			t.Errorf("ASSERTION: window 23, working before and after, lost its settle")
		}
	})

	t.Run("a window closed in the middle fills it once", func(t *testing.T) {
		st1, st2 := benchState(100), benchState(100)
		st2.Windows = slices.Delete(st2.Windows, 40, 41)

		before, after, events := change(st1, st2)

		if len(before.index) != 100 || len(after.index) != 99 {
			t.Errorf("ASSERTION: after a close the index holds %d entries before and %d after, want 100 and 99", len(before.index), len(after.index))
		}
		var closed, created []string
		for _, ev := range events {
			switch ev.Type {
			case EventWindowClosed:
				closed = append(closed, ev.Window)
			case EventWindowCreated:
				created = append(created, ev.Window)
			}
		}
		if !slices.Equal(closed, []string{st1.Windows[40].ID}) || len(created) != 0 {
			t.Errorf("ASSERTION: closing %s reported closed %v and created %v, want only it closed: a window was taken by its place without its ID",
				st1.Windows[40].ID, closed, created)
		}

		delete(before.index, st1.Windows[70].ID)
		created = nil
		for _, ev := range diffLifecycle(before, after) {
			if ev.Type == EventWindowCreated {
				created = append(created, ev.Window)
			}
		}
		if !slices.Equal(created, []string{st1.Windows[70].ID}) {
			t.Errorf("ASSERTION: with %s taken out of the filled index the diff reported created %v, want only it: the index is filled again at every miss",
				st1.Windows[70].ID, created)
		}
	})

	t.Run("windows in another order keep their own past", func(t *testing.T) {
		now := []AgentMetaToken{{Key: AgentMetaNow, Value: "x"}}
		a := WindowState{ID: "a", AgentState: AgentStateWorking, AgentStateAt: 1, AgentReason: "silent", AgentSettled: true}
		b := WindowState{ID: "b", AgentState: AgentStateNeedsInput, AgentStateAt: 2, AgentSettled: true, AgentMeta: now}
		c := WindowState{ID: "c", AgentState: AgentStateWorking, AgentStateAt: 3, AgentReason: "silent", AgentSettled: true, AgentQuiet: true, AgentMeta: now}
		e := WindowState{ID: "e", AgentState: AgentStateDone, AgentStateAt: 4, AgentMeta: now}
		f := WindowState{ID: "f", AgentState: AgentStateNone, AgentQuiet: true}
		st1 := &SessionState{Windows: []WindowState{a, b, c, e, f}}
		c2 := c
		c2.AgentState, c2.AgentStateAt = AgentStateNone, 9
		d := WindowState{ID: "d", AgentReason: "silent", AgentSettled: true, AgentQuiet: true}
		st2 := &SessionState{Windows: []WindowState{b, a, c2, f, e, d}}

		change(st1, st2)

		byID := func(id string) WindowState {
			i := slices.IndexFunc(st2.Windows, func(w WindowState) bool { return w.ID == id })
			if i < 0 {
				t.Fatalf("ASSERTION: window %s is gone from the state", id)
			}
			return st2.Windows[i]
		}
		if w := byID("a"); w.AgentReason != "silent" || !w.AgentSettled || w.CompletionSeq != 0 {
			t.Errorf("ASSERTION: a, working since the same moment, has reason %q, settled %v, CompletionSeq %d, want silent, true, 0", w.AgentReason, w.AgentSettled, w.CompletionSeq)
		}
		if w := byID("b"); !w.AgentSettled || len(w.AgentMeta) != 1 || w.CompletionSeq != 0 {
			t.Errorf("ASSERTION: b, still waiting, has settled %v, %d meta tokens, CompletionSeq %d, want true, 1, 0", w.AgentSettled, len(w.AgentMeta), w.CompletionSeq)
		}
		if w := byID("e"); len(w.AgentMeta) != 1 || w.CompletionSeq != 0 {
			t.Errorf("ASSERTION: e, done before and after, has %d meta tokens, CompletionSeq %d, want 1, 0", len(w.AgentMeta), w.CompletionSeq)
		}
		if w := byID("f"); !w.AgentQuiet {
			t.Errorf("ASSERTION: f, with no agent before and after, lost its quiet mark")
		}
		if w := byID("c"); w.AgentReason != "" || w.AgentSettled || w.AgentQuiet || w.AgentMeta != nil {
			t.Errorf("ASSERTION: c, whose agent left, has reason %q, settled %v, quiet %v, meta %v, want none of them", w.AgentReason, w.AgentSettled, w.AgentQuiet, w.AgentMeta)
		}
		if w := byID("d"); w.AgentReason != "" || !w.AgentSettled || !w.AgentQuiet {
			t.Errorf("ASSERTION: d, new, has reason %q, settled %v, quiet %v, want none, true, true", w.AgentReason, w.AgentSettled, w.AgentQuiet)
		}
	})

	t.Run("a daemon-side change, 100 windows", func(t *testing.T) {
		sess := session(t, "lifecycle-rename")
		names := [2]string{"first name", "second name"}
		var i int
		allocs := least(func() {
			i++
			if err := sess.SetDisplayName(names[i&1]); err != nil {
				t.Fatalf("SetDisplayName: %v", err)
			}
		})
		if got, want := sess.GetState().DisplayName, names[i&1]; got != want {
			t.Fatalf("ASSERTION: the session is named %q, want %q, the last name set: nothing was measured", got, want)
		}
		if allocs > lifecycleChangeAllocs {
			t.Errorf("ASSERTION: a daemon-side rename of a session with 100 windows took %.0f allocations (least of 3), want at most %d: each snapshot builds its window index",
				allocs, lifecycleChangeAllocs)
		}
	})

	t.Run("a focus move, 100 windows", func(t *testing.T) {
		sess := session(t, "lifecycle-focus")
		st := sess.GetState()
		ids := [2]string{st.Windows[50].ID, st.Windows[51].ID}
		var i int
		allocs := least(func() {
			i++
			err := sess.mutateState(func(st *SessionState) error {
				st.FocusedWindowID = ids[i&1]
				return nil
			})
			if err != nil {
				t.Fatalf("mutateState: %v", err)
			}
		})
		if got, want := sess.GetState().FocusedWindowID, ids[i&1]; got != want {
			t.Fatalf("ASSERTION: the focused window is %q, want %q, the last one set: nothing was measured", got, want)
		}
		if allocs > lifecycleChangeAllocs {
			t.Errorf("ASSERTION: a focus move in a session with 100 windows took %.0f allocations (least of 3), want at most %d: a snapshot builds its window index",
				allocs, lifecycleChangeAllocs)
		}
	})
}
