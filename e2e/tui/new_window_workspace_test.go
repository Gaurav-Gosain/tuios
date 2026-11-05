package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// An agent in a pane opens a window and starts an agent with no --cwd, while
// the person's focus is on a pane in another folder. Both start in the
// agent's folder. The same new-window from outside every pane still starts in
// the focused pane's folder.
//
// How this could pass wrongly, written down first:
//   - The caller's folder could also be the focused pane's. The two panes
//     start in two different folders, and the test checks which one has the
//     focus.
//   - list-windows could report a folder the shell has not reached yet. The
//     test waits until the probe reports a folder at all.
//   - The default could move for every caller. The call from outside every
//     pane must still start in the focused pane's folder.
//
// Negative control: with the CallerCwd line cut from verbNewWindow, the probe
// starts in the focused pane's folder and the test fails.
func TestNewWindowFromAPaneStartsInThatPanesFolder(t *testing.T) {
	base := t.TempDir()
	const sess = "nwcwd"
	term := startHome(t, base, sess)

	mine, theirs := filepath.Join(base, "agent-dir"), filepath.Join(base, "person-dir")
	for _, dir := range []string{mine, theirs} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The person's pane takes the focus. The agent's pane does not.
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "person", "--cwd", theirs); err != nil {
		t.Fatalf("new-window person: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "agent", "--cwd", mine, "--no-focus"); err != nil {
		t.Fatalf("new-window agent: %v\n%s", err, out)
	}
	type row struct {
		ID   string `json:"window_id"`
		Name string `json:"display_name"`
		Cwd  string `json:"cwd"`
	}
	cwdOf := func(name string) (row, bool) {
		out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", sess)
		if err != nil {
			t.Fatalf("list-windows: %v\n%s", err, out)
		}
		var res struct {
			Windows []row `json:"windows"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("list-windows: %v\n%s", err, out)
		}
		for _, r := range res.Windows {
			if r.Name == name && r.Cwd != "" {
				return r, true
			}
		}
		return row{}, false
	}
	waitCwd := func(name string) row {
		t.Helper()
		deadline := time.Now().Add(shellTimeout)
		for {
			if r, ok := cwdOf(name); ok {
				return r
			}
			if time.Now().After(deadline) {
				t.Fatalf("no window named %s with a folder\n%s", name, term.Snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	agent := waitCwd("agent")
	if got := waitCwd("person").Cwd; got != theirs {
		t.Fatalf("the person's pane is in %s, want %s", got, theirs)
	}
	out, err := tuiosCLI(t, base, "get-window", "-s", sess, "--json")
	var focused row
	if err != nil || json.Unmarshal([]byte(out), &focused) != nil || focused.Name != "person" {
		t.Fatalf("the person's pane does not have the focus: %v\n%s", err, out)
	}

	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", agent.ID, tuiosBin+" new-window -s "+sess+" probe --no-focus\n"); err != nil {
		t.Fatalf("send-text into agent: %v\n%s", err, out)
	}
	probe := waitCwd("probe")
	t.Logf("agent %s, person %s, probe %s", mine, theirs, probe.Cwd)
	if probe.Cwd != mine {
		t.Fatalf("ASSERTION: the window a pane in %s opened starts in %s, want %s", mine, probe.Cwd, mine)
	}

	// Outside every pane the default is still the focused pane's folder.
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "outside", "--no-focus"); err != nil {
		t.Fatalf("new-window outside: %v\n%s", err, out)
	}
	if got := waitCwd("outside").Cwd; got != theirs {
		t.Fatalf("a window opened outside every pane starts in %s, want the focused pane's %s", got, theirs)
	}
}
