package tuie2e

import (
	"testing"
	"time"
)

// An agent in a pane on workspace 3 opens a window while the person looks at
// workspace 1 (#1494). The window opens on workspace 3, beside the agent, and
// the person stays where they are. The same call from outside every pane
// still opens on the workspace the session shows.
//
// How this could pass wrongly, written down first:
//   - The session could show workspace 3 when the call runs. The test checks
//     that it shows 1 before and after.
//   - The probe could be read before it exists. The test waits for the row.
//   - The default could move for every caller. The call from outside every
//     pane must still land on workspace 1.
//
// Negative control: with the callerWorkspaceIn call cut from verbNewWindow,
// the probe opens on workspace 1 and the test fails.
func TestNewWindowFromAPaneOpensOnThatPanesWorkspace(t *testing.T) {
	base := t.TempDir()
	const sess = "nwws"
	term := startHome(t, base, sess)

	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "caller", "--workspace", "3", "--no-focus"); err != nil {
		t.Fatalf("new-window caller: %v\n%s", err, out)
	}
	rowNamed := func(name string) (xpanesRow, bool) {
		for _, r := range xpanesRowsIn(t, base, sess) {
			if r.Name == name {
				return r, true
			}
		}
		return xpanesRow{}, false
	}
	waitRow := func(name string) xpanesRow {
		t.Helper()
		deadline := time.Now().Add(shellTimeout)
		for {
			if r, ok := rowNamed(name); ok {
				return r
			}
			if time.Now().After(deadline) {
				t.Fatalf("no window named %s: %v\n%s", name, xpanesRowsIn(t, base, sess), term.Snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	caller := waitRow("caller")
	if caller.Workspace != 3 {
		t.Fatalf("caller is on workspace %d, want 3", caller.Workspace)
	}
	if ws := currentWorkspace(t, base, sess); ws != 1 {
		t.Fatalf("the session shows workspace %d before the call, want 1", ws)
	}

	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", caller.ID, tuiosBin+" new-window -s "+sess+" probe --no-focus\n"); err != nil {
		t.Fatalf("send-text into caller: %v\n%s", err, out)
	}
	probe := waitRow("probe")
	saveFrame(t, term, "new-window-from-pane")
	for _, r := range xpanesRowsIn(t, base, sess) {
		t.Logf("window %s on workspace %d", r.Name, r.Workspace)
	}
	if probe.Workspace != 3 {
		t.Fatalf("ASSERTION: the window a pane on workspace 3 opened is on workspace %d, want 3", probe.Workspace)
	}
	if ws := currentWorkspace(t, base, sess); ws != 1 {
		t.Fatalf("the session shows workspace %d after a --no-focus call, want 1", ws)
	}

	// Outside every pane the default is still the shown workspace.
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "outside", "--no-focus"); err != nil {
		t.Fatalf("new-window outside: %v\n%s", err, out)
	}
	if outside := waitRow("outside"); outside.Workspace != 1 {
		t.Fatalf("a window opened outside every pane is on workspace %d, want 1", outside.Workspace)
	}
}
