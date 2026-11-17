package tuie2e

import (
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// base_index = 0 shifts the workspace number and the digit keys together. The
// workspace held in slot 1 is shown as 0 and reached with opt+0; slot 2 is
// shown as 1 and reached with opt+1. The daemon still numbers its workspaces
// from 1, so clientWorkspace reads the slot and not the label.
//
// How this could pass wrongly, written down first:
//   - The keys could shift without the labels: the pills below name both
//     workspaces through {index}, so a key-only change still fails waitPills.
//   - The labels could shift without the keys: the two alt presses check the
//     slot the client actually lands on, not the number on the pill.
//   - A switch that did nothing looks right from slot 1. The test presses
//     opt+1 first (which must leave slot 1) and opt+0 after (which must come
//     back), so a no-op fails one of the two.
//
// Negative control: make applyWorkspaceBaseIndex a no-op while base_index is
// 0 and the pill labels fail; make FormatWorkspaceTab ignore the offset and
// the label assertion fails too.
func TestWorkspaceBaseIndexZeroShiftsKeysAndNumbers(t *testing.T) {
	const sess = "bz"
	base := t.TempDir()
	// renumberConfig sets dock_workspace_tab_format = '{index}·{name}', which
	// puts the displayed number on each pill beside a stable, unique name.
	writeConfig(t, base, "[workspaces]\nbase_index = 0\n"+renumberConfig)
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	// The fresh session already holds one pane on workspace 1. Add one on 2.
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "--workspace", "2", "--no-focus"); err != nil {
		t.Fatalf("new-window on 2: %v: %s", err, out)
	}
	for ws, name := range map[string]string{"1": "EDIT", "2": "REVW"} {
		if out, err := tuiosCLI(t, base, "set-workspace-name", ws, name, "--session", sess); err != nil {
			t.Fatalf("name workspace %s: %v: %s", ws, err, out)
		}
	}

	term := attachIn(t, base, sess, startOpts{})
	waitBoot(t, term)
	// Slot 1 shows 0 and slot 2 shows 1, not 1 and 2.
	waitPills(t, term, "0·EDIT", "1·REVW")

	// opt+1 reaches slot 2 (shown as 1); opt+0 comes back to slot 1.
	if err := term.SendKeys(tuitest.Alt('1')); err != nil {
		t.Fatalf("press alt+1: %v", err)
	}
	if got := waitClientWorkspace(t, base, 2); got != 2 {
		t.Fatalf("ASSERTION: alt+1 left the client on workspace %d, want 2\n%s", got, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Alt('0')); err != nil {
		t.Fatalf("press alt+0: %v", err)
	}
	if got := waitClientWorkspace(t, base, 1); got != 1 {
		t.Fatalf("ASSERTION: alt+0 left the client on workspace %d, want 1\n%s", got, term.Snapshot())
	}
	waitPills(t, term, "0·EDIT", "1·REVW")
	alive(t, term, "after base-index workspace switches")
}
