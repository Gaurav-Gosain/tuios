package session

import (
	"reflect"
	"slices"
	"testing"
)

// TestAChangeThatMovesNoPaneBuildsNoPaneTable: every change to a session's
// state, a client's push and a daemon-side change alike, runs
// notePaneMovesLocked, which built a new table of where each pane is (and two
// sets of workspaces beside it) and threw the old one away, even when no pane
// had opened, closed, moved, got another PTY or changed whether an agent is
// live in it. At 100 panes that was 7 allocations a call.
//
// The budget is exactly 0: when no pane moved, the table the call would build
// is the one it already holds, so it has nothing to make. The count is the
// least of three testing.AllocsPerRun readings (leastAllocsPerRun), so a
// goroutine an earlier test left winding down is not charged to the call.
//
// It could pass wrongly in three ways, and each has a row:
//   - A pane that left, moved to another workspace or came is not seen, so the
//     close-up never hears of it. "a pane a person closed is seen", "a pane
//     moved to another workspace is seen" and "a new pane is seen" check the
//     table and the report after each.
//   - A pane's new PTY or agent mark is not taken into the table, which shows
//     only when that pane later leaves: its exit is then read against the old
//     PTY or the old mark. "an agent that came without a move is remembered
//     when its shell exits" (by a harness, or by a state alone) and "a PTY
//     that changed without a move is remembered when its program exits" each
//     have the case that must not count as a person's close beside the one
//     that must.
//   - The function did nothing at all. "no pane moved" checks that the table
//     it returns holds the 99 panes that are not scratch panes.
//
// Each row has its own session.
func TestAChangeThatMovesNoPaneBuildsNoPaneTable(t *testing.T) {
	// A session of 100 panes, one of them a scratch pane and one on
	// workspace 0, whose table has been built once.
	fixture := func() *Session {
		st := benchState(100)
		st.Windows[10].Scratch = true
		st.Windows[18].Workspace = 0
		s := &Session{state: st}
		s.notePaneMovesLocked()
		return s
	}
	// A session of two panes, on workspaces 1 and 2, whose table has been
	// built once.
	twoPanes := func() *Session {
		s := &Session{state: &SessionState{Windows: []WindowState{
			{ID: "a", PTYID: "p1", Workspace: 1},
			{ID: "b", PTYID: "q1", Workspace: 2},
		}}}
		s.notePaneMovesLocked()
		return s
	}

	t.Run("no pane moved", func(t *testing.T) {
		s := fixture()
		if n := len(s.panesAt); n != 99 {
			t.Fatalf("ASSERTION: the table holds %d panes after the first call, want 99 (100 panes, one of them a scratch pane)", n)
		}
		before := reflect.ValueOf(s.panesAt).Pointer()

		if allocs := leastAllocsPerRun(func() { s.notePaneMovesLocked() }); allocs != 0 {
			t.Errorf("ASSERTION: a change that moves no pane allocated %.0f times at 100 panes (least of three readings), want 0: it builds the pane table again", allocs)
		}

		m, byPerson := s.notePaneMovesLocked()
		if got := reflect.ValueOf(m).Pointer(); got != before {
			t.Errorf("ASSERTION: a change that moves no pane returned another map (%#x), want the table it held (%#x)", got, before)
		}
		if got := reflect.ValueOf(s.panesAt).Pointer(); got != before {
			t.Errorf("ASSERTION: a change that moves no pane replaced the table it held (%#x) with another map (%#x)", before, got)
		}
		if len(m) != 99 {
			t.Errorf("ASSERTION: a change that moves no pane returned a table of %d panes, want 99", len(m))
		}
		if byPerson {
			t.Errorf("ASSERTION: a change that moves no pane reported that a person took a pane off a workspace")
		}
	})

	t.Run("a pane a person closed is seen", func(t *testing.T) {
		s := fixture()
		id := s.state.Windows[40].ID
		s.state.Windows = slices.Delete(s.state.Windows, 40, 41)

		m, byPerson := s.notePaneMovesLocked()
		if len(m) != 98 {
			t.Errorf("ASSERTION: after a pane was closed the table holds %d panes, want 98", len(m))
		}
		if at, ok := m[id]; ok {
			t.Errorf("ASSERTION: after pane %s was closed the table still holds it at %+v", id, at)
		}
		if !byPerson {
			t.Errorf("ASSERTION: a pane closed without its process exiting on its own was not reported as a person's close")
		}
	})

	t.Run("a pane moved to another workspace is seen", func(t *testing.T) {
		s := fixture()
		w := &s.state.Windows[40]
		w.Workspace = w.Workspace%9 + 1

		m, byPerson := s.notePaneMovesLocked()
		if got := m[w.ID].ws; got != w.Workspace {
			t.Errorf("ASSERTION: after pane %s moved to workspace %d the table holds it on workspace %d", w.ID, w.Workspace, got)
		}
		if !byPerson {
			t.Errorf("ASSERTION: a pane moved to another workspace was not reported as a person's move")
		}
	})

	t.Run("a new pane is seen", func(t *testing.T) {
		s := fixture()
		s.state.Windows = append(s.state.Windows, WindowState{ID: "new", PTYID: "pty-new", Workspace: 2})

		m, byPerson := s.notePaneMovesLocked()
		if len(m) != 100 {
			t.Errorf("ASSERTION: after a pane opened the table holds %d panes, want 100", len(m))
		}
		if got, want := m["new"], (paneAt{ws: 2, pty: "pty-new"}); got != want {
			t.Errorf("ASSERTION: after a pane opened the table holds it as %+v, want %+v", got, want)
		}
		if byPerson {
			t.Errorf("ASSERTION: a pane that opened was reported as a person taking a pane off a workspace")
		}
	})

	t.Run("an agent that came without a move is remembered when its shell exits", func(t *testing.T) {
		run := func(agent bool) (bool, bool) {
			s := twoPanes()
			if agent {
				s.state.Windows[1].AgentHarness = "claude-code"
			}
			s.notePaneMovesLocked()
			s.notePaneExitedAlone("q1", true, 0)
			s.state.Windows = s.state.Windows[:1]
			_, byPerson := s.notePaneMovesLocked()
			return byPerson, s.selfEmptied[2]
		}

		if byPerson, selfEmptied := run(true); byPerson || !selfEmptied {
			t.Errorf("ASSERTION: the shell of a pane an agent came to without a move exited: reported a person's close %v, workspace 2 emptied on its own %v; want false and true: the agent mark was not taken into the table", byPerson, selfEmptied)
		}
		if byPerson, selfEmptied := run(false); !byPerson || selfEmptied {
			t.Errorf("ASSERTION: the shell of a pane with no agent exited: reported a person's close %v, workspace 2 emptied on its own %v; want true and false: a shell that exits is a person's close", byPerson, selfEmptied)
		}

		runStateOnly := func() (bool, bool) {
			s := twoPanes()
			s.state.Windows[1].AgentState = AgentStateWorking
			s.notePaneMovesLocked()
			s.notePaneExitedAlone("q1", true, 0)
			s.state.Windows = s.state.Windows[:1]
			_, byPerson := s.notePaneMovesLocked()
			return byPerson, s.selfEmptied[2]
		}
		if byPerson, selfEmptied := runStateOnly(); byPerson || !selfEmptied {
			t.Errorf("ASSERTION: the shell of a pane whose agent state was set without a harness exited: reported a person's close %v, workspace 2 emptied on its own %v; want false and true: an agent state set without a harness was not taken into the table", byPerson, selfEmptied)
		}
	})

	t.Run("a PTY that changed without a move is remembered when its program exits", func(t *testing.T) {
		run := func(exited string) (bool, bool) {
			s := twoPanes()
			s.state.Windows[1].PTYID = "q2"
			s.notePaneMovesLocked()
			s.notePaneExitedAlone(exited, false, 0)
			s.state.Windows = s.state.Windows[:1]
			_, byPerson := s.notePaneMovesLocked()
			return byPerson, s.selfEmptied[2]
		}

		if byPerson, selfEmptied := run("q2"); byPerson || !selfEmptied {
			t.Errorf("ASSERTION: the program on a pane's new PTY q2 exited on its own: reported a person's close %v, workspace 2 emptied on its own %v; want false and true: the new PTY was not taken into the table", byPerson, selfEmptied)
		}
		if byPerson, selfEmptied := run("q1"); !byPerson || selfEmptied {
			t.Errorf("ASSERTION: only the pane's old PTY q1 was marked as exited on its own: reported a person's close %v, workspace 2 emptied on its own %v; want true and false: the pane on q2 was closed by a person", byPerson, selfEmptied)
		}
	})
}
