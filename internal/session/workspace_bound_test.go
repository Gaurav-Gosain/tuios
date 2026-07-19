package session

import "testing"

// TestWorkspaceBoundFollowsTheSession pins that the workspace range check is the
// session's own, not a constant this package guessed. It used to be a hardcoded
// 9 duplicated here to avoid a config import, which meant a client configured
// for more workspaces could reach one the daemon then refused to switch to, and
// one configured for fewer got no error for a workspace it does not have.
func TestWorkspaceBoundFollowsTheSession(t *testing.T) {
	for _, tc := range []struct {
		name    string
		declare int
		ws      int
		wantErr bool
	}{
		{"unstated falls back to the default", 0, 9, false},
		{"unstated rejects past the default", 0, 10, true},
		{"a larger session accepts past the default", 12, 12, false},
		{"a smaller session rejects inside the default", 3, 4, true},
		{"zero is out of range whatever the count", 12, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := newTestSession(t)
			if tc.declare > 0 {
				// This is how the count arrives in practice: a client reports it
				// with the rest of its state.
				st := sess.GetState()
				st.NumWorkspaces = tc.declare
				sess.UpdateState(st)
			}

			err := sess.SwitchDaemonWorkspace(tc.ws)
			if (err != nil) != tc.wantErr {
				t.Fatalf("SwitchDaemonWorkspace(%d) error = %v, wantErr = %v", tc.ws, err, tc.wantErr)
			}
			if err == nil && sess.GetState().CurrentWorkspace != tc.ws {
				t.Fatalf("workspace = %d, want %d", sess.GetState().CurrentWorkspace, tc.ws)
			}
		})
	}
}

// TestWorkspaceCountIsReportedNotAssumed covers the query side: session info
// reported the constant regardless of what the session actually had.
func TestWorkspaceCountIsReportedNotAssumed(t *testing.T) {
	sess := newTestSession(t)
	st := sess.GetState()
	st.NumWorkspaces = 4
	st.LayoutMode = "scrolling"
	sess.UpdateState(st)

	data := buildSessionInfoData(sess, sess.GetState(), false)
	if got := data["num_workspaces"]; got != 4 {
		t.Fatalf("num_workspaces = %v, want 4", got)
	}
	if got := data["layout_mode"]; got != "scrolling" {
		t.Fatalf("layout_mode = %v, want scrolling", got)
	}
}
