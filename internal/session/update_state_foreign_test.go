package session

import (
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestUpdateStateDropsAnotherSessionsWindows is the daemon half of the race a
// switch to a linked host and back can lose: the client applies a state sync
// the host's connection queued before the switch, and its next push hands this
// session the host's window list. The end-to-end test reaches that only by
// timing, and not at all once the client stops applying the left connection's
// syncs, so the daemon's own refusal is held here as a deterministic race
// regression: a real client pushes, a real daemon answers.
//
// Written down before the code, how this test could pass with the session
// still wrong:
//   - the push is never sent or never handled, so the session stays as it was
//     for the wrong reason: the push's own count must show in PushSeen;
//   - the drop happens but the sender is never told, so the client keeps the
//     foreign view: the "reconcile" reply must list the session's windows;
//   - the reply is the session's state but the session itself took the push:
//     GetState is read too, with its version and its focus;
//   - the drop refuses legitimate pushes as well (a mixed list, an empty list,
//     a window whose live pane the session made): rows b1 to b3, each taken;
//   - the drop also catches a stale push, which the stale merge already
//     answers by restoring the session's windows and keeping what the client
//     owns: row b4 pushes a ratio from a push behind the session and must see
//     it taken, with no refusal logged;
//   - the push is behind and goes through the stale merge, which drops unknown
//     windows already: the push in the first row is built on a version above
//     the session's, so only the new refusal can keep the windows;
//   - the refusal is silent, or logged for every push: the log line is looked
//     for in the drop and its absence in the takes, and a second foreign push
//     from the same client must not add a second line (row a2).
func TestUpdateStateDropsAnotherSessionsWindows(t *testing.T) {
	d, _ := startTestDaemon(t)

	t.Run("a list naming none of the session's windows is answered with the session's state", func(t *testing.T) {
		name := foreignSessionName("foreign-drop")
		sess := makeSessionWithWindow(t, d, name)
		c := attachTestClient(t, name)
		syncs := takeSyncs(c)
		before := sess.GetState()
		held := windowPTYs(before.Windows)
		if len(held) != 1 {
			t.Fatalf("the session holds %d windows, want 1", len(held))
		}

		push := sess.GetState()
		push.Windows = []WindowState{
			{ID: "far-window-one", PTYID: "far-pty-one", Title: "far one", Workspace: 1, Width: 40, Height: 12},
			{ID: "far-window-two", PTYID: "far-pty-two", Title: "far two", Workspace: 1, X: 40, Width: 40, Height: 12},
		}
		push.FocusedWindowID = "far-window-one"
		// A busy host numbers its versions far ahead of this session's.
		push.BaseVersion = before.Version + 40
		seq := pushFrom(t, c, push)
		after := stateOnceCounted(t, sess, c, seq)

		if got := windowPTYs(after.Windows); !slices.Equal(got, held) {
			t.Errorf("the session lists %v after the foreign push, want its own %v", got, held)
		}
		if after.Version != before.Version {
			t.Errorf("the session's version is %d after the foreign push, want %d", after.Version, before.Version)
		}
		if after.FocusedWindowID != before.FocusedWindowID {
			t.Errorf("the session's focus is %q after the foreign push, want %q", after.FocusedWindowID, before.FocusedWindowID)
		}
		reply := syncs.next(t, "reconcile")
		if reply.state == nil {
			t.Fatal("the reconcile reply carried no state")
		}
		if got := windowPTYs(reply.state.Windows); !slices.Equal(got, held) {
			t.Errorf("the reconcile reply lists %v, want the session's own %v", got, held)
		}
		if !refusalLogged(sess.Name()) {
			t.Errorf("the log holds no line %q", foreignRefusalLine(sess.Name()))
		}
	})

	t.Run("a second foreign push from the same client is logged once", func(t *testing.T) {
		name := foreignSessionName("foreign-once")
		sess := makeSessionWithWindow(t, d, name)
		c := attachTestClient(t, name)
		before := sess.GetState()
		for range 2 {
			push := sess.GetState()
			push.Windows = []WindowState{{ID: "far-window", PTYID: "far-pty", Title: "far", Workspace: 1, Width: 40, Height: 12}}
			push.FocusedWindowID = "far-window"
			push.BaseVersion = before.Version + 40
			seq := pushFrom(t, c, push)
			stateOnceCounted(t, sess, c, seq)
		}
		if n := refusalCount(sess.Name()); n != 1 {
			t.Errorf("the log holds %d lines %q after two foreign pushes from one client, want 1", n, foreignRefusalLine(sess.Name()))
		}
	})

	t.Run("a stale push naming none of the session's windows is merged, not refused", func(t *testing.T) {
		name := foreignSessionName("foreign-stale")
		sess := makeSessionWithWindow(t, d, name)
		c := attachTestClient(t, name)
		takeSyncs(c)
		if err := sess.SetDisplayName("first"); err != nil {
			t.Fatal(err)
		}
		if err := sess.SetDisplayName("second"); err != nil {
			t.Fatal(err)
		}
		held := windowPTYs(sess.GetState().Windows)
		// Version 1 is behind the session's.
		push := sess.GetState()
		push.Windows = []WindowState{{ID: "far-window", PTYID: "far-pty", Title: "far", Workspace: 1, Width: 40, Height: 12}}
		push.FocusedWindowID = "far-window"
		push.WorkspaceMasterRatio = map[int]float64{1: 0.7}
		push.BaseVersion = 1
		seq := pushFrom(t, c, push)
		after := stateOnceCounted(t, sess, c, seq)
		if got := windowPTYs(after.Windows); !slices.Equal(got, held) {
			t.Errorf("the session lists %v after a stale foreign push, want its own %v", got, held)
		}
		if got := after.WorkspaceMasterRatio[1]; got != 0.7 {
			t.Errorf("the session's master ratio is %v after a stale push that set 0.7, want the push's", got)
		}
		if refusalLogged(sess.Name()) {
			t.Errorf("the log holds %q for a stale push, which the stale merge answers", foreignRefusalLine(sess.Name()))
		}
	})

	t.Run("a list naming a held window and an unknown one is taken", func(t *testing.T) {
		name := foreignSessionName("foreign-mixed")
		sess := makeSessionWithWindow(t, d, name)
		c := attachTestClient(t, name)
		before := sess.GetState()
		push := sess.GetState()
		push.Windows = append(push.Windows, WindowState{ID: "mixed-window", PTYID: "mixed-pty", Title: "mixed", Workspace: 1, X: 40, Width: 40, Height: 12})
		push.BaseVersion = before.Version
		want := windowPTYs(push.Windows)
		seq := pushFrom(t, c, push)
		after := stateOnceCounted(t, sess, c, seq)
		if got := windowPTYs(after.Windows); !slices.Equal(got, want) {
			t.Errorf("the session lists %v after a mixed push, want both windows %v", got, want)
		}
		if refusalLogged(sess.Name()) {
			t.Errorf("the log holds %q for a mixed push", foreignRefusalLine(sess.Name()))
		}
	})

	t.Run("a list with no windows is taken", func(t *testing.T) {
		name := foreignSessionName("foreign-empty")
		sess := makeSessionWithWindow(t, d, name)
		c := attachTestClient(t, name)
		before := sess.GetState()
		push := sess.GetState()
		push.Windows = nil
		push.FocusedWindowID = ""
		push.BaseVersion = before.Version
		seq := pushFrom(t, c, push)
		after := stateOnceCounted(t, sess, c, seq)
		if len(after.Windows) != 0 {
			t.Errorf("the session lists %v after a push with no windows, want none", windowPTYs(after.Windows))
		}
		if refusalLogged(sess.Name()) {
			t.Errorf("the log holds %q for a push with no windows", foreignRefusalLine(sess.Name()))
		}
	})

	t.Run("a list naming only a new window on a live pane of the session is taken", func(t *testing.T) {
		name := foreignSessionName("foreign-live")
		sess := makeSessionWithWindow(t, d, name)
		c := attachTestClient(t, name)
		before := sess.GetState()
		pty, err := sess.CreatePTY("live-window", 40, 12, nil)
		if err != nil {
			t.Fatalf("CreatePTY: %v", err)
		}
		push := sess.GetState()
		push.Windows = []WindowState{{ID: "live-window", PTYID: pty.ID, Title: "live", Workspace: 1, Width: 80, Height: 24}}
		push.FocusedWindowID = "live-window"
		push.BaseVersion = before.Version
		want := windowPTYs(push.Windows)
		seq := pushFrom(t, c, push)
		after := stateOnceCounted(t, sess, c, seq)
		if got := windowPTYs(after.Windows); !slices.Equal(got, want) {
			t.Errorf("the session lists %v after a push naming its live pane, want %v", got, want)
		}
		if refusalLogged(sess.Name()) {
			t.Errorf("the log holds %q for a push naming a live pane", foreignRefusalLine(sess.Name()))
		}
	})
}

// foreignSessionCounter numbers the sessions this file's tests make.
var foreignSessionCounter atomic.Uint64

// foreignSessionName is stem with a number that no other call in the process
// has returned. The log buffer is the process's, so a fixed session name would
// count the refusal lines of an earlier run of the same test.
func foreignSessionName(stem string) string {
	return stem + "-" + strconv.FormatUint(foreignSessionCounter.Add(1), 10)
}

// pushFrom sends state as c's next push and returns the push's count.
func pushFrom(t *testing.T, c *TUIClient, state *SessionState) uint64 {
	t.Helper()
	if err := c.UpdateState(state); err != nil {
		t.Fatalf("push: %v", err)
	}
	return c.pushSeq.Load()
}

// stateOnceCounted waits until the session has counted c's push seq, and
// returns the state as it stood then. The count is taken under the same lock
// as the state the push makes, so once it shows, the push has been decided.
func stateOnceCounted(t *testing.T, sess *Session, c *TUIClient, seq uint64) *SessionState {
	t.Helper()
	origin := c.pushOrigin.Load()
	if origin == nil {
		t.Fatal("the client has no push origin")
	}
	deadline := time.Now().Add(5 * time.Second * testDeadlineScale)
	for {
		st := sess.GetState()
		if got := st.PushSeen[*origin]; got >= seq {
			if got != seq {
				t.Fatalf("PushSeen for the client says %d, want the push's %d", got, seq)
			}
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("PushSeen for the client says %d, never the push's %d", st.PushSeen[*origin], seq)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// windowPTYs is each window as "id=pty", sorted.
func windowPTYs(ws []WindowState) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.ID+"="+w.PTYID)
	}
	slices.Sort(out)
	return out
}

// foreignRefusalLine is the daemon's log line for a push it dropped as
// another session's windows.
func foreignRefusalLine(session string) string {
	return "Refused a state update to " + session + ": it names none of the session's windows"
}

// refusalLogged reports whether the log buffer holds the refusal line for
// session.
func refusalLogged(session string) bool {
	return refusalCount(session) > 0
}

// refusalCount is how many lines of the log buffer are the refusal line for
// session.
func refusalCount(session string) int {
	line := foreignRefusalLine(session)
	n := 0
	for _, e := range GetLogEntries(0) {
		if strings.Contains(e.Message, line) {
			n++
		}
	}
	return n
}
